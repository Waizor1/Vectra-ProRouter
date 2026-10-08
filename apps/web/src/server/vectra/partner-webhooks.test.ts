import { eventLog, partnerWebhooks, routers } from "@vectra/db";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  PARTNER_REQUEST_ID_HEADER,
  PARTNER_VERSION_HEADER,
  verifyPartnerRequest,
} from "./partner-request-signature";
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
  partnerWebhookTarget,
  resolvePartnerWebhookTarget,
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
    partnerId: null,
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
      partnerId: "vectra",
    });

    expect(id).toEqual(expect.any(String));
    expect(fetchSpy).not.toHaveBeenCalled();
    const [row] = fake.inserts(partnerWebhooks);
    expect(row).toMatchObject({
      event: "router.failed",
      routerId: ROUTER_ID,
      nextAttemptAt: NOW,
      partnerId: "vectra",
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

const VECTRA_HOOK = "https://api-app.example/partner/prorouter/webhook";
const BLOOP_HOOK = "https://bloopcat.example/partner/prorouter/webhook";

describe("webhooks per partner", () => {
  beforeEach(() => {
    envMock.env = {
      VECTRA_CONNECT_WEBHOOK_URL: VECTRA_HOOK,
      VECTRA_CONNECT_WEBHOOK_SECRET: WEBHOOK_SECRET,
      VECTRA_PARTNERS: JSON.stringify([
        {
          id: "bloopcat",
          brand: "bloopcat",
          label: "BloopCat",
          secrets: ["bloopcat-partner-secret-0123456789abcdef"],
          webhookUrl: BLOOP_HOOK,
          webhookSecret: "bloopcat-webhook-secret-0123456789abcdef",
        },
        {
          id: "nohook",
          brand: "nohook",
          label: "NoHook",
          secrets: ["nohook-partner-secret-0123456789abcdefgh"],
        },
      ]),
    };
  });

  it("queues a BloopCat webhook with its partner", async () => {
    const fake = createFakeDb({});
    const id = await enqueuePartnerWebhookWithDb(fake.db as never, {
      event: "router.ready",
      routerId: ROUTER_ID,
      ownerRef: "bc_1",
      partnerId: "bloopcat",
      at: NOW,
    });
    expect(id).toEqual(expect.any(String));
    expect(fake.inserts(partnerWebhooks)[0]).toMatchObject({
      event: "router.ready",
      partnerId: "bloopcat",
    });
  });

  it("queues nothing for a partner without a webhook address", async () => {
    const fake = createFakeDb({});
    expect(
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.ready",
        routerId: ROUTER_ID,
        ownerRef: "x_1",
        partnerId: "nohook",
      }),
    ).toBeNull();
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });

  it("queues nothing for a partner that is not known at all", async () => {
    const fake = createFakeDb({});
    expect(
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.ready",
        routerId: ROUTER_ID,
        ownerRef: "x_1",
        partnerId: "ghost",
      }),
    ).toBeNull();
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });

  it("reads the router's partner when the caller did not say", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[{ partnerId: "bloopcat" }]]]],
    });
    await enqueuePartnerWebhookWithDb(fake.db as never, {
      event: "router.offline",
      routerId: ROUTER_ID,
      ownerRef: "bc_1",
      at: NOW,
    });
    expect(fake.inserts(partnerWebhooks)[0]).toMatchObject({
      partnerId: "bloopcat",
    });
  });

  it("treats a router without a partner (from before there were several) as Vectra Connect's", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[{ partnerId: null }]]]],
    });
    await enqueuePartnerWebhookWithDb(fake.db as never, {
      event: "router.offline",
      routerId: ROUTER_ID,
      ownerRef: "acct-42",
      at: NOW,
    });
    expect(fake.inserts(partnerWebhooks)[0]).toMatchObject({
      partnerId: "vectra",
    });
  });

  it("does not read the router when the caller names the partner", async () => {
    const fake = createFakeDb({});
    await enqueuePartnerWebhookWithDb(fake.db as never, {
      event: "router.ready",
      routerId: ROUTER_ID,
      ownerRef: "bc_1",
      partnerId: "bloopcat",
    });
    expect(fake.calls.filter((call) => call.kind === "select")).toEqual([]);
  });

  it("is a no-op without reading anything when no partner has a webhook", async () => {
    envMock.env = {
      VECTRA_PARTNERS: JSON.stringify([
        {
          id: "nohook",
          brand: "nohook",
          label: "NoHook",
          secrets: ["nohook-partner-secret-0123456789abcdefgh"],
        },
      ]),
    };
    const fake = createFakeDb({});
    expect(
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.ready",
        routerId: ROUTER_ID,
        ownerRef: "x_1",
      }),
    ).toBeNull();
    expect(fake.calls).toEqual([]);
  });

  it("targets: the default partner's is Vectra Connect's, others come from the registry", () => {
    expect(partnerWebhookTarget("vectra")).toEqual({
      url: VECTRA_HOOK,
      secret: WEBHOOK_SECRET,
    });
    expect(resolvePartnerWebhookTarget()).toEqual(partnerWebhookTarget("vectra"));
    expect(partnerWebhookTarget("bloopcat")).toEqual({
      url: BLOOP_HOOK,
      secret: "bloopcat-webhook-secret-0123456789abcdef",
    });
    expect(partnerWebhookTarget("nohook")).toBeNull();
    expect(partnerWebhookTarget("ghost")).toBeNull();
  });

  it("delivers each row to its own partner's address, signed with its own secret", async () => {
    const vectraRow = dueRow({ id: "w-vectra", partnerId: null });
    const bloopRow = dueRow({ id: "w-bloop", partnerId: "bloopcat" });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[vectraRow, bloopRow]]]],
      updateReturns: [
        [
          partnerWebhooks,
          [[{ ...vectraRow, attempts: 1 }], [{ ...bloopRow, attempts: 1 }]],
        ],
      ],
    });
    const seen: Array<{ url: string; id: string }> = [];

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async (url: string, init: RequestInit) => {
        seen.push({
          url,
          id: (init.headers as Record<string, string>)[PARTNER_WEBHOOK_ID_HEADER]!,
        });
        return { ok: true, status: 200, body: null };
      },
    });

    expect(summary).toEqual({ attempted: 2, delivered: 2, rescheduled: 0, gaveUp: 0 });
    expect(seen).toEqual(
      expect.arrayContaining([
        { url: VECTRA_HOOK, id: "w-vectra" },
        { url: BLOOP_HOOK, id: "w-bloop" },
      ]),
    );
    expect(seen).toHaveLength(2);
  });

  it("does not let one partner's hanging endpoint hold the others", async () => {
    const vectraRow = dueRow({ id: "w-vectra", partnerId: null });
    const bloopRow = dueRow({ id: "w-bloop", partnerId: "bloopcat" });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[vectraRow, bloopRow]]]],
      updateReturns: [
        [
          partnerWebhooks,
          [[{ ...vectraRow, attempts: 1 }], [{ ...bloopRow, attempts: 1 }]],
        ],
      ],
    });
    let releaseVectra: () => void = () => undefined;
    const vectraStuck = new Promise<void>((resolve) => {
      releaseVectra = resolve;
    });
    const fetchImpl = async (url: string) => {
      if (url === VECTRA_HOOK) {
        await vectraStuck;
      }
      return { ok: true, status: 200, body: null };
    };

    const dispatching = dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl,
    });

    // BloopCat's webhook is marked delivered while Vectra's is still waiting.
    await vi.waitFor(() =>
      expect(
        fake.updates(partnerWebhooks).filter((set) => set.deliveredAt),
      ).toHaveLength(1),
    );
    releaseVectra();
    const summary = await dispatching;

    expect(summary.delivered).toBe(2);
    expect(
      fake.updates(partnerWebhooks).filter((set) => set.deliveredAt),
    ).toHaveLength(2);
  });

  it("keeps one partner's rows in order", async () => {
    const first = dueRow({ id: "w-1", partnerId: "bloopcat" });
    const second = dueRow({ id: "w-2", partnerId: "bloopcat" });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[first, second]]]],
      updateReturns: [
        [
          partnerWebhooks,
          [[{ ...first, attempts: 1 }], [{ ...second, attempts: 1 }]],
        ],
      ],
    });
    const order: string[] = [];

    await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async (_url: string, init: RequestInit) => {
        const id = (init.headers as Record<string, string>)[PARTNER_WEBHOOK_ID_HEADER]!;
        order.push(`start ${id}`);
        // The first webhook is slower: the second still waits for it.
        await new Promise((resolve) => setTimeout(resolve, id === "w-1" ? 20 : 0));
        order.push(`end ${id}`);
        return { ok: true, status: 200, body: null };
      },
    });

    expect(order).toEqual(["start w-1", "end w-1", "start w-2", "end w-2"]);
  });

  it("one partner's failure does not touch another partner's row", async () => {
    const vectraRow = dueRow({ id: "w-vectra", partnerId: null });
    const bloopRow = dueRow({ id: "w-bloop", partnerId: "bloopcat" });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[vectraRow, bloopRow]]]],
      updateReturns: [
        [
          partnerWebhooks,
          [[{ ...vectraRow, attempts: 1 }], [{ ...bloopRow, attempts: 1 }]],
        ],
      ],
    });

    const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async (url: string) =>
        url === BLOOP_HOOK
          ? { ok: false, status: 503, body: null }
          : { ok: true, status: 200, body: null },
    });

    expect(summary).toEqual({ attempted: 2, delivered: 1, rescheduled: 1, gaveUp: 0 });
    const done = fake.updates(partnerWebhooks).filter((set) => set.deliveredAt);
    const retried = fake.updates(partnerWebhooks).filter((set) => set.lastStatus === 503);
    expect(done).toHaveLength(1);
    expect(retried).toEqual([
      {
        nextAttemptAt: new Date(NOW.getTime() + 30_000),
        lastStatus: 503,
        lastError: "HTTP 503",
      },
    ]);
  });

  it.each(["nohook", "ghost"])(
    "stops retrying a row whose partner (%s) has no webhook address",
    async (partnerId) => {
      const fake = createFakeDb({
        selects: [[partnerWebhooks, [[dueRow({ id: "w-orphan", partnerId })]]]],
      });
      const fetchImpl = vi.fn(async () => ({ ok: true, status: 200, body: null }));

      const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        now: NOW,
        fetchImpl,
      });

      expect(fetchImpl).not.toHaveBeenCalled();
      expect(summary).toEqual({ attempted: 0, delivered: 0, rescheduled: 0, gaveUp: 0 });
      expect(fake.updates(partnerWebhooks)).toEqual([
        { nextAttemptAt: null, lastError: "no_webhook_target" },
      ]);
    },
  );

  it("names the partner in the give-up journal entry", async () => {
    const lastTry = PARTNER_WEBHOOK_MAX_ATTEMPTS - 1;
    const row = dueRow({ attempts: lastTry, partnerId: "bloopcat" });
    const fake = createFakeDb({
      selects: [[partnerWebhooks, [[row]]]],
      updateReturns: [
        [partnerWebhooks, [[{ ...row, attempts: PARTNER_WEBHOOK_MAX_ATTEMPTS }]]],
      ],
    });

    await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
      now: NOW,
      fetchImpl: async () => ({ ok: false, status: 500, body: null }),
    });

    const [entry] = fake.inserts(eventLog);
    expect(entry).toMatchObject({
      type: "partner.webhook.gave_up",
      message: expect.stringContaining("by partner bloopcat"),
      metadata: expect.objectContaining({ partnerId: "bloopcat" }),
    });
    expect(String(entry?.message)).not.toContain("Vectra backend");
  });

  it("schedules a delivery when only a non-default partner has a webhook, none when nobody has", () => {
    vi.useFakeTimers();
    try {
      envMock.env = {
        VECTRA_PARTNERS: JSON.stringify([
          {
            id: "bloopcat",
            brand: "bloopcat",
            label: "BloopCat",
            secrets: ["bloopcat-partner-secret-0123456789abcdef"],
            webhookUrl: BLOOP_HOOK,
            webhookSecret: "bloopcat-webhook-secret-0123456789abcdef",
          },
        ]),
      };
      schedulePartnerWebhookDelivery({ force: true });
      expect(vi.getTimerCount()).toBe(1);

      vi.clearAllTimers();
      envMock.env = {};
      schedulePartnerWebhookDelivery({ force: true });
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.clearAllTimers();
      vi.useRealTimers();
    }
  });
});

describe("deliverPartnerWebhook", () => {
  it("signs with partner signature v2, the webhook id as request id and idempotency key", async () => {
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
    expect(init.headers[PARTNER_VERSION_HEADER]).toBe("2");
    expect(init.headers[PARTNER_REQUEST_ID_HEADER]).toBe(row.id);
    expect(init.headers["Idempotency-Key"]).toBe(row.id);
    const verified = verifyPartnerRequest({
      request: new Request(url, {
        method: "POST",
        headers: init.headers,
        body: init.body as string,
      }),
      secret: WEBHOOK_SECRET,
      rawBody: new TextEncoder().encode(init.body as string),
      nowMs: NOW.getTime(),
      method: "POST",
      path: "/hooks/prorouter",
    });
    expect(verified).toMatchObject({ ok: true, requestId: row.id });
    // The bare v1 body signature (no id, method or path) is gone.
    expect(
      verifyPartnerSignature({
        secret: WEBHOOK_SECRET,
        timestamp: init.headers[PARTNER_TIMESTAMP_HEADER],
        signature: init.headers[PARTNER_SIGNATURE_HEADER],
        rawBody: init.body as string,
        nowMs: NOW.getTime(),
      }),
    ).toMatchObject({ ok: false, error: "bad_signature" });
  });

  it("binds the signature to the webhook id: a captured body under another id does not verify", async () => {
    const fetchImpl = vi.fn(async () => ({ ok: true, status: 200, body: null }));
    const row = dueRow();
    await deliverPartnerWebhook(
      row,
      { url: "https://backend.example.test/hooks/prorouter", secret: WEBHOOK_SECRET },
      { fetchImpl, nowMs: NOW.getTime() },
    );
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [
      string,
      RequestInit & { headers: Record<string, string> },
    ];
    const swapped = {
      ...init.headers,
      [PARTNER_REQUEST_ID_HEADER]: "4c0e7d2f-3a5b-4f9c-8d8e-2b3c4d5e6f70",
      [PARTNER_WEBHOOK_ID_HEADER]: "4c0e7d2f-3a5b-4f9c-8d8e-2b3c4d5e6f70",
    };
    expect(
      verifyPartnerRequest({
        request: new Request(url, { method: "POST", headers: swapped, body: init.body as string }),
        secret: WEBHOOK_SECRET,
        rawBody: new TextEncoder().encode(init.body as string),
        nowMs: NOW.getTime(),
        method: "POST",
        path: "/hooks/prorouter",
      }),
    ).toMatchObject({ ok: false, error: "bad_signature" });
  });

  it("counts a 409 (request id already processed by the backend) as delivered", async () => {
    expect(
      await deliverPartnerWebhook(
        dueRow(),
        { url: "https://backend.example.test/hooks/prorouter", secret: WEBHOOK_SECRET },
        { fetchImpl: async () => ({ ok: false, status: 409, body: null }) },
      ),
    ).toEqual({ ok: true, status: 409 });
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
