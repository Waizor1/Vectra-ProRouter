import { randomUUID } from "node:crypto";

import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "router-claim-late-pg-test-secrets-key-0123456789",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET: "router-claim-late-pg-test-webhook-0123456789",
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);

/**
 * recordJobResult works on the module's db: here, the real database behind a
 * probe. Armed, the probe holds the result right after one query of its own
 * returns — every transaction it opens is probed as well — so the test can
 * act in between:
 * - "before the gate": after the result's own record (vectra_job_result) is
 *   stored, before anything is judged;
 * - "after the gate": after the result's second read of the router — the one
 *   its claim gate is judged on (in the fixed code, the SELECT … FOR UPDATE
 *   inside the transaction that also does every write).
 */
type Hold =
  | { at: "insert"; table: unknown; pause: () => Promise<void> }
  | { at: "router-read"; remaining: number; pause: () => Promise<void> };
const probe = vi.hoisted(() => ({
  real: undefined as unknown,
  routersTable: undefined as unknown,
  hold: null as Hold | null,
}));

type Chain = Record<string | symbol, unknown> & PromiseLike<unknown>;

// Every chained call keeps the hold; awaiting the query runs it, then holds.
function holdAfter(query: Chain, pause: () => Promise<void>): Chain {
  return new Proxy(query, {
    get(target, property) {
      if (property === "then") {
        return (
          onfulfilled?: (value: unknown) => unknown,
          onrejected?: (reason: unknown) => unknown,
        ) =>
          target
            .then(async (rows) => {
              await pause();
              return rows;
            })
            .then(onfulfilled, onrejected);
      }
      const value = Reflect.get(target, property) as unknown;
      if (typeof value !== "function") return value;
      return (...args: unknown[]) => {
        const next = (value as (...a: unknown[]) => unknown).apply(target, args);
        return next === target ? holdAfter(target, pause) : next;
      };
    },
  }) as Chain;
}

function probed<T extends object>(client: T): T {
  return new Proxy(client, {
    get(target, property) {
      const value = Reflect.get(target, property) as unknown;
      if (property === "transaction") {
        return (run: (tx: unknown) => unknown, ...rest: unknown[]) =>
          (value as (...a: unknown[]) => unknown).call(
            target,
            (tx: object) => run(probed(tx)),
            ...rest,
          );
      }
      if (property === "select") {
        return (...args: unknown[]) => {
          const selection = (value as (...a: unknown[]) => Chain).apply(target, args);
          return new Proxy(selection, {
            get(selectTarget, selectProperty) {
              const member = Reflect.get(selectTarget, selectProperty) as unknown;
              if (selectProperty !== "from" || typeof member !== "function") {
                return typeof member === "function" ? member.bind(selectTarget) : member;
              }
              return (table: unknown, ...rest: unknown[]) => {
                const query = (member as (...a: unknown[]) => Chain).call(selectTarget, table, ...rest);
                const hold = probe.hold;
                if (hold?.at !== "router-read" || table !== probe.routersTable) return query;
                hold.remaining -= 1;
                if (hold.remaining > 0) return query;
                probe.hold = null;
                return holdAfter(query, hold.pause);
              };
            },
          });
        };
      }
      if (property === "insert") {
        return (table: unknown) => {
          const builder = (value as (t: unknown) => Chain).call(target, table);
          const hold = probe.hold;
          if (hold?.at !== "insert" || hold.table !== table) return builder;
          return new Proxy(builder, {
            get(insertTarget, insertProperty) {
              const member = Reflect.get(insertTarget, insertProperty) as unknown;
              if (insertProperty !== "values" || typeof member !== "function") {
                return typeof member === "function" ? member.bind(insertTarget) : member;
              }
              return (...args: unknown[]) => {
                probe.hold = null;
                return holdAfter(
                  (member as (...a: unknown[]) => Chain).apply(insertTarget, args),
                  hold.pause,
                );
              };
            },
          });
        };
      }
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
}

vi.mock("~/server/db", () => ({
  db: new Proxy(
    {},
    {
      get(_target, property) {
        const client = probed(probe.real as object) as Record<string | symbol, unknown>;
        return client[property];
      },
    },
  ),
}));

import postgres from "postgres";
import { and, eq, inArray, like, sql as sqlTag } from "drizzle-orm";
import { drizzle } from "drizzle-orm/postgres-js";
import {
  partnerRouterClaimRequestSchema,
  VECTRA_PROTOCOL_VERSION,
} from "@vectra/contracts";
import * as schema from "@vectra/db";

import { recordJobResult } from "./router-control";
import { claimRouterWithDb, unbindRouterClaimWithDb } from "./router-claims";

/**
 * R120-1 and R129-1 against a REAL PostgreSQL. A result of claim A's job is
 * held at a barrier; meanwhile the owner unbinds the router and claims it
 * again (B: same router, same ownerRef, new claim id) from ANOTHER session;
 * then A's result carries on. Whatever A reports says A — never B — and
 * nothing of A lands in the router once it is on B: either the re-claim waits
 * for A (serialized on the router row) or A changes nothing.
 *
 * Gate: PANEL_PARTNER_PG_URL, loopback hosts only (a disposable database with
 * the migrations applied). It is read from nowhere else.
 */
const url = process.env.PANEL_PARTNER_PG_URL;

const MARK = "pg-claim-late-";
const OWNER = "acct-claim-late";
const DEVICE_KEY = Buffer.alloc(32, 6).toString("base64");
const NONCE = Buffer.alloc(16, 2).toString("base64");
// How long the re-claim is given to get through while A is held: plenty for
// a free row, and well under the other session's lock_timeout (5 s).
const RECLAIM_GRACE_MS = 750;

type Db = ReturnType<typeof drizzle<typeof schema>>;

describe.skipIf(!url)("real PostgreSQL: a late result of a re-claimed router's old job", () => {
  let sql: ReturnType<typeof postgres>;
  let otherSql: ReturnType<typeof postgres>;
  let db: Db;
  // The owner's unbind and re-claim run here: another session, which gives
  // up on a row lock after 5 s instead of waiting for ever.
  let other: Db;

  async function markedRouterIds() {
    return (
      await db
        .select({ id: schema.routers.id })
        .from(schema.routers)
        .where(like(schema.routers.deviceIdentifier, `${MARK}%`))
    ).map((row) => row.id);
  }

  async function removeRouters(ids: string[]) {
    if (ids.length === 0) return;
    await db.delete(schema.eventLog).where(inArray(schema.eventLog.routerId, ids));
    await db
      .delete(schema.partnerWebhooks)
      .where(inArray(schema.partnerWebhooks.routerId, ids));
    await db.delete(schema.routers).where(inArray(schema.routers.id, ids));
  }

  beforeAll(async () => {
    const host = new URL(url!).hostname;
    if (host !== "127.0.0.1" && host !== "localhost")
      throw new Error(
        "PANEL_PARTNER_PG_URL must point at a loopback host (127.0.0.1 or localhost)",
      );
    sql = postgres(url!, { max: 5, onnotice: () => undefined });
    otherSql = postgres(url!, {
      max: 2,
      onnotice: () => undefined,
      connection: { lock_timeout: 5000 },
    });
    db = drizzle(sql, { schema });
    other = drizzle(otherSql, { schema });
    probe.real = db;
    probe.routersTable = schema.routers;
    const [setting] = await otherSql`show lock_timeout`;
    expect(setting?.lock_timeout).toBe("5s");
    await removeRouters(await markedRouterIds());
  });

  afterAll(async () => {
    probe.hold = null;
    if (db) await removeRouters(await markedRouterIds());
    await sql?.end();
    await otherSql?.end();
  });

  function claimByDevice(client: Db, deviceIdentifier: string) {
    return claimRouterWithDb(
      client as never,
      partnerRouterClaimRequestSchema.parse({
        device: { deviceIdentifier, devicePublicKey: DEVICE_KEY, nonce: NONCE },
        owner: { ref: OWNER, label: "ow***" },
        subscription: { url: "https://sub.example.test/api/sub/LATE_TOKEN" },
      }),
    );
  }

  async function routerRow(routerId: string) {
    const [row] = await db
      .select()
      .from(schema.routers)
      .where(eq(schema.routers.id, routerId));
    return row!;
  }

  // Everything a job result could change on the router, as it is now.
  async function routerState(routerId: string) {
    const { updatedAt: _updatedAt, ...router } = await routerRow(routerId);
    const incidents = await db
      .select({
        id: schema.healthIncidents.id,
        type: schema.healthIncidents.type,
        state: schema.healthIncidents.state,
      })
      .from(schema.healthIncidents)
      .where(eq(schema.healthIncidents.routerId, routerId))
      .orderBy(schema.healthIncidents.id);
    const applied = await db
      .select({
        id: schema.passwallAppliedRevisions.id,
        result: schema.passwallAppliedRevisions.result,
      })
      .from(schema.passwallAppliedRevisions)
      .where(eq(schema.passwallAppliedRevisions.routerId, routerId))
      .orderBy(schema.passwallAppliedRevisions.id);
    const desired = await db
      .select({
        id: schema.passwallDesiredRevisions.id,
        status: schema.passwallDesiredRevisions.status,
      })
      .from(schema.passwallDesiredRevisions)
      .where(eq(schema.passwallDesiredRevisions.routerId, routerId))
      .orderBy(schema.passwallDesiredRevisions.id);
    const rescue = await db
      .select({
        id: schema.rescueCases.id,
        state: schema.rescueCases.state,
        attempts: sqlTag<number>`jsonb_array_length(${schema.rescueCases.repairAttempts})`,
      })
      .from(schema.rescueCases)
      .where(eq(schema.rescueCases.routerId, routerId))
      .orderBy(schema.rescueCases.id);
    return { router, incidents, applied, desired, rescue };
  }

  type Kind = "apply" | "action" | "legacy-apply";

  // A's claim, then the job whose result will come late.
  async function claimA(kind: Kind) {
    const deviceIdentifier = `${MARK}${randomUUID().slice(0, 8)}`;
    const claimed = await claimByDevice(db, deviceIdentifier);
    const { routerId, claimId } = (
      claimed as { body: { routerId: string; claimId: string } }
    ).body;
    const [apply] = await db
      .select()
      .from(schema.jobs)
      .where(
        and(
          eq(schema.jobs.routerId, routerId),
          eq(schema.jobs.type, "apply_xray_config"),
        ),
      );
    // The claim's apply job recorded its claim when it was queued.
    expect(apply?.partnerClaim).toEqual({
      claimId,
      ownerRef: OWNER,
      partnerId: "vectra",
    });
    if (kind === "legacy-apply") {
      // As if queued before jobs recorded their claim.
      const [legacy] = await db
        .update(schema.jobs)
        .set({ partnerClaim: null })
        .where(eq(schema.jobs.id, apply!.id))
        .returning();
      return { deviceIdentifier, routerId, claimId, job: legacy! };
    }
    if (kind === "apply") {
      return { deviceIdentifier, routerId, claimId, job: apply! };
    }
    // An owner's action under claim A, as queuePartnerActionWithDb stores it.
    const actionId = randomUUID();
    const [action] = await db
      .insert(schema.jobs)
      .values({
        id: actionId,
        routerId,
        type: "reload_xray_outbound",
        state: "running",
        payload: {
          origin: "partner_action",
          actionId,
          action: "restart_vpn",
          ownerRef: OWNER,
          partnerId: "vectra",
        },
        partnerClaim: { claimId, ownerRef: OWNER, partnerId: "vectra" },
      })
      .returning();
    return { deviceIdentifier, routerId, claimId, job: action! };
  }

  /**
   * Hold A's result at `where`, let the owner unbind A and claim B from the
   * other session, give that RECLAIM_GRACE_MS, then let A go on. Records the
   * order things finished in, and the router's state the moment B's claim was
   * done.
   */
  async function raceReclaim(
    a: Awaited<ReturnType<typeof claimA>>,
    report: Record<string, unknown>,
    where: "before-gate" | "after-gate",
  ) {
    const order: string[] = [];
    let release!: () => void;
    const reached = new Promise<void>((resolve) => {
      const pause = () => {
        resolve();
        return new Promise<void>((resume) => {
          release = resume;
        });
      };
      probe.hold =
        where === "before-gate"
          ? { at: "insert", table: schema.jobResults, pause }
          : { at: "router-read", remaining: 2, pause };
    });
    const pending = recordJobResult(a.routerId, {
      protocolVersion: VECTRA_PROTOCOL_VERSION,
      routerId: a.routerId,
      jobId: a.job.id,
      ...report,
    }).then(() => {
      order.push("A's result done");
    });
    await Promise.race([
      reached,
      pending.then(() => {
        throw new Error(`A's result finished without reaching the ${where} barrier`);
      }),
    ]);
    order.push(`A held ${where}`);

    const reclaim = (async () => {
      const unbound = await unbindRouterClaimWithDb(other as never, {
        routerId: a.routerId,
        ownerRef: OWNER,
      });
      order.push("unbind A done");
      const reclaimed = await claimByDevice(other, a.deviceIdentifier);
      order.push("claim B done");
      return { unbound, reclaimed, stateOnB: await routerState(a.routerId) };
    })();
    const meanwhile = await Promise.race([
      reclaim.then(() => "re-claimed while A was held"),
      new Promise((resolve) =>
        setTimeout(() => resolve("re-claim waiting for A"), RECLAIM_GRACE_MS),
      ),
    ]);
    order.push(String(meanwhile));
    release();
    await pending;
    const { unbound, reclaimed, stateOnB } = await reclaim;
    expect(unbound).toMatchObject({ ok: true });
    const claimB = (reclaimed as { body: { claimId: string; routerId: string } })
      .body;
    expect(claimB.routerId).toBe(a.routerId);
    expect(claimB.claimId).not.toBe(a.claimId);
    return { order, claimB: claimB.claimId, stateOnB };
  }

  async function eventsOf(
    routerId: string,
    event: "router.ready" | "router.failed" | "router.action",
  ) {
    return (
      await db
        .select()
        .from(schema.partnerWebhooks)
        .where(
          and(
            eq(schema.partnerWebhooks.routerId, routerId),
            eq(schema.partnerWebhooks.event, event),
          ),
        )
    ).map((row) => row.payload as Record<string, unknown>);
  }

  const failure = {
    status: "failure",
    result: { error: "provider config: http 403", enteredDirectMode: true },
  };

  describe("held BEFORE the claim gate (R120-1)", () => {
    it("a failed apply of claim A: router.failed says A, and B's router is what B's claim made it", async () => {
      const a = await claimA("apply");

      const { claimB, stateOnB } = await raceReclaim(a, failure, "before-gate");

      expect(await eventsOf(a.routerId, "router.failed")).toEqual([
        expect.objectContaining({ ownerRef: OWNER, claimId: a.claimId }),
      ]);
      expect(JSON.stringify(await eventsOf(a.routerId, "router.failed"))).not.toContain(claimB);
      expect(await routerState(a.routerId)).toEqual(stateOnB);
      expect(stateOnB.router.claimId).toBe(claimB);
      expect(stateOnB.incidents).toEqual([]);
    }, 20_000);

    it("a succeeded apply of claim A: router.ready says A, and A's revision never becomes B's", async () => {
      const a = await claimA("apply");

      const { claimB, stateOnB } = await raceReclaim(
        a,
        { status: "success", appliedRevisionId: a.job.desiredRevisionId },
        "before-gate",
      );

      expect(await eventsOf(a.routerId, "router.ready")).toEqual([
        expect.objectContaining({ ownerRef: OWNER, claimId: a.claimId }),
      ]);
      expect(JSON.stringify(await eventsOf(a.routerId, "router.ready"))).not.toContain(claimB);
      const final = await routerState(a.routerId);
      expect(final).toEqual(stateOnB);
      expect(final.router.activeRevisionId).not.toBe(a.job.desiredRevisionId);
      expect(final.applied).toEqual([]);
    }, 20_000);

    it("an owner's action of claim A: router.action says A, and B's router is what B's claim made it", async () => {
      const a = await claimA("action");

      const { claimB, stateOnB } = await raceReclaim(
        a,
        { status: "success", appliedRevisionId: a.job.desiredRevisionId ?? undefined },
        "before-gate",
      );

      expect(await eventsOf(a.routerId, "router.action")).toEqual([
        expect.objectContaining({
          ownerRef: OWNER,
          claimId: a.claimId,
          detail: expect.objectContaining({ actionId: a.job.id, state: "applied" }),
        }),
      ]);
      expect(JSON.stringify(await eventsOf(a.routerId, "router.action"))).not.toContain(claimB);
      expect(await routerState(a.routerId)).toEqual(stateOnB);
    }, 20_000);
  });

  // R129-1: the gate has passed (the job saw its router still on A); THEN the
  // owner tries to unbind A and claim B. Nothing A writes may land under B:
  // the re-claim must wait for A's transaction (it holds the router row), and
  // whatever A wrote then is from before B existed.
  describe("held AFTER the claim gate passed (R129-1)", () => {
    function expectSerialized(order: string[]) {
      expect(order).toEqual([
        "A held after-gate",
        "re-claim waiting for A",
        // A's transaction ends on release; only then can the unbind take the
        // router row. (A's result may still be queueing its event.)
        ...order.slice(2),
      ]);
      expect(order.indexOf("unbind A done")).toBeGreaterThan(
        order.indexOf("re-claim waiting for A"),
      );
      expect(order.indexOf("claim B done")).toBeGreaterThan(
        order.indexOf("unbind A done"),
      );
    }

    it("a failed apply of claim A: nothing of A lands once B's claim is done, and the re-claim waited for A", async () => {
      const a = await claimA("apply");
      const atStart = await routerState(a.routerId);

      const { order, claimB, stateOnB } = await raceReclaim(a, failure, "after-gate");

      // No incident, no direct mode, no revision change after B's claim.
      expect(await routerState(a.routerId)).toEqual(stateOnB);
      expect(stateOnB.router.claimId).toBe(claimB);
      expectSerialized(order);
      // A's result was A's: its incident was opened while A was the claim,
      // before the unbind, and its event says A.
      expect(stateOnB.incidents.length).toBe(atStart.incidents.length + 1);
      expect(await eventsOf(a.routerId, "router.failed")).toEqual([
        expect.objectContaining({ ownerRef: OWNER, claimId: a.claimId }),
      ]);
    }, 20_000);

    it("a succeeded apply of claim A: no revision history changes under B, and the re-claim waited for A", async () => {
      const a = await claimA("apply");

      const { order, claimB, stateOnB } = await raceReclaim(
        a,
        { status: "success", appliedRevisionId: a.job.desiredRevisionId },
        "after-gate",
      );

      const final = await routerState(a.routerId);
      expect(final).toEqual(stateOnB);
      expect(final.router.claimId).toBe(claimB);
      // B's claim starts from nothing applied: A's revision is not B's.
      expect(final.router.activeRevisionId).toBeNull();
      expectSerialized(order);
      expect(await eventsOf(a.routerId, "router.ready")).toEqual([
        expect.objectContaining({ claimId: a.claimId }),
      ]);
    }, 20_000);

    it("an owner's action of claim A: nothing of A lands once B's claim is done, and the re-claim waited for A", async () => {
      const a = await claimA("action");
      const [revisionA] = await db
        .select({ id: schema.passwallDesiredRevisions.id })
        .from(schema.passwallDesiredRevisions)
        .where(eq(schema.passwallDesiredRevisions.routerId, a.routerId));

      // The router reports the revision it runs with the action's result.
      const { order, claimB, stateOnB } = await raceReclaim(
        a,
        { status: "success", appliedRevisionId: revisionA!.id },
        "after-gate",
      );

      const final = await routerState(a.routerId);
      expect(final).toEqual(stateOnB);
      expect(final.router.claimId).toBe(claimB);
      expect(final.router.activeRevisionId).toBeNull();
      expectSerialized(order);
      expect(await eventsOf(a.routerId, "router.action")).toEqual([
        expect.objectContaining({ claimId: a.claimId }),
      ]);
    }, 20_000);

    it("an apply queued before jobs recorded their claim changes nothing at all (fail closed), and says its claim is unknown", async () => {
      const a = await claimA("legacy-apply");
      const atStart = await routerState(a.routerId);

      const { order, stateOnB } = await raceReclaim(a, failure, "after-gate");

      const final = await routerState(a.routerId);
      expect(final).toEqual(stateOnB);
      // Not even under A: no incident was opened, no revision touched by it.
      expect(final.incidents).toEqual(atStart.incidents);
      expect(final.applied).toEqual([]);
      expectSerialized(order);
      const [event] = await eventsOf(a.routerId, "router.failed");
      expect(event).toMatchObject({ claimGeneration: "unknown" });
      expect(event).not.toHaveProperty("claimId");
      // The job's own record is still kept.
      const [job] = await db
        .select({ state: schema.jobs.state })
        .from(schema.jobs)
        .where(eq(schema.jobs.id, a.job.id));
      expect(job?.state).toBe("failed");
    }, 20_000);
  });
});
