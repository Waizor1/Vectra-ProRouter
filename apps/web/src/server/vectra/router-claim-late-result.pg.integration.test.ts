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

// recordJobResult works on the module's db: here, the real database, with a
// barrier the test can arm. Armed, it holds the result right after its job
// row turns terminal (succeeded / failed) — where R120-1 puts the race.
const barrier = vi.hoisted(() => ({
  real: undefined as unknown as Record<string, unknown>,
  jobsTable: undefined as unknown,
  afterTerminalJobUpdate: null as null | (() => Promise<void>),
}));
vi.mock("~/server/db", () => ({
  db: new Proxy(
    {},
    {
      get(_target, property) {
        const real = barrier.real as Record<string | symbol, unknown> & {
          update: (table: unknown) => {
            set: (values: Record<string, unknown>) => {
              where: (condition: unknown) => PromiseLike<unknown> & {
                returning: (...args: unknown[]) => unknown;
              };
            };
          };
        };
        if (property !== "update") {
          const value = real[property];
          return typeof value === "function" ? value.bind(real) : value;
        }
        return (table: unknown) => {
          const builder = real.update(table);
          if (table !== barrier.jobsTable) return builder;
          return {
            set(values: Record<string, unknown>) {
              const set = builder.set(values);
              return {
                where(condition: unknown) {
                  const query = set.where(condition);
                  const pause =
                    values.state === "failed" || values.state === "succeeded"
                      ? barrier.afterTerminalJobUpdate
                      : null;
                  if (!pause) return query;
                  barrier.afterTerminalJobUpdate = null;
                  return {
                    returning: (...args: unknown[]) => query.returning(...args),
                    then<T1, T2>(
                      onfulfilled?: ((value: unknown) => T1 | PromiseLike<T1>) | null,
                      onrejected?: ((reason: unknown) => T2 | PromiseLike<T2>) | null,
                    ) {
                      return Promise.resolve(query)
                        .then(async (rows) => {
                          await pause();
                          return rows;
                        })
                        .then(onfulfilled, onrejected);
                    },
                  };
                },
              };
            },
          };
        };
      },
    },
  ),
}));

import postgres from "postgres";
import { and, eq, inArray, like } from "drizzle-orm";
import { drizzle } from "drizzle-orm/postgres-js";
import {
  partnerRouterClaimRequestSchema,
  VECTRA_PROTOCOL_VERSION,
} from "@vectra/contracts";
import * as schema from "@vectra/db";

import { recordJobResult } from "./router-control";
import { claimRouterWithDb, unbindRouterClaimWithDb } from "./router-claims";

/**
 * R120-1 against a REAL PostgreSQL, with a real barrier: a result of claim A's
 * job is held right after its job row turns terminal; meanwhile the owner
 * unbinds the router (which no longer cancels that job) and claims it again
 * (B: same router, same ownerRef, new claim id); then A's result carries on.
 * What it reports must say A — never B — and B's router must be left as B's
 * claim left it.
 *
 * Gate: PANEL_PARTNER_PG_URL, loopback hosts only (a disposable database with
 * the migrations applied). It is read from nowhere else.
 */
const url = process.env.PANEL_PARTNER_PG_URL;

const MARK = "pg-claim-late-";
const OWNER = "acct-claim-late";
const DEVICE_KEY = Buffer.alloc(32, 6).toString("base64");
const NONCE = Buffer.alloc(16, 2).toString("base64");

describe.skipIf(!url)("real PostgreSQL: a late result of a re-claimed router's old job", () => {
  let sql: ReturnType<typeof postgres>;
  let db: ReturnType<typeof drizzle<typeof schema>>;

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
    db = drizzle(sql, { schema });
    barrier.real = db as unknown as Record<string, unknown>;
    barrier.jobsTable = schema.jobs;
    await removeRouters(await markedRouterIds());
  });

  afterAll(async () => {
    barrier.afterTerminalJobUpdate = null;
    if (db) await removeRouters(await markedRouterIds());
    await sql?.end();
  });

  function claimByDevice(deviceIdentifier: string) {
    return claimRouterWithDb(
      db as never,
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

  // A's claim, then the job whose result will come late.
  async function claimA(kind: "apply" | "action") {
    const deviceIdentifier = `${MARK}${randomUUID().slice(0, 8)}`;
    const claimed = await claimByDevice(deviceIdentifier);
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
   * Run A's result up to the barrier, unbind A and claim B there, then let it
   * finish. Returns B's id and B's router as the re-claim left it.
   */
  async function raceReclaim(
    a: Awaited<ReturnType<typeof claimA>>,
    report: Record<string, unknown>,
  ) {
    let release!: () => void;
    const reached = new Promise<void>((resolve) => {
      barrier.afterTerminalJobUpdate = () => {
        resolve();
        return new Promise<void>((resume) => {
          release = resume;
        });
      };
    });
    const pending = recordJobResult(a.routerId, {
      protocolVersion: VECTRA_PROTOCOL_VERSION,
      routerId: a.routerId,
      jobId: a.job.id,
      ...report,
    });
    await Promise.race([
      reached,
      pending.then(() => {
        throw new Error("the result finished without reaching the barrier");
      }),
    ]);

    // Held after the job update: the job is already terminal…
    const [held] = await db
      .select({ state: schema.jobs.state })
      .from(schema.jobs)
      .where(eq(schema.jobs.id, a.job.id));
    expect(["failed", "succeeded"]).toContain(held?.state);
    // …so the unbind leaves it as it is,
    expect(
      await unbindRouterClaimWithDb(db as never, {
        routerId: a.routerId,
        ownerRef: OWNER,
      }),
    ).toMatchObject({ ok: true });
    // and the same owner claims the same router again: claim B.
    const reclaimed = await claimByDevice(a.deviceIdentifier);
    const claimB = (reclaimed as { body: { claimId: string; routerId: string } })
      .body;
    expect(claimB.routerId).toBe(a.routerId);
    expect(claimB.claimId).not.toBe(a.claimId);
    const routerOnB = await routerRow(a.routerId);
    const incidentsOnB = await db
      .select()
      .from(schema.healthIncidents)
      .where(eq(schema.healthIncidents.routerId, a.routerId));

    release();
    await pending;
    return { claimB: claimB.claimId, routerOnB, incidentsOnB };
  }

  async function eventsOf(routerId: string, event: "router.ready" | "router.failed" | "router.action") {
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

  // B's router as the re-claim left it; a partner action's report may only
  // stamp updated_at (its row lock), nothing of the claim's state.
  function expectUntouched(
    before: typeof schema.routers.$inferSelect,
    after: typeof schema.routers.$inferSelect,
  ) {
    const { updatedAt: _beforeUpdatedAt, ...beforeState } = before;
    const { updatedAt: _afterUpdatedAt, ...afterState } = after;
    expect(afterState).toEqual(beforeState);
  }

  it("a failed apply of claim A, held after its job update: router.failed says A, B's router is untouched", async () => {
    const a = await claimA("apply");

    const { claimB, routerOnB, incidentsOnB } = await raceReclaim(a, {
      status: "failure",
      result: { error: "provider config: http 403", enteredDirectMode: true },
    });

    expect(await eventsOf(a.routerId, "router.failed")).toEqual([
      expect.objectContaining({ ownerRef: OWNER, claimId: a.claimId }),
    ]);
    expect(JSON.stringify(await eventsOf(a.routerId, "router.failed"))).not.toContain(claimB);
    const after = await routerRow(a.routerId);
    expect(after.claimId).toBe(claimB);
    expect(after.updatedAt).toEqual(routerOnB.updatedAt);
    expectUntouched(routerOnB, after);
    // No direct mode, no incident on B's router.
    expect(
      await db
        .select()
        .from(schema.healthIncidents)
        .where(eq(schema.healthIncidents.routerId, a.routerId)),
    ).toEqual(incidentsOnB);
  });

  it("a succeeded apply of claim A, held after its job update: router.ready says A, A's revision never becomes B's", async () => {
    const a = await claimA("apply");

    const { claimB, routerOnB } = await raceReclaim(a, {
      status: "success",
      appliedRevisionId: a.job.desiredRevisionId,
    });

    expect(await eventsOf(a.routerId, "router.ready")).toEqual([
      expect.objectContaining({ ownerRef: OWNER, claimId: a.claimId }),
    ]);
    expect(JSON.stringify(await eventsOf(a.routerId, "router.ready"))).not.toContain(claimB);
    const after = await routerRow(a.routerId);
    expect(after.updatedAt).toEqual(routerOnB.updatedAt);
    expectUntouched(routerOnB, after);
    expect(after.activeRevisionId).not.toBe(a.job.desiredRevisionId);
    // A's revision stays as the unbind left it (discarded), not "applied".
    const [revisionA] = await db
      .select({ status: schema.passwallDesiredRevisions.status })
      .from(schema.passwallDesiredRevisions)
      .where(eq(schema.passwallDesiredRevisions.id, a.job.desiredRevisionId!));
    expect(revisionA?.status).toBe("discarded");
  });

  it("an owner's action of claim A, held after its job update: router.action says A, B's router state is untouched", async () => {
    const a = await claimA("action");

    const { claimB, routerOnB } = await raceReclaim(a, { status: "success" });

    expect(await eventsOf(a.routerId, "router.action")).toEqual([
      expect.objectContaining({
        ownerRef: OWNER,
        claimId: a.claimId,
        detail: expect.objectContaining({ actionId: a.job.id, state: "applied" }),
      }),
    ]);
    expect(JSON.stringify(await eventsOf(a.routerId, "router.action"))).not.toContain(claimB);
    expectUntouched(routerOnB, await routerRow(a.routerId));
  });
});
