import { randomUUID } from "node:crypto";

import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "router-claim-id-pg-test-secrets-key-0123456789",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET: "router-claim-id-pg-test-webhook-0123456789",
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);
// The claim service and the outbox must only use the client they are handed.
vi.mock("~/server/db", () => ({ db: {} }));

import postgres from "postgres";
import { and, eq, inArray, like } from "drizzle-orm";
import { drizzle } from "drizzle-orm/postgres-js";
import { partnerRouterClaimRequestSchema } from "@vectra/contracts";
import * as schema from "@vectra/db";

import {
  deliverPartnerWebhook,
  enqueuePartnerWebhookWithDb,
  PARTNER_WEBHOOK_ID_HEADER,
} from "./partner-webhooks";
import { claimRouterWithDb, unbindRouterClaimWithDb } from "./router-claims";

/**
 * The claim id (claimId) against a REAL PostgreSQL: the unit tests run on
 * createFakeDb, which never evaluates SQL, so this proves the column, the
 * claim and unbind writes, and the outbox's read of the router's claim id.
 *
 * Gate: PANEL_PARTNER_PG_URL, loopback hosts only (a disposable database with
 * the migrations applied). It is read from nowhere else — never DATABASE_URL,
 * never an .env file.
 */
const url = process.env.PANEL_PARTNER_PG_URL;

const MARK = "pg-claim-id-";
const OWNER = "acct-claim-id";
const DEVICE_KEY = Buffer.alloc(32, 5).toString("base64");
const NONCE = Buffer.alloc(16, 4).toString("base64");
const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe.skipIf(!url)("real PostgreSQL claim ids", () => {
  let sql: ReturnType<typeof postgres>;
  let db: ReturnType<typeof drizzle<typeof schema>>;

  async function removeRouters(ids: string[]) {
    if (ids.length === 0) return;
    // event_log and partner_webhook keep their rows (ON DELETE SET NULL): drop
    // them explicitly so a run leaves nothing behind.
    await db.delete(schema.eventLog).where(inArray(schema.eventLog.routerId, ids));
    await db
      .delete(schema.partnerWebhooks)
      .where(inArray(schema.partnerWebhooks.routerId, ids));
    await db.delete(schema.routers).where(inArray(schema.routers.id, ids));
  }

  async function markedRouterIds() {
    return (
      await db
        .select({ id: schema.routers.id })
        .from(schema.routers)
        .where(like(schema.routers.deviceIdentifier, `${MARK}%`))
    ).map((row) => row.id);
  }

  beforeAll(async () => {
    const host = new URL(url!).hostname;
    if (host !== "127.0.0.1" && host !== "localhost")
      throw new Error(
        "PANEL_PARTNER_PG_URL must point at a loopback host (127.0.0.1 or localhost)",
      );
    sql = postgres(url!, { max: 5, onnotice: () => undefined });
    db = drizzle(sql, { schema });
    // Leftovers of an aborted earlier run of THIS file only.
    await removeRouters(await markedRouterIds());
  });

  afterAll(async () => {
    if (db) await removeRouters(await markedRouterIds());
    await sql?.end();
  });

  function claimByDevice(deviceIdentifier: string) {
    return claimRouterWithDb(
      db as never,
      partnerRouterClaimRequestSchema.parse({
        device: { deviceIdentifier, devicePublicKey: DEVICE_KEY, nonce: NONCE },
        owner: { ref: OWNER, label: "ow***" },
        subscription: { url: "https://sub.example.test/api/sub/CLAIM_ID_TOKEN" },
      }),
    );
  }

  async function storedClaimId(routerId: string) {
    const [row] = await db
      .select({ claimId: schema.routers.claimId })
      .from(schema.routers)
      .where(eq(schema.routers.id, routerId));
    return row?.claimId;
  }

  async function webhookRows(
    routerId: string,
    event: (typeof schema.partnerWebhooks.$inferSelect)["event"],
  ) {
    return db
      .select()
      .from(schema.partnerWebhooks)
      .where(
        and(
          eq(schema.partnerWebhooks.routerId, routerId),
          eq(schema.partnerWebhooks.event, event),
        ),
      );
  }

  // As the claim-apply result path queues it: the caller names neither the
  // partner nor the claim; both are read from the router row in SQL.
  async function queueReady(routerId: string) {
    const id = await enqueuePartnerWebhookWithDb(db as never, {
      event: "router.ready",
      routerId,
      ownerRef: OWNER,
    });
    const [row] = await db
      .select()
      .from(schema.partnerWebhooks)
      .where(eq(schema.partnerWebhooks.id, id!));
    return row!;
  }

  async function send(row: { id: string; payload: unknown }) {
    const fetchImpl = vi.fn(async () => ({ ok: true, status: 200, body: null }));
    await deliverPartnerWebhook(
      row as never,
      { url: "https://backend.example.test/hooks", secret: "pg-claim-id-secret" },
      { fetchImpl },
    );
    const [, init] = fetchImpl.mock.calls[0] as unknown as [
      string,
      RequestInit & { headers: Record<string, string> },
    ];
    return { body: init.body as string, id: init.headers[PARTNER_WEBHOOK_ID_HEADER] };
  }

  it("one id per claim attempt, kept by a repeat, cleared by unbind, frozen into every event", async () => {
    const deviceIdentifier = `${MARK}${randomUUID().slice(0, 8)}`;

    // Attempt 1 (a router the panel has never seen: created pre-claimed).
    const first = await claimByDevice(deviceIdentifier);
    expect(first).toMatchObject({ ok: true, body: { alreadyClaimed: false } });
    const { routerId, claimId: claimA } = (
      first as { body: { routerId: string; claimId: string } }
    ).body;
    expect(claimA).toMatch(UUID_V4);
    expect(await storedClaimId(routerId)).toBe(claimA);
    expect((await webhookRows(routerId, "router.claimed"))[0]?.payload).toMatchObject({
      claimId: claimA,
    });

    // A repeat of the claim: the same id, nothing rotated, no second event.
    const repeat = await claimByDevice(deviceIdentifier);
    expect(repeat).toMatchObject({
      ok: true,
      body: { alreadyClaimed: true, routerId, claimId: claimA },
    });
    expect(await storedClaimId(routerId)).toBe(claimA);
    expect(await webhookRows(routerId, "router.claimed")).toHaveLength(1);

    // Attempt 1's router.ready is queued (and not delivered yet).
    const lateReady = await queueReady(routerId);
    expect(lateReady.payload).toMatchObject({ claimId: claimA });
    const firstSend = await send(lateReady);

    // Unbind: the id is cleared; an event queued now has no claim.
    expect(
      await unbindRouterClaimWithDb(db as never, { routerId, ownerRef: OWNER }),
    ).toMatchObject({ ok: true });
    expect(await storedClaimId(routerId)).toBeNull();
    const unclaimed = await enqueuePartnerWebhookWithDb(db as never, {
      event: "router.offline",
      routerId,
      ownerRef: OWNER,
    });
    const [unclaimedRow] = await db
      .select()
      .from(schema.partnerWebhooks)
      .where(eq(schema.partnerWebhooks.id, unclaimed!));
    expect(unclaimedRow?.payload).toHaveProperty("claimId", null);

    // Attempt 2: the same router, the same owner, a new id.
    const second = await claimByDevice(deviceIdentifier);
    expect(second).toMatchObject({
      ok: true,
      body: { alreadyClaimed: false, routerId },
    });
    const claimB = (second as { body: { claimId: string } }).body.claimId;
    expect(claimB).toMatch(UUID_V4);
    expect(claimB).not.toBe(claimA);
    expect(await storedClaimId(routerId)).toBe(claimB);

    // Attempt 2's early router.ready carries the new id…
    const earlyReady = await queueReady(routerId);
    expect(earlyReady.payload).toMatchObject({ claimId: claimB });

    // …while attempt 1's, delivered only now, still carries the old one, with
    // the same request id and the same bytes as its first attempt.
    const [storedLate] = await db
      .select()
      .from(schema.partnerWebhooks)
      .where(eq(schema.partnerWebhooks.id, lateReady.id));
    const lateSend = await send(storedLate!);
    expect(JSON.parse(lateSend.body)).toMatchObject({
      event: "router.ready",
      claimId: claimA,
    });
    expect(lateSend.id).toBe(lateReady.id);
    expect(lateSend.id).toBe(firstSend.id);
    expect(lateSend.body).toBe(firstSend.body);
  });
});
