import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
vi.mock("~/server/db", () => ({ db: {} }));
import postgres from "postgres";
import { and, eq, inArray, like } from "drizzle-orm";
import { drizzle } from "drizzle-orm/postgres-js";
import * as schema from "@vectra/db";
import {
  cancelPartnerActionWithDb,
  queuePartnerActionWithDb,
  readPartnerRoutersWithDb,
} from "./partner-routers";
import { routerOwnedByPartner } from "./partner-scope";
import { unbindRouterClaimWithDb } from "./router-claims";

/**
 * The unit tests run on createFakeDb, which never evaluates a WHERE: only the
 * explicit JS ownership checks are pinned there. This file proves the SQL
 * half against a REAL PostgreSQL — partners that share one ownerRef must never
 * see, act on, cancel or unbind each other's routers.
 *
 * Gate: PANEL_PARTNER_PG_URL, loopback hosts only (a disposable container).
 * It is read from nowhere else — never DATABASE_URL, never an .env file.
 */
const url = process.env.PANEL_PARTNER_PG_URL;

const MARK = "pg-scoping-";
const SHARED_OWNER = "acct-shared";
const NOW = () => new Date();

const MIGRATION_0024 = fileURLToPath(
  new URL(
    "../../../../../packages/db/drizzle/0024_partner_scoping.sql",
    import.meta.url,
  ),
);

describe.skipIf(!url)("real PostgreSQL partner scoping", () => {
  let sql: ReturnType<typeof postgres>;
  let db: ReturnType<typeof drizzle<typeof schema>>;

  beforeAll(async () => {
    const host = new URL(url!).hostname;
    if (host !== "127.0.0.1" && host !== "localhost")
      throw new Error(
        "PANEL_PARTNER_PG_URL must point at a loopback host (127.0.0.1 or localhost)",
      );
    sql = postgres(url!, { max: 5, onnotice: () => undefined });
    db = drizzle(sql, { schema });
    // Leftovers of an aborted earlier run of THIS file only.
    await removeRouters(
      (
        await db
          .select({ id: schema.routers.id })
          .from(schema.routers)
          .where(like(schema.routers.deviceIdentifier, `${MARK}%`))
      ).map((row) => row.id),
    );
  });

  afterAll(async () => {
    await sql?.end();
  });

  async function removeRouters(ids: string[]) {
    if (ids.length === 0) return;
    // event_log and partner_webhook keep their rows (ON DELETE SET NULL): drop
    // them explicitly so a run leaves nothing behind. Jobs, snapshots and the
    // rest cascade with the router.
    await db
      .delete(schema.eventLog)
      .where(inArray(schema.eventLog.routerId, ids));
    await db
      .delete(schema.partnerWebhooks)
      .where(inArray(schema.partnerWebhooks.routerId, ids));
    await db.delete(schema.routers).where(inArray(schema.routers.id, ids));
  }

  type RouterOverrides = Partial<typeof schema.routers.$inferInsert>;

  // A router claimed by `ownerRef` for `partnerId` (null = a row from before
  // there were partners).
  async function insertRouter(
    partnerId: string | null,
    overrides: RouterOverrides = {},
  ) {
    const id = crypto.randomUUID();
    const [row] = await db
      .insert(schema.routers)
      .values({
        id,
        deviceIdentifier: `${MARK}${id}`,
        ownerRef: SHARED_OWNER,
        partnerId,
        claimedAt: new Date(Date.now() - 60_000),
        engineMode: "xray-direct",
        status: "active",
        importState: "approved",
        approvedAt: new Date(Date.now() - 60_000),
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        ...overrides,
      })
      .returning();
    return row!;
  }

  // Ready for a partner action: applied revision plus a snapshot in which the
  // router reports the owner and the `reboot` capability.
  async function insertReadyRouter(partnerId: string | null) {
    const router = await insertRouter(partnerId, {
      lastAppliedRevisionId: "fake-revision",
    });
    await db.insert(schema.routerInventorySnapshots).values({
      routerId: router.id,
      createdAt: new Date(),
      payload: {
        engineMode: "xray-direct",
        connect: { ownerRef: SHARED_OWNER, capabilities: ["reboot"] },
      } as typeof schema.routerInventorySnapshots.$inferInsert.payload,
    });
    return router;
  }

  async function jobsOf(routerId: string) {
    return db
      .select()
      .from(schema.jobs)
      .where(eq(schema.jobs.routerId, routerId));
  }

  async function routerRow(id: string) {
    const [row] = await db
      .select()
      .from(schema.routers)
      .where(eq(schema.routers.id, id));
    return row;
  }

  const reboot = (routerId: string) => ({
    routerId,
    ownerRef: SHARED_OWNER,
    action: "reboot" as const,
    params: {},
  });

  const visibleTo = async (partnerId: string) =>
    (
      await readPartnerRoutersWithDb(
        db,
        SHARED_OWNER,
        undefined,
        NOW(),
        partnerId,
      )
    )
      .map((router) => router.routerId)
      .sort();

  it("a read shows a partner only its own routers, though the ownerRef is the same", async () => {
    const created: string[] = [];
    try {
      const legacy = await insertRouter(null);
      const bloop = await insertRouter("bloopcat");
      const literal = await insertRouter("vectra");
      created.push(legacy.id, bloop.id, literal.id);

      // NULL (a router from before partners) and the literal 'vectra' are both
      // Vectra Connect's; bloopcat sees only its own.
      expect(await visibleTo("vectra")).toEqual([legacy.id, literal.id].sort());
      expect(await visibleTo("bloopcat")).toEqual([bloop.id]);
      // A partner nobody has routers for sees nothing, never the NULL rows.
      expect(await visibleTo("otherpartner")).toEqual([]);

      // By id: another partner's router is simply not there.
      expect(
        await readPartnerRoutersWithDb(
          db,
          SHARED_OWNER,
          bloop.id,
          NOW(),
          "vectra",
        ),
      ).toEqual([]);
      expect(
        await readPartnerRoutersWithDb(
          db,
          SHARED_OWNER,
          legacy.id,
          NOW(),
          "bloopcat",
        ),
      ).toEqual([]);
      expect(
        (
          await readPartnerRoutersWithDb(
            db,
            SHARED_OWNER,
            legacy.id,
            NOW(),
            "vectra",
          )
        ).map((router) => router.routerId),
      ).toEqual([legacy.id]);
    } finally {
      await removeRouters(created);
    }
  });

  it("an action on a partner's router from another partner is 404 and queues nothing", async () => {
    const created: string[] = [];
    try {
      const bloop = await insertReadyRouter("bloopcat");
      created.push(bloop.id);
      const key = `key-${crypto.randomUUID()}`;

      const asVectra = await queuePartnerActionWithDb(
        db,
        reboot(bloop.id),
        key,
        NOW(),
        "vectra",
      );
      expect(asVectra).toEqual({
        ok: false,
        status: 404,
        body: { error: "not_found" },
      });
      expect(await jobsOf(bloop.id)).toHaveLength(0);

      const asBloop = await queuePartnerActionWithDb(
        db,
        reboot(bloop.id),
        key,
        NOW(),
        "bloopcat",
      );
      expect(asBloop.status).toBe(202);
      const [job] = await jobsOf(bloop.id);
      expect(job).toBeDefined();
      expect(job!.dedupeKey!.endsWith(`bloopcat:${key}`)).toBe(true);
      expect(job!.dedupeKey).toBe(`partner-action:bloopcat:${key}`);
      expect(job!.payload.idempotencyKey).toBe(key);
      expect(job!.payload.partnerId).toBe("bloopcat");
      expect(await jobsOf(bloop.id)).toHaveLength(1);
    } finally {
      await removeRouters(created);
    }
  });

  it("the same Idempotency-Key from two partners for one ownerRef makes two separate actions", async () => {
    const created: string[] = [];
    try {
      const legacy = await insertReadyRouter(null);
      const bloop = await insertReadyRouter("bloopcat");
      created.push(legacy.id, bloop.id);
      const key = `shared-key-${crypto.randomUUID()}`;

      const first = await queuePartnerActionWithDb(
        db,
        reboot(legacy.id),
        key,
        NOW(),
        "vectra",
      );
      const second = await queuePartnerActionWithDb(
        db,
        reboot(bloop.id),
        key,
        NOW(),
        "bloopcat",
      );
      expect(first.status).toBe(202);
      expect(second.status).toBe(202);
      expect((first.body as { actionId: string }).actionId).not.toBe(
        (second.body as { actionId: string }).actionId,
      );
      const [legacyJob] = await jobsOf(legacy.id);
      const [bloopJob] = await jobsOf(bloop.id);
      // Vectra's key is stored exactly as before partners existed.
      expect(legacyJob!.dedupeKey).toBe(`partner-action:${key}`);
      expect(bloopJob!.dedupeKey).toBe(`partner-action:bloopcat:${key}`);
      // Both retries answer from their own job.
      const retry = await queuePartnerActionWithDb(
        db,
        reboot(bloop.id),
        key,
        NOW(),
        "bloopcat",
      );
      expect((retry.body as { actionId: string }).actionId).toBe(
        (second.body as { actionId: string }).actionId,
      );
    } finally {
      await removeRouters(created);
    }
  });

  it("another partner cannot cancel an action: 404 and the job stays queued", async () => {
    const created: string[] = [];
    try {
      const bloop = await insertReadyRouter("bloopcat");
      created.push(bloop.id);
      const key = `cancel-key-${crypto.randomUUID()}`;
      const queued = await queuePartnerActionWithDb(
        db,
        reboot(bloop.id),
        key,
        NOW(),
        "bloopcat",
      );
      expect(queued.status).toBe(202);

      const cancelInput = {
        routerId: bloop.id,
        ownerRef: SHARED_OWNER,
        idempotencyKey: key,
      };
      const asVectra = await cancelPartnerActionWithDb(
        db,
        cancelInput,
        NOW(),
        "vectra",
      );
      expect(asVectra).toEqual({
        ok: false,
        status: 404,
        body: { error: "not_found" },
      });
      expect((await jobsOf(bloop.id))[0]!.state).toBe("queued");

      const asBloop = await cancelPartnerActionWithDb(
        db,
        cancelInput,
        NOW(),
        "bloopcat",
      );
      expect(asBloop.status).toBe(200);
      expect(asBloop.body).toMatchObject({ state: "cancelled" });
      expect((await jobsOf(bloop.id))[0]!.state).toBe("cancelled");
    } finally {
      await removeRouters(created);
    }
  });

  it("an unbind by another partner is 409 and changes nothing; by the owning partner it unlinks", async () => {
    const created: string[] = [];
    try {
      const bloop = await insertRouter("bloopcat");
      created.push(bloop.id);
      const before = await routerRow(bloop.id);

      const asVectra = await unbindRouterClaimWithDb(db, {
        routerId: bloop.id,
        ownerRef: SHARED_OWNER,
        partnerId: "vectra",
      });
      expect(asVectra).toMatchObject({
        ok: false,
        status: 409,
        body: { error: "claimed_by_other" },
      });
      // Without a partner id it is Vectra Connect's call — same refusal.
      const asDefault = await unbindRouterClaimWithDb(db, {
        routerId: bloop.id,
        ownerRef: SHARED_OWNER,
      });
      expect(asDefault).toMatchObject({ ok: false, status: 409 });
      expect(await routerRow(bloop.id)).toEqual(before);

      const asBloop = await unbindRouterClaimWithDb(db, {
        routerId: bloop.id,
        ownerRef: SHARED_OWNER,
        partnerId: "bloopcat",
      });
      expect(asBloop).toMatchObject({
        ok: true,
        status: 200,
        body: { state: "unclaimed" },
      });
      const after = await routerRow(bloop.id);
      expect(after!.ownerRef).toBeNull();
      expect(after!.partnerId).toBeNull();
      expect(after!.releasedAt).not.toBeNull();
      expect(after!.importState).toBe("awaiting_import");
    } finally {
      await removeRouters(created);
    }
  });

  it("the SQL predicate alone (without the JS recheck) fences partners apart", async () => {
    const created: string[] = [];
    try {
      const legacy = await insertRouter(null);
      const bloop = await insertRouter("bloopcat");
      const literal = await insertRouter("vectra");
      created.push(legacy.id, bloop.id, literal.id);
      const ids = [legacy.id, bloop.id, literal.id];
      const matching = async (partnerId: string) =>
        (
          await db
            .select({ id: schema.routers.id })
            .from(schema.routers)
            .where(
              and(
                inArray(schema.routers.id, ids),
                routerOwnedByPartner(partnerId),
              ),
            )
        )
          .map((row) => row.id)
          .sort();

      expect(await matching("vectra")).toEqual([legacy.id, literal.id].sort());
      expect(await matching("bloopcat")).toEqual([bloop.id]);
      expect(await matching("otherpartner")).toEqual([]);

      // A write guarded only by the predicate touches no row of another partner.
      const touched = await db
        .update(schema.routers)
        .set({ ownerLabel: "must-not-happen" })
        .where(
          and(eq(schema.routers.id, bloop.id), routerOwnedByPartner("vectra")),
        )
        .returning({ id: schema.routers.id });
      expect(touched).toEqual([]);
      expect((await routerRow(bloop.id))!.ownerLabel).toBeNull();
    } finally {
      await removeRouters(created);
    }
  });

  it("migration 0024 is idempotent: running it again changes nothing", async () => {
    const created: string[] = [];
    try {
      const bloop = await insertRouter("bloopcat");
      const legacy = await insertRouter(null);
      created.push(bloop.id, legacy.id);

      const statements = readFileSync(MIGRATION_0024, "utf8")
        .split("--> statement-breakpoint")
        .map((part) => part.trim())
        .filter((part) => part.length > 0);
      expect(statements).toHaveLength(3);
      for (let run = 0; run < 2; run += 1)
        for (const statement of statements) await sql.unsafe(statement);

      const columns = await sql<{ table_name: string }[]>`
        select table_name from information_schema.columns
        where table_schema = 'public' and column_name = 'partner_id'
          and table_name in ('vectra_router', 'vectra_partner_webhook')`;
      expect(columns.map((row) => row.table_name).sort()).toEqual([
        "vectra_partner_webhook",
        "vectra_router",
      ]);
      const indexes = await sql<{ indexdef: string }[]>`
        select indexdef from pg_indexes
        where indexname = 'vectra_router_partner_owner_idx'`;
      expect(indexes).toHaveLength(1);
      expect(indexes[0]!.indexdef).toContain("(partner_id, owner_ref)");
      // Existing data keeps its meaning.
      expect((await routerRow(bloop.id))!.partnerId).toBe("bloopcat");
      expect((await routerRow(legacy.id))!.partnerId).toBeNull();
    } finally {
      await removeRouters(created);
    }
  });
});
