import { eventLog, partnerWebhooks, routers } from "@vectra/db";
import { type SQL } from "drizzle-orm";
import { PgDialect } from "drizzle-orm/pg-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { loopTickTimes, setBackgroundLockClientForTest } from "./background-lock";
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
  runPartnerWebhookTick,
  schedulePartnerWebhookDelivery,
  selectDuePartnerWebhooksWithDb,
} from "./partner-webhooks";
import { createFakeDb, type FakeDbScript } from "./testing/fake-db";

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

// The per-partner delivery loops live on globalThis for the whole process (a
// loop's rerun flag lives on its entry). A test that leaves one behind — a
// hanging endpoint it never released, an assertion that failed first — would
// otherwise turn every later test's pass for that partner into a mere rerun
// flag on a loop that belongs to a finished test.
afterEach(() => {
  (
    globalThis as { __vectraPartnerWebhookFlights?: Map<string, unknown> }
  ).__vectraPartnerWebhookFlights?.clear();
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

  // The registry may only be hiding the partner (a VECTRA_PARTNERS typo makes it
  // serve Vectra alone): the row is queued and waits for the address, as the
  // dispatcher does for any row without one — at most a week, journaled.
  it("queues the event of a partner the registry does not know", async () => {
    const fake = createFakeDb({});
    expect(
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.ready",
        routerId: ROUTER_ID,
        ownerRef: "x_1",
        partnerId: "ghost",
      }),
    ).toEqual(expect.any(String));
    expect(fake.inserts(partnerWebhooks)).toEqual([
      expect.objectContaining({ event: "router.ready", partnerId: "ghost" }),
    ]);
  });

  it("keeps BloopCat's events while a VECTRA_PARTNERS typo hides BloopCat", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      envMock.env.VECTRA_PARTNERS = '[{"id":"bloopcat",';
      // The registry serves Vectra Connect alone now.
      expect(partnerWebhookTarget("bloopcat")).toBeNull();
      expect(partnerWebhookTarget("vectra")).not.toBeNull();

      const fake = createFakeDb({
        selects: [[routers, [[{ partnerId: "bloopcat" }]]]],
      });
      await enqueuePartnerWebhookWithDb(fake.db as never, {
        event: "router.offline",
        routerId: ROUTER_ID,
        ownerRef: "bc_1",
        at: NOW,
      });
      expect(fake.inserts(partnerWebhooks)).toEqual([
        expect.objectContaining({ event: "router.offline", partnerId: "bloopcat" }),
      ]);
    } finally {
      error.mockRestore();
    }
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
      // Per row: its lease, then the delivered mark (which reports the row
      // it touched).
      updateReturns: [
        [
          partnerWebhooks,
          [
            [{ ...first, attempts: 1 }],
            [{ id: first.id }],
            [{ ...second, attempts: 1 }],
            [{ id: second.id }],
          ],
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

  describe("a row whose partner has no webhook address", () => {
    const DAY_MS = 24 * 60 * 60 * 1000;
    const dialect = new PgDialect();

    async function dispatchOnce(
      row: ReturnType<typeof dueRow>,
      fetchImpl: ReturnType<typeof vi.fn>,
    ) {
      const fake = createFakeDb({ selects: [[partnerWebhooks, [[row]]]] });
      const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        now: NOW,
        fetchImpl: fetchImpl as never,
      });
      return { fake, summary };
    }

    it.each(["nohook", "ghost"])(
      "waits an hour instead of vanishing, attempts untouched, and says so once (%s)",
      async (partnerId) => {
        const fetchImpl = vi.fn(async () => ({ ok: true, status: 200, body: null }));
        const { fake, summary } = await dispatchOnce(
          dueRow({
            id: "w-orphan",
            partnerId,
            attempts: 2,
            createdAt: new Date(NOW.getTime() - DAY_MS),
          }),
          fetchImpl,
        );

        expect(fetchImpl).not.toHaveBeenCalled();
        expect(summary).toEqual({ attempted: 0, delivered: 0, rescheduled: 0, gaveUp: 0 });
        expect(fake.updates(partnerWebhooks)).toEqual([
          {
            nextAttemptAt: new Date(NOW.getTime() + 60 * 60 * 1000),
            lastError: "no_webhook_target",
          },
        ]);
        expect(fake.inserts(eventLog)).toEqual([
          expect.objectContaining({
            routerId: ROUTER_ID,
            type: "partner.webhook.no_target",
            severity: "warning",
            message: expect.stringContaining(`partner ${partnerId}`),
            metadata: { webhookId: "w-orphan", event: "router.ready", partnerId },
          }),
        ]);
      },
    );

    it("keeps waiting without a second journal entry once it is marked", async () => {
      const { fake } = await dispatchOnce(
        dueRow({
          partnerId: "nohook",
          lastError: "no_webhook_target",
          createdAt: new Date(NOW.getTime() - 2 * DAY_MS),
        }),
        vi.fn(),
      );

      expect(fake.updates(partnerWebhooks)).toEqual([
        {
          nextAttemptAt: new Date(NOW.getTime() + 60 * 60 * 1000),
          lastError: "no_webhook_target",
        },
      ]);
      expect(fake.inserts(eventLog)).toEqual([]);
    });

    it("says so again when an earlier failure had replaced the mark", async () => {
      const { fake } = await dispatchOnce(
        dueRow({
          partnerId: "nohook",
          lastError: "HTTP 503",
          createdAt: new Date(NOW.getTime() - DAY_MS),
        }),
        vi.fn(),
      );
      expect(fake.inserts(eventLog)).toHaveLength(1);
    });

    it("waits up to exactly seven days, then gives up and journals it", async () => {
      const edge = await dispatchOnce(
        dueRow({ partnerId: "nohook", createdAt: new Date(NOW.getTime() - 7 * DAY_MS) }),
        vi.fn(),
      );
      expect(edge.fake.updates(partnerWebhooks)[0]).toMatchObject({
        nextAttemptAt: new Date(NOW.getTime() + 60 * 60 * 1000),
      });
      expect(edge.summary.gaveUp).toBe(0);

      const old = await dispatchOnce(
        dueRow({
          id: "w-old",
          partnerId: "nohook",
          createdAt: new Date(NOW.getTime() - 7 * DAY_MS - 1),
        }),
        vi.fn(),
      );
      expect(old.summary.gaveUp).toBe(1);
      expect(old.fake.updates(partnerWebhooks)).toEqual([
        { nextAttemptAt: null, lastError: "no_webhook_target" },
      ]);
      expect(old.fake.inserts(eventLog)).toEqual([
        expect.objectContaining({
          routerId: ROUTER_ID,
          type: "partner.webhook.gave_up",
          severity: "warning",
          message: expect.stringContaining("partner nohook"),
          metadata: {
            webhookId: "w-old",
            event: "router.ready",
            partnerId: "nohook",
            reason: "no_webhook_target",
          },
        }),
      ]);
    });

    it("is delivered on the next pass once the partner regains its address", async () => {
      const row = dueRow({
        id: "w-wait",
        partnerId: "nohook",
        createdAt: new Date(NOW.getTime() - DAY_MS),
      });
      const fake = createFakeDb({
        // The same row comes back due, as the database would return it an hour on.
        selects: [[partnerWebhooks, [[row], [{ ...row, lastError: "no_webhook_target" }]]]],
        updateReturns: [[partnerWebhooks, [[{ ...row, attempts: 1 }]]]],
      });
      const fetchImpl = vi.fn(async (_url: string) => ({ ok: true, status: 200, body: null }));

      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { now: NOW, fetchImpl });
      expect(fetchImpl).not.toHaveBeenCalled();

      // The typo is fixed: nohook now has an address.
      envMock.env.VECTRA_PARTNERS = JSON.stringify([
        {
          id: "nohook",
          brand: "nohook",
          label: "NoHook",
          secrets: ["nohook-partner-secret-0123456789abcdefgh"],
          webhookUrl: "https://nohook.example/hooks",
          webhookSecret: "nohook-webhook-secret-0123456789abcdefgh",
        },
      ]);
      const summary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        now: new Date(NOW.getTime() + 60 * 60 * 1000),
        fetchImpl,
      });

      expect(summary).toEqual({ attempted: 1, delivered: 1, rescheduled: 0, gaveUp: 0 });
      expect(fetchImpl.mock.calls[0]![0]).toBe("https://nohook.example/hooks");
      expect(fake.updates(partnerWebhooks).at(-1)).toMatchObject({
        lastStatus: 200,
        lastError: null,
      });
    });

    it("never touches a row that has been delivered meanwhile", async () => {
      for (const createdAt of [
        new Date(NOW.getTime() - DAY_MS),
        new Date(NOW.getTime() - 8 * DAY_MS),
      ]) {
        const base = createFakeDb({
          selects: [[partnerWebhooks, [[dueRow({ partnerId: "nohook", createdAt })]]]],
        });
        const wheres: Array<{ sql: string; params: unknown[] }> = [];
        const db = {
          ...base.db,
          update: (table: unknown) => ({
            set: (set: Record<string, unknown>) => {
              const inner = base.db.update(table).set(set);
              return {
                where: (condition: SQL) => {
                  wheres.push(dialect.sqlToQuery(condition));
                  return inner.where();
                },
              };
            },
          }),
        };

        await dispatchDuePartnerWebhooksWithDb(db as never, { now: NOW });

        expect(wheres).toHaveLength(1);
        expect(wheres[0]!.sql).toMatch(/"delivered_at" is null/);
        expect(wheres[0]!.sql).toMatch(/"id" = \$\d+/);
      }
    });
  });

  it("lets the other partners finish when one group's database write fails, then rejects", async () => {
    const vectraRow = dueRow({ id: "w-vectra", partnerId: null });
    const bloopRow = dueRow({ id: "w-bloop", partnerId: "bloopcat" });
    const base = createFakeDb({
      selects: [[partnerWebhooks, [[vectraRow, bloopRow]]]],
      updateReturns: [
        [
          partnerWebhooks,
          [[{ ...vectraRow, attempts: 1 }], [{ ...bloopRow, attempts: 1 }]],
        ],
      ],
    });
    // The first "delivered" write (Vectra's: BloopCat is still held below) fails.
    let failed = false;
    const db = {
      ...base.db,
      update: (table: unknown) => ({
        set: (set: Record<string, unknown>) => {
          if (set.deliveredAt && !failed) {
            failed = true;
            throw new Error("database went away");
          }
          return base.db.update(table).set(set);
        },
      }),
    };
    let releaseBloop: () => void = () => undefined;
    const bloopHeld = new Promise<void>((resolve) => {
      releaseBloop = resolve;
    });
    const fetchImpl = async (url: string) => {
      if (url === BLOOP_HOOK) {
        await bloopHeld;
      }
      return { ok: true, status: 200, body: null };
    };

    let outcome: unknown = "pending";
    const dispatching = dispatchDuePartnerWebhooksWithDb(db as never, {
      now: NOW,
      fetchImpl,
    }).then(
      () => (outcome = "resolved"),
      (error: unknown) => (outcome = error),
    );

    await vi.waitFor(() => expect(failed).toBe(true));
    // Vectra's group has failed, but the dispatcher waits for BloopCat's.
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(outcome).toBe("pending");

    releaseBloop();
    await dispatching;

    expect(outcome).toEqual(new Error("database went away"));
    expect(
      base.updates(partnerWebhooks).filter((set) => set.deliveredAt),
    ).toHaveLength(1);
  });

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

// A fake whose lease (the UPDATE … RETURNING compare-and-swap) answers with the
// row it names, as PostgreSQL would, so a long backlog needs no scripted lease
// per row. Every write is recorded with the id it was aimed at.
function leasingDb(script: FakeDbScript, rows: Array<ReturnType<typeof dueRow>>) {
  const base = createFakeDb(script);
  const byId = new Map(rows.map((row) => [row.id, row]));
  const writes: Array<{ id: string; set: Record<string, unknown> }> = [];
  const db = {
    ...base.db,
    update: () => ({
      set: (set: Record<string, unknown>) => ({
        where: (condition: SQL) => {
          const id = String(new PgDialect().sqlToQuery(condition).params[0]);
          writes.push({ id, set });
          const row = byId.get(id);
          return {
            returning: async () => (row ? [{ ...row, ...set }] : []),
            then: (resolve: (value: unknown[]) => unknown) =>
              Promise.resolve([]).then(resolve),
          };
        },
      }),
    }),
  };
  return {
    ...base,
    db,
    writes,
    delivered: () =>
      writes.filter((write) => write.set.deliveredAt).map((write) => write.id),
  };
}

// A webhook table with state: every UPDATE applies its WHERE as PostgreSQL
// would for the predicates the dispatcher writes (id, attempts, delivered_at
// is null) and returns the rows it touched. Each due-row SELECT is answered by
// the next of `selects`, read from the table at that moment.
function webhookTable(
  rows: Array<ReturnType<typeof dueRow>>,
  selects: Array<(table: Map<string, Record<string, unknown>>) => unknown[]>,
) {
  const base = createFakeDb();
  const table = new Map<string, Record<string, unknown>>(
    rows.map((row) => [row.id, { ...row }]),
  );
  const dialect = new PgDialect();
  const touched = (condition: SQL) => {
    const { sql, params } = dialect.sqlToQuery(condition);
    const row = table.get(String(params[0]));
    if (!row) return null;
    const attempts = /"attempts" = \$(\d+)/.exec(sql);
    if (attempts && row.attempts !== params[Number(attempts[1]) - 1]) return null;
    if (/"delivered_at" is null/.test(sql) && row.deliveredAt !== null) return null;
    return row;
  };
  let read = 0;
  const db = {
    ...base.db,
    select: () => ({
      from: (from: unknown) => {
        const answer = () =>
          from === partnerWebhooks ? (selects[read++]?.(table) ?? []) : [];
        const chain = {
          where: () => chain,
          orderBy: () => chain,
          limit: () => Promise.resolve().then(answer),
          then: (resolve: (value: unknown[]) => unknown, reject?: (error: unknown) => unknown) =>
            Promise.resolve().then(answer).then(resolve, reject),
        };
        return chain;
      },
    }),
    update: () => ({
      set: (set: Record<string, unknown>) => ({
        where: (condition: SQL) => {
          const apply = () => {
            const row = touched(condition);
            if (!row) return [];
            Object.assign(row, set);
            return [{ ...row }];
          };
          return {
            returning: () => Promise.resolve().then(apply),
            then: (resolve: (value: unknown) => unknown, reject?: (error: unknown) => unknown) =>
              Promise.resolve().then(apply).then(() => [], undefined).then(resolve, reject),
          };
        },
      }),
    }),
  };
  return {
    ...base,
    db,
    row: (id: string) => ({ ...table.get(id)! }),
    // How many due-row SELECTs were made.
    reads: () => read,
  };
}

// BloopCat's endpoint hangs (every attempt can take the full 10 s timeout) and
// a backlog of its rows is due — more than one pass takes of a partner.
describe("a dead partner endpoint never delays another partner's webhooks", () => {
  const bloopRows = Array.from({ length: 25 }, (_, index) =>
    dueRow({
      id: `w-bloop-${index}`,
      partnerId: "bloopcat",
      nextAttemptAt: new Date(NOW.getTime() - 60_000 + index),
    }),
  );
  // Queued later than all of BloopCat's.
  const vectraRow = dueRow({ id: "w-vectra", partnerId: null });

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
      ]),
    };
  });

  function hangingBloopCat() {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const sent: string[] = [];
    const fetchImpl = async (url: string, init: RequestInit) => {
      const id = (init.headers as Record<string, string>)[PARTNER_WEBHOOK_ID_HEADER]!;
      sent.push(id);
      if (url === BLOOP_HOOK) await gate;
      return { ok: true, status: 200, body: null };
    };
    return { fetchImpl, release: () => release(), sent };
  }

  it("delivers Vectra's row on the next pass while BloopCat's backlog still hangs", async () => {
    const lateBloop = dueRow({ id: "w-bloop-late", partnerId: "bloopcat" });
    const fake = leasingDb(
      {
        selects: [
          [
            partnerWebhooks,
            [
              // pass 1: BloopCat's backlog
              bloopRows,
              // pass 2: Vectra's row is due now; BloopCat's untouched rows still are
              [vectraRow, ...bloopRows.slice(1), lateBloop],
              // BloopCat, once free, looks again for what came in meanwhile
              [lateBloop],
            ],
          ],
        ],
      },
      [...bloopRows, vectraRow, lateBloop],
    );
    const { fetchImpl, release, sent } = hangingBloopCat();
    const deliver = () =>
      schedulePartnerWebhookDelivery({ client: fake.db as never, fetchImpl, force: true });

    try {
      deliver();
      await vi.waitFor(() => expect(sent).toEqual(["w-bloop-0"]));

      deliver();
      await vi.waitFor(() => expect(fake.delivered()).toEqual(["w-vectra"]));
      // BloopCat is still stuck on its first row, and nobody else took its rows.
      expect(sent).toEqual(["w-bloop-0", "w-vectra"]);
    } finally {
      release();
    }

    // Free again, BloopCat sends each of its rows exactly once, in order, then
    // the one that was queued while it hung.
    await vi.waitFor(() => expect(fake.delivered()).toHaveLength(27));
    expect(sent.filter((id) => id !== "w-vectra")).toEqual([
      ...bloopRows.map((row) => row.id),
      "w-bloop-late",
    ]);
  });

  it("a sweep tick does not wait for a hanging partner: the next tick still delivers the others", async () => {
    const fake = leasingDb(
      {
        selects: [[partnerWebhooks, [bloopRows.slice(0, 3), [vectraRow]]]],
      },
      [...bloopRows, vectraRow],
    );
    // The loop lock as withLoopLock behaves within one process: a tick that
    // finds the previous one still inside is turned away.
    let inside = false;
    const lock = async <T,>(_loop: string, run: () => Promise<T>) => {
      if (inside) return { acquired: false as const };
      inside = true;
      try {
        return { acquired: true as const, value: await run() };
      } finally {
        inside = false;
      }
    };
    const { fetchImpl, release, sent } = hangingBloopCat();
    const tick = () =>
      void runPartnerWebhookTick({ client: fake.db as never, fetchImpl, lock: lock as never });

    try {
      tick();
      await vi.waitFor(() => expect(sent).toEqual(["w-bloop-0"]));

      tick();
      await vi.waitFor(() => expect(fake.delivered()).toEqual(["w-vectra"]));
    } finally {
      release();
    }
    await vi.waitFor(() => expect(fake.delivered()).toHaveLength(4));
  });

  it("takes each partner's own oldest due rows, so one backlog cannot fill a pass", async () => {
    const base = createFakeDb();
    const queries: Array<{ sql: string; params: unknown[] }> = [];
    const dialect = new PgDialect();
    const db = {
      ...base.db,
      select: () => ({
        from: (table: unknown) => ({
          where: (condition: SQL) => {
            queries.push(dialect.sqlToQuery(condition));
            return base.db.select().from(table);
          },
        }),
      }),
    };

    await selectDuePartnerWebhooksWithDb(db as never, NOW, 20);
    await selectDuePartnerWebhooksWithDb(db as never, NOW, 20, "bloopcat");

    const [all, one] = queries;
    // Ranked within each partner (no partner = Vectra Connect), the first 20 of each.
    expect(all!.sql).toMatch(
      /row_number\(\) over \(\s*partition by coalesce\("(\w+)"\."partner_id", \$\d+\)\s+order by "\1"\."next_attempt_at"/,
    );
    expect(all!.sql).toMatch(/"delivered_at" is null/);
    expect(all!.sql).toMatch(/"rank" <= \$\d+\)/);
    expect(all!.params).toEqual(expect.arrayContaining(["vectra", 20]));
    // One partner's own rows, when its delivery looks again.
    expect(one!.sql).toMatch(/coalesce\("vectra_partner_webhook"\."partner_id", \$\d+\) = \$\d+/);
    expect(one!.params).toEqual(expect.arrayContaining(["vectra", "bloopcat"]));
  });

  it("leases each row by the clock of that moment, not the start of a long pass", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    try {
      vi.setSystemTime(NOW);
      const rows = [
        dueRow({ id: "w-1", partnerId: "bloopcat" }),
        dueRow({ id: "w-2", partnerId: "bloopcat" }),
      ];
      const fake = leasingDb({ selects: [[partnerWebhooks, [rows]]] }, rows);
      const stamps: string[] = [];

      await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        fetchImpl: async (_url: string, init: RequestInit) => {
          stamps.push((init.headers as Record<string, string>)[PARTNER_TIMESTAMP_HEADER]!);
          // This attempt took the full timeout.
          vi.setSystemTime(new Date(Date.now() + 10_000));
          return { ok: true, status: 200, body: null };
        },
      });

      const leases = fake.writes.filter((write) => "attempts" in write.set);
      expect(leases.map((write) => write.set.nextAttemptAt)).toEqual([
        new Date(NOW.getTime() + 60_000),
        new Date(NOW.getTime() + 10_000 + 60_000),
      ]);
      expect(stamps).toEqual([
        String(Math.floor(NOW.getTime() / 1000)),
        String(Math.floor((NOW.getTime() + 10_000) / 1000)),
      ]);
    } finally {
      vi.useRealTimers();
    }
  });
});

// A delivery await that never settles (a socket the 10 s timeout did not
// reach, a database write that hangs) parked that partner in this process for
// good: every later pass found its loop "still running" and only set rerun.
describe("a partner delivery that stops making progress", () => {
  const STALE_MS = 5 * 60 * 1000;
  const BLOOP_WEBHOOK_SECRET = "bloopcat-webhook-secret-0123456789abcdef";
  const at = (ms: number) => new Date(NOW.getTime() + ms);

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
          webhookSecret: BLOOP_WEBHOOK_SECRET,
        },
      ]),
    };
    // Loop health counters are per process; start each test from none.
    setBackgroundLockClientForTest(null);
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // BloopCat's endpoint as one that answers, except for the attempts listed in
  // `hang` (by their order, 1-based): those never settle until released.
  function endpoint(options: { hang?: number[]; spend?: Record<string, number> } = {}) {
    const sent: string[] = [];
    const releases: Array<() => void> = [];
    const fetchImpl = async (_url: string, init: RequestInit) => {
      const id = (init.headers as Record<string, string>)[PARTNER_WEBHOOK_ID_HEADER]!;
      sent.push(id);
      if (options.hang?.includes(sent.length)) {
        await new Promise<void>((resolve) => releases.push(resolve));
      }
      // This attempt took that long.
      const spent = options.spend?.[id];
      if (spent) vi.setSystemTime(new Date(Date.now() + spent));
      return { ok: true, status: 200, body: null };
    };
    return { fetchImpl, sent, release: () => releases.forEach((release) => release()) };
  }

  function flightOf(partnerId: string) {
    return (
      globalThis as { __vectraPartnerWebhookFlights?: Map<string, { rerun: boolean }> }
    ).__vectraPartnerWebhookFlights?.get(partnerId);
  }

  it("replaces a delivery that made no progress for 5 minutes; the next row is delivered", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const rows = [
        dueRow({ id: "w-1", partnerId: "bloopcat" }),
        dueRow({ id: "w-2", partnerId: "bloopcat" }),
      ];
      const fake = leasingDb(
        {
          selects: [
            [
              partnerWebhooks,
              [
                rows,
                // Later: w-1's lease ran out, w-2 was never reached.
                [{ ...rows[0]!, attempts: 1 }, rows[1]!],
              ],
            ],
          ],
        },
        rows,
      );
      const { fetchImpl, sent } = endpoint({ hang: [1] });

      void dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sent).toEqual(["w-1"]));
      const hung = flightOf("bloopcat");
      expect(hung).toBeDefined();

      vi.setSystemTime(at(STALE_MS + 1_000));
      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });

      expect(sent).toEqual(["w-1", "w-1", "w-2"]);
      expect(fake.delivered()).toEqual(["w-1", "w-2"]);
      // The new delivery finished and let go; the hung one holds nothing.
      expect(flightOf("bloopcat")).toBeUndefined();
      // Said once, naming the partner and nothing else of it.
      expect(error).toHaveBeenCalledTimes(1);
      const logged = error.mock.calls[0]!.map(String).join(" ");
      expect(logged).toContain("bloopcat");
      expect(logged).not.toContain(BLOOP_HOOK);
      expect(logged).not.toContain(BLOOP_WEBHOOK_SECRET);
      expect(logged).not.toContain("w-1");
      // The loop health surface counts it.
      expect(loopTickTimes().partnerWebhookDispatcher?.staleFlights).toBe(1);
    } finally {
      error.mockRestore();
    }
  });

  it("leaves a delivery younger than 5 minutes alone: the pass only asks it to look again", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const rows = [
        dueRow({ id: "w-1", partnerId: "bloopcat" }),
        dueRow({ id: "w-2", partnerId: "bloopcat" }),
      ];
      const fake = leasingDb(
        { selects: [[partnerWebhooks, [rows, [rows[1]!]]]] },
        rows,
      );
      const { fetchImpl, sent, release } = endpoint({ hang: [1] });

      const first = dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sent).toEqual(["w-1"]));
      const flight = flightOf("bloopcat");

      vi.setSystemTime(at(STALE_MS - 1_000));
      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });

      expect(sent).toEqual(["w-1"]);
      expect(flightOf("bloopcat")).toBe(flight);
      expect(flight).toMatchObject({ rerun: true });
      expect(error).not.toHaveBeenCalled();
      expect(loopTickTimes().partnerWebhookDispatcher?.staleFlights ?? 0).toBe(0);

      // Once its attempt settles it carries on by itself.
      release();
      await first;
      expect(fake.delivered()).toEqual(["w-1", "w-2"]);
    } finally {
      error.mockRestore();
    }
  });

  it("counts from the last row a delivery finished, not from its start", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const rows = ["w-1", "w-2", "w-3"].map((id) =>
        dueRow({ id, partnerId: "bloopcat" }),
      );
      const fake = leasingDb(
        { selects: [[partnerWebhooks, [rows, [rows[2]!]]]] },
        rows,
      );
      // A slow endpoint, not a dead one: w-1 and w-2 take 3 minutes each.
      const { fetchImpl, sent, release } = endpoint({
        hang: [3],
        spend: { "w-1": 3 * 60_000, "w-2": 3 * 60_000 },
      });

      const first = dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sent).toEqual(["w-1", "w-2", "w-3"]));
      const flight = flightOf("bloopcat");

      // Started 6.5 minutes ago, but its last row finished 30 s ago.
      vi.setSystemTime(at(6 * 60_000 + 30_000));
      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });

      expect(flightOf("bloopcat")).toBe(flight);
      expect(sent).toEqual(["w-1", "w-2", "w-3"]);
      expect(error).not.toHaveBeenCalled();

      release();
      await first;
    } finally {
      error.mockRestore();
    }
  });

  it("a replaced delivery that settles late takes no further row", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const rows = [
        dueRow({ id: "w-1", partnerId: "bloopcat" }),
        dueRow({ id: "w-2", partnerId: "bloopcat" }),
      ];
      // This fake answers every lease (no compare-and-swap), so only the old
      // delivery standing down keeps it off w-2.
      const fake = leasingDb(
        {
          selects: [
            [partnerWebhooks, [rows, [{ ...rows[0]!, attempts: 1 }, rows[1]!]]],
          ],
        },
        rows,
      );
      const { fetchImpl, sent, release } = endpoint({ hang: [1] });

      const first = dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sent).toEqual(["w-1"]));
      vi.setSystemTime(at(STALE_MS + 1_000));
      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      expect(sent).toEqual(["w-1", "w-1", "w-2"]);

      // The hung attempt finally comes back.
      release();
      await first;

      expect(sent).toEqual(["w-1", "w-1", "w-2"]);
      const leasesOfW2 = fake.writes.filter(
        (write) => write.id === "w-2" && "attempts" in write.set,
      );
      expect(leasesOfW2).toHaveLength(1);
      expect(flightOf("bloopcat")).toBeUndefined();
    } finally {
      error.mockRestore();
    }
  });

  // In production the sweep's 30 s passes keep finding the hung flight during
  // its first 5 minutes and set its rerun. When its send finally settles
  // after it was replaced, that rerun must not send it back for more rows.
  it("a replaced delivery that wakes with a rerun stands down: no select, no lease, no third flight", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const current = (table: Map<string, Record<string, unknown>>) => [
        { ...table.get("w-1")! },
      ];
      // Three passes read the row; a fourth read would be the old flight's.
      const fake = webhookTable(
        [dueRow({ id: "w-1", partnerId: "bloopcat" })],
        [current, current, current, current],
      );
      // The first two sends hang until released (the old one then fails);
      // any later send would be answered at once.
      const gates: Array<() => void> = [];
      let sends = 0;
      const fetchImpl = async () => {
        sends += 1;
        const status = sends === 1 ? 500 : 200;
        if (sends <= 2) await new Promise<void>((resolve) => gates.push(resolve));
        return { ok: status === 200, status, body: null };
      };

      const first = dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sends).toBe(1));
      const hung = flightOf("bloopcat")!;

      vi.setSystemTime(at(4 * 60_000));
      await dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      expect(flightOf("bloopcat")).toBe(hung);
      expect(hung.rerun).toBe(true);

      vi.setSystemTime(at(STALE_MS + 1_000));
      const third = dispatchDuePartnerWebhooksWithDb(fake.db as never, { fetchImpl });
      await vi.waitFor(() => expect(sends).toBe(2));
      const successor = flightOf("bloopcat")!;
      expect(successor).not.toBe(hung);

      // The old send comes back, its rerun still set, while the new one hangs.
      gates[0]!();
      await first;

      expect(fake.reads()).toBe(3);
      expect(sends).toBe(2);
      // Two leases: the old flight's and its successor's.
      expect(fake.row("w-1")).toMatchObject({ attempts: 2, deliveredAt: null });
      expect(flightOf("bloopcat")).toBe(successor);

      gates[1]!();
      await third;
      expect(fake.row("w-1").deliveredAt).toBeInstanceOf(Date);
      expect(flightOf("bloopcat")).toBeUndefined();
    } finally {
      error.mockRestore();
    }
  });

  // The replaced flight's hung send settles only after the new flight has
  // re-leased the row (attempts moved on) and written its own outcome. What
  // the old send learned is about a lease that is no longer the row's.
  describe("its late outcome never overwrites the row's new lease", () => {
    // BloopCat answers each attempt with the next status; the first one only
    // once released.
    function lateEndpoint(statuses: number[]) {
      let release: () => void = () => undefined;
      const gate = new Promise<void>((resolve) => (release = resolve));
      let calls = 0;
      const fetchImpl = async () => {
        const status = statuses[calls] ?? 200;
        calls += 1;
        if (calls === 1) await gate;
        return { ok: status >= 200 && status < 300, status, body: null };
      };
      return { fetchImpl, release: () => release(), calls: () => calls };
    }

    // Pass 1 at NOW sends attempt 1, which hangs; pass 2 after 5 min replaces
    // the flight, which re-leases the row (its lease ran out) and sends attempt
    // 2; then attempt 1 comes back.
    async function hangThenReplace(
      row: ReturnType<typeof dueRow>,
      statuses: number[],
    ) {
      vi.setSystemTime(NOW);
      const fake = webhookTable([row], [
        (table) => [{ ...table.get(row.id)! }],
        (table) => [{ ...table.get(row.id)! }],
      ]);
      const endpoint = lateEndpoint(statuses);
      const first = dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        fetchImpl: endpoint.fetchImpl,
      });
      await vi.waitFor(() => expect(endpoint.calls()).toBe(1));
      vi.setSystemTime(at(STALE_MS + 1_000));
      const newSummary = await dispatchDuePartnerWebhooksWithDb(fake.db as never, {
        fetchImpl: endpoint.fetchImpl,
      });
      expect(endpoint.calls()).toBe(2);
      const afterNewFlight = fake.row(row.id);
      endpoint.release();
      // The pass that started the old flight resolves once it is done.
      const oldSummary = await first;
      return { fake, afterNewFlight, oldSummary, newSummary };
    }

    it("a late error leaves lastError, nextAttemptAt and attempts as the new flight wrote them", async () => {
      const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        // Attempt 1 (old flight) fails late with 500; attempt 2 (new flight)
        // fails at once with 503 and is rescheduled.
        const { fake, afterNewFlight } = await hangThenReplace(
          dueRow({ id: "w-1", partnerId: "bloopcat" }),
          [500, 503],
        );

        expect(afterNewFlight).toMatchObject({
          attempts: 2,
          lastStatus: 503,
          lastError: "HTTP 503",
          nextAttemptAt: at(STALE_MS + 1_000 + 120_000),
          deliveredAt: null,
        });
        expect(fake.row("w-1")).toEqual(afterNewFlight);
      } finally {
        error.mockRestore();
      }
    });

    it("a late failure at the last attempt gives nothing up and journals nothing once the row was re-leased", async () => {
      const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        // Attempt 1 of this flight is the row's last (attempts reaches the
        // maximum); the new flight's re-lease delivers it.
        const { fake, afterNewFlight } = await hangThenReplace(
          dueRow({
            id: "w-1",
            partnerId: "bloopcat",
            attempts: PARTNER_WEBHOOK_MAX_ATTEMPTS - 1,
          }),
          [500, 200],
        );

        expect(afterNewFlight).toMatchObject({
          attempts: PARTNER_WEBHOOK_MAX_ATTEMPTS + 1,
          lastStatus: 200,
          lastError: null,
          nextAttemptAt: null,
        });
        expect(afterNewFlight.deliveredAt).toBeInstanceOf(Date);
        expect(fake.row("w-1")).toEqual(afterNewFlight);
        expect(fake.inserts(eventLog)).toEqual([]);
      } finally {
        error.mockRestore();
      }
    });

    it("a late success still counts: the event reached the partner", async () => {
      const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        // The new flight's attempt fails and is rescheduled; then the old
        // attempt turns out to have been accepted.
        const { fake } = await hangThenReplace(
          dueRow({ id: "w-1", partnerId: "bloopcat" }),
          [200, 503],
        );

        expect(fake.row("w-1")).toMatchObject({
          attempts: 2,
          lastStatus: 200,
          lastError: null,
          nextAttemptAt: null,
        });
        expect(fake.row("w-1").deliveredAt).toBeInstanceOf(Date);
      } finally {
        error.mockRestore();
      }
    });

    it("counts a late success as delivered only when it marked the row", async () => {
      const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        // The new flight delivered first: the old 409 marks nothing.
        const already = await hangThenReplace(
          dueRow({ id: "w-1", partnerId: "bloopcat" }),
          [409, 200],
        );
        expect(already.newSummary).toMatchObject({ attempted: 1, delivered: 1 });
        expect(already.oldSummary).toMatchObject({ attempted: 1, delivered: 0 });

        // The new flight's attempt failed: the old 200 is what marks it.
        const late = await hangThenReplace(
          dueRow({ id: "w-2", partnerId: "bloopcat" }),
          [200, 503],
        );
        expect(late.newSummary).toMatchObject({ delivered: 0, rescheduled: 1 });
        expect(late.oldSummary).toMatchObject({ delivered: 1 });
      } finally {
        error.mockRestore();
      }
    });

    it("a late success does not restamp a row the new flight already delivered", async () => {
      const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        const { fake, afterNewFlight } = await hangThenReplace(
          dueRow({ id: "w-1", partnerId: "bloopcat" }),
          [409, 200],
        );

        expect(afterNewFlight).toMatchObject({ attempts: 2, lastStatus: 200 });
        expect(fake.row("w-1")).toEqual(afterNewFlight);
      } finally {
        error.mockRestore();
      }
    });
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
