import { describe, it, expect, vi } from "vitest";
vi.mock("~/server/db", () => ({ db: {} }));
import postgres from "postgres";
import { drizzle } from "drizzle-orm/postgres-js";
import * as schema from "@vectra/db";
import {
  reservePartnerNonceWithDb,
  findIdempotencyRecordWithDb,
  storeIdempotencyRecordWithDb,
} from "./partner-api";
import {
  queuePartnerActionWithDb,
  handlePartnerRouterAction,
  type PartnerRoutersDeps,
} from "./partner-routers";
import { buildPartnerRequestHeaders } from "./partner-request-signature";
// Explicit isolated Unix socket only. Never resolve DATABASE_URL or .env.
const socket = process.env.PANEL_SYNTHETIC_PG_SOCKET;
describe.skipIf(!socket)("real isolated PostgreSQL partner concurrency", () => {
  it("atomically consumes a nonce and preserves one native action across concurrent retries", async () => {
    if (
      socket !== "/Users/waizor/Documents/Codex/2026-10-01/task-5/pg-v2-socket"
    )
      throw new Error("Only isolated synthetic socket allowed");
    const sql = postgres({
      host: socket,
      port: 55439,
      username: "panel_test",
      database: "postgres",
      max: 10,
    });
    const db = drizzle(sql, { schema });
    try {
      const nonce = crypto.randomUUID();
      const results = await Promise.all(
        Array.from({ length: 10 }, () =>
          reservePartnerNonceWithDb(
            db,
            nonce,
            "synthetic-fingerprint",
            new Date(Date.now() + 900000),
          ),
        ),
      );
      expect(results.filter(Boolean)).toHaveLength(1);
      const routerId = crypto.randomUUID(),
        now = new Date();
      await db.insert(schema.routers).values({
        id: routerId,
        deviceIdentifier: `synthetic-${routerId}`,
        ownerRef: "acct-42",
        claimedAt: now,
        engineMode: "xray-direct",
        status: "active",
        importState: "approved",
        approvedAt: now,
        lastAppliedRevisionId: "fake-revision",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
      });
      await db.insert(schema.routerInventorySnapshots).values({
        routerId,
        payload: {
          engineMode: "xray-direct",
          connect: { ownerRef: "acct-42", capabilities: ["reboot"] },
        } as typeof schema.routerInventorySnapshots.$inferInsert.payload,
      });
      const input = {
        routerId,
        ownerRef: "acct-42",
        action: "reboot" as const,
        params: {},
      };
      const key = crypto.randomUUID();
      const secret = "synthetic-postgres-signing-key";
      const deps: PartnerRoutersDeps = {
        api: {
          secret,
          now: () => new Date(),
          claim: vi.fn(),
          unbind: vi.fn(),
          reserveNonce: (id, hash, expiry) =>
            reservePartnerNonceWithDb(db, id, hash, expiry),
          findIdempotent: (key) => findIdempotencyRecordWithDb(db, key),
          storeIdempotent: (key, record) =>
            storeIdempotencyRecordWithDb(db, key, record),
        },
        read: vi.fn(),
        action: (body, key) => queuePartnerActionWithDb(db, body, key),
      };
      const url = `https://fake.example/api/partner/routers/${routerId}/actions`,
        raw = JSON.stringify(input);
      const request = () =>
        new Request(url, {
          method: "POST",
          body: raw,
          headers: buildPartnerRequestHeaders(secret, "POST", url, raw, key),
        });
      const captured = request();
      const outcomes = await Promise.all(
        Array.from({ length: 10 }, () =>
          handlePartnerRouterAction(request(), routerId, deps),
        ),
      );
      expect(outcomes.every((result) => result.status === 202)).toBe(true);
      const bodies = await Promise.all(
        outcomes.map(
          (result) => result.json() as Promise<{ actionId: string }>,
        ),
      );
      expect(new Set(bodies.map((body) => body.actionId)).size).toBe(1);
      const accepted = await handlePartnerRouterAction(
        captured.clone(),
        routerId,
        deps,
      );
      expect(accepted.status).toBe(202);
      expect(
        (await handlePartnerRouterAction(captured.clone(), routerId, deps))
          .status,
      ).toBe(409);
      const tampered = new Headers(captured.headers);
      tampered.set("Idempotency-Key", "different-key");
      expect(
        (
          await handlePartnerRouterAction(
            new Request(url, { method: "POST", body: raw, headers: tampered }),
            routerId,
            deps,
          )
        ).status,
      ).toBe(401);
      const retry = await handlePartnerRouterAction(request(), routerId, deps);
      expect(retry.status).toBe(202);
      expect(((await retry.json()) as { actionId: string }).actionId).toBe(
        bodies[0]?.actionId,
      );
      const foreignRaw = JSON.stringify({ ...input, ownerRef: "foreign" });
      const foreign = new Request(url, {
        method: "POST",
        body: foreignRaw,
        headers: buildPartnerRequestHeaders(
          secret,
          "POST",
          url,
          foreignRaw,
          key,
        ),
      });
      expect(
        (await handlePartnerRouterAction(foreign, routerId, deps)).status,
      ).toBe(422);
      expect(
        (await sql`select id from vectra_job where router_id=${routerId}`)
          .length,
      ).toBe(1);
    } finally {
      await sql.end();
    }
  });
});
