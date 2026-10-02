import { eventLog, partnerWebhooks } from "@vectra/db";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  PARTNER_SIGNATURE_HEADER,
  PARTNER_TIMESTAMP_HEADER,
  verifyPartnerSignature,
} from "./partner-signature";
import {
  deliverPartnerWebhook,
  dispatchDuePartnerWebhooksWithDb,
  enqueuePartnerWebhookWithDb,
  PARTNER_WEBHOOK_ID_HEADER,
  PARTNER_WEBHOOK_MAX_ATTEMPTS,
  partnerWebhookBackoffSeconds,
  schedulePartnerWebhookDelivery,
} from "./partner-webhooks";
import { createFakeDb } from "./testing/fake-db";

const envMock = vi.hoisted(() => ({
  env: {} as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", () => ({ db: {} }));

const WEBHOOK_SECRET = "partner-webhooks-test-secret-0123456789";
const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const NOW = new Date("2026-09-28T10:00:00.000Z");

function dueRow(overrides: Record<string, unknown> = {}) {
  return {
    id: "3b9d6c1e-2f4a-4e8b-9c7d-1a2b3c4d5e6f",
    event: "router.ready",
    routerId: ROUTER_ID,
    payload: {
      event: "router.ready",
      routerId: ROUTER_ID,
      ownerRef: "acct-42",
      at: NOW.toISOString(),
    },
    attempts: 0,
    nextAttemptAt: NOW,
    deliveredAt: null,
    lastStatus: null,
    lastError: null,
    createdAt: NOW,
    ...overrides,
  };
}

beforeEach(() => {
  envMock.env = {
    NODE_ENV: "test",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks/prorouter",
    VECTRA_CONNECT_WEBHOOK_SECRET: WEBHOOK_SECRET,
  };
});

describe("enqueuePartnerWebhookWithDb", () => {
  it("writes one outbox row and touches no network", async () => {
    const fake = createFakeDb();
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const id = await enqueuePartnerWebhookWithDb(fake.db as never, {
      event: "router.failed",
      routerId: ROUTER_ID,
      ownerRef: "acct-42",
      detail: `  ${"x".repeat(600)}  `,
      at: NOW,
    });

    expect(id).toEqual(expect.any(String));
    expect(fetchSpy).not.toHaveBeenCalled();
    const [row] = fake.inserts(partnerWebhooks);
    expect(row).toMatchObject({
      event: "router.failed",
      routerId: ROUTER_ID,
      nextAttemptAt: NOW,
    });
    expect(row!.payload).toEqual({
      event: "router.failed",
      routerId: ROUTER_ID,
      ownerRef: "acct-42",
      at: NOW.toISOString(),
      detail: "x".repeat(500),
    });
    fetchSpy.mockRestore();
  });

  it("is a no-op while the webhook is not configured", async () => {
    envMock.env.VECTRA_CONNECT_WEBHOOK_SECRET = undefined;
    const fake = createFakeDb();

    expect(
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.claimed",
        routerId: ROUTER_ID,
        ownerRef: "acct-42",
      }),
    ).toBeNull();
    expect(fake.calls).toEqual([]);
  });
});

describe("deliverPartnerWebhook", () => {
  it("signs the body the same way the partner API is signed", async () => {
    const fetchImpl = vi.fn(async () => ({ ok: true, status: 204, body: null }));
    const row = dueRow();

    const result = await deliverPartnerWebhook(
      row,
      {
        url: "https://backend.example.test/hooks/prorouter",
        secret: WEBHOOK_SECRET,
      },
      { fetchImpl, nowMs: NOW.getTime() },
    );

    expect(result).toEqual({ ok: true, status: 204 });
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [
      string,
      RequestInit & { headers: Record<string, string> },
    ];
    expect(url).toBe("https://backend.example.test/hooks/prorouter");
    expect(init.method).toBe("POST");
    expect(init.redirect).toBe("manual");
    expect(init.headers[PARTNER_WEBHOOK_ID_HEADER]).toBe(row.id);
    expect(JSON.parse(init.body as string)).toEqual(row.payload);
    expect(
      verifyPartnerSignature({
        secret: WEBHOOK_SECRET,
        timestamp: init.headers[PARTNER_TIMESTAMP_HEADER],
        signature: init.headers[PARTNER_SIGNATURE_HEADER],
        rawBody: init.body as string,
        nowMs: NOW.getTime(),
      }),
    ).toEqual({ ok: true });
  });

  it("reports a refusal or a network error instead of throwing", async () => {
    const target = {
      url: "https://backend.example.test/hooks/prorouter",
      secret: WEBHOOK_SECRET,
    };
    expect(
      await deliverPartnerWebhook(dueRow(), target, {
        fetchImpl: async () => ({ ok: false, status: 502, body: null }),
      }),
    ).toEqual({ ok: false, status: 502, error: "HTTP 502" });
    expect(
      await deliverPartnerWebhook(dueRow(), target, {
        fetchImpl: async () => {
          throw new Error("connect ECONNREFUSED");
        },
      }),
    ).toEqual({ ok: false, status: null, error: "connect ECONNREFUSED" });
  });
});

describe("dispatchDuePartnerWebhooksWithDb", () => {
  it("marks a delivered webhook", async () => {
    const leased = dueRow({ attempts: 1 });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[dueRow()]]]],
      updateReturns: [[partnerWebhooks, [[leased]]]],
    });

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async () => ({ ok: true, status: 200, body: null }),
    });

    expect(summary).toEqual({ attempted: 1, delivered: 1, rescheduled: 0, gaveUp: 0 });
    const [lease, done] = fake.updates(partnerWebhooks);
    // The lease is a compare-and-swap on the attempt counter.
    expect(lease).toEqual({
      attempts: 1,
      nextAttemptAt: new Date(NOW.getTime() + 60_000),
    });
    expect(done).toMatchObject({
      nextAttemptAt: null,
      lastStatus: 200,
      lastError: null,
    });
    expect(done?.deliveredAt).toBeInstanceOf(Date);
  });

  it("reschedules a failed attempt with backoff", async () => {
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[dueRow()]]]],
      updateReturns: [[partnerWebhooks, [[dueRow({ attempts: 1 })]]]],
    });

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async () => ({ ok: false, status: 503, body: null }),
    });

    expect(summary).toMatchObject({ attempted: 1, rescheduled: 1 });
    expect(fake.updates(partnerWebhooks)[1]).toEqual({
      nextAttemptAt: new Date(NOW.getTime() + 30_000),
      lastStatus: 503,
      lastError: "HTTP 503",
    });
  });

  it("gives up after the last attempt and journals it", async () => {
    const lastTry = PARTNER_WEBHOOK_MAX_ATTEMPTS - 1;
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[dueRow({ attempts: lastTry })]]]],
      updateReturns: [
        [partnerWebhooks, [[dueRow({ attempts: PARTNER_WEBHOOK_MAX_ATTEMPTS })]]],
      ],
    });

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async () => ({ ok: false, status: 500, body: null }),
    });

    expect(summary).toMatchObject({ gaveUp: 1 });
    expect(fake.updates(partnerWebhooks)[1]).toEqual({
      nextAttemptAt: null,
      lastStatus: 500,
      lastError: "HTTP 500",
    });
    expect(fake.inserts(eventLog)[0]).toMatchObject({
      type: "partner.webhook.gave_up",
      routerId: ROUTER_ID,
    });
  });

  it("skips a row another dispatcher already leased", async () => {
    const fetchImpl = vi.fn(async () => ({ ok: true, status: 200, body: null }));
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[dueRow()]]]],
      updateReturns: [[partnerWebhooks, [[]]]],
    });

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl,
    });

    expect(summary.attempted).toBe(0);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("does nothing while the webhook is not configured", async () => {
    envMock.env.VECTRA_CONNECT_WEBHOOK_URL = undefined;
    const fake = createFakeDb({ selects: [[partnerWebhooks, [[dueRow()]]]] });

    await dispatchDuePartnerWebhooksWithDb(fake.db as never, { now: NOW });

    expect(fake.calls).toEqual([]);
  });

  it("backs off 30 s, 2 min, 10 min … up to 8 h", () => {
    expect(partnerWebhookBackoffSeconds(1)).toBe(30);
    expect(partnerWebhookBackoffSeconds(2)).toBe(120);
    expect(partnerWebhookBackoffSeconds(3)).toBe(600);
    expect(partnerWebhookBackoffSeconds(50)).toBe(28_800);
  });
});

describe("schedulePartnerWebhookDelivery", () => {
  it("returns at once and delivers on a later tick, even if the backend hangs", async () => {
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[dueRow()]]]],
      updateReturns: [[partnerWebhooks, [[dueRow({ attempts: 1 })]]]],
    });
    let calledBack: () => void = () => undefined;
    const called = new Promise<void>((resolve) => {
      calledBack = resolve;
    });
    let answer: () => void = () => undefined;
    const answered = new Promise<{ ok: boolean; status: number; body: null }>(
      (resolve) => {
        answer = () => resolve({ ok: true, status: 200, body: null });
      },
    );
    // The backend does not answer until the test lets it.
    const fetchImpl = vi.fn(() => {
      calledBack();
      return answered;
    });

    const returned = schedulePartnerWebhookDelivery({
      client: fake.db as never,
      fetchImpl,
      force: true,
    });

    expect(returned).toBeUndefined();
    expect(fetchImpl).not.toHaveBeenCalled();
    await called;
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    answer();
    await vi.waitFor(() =>
      expect(fake.updates(partnerWebhooks).at(-1)).toMatchObject({
        lastStatus: 200,
      }),
    );
  });

  it("stays off under test unless forced", () => {
    const fake = createFakeDb();
    schedulePartnerWebhookDelivery({ client: fake.db as never });
    expect(fake.calls).toEqual([]);
  });
});
