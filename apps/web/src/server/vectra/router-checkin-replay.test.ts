import { beforeEach, describe, expect, it, vi } from "vitest";

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
// The module imports `db` only as a default fallback; every test passes its own
// stub, so a real client is never built.
vi.mock("~/server/db", () => ({ db: {} }));

import {
  consumeCheckInNonce,
  resetCheckInReplayStateForTest,
} from "./router-checkin-replay";
import {
  CHECKIN_NONCE_MAX_AGE_SECONDS,
  ROUTER_AUTH_CLOCK_SKEW_SECONDS,
} from "./router-auth-integrity";

type ReplayDb = NonNullable<
  Parameters<typeof consumeCheckInNonce>[0]["database"]
>;

const ROUTER_ID = "11111111-1111-4111-8111-111111111111";
const OTHER_ROUTER = "22222222-2222-4222-8222-222222222222";
const NOW = new Date("2026-10-08T12:00:00.000Z");
const IAT = Math.floor(NOW.getTime() / 1000);

type Row = Record<string, unknown>;

/**
 * A stateful stand-in for the ledger: `insert(...).onConflictDoNothing()`
 * deduplicates by (routerId, nonceHash) exactly as the composite primary key
 * does, so a reused tuple's second insert returns no row. Predicates on the
 * prune `delete` are not evaluated (Postgres owns the filtering); the stub only
 * records that a prune was attempted.
 */
function createLedgerStub() {
  const consumed = new Set<string>();
  let deleteCalls = 0;
  let insertCalls = 0;
  let failInsert = false;
  let lastValues: Row | null = null;

  const db = {
    delete() {
      deleteCalls += 1;
      return { where: () => Promise.resolve([]) };
    },
    insert() {
      return {
        values(values: Row) {
          lastValues = values;
          const statement = {
            onConflictDoNothing() {
              return statement;
            },
            returning: async () => {
              insertCalls += 1;
              if (failInsert) {
                throw new Error("relation does not exist");
              }
              const key = `${values.routerId as string}|${values.nonceHash as string}`;
              if (consumed.has(key)) {
                return [] as Row[];
              }
              consumed.add(key);
              return [{ nonceHash: values.nonceHash }];
            },
          };
          return statement;
        },
      };
    },
  };

  return {
    db: db as unknown as ReplayDb,
    consumed,
    get deleteCalls() {
      return deleteCalls;
    },
    get insertCalls() {
      return insertCalls;
    },
    get lastValues() {
      return lastValues;
    },
    failInsert(value: boolean) {
      failInsert = value;
    },
  };
}

beforeEach(() => {
  envMock.env = { NODE_ENV: "test", VECTRA_SECRETS_KEY: "x".repeat(32) };
  resetCheckInReplayStateForTest();
});

describe("consumeCheckInNonce", () => {
  it("accepts a fresh nonce (replay:false) and records it", async () => {
    const ledger = createLedgerStub();
    const result = await consumeCheckInNonce({
      routerId: ROUTER_ID,
      nonce: "vcn1.fresh",
      nonceIatUnix: IAT,
      now: NOW,
      database: ledger.db,
    });
    expect(result.replay).toBe(false);
    expect(ledger.consumed.size).toBe(1);
  });

  it("flags the same (router, nonce) consumed a second time as a replay", async () => {
    const ledger = createLedgerStub();
    const call = () =>
      consumeCheckInNonce({
        routerId: ROUTER_ID,
        nonce: "vcn1.reused",
        nonceIatUnix: IAT,
        now: NOW,
        database: ledger.db,
      });
    expect((await call()).replay).toBe(false);
    expect((await call()).replay).toBe(true);
    expect(ledger.consumed.size).toBe(1);
  });

  it("keeps the same nonce distinct per router (keyed by routerId)", async () => {
    const ledger = createLedgerStub();
    const nonce = "vcn1.shared";
    expect(
      (
        await consumeCheckInNonce({
          routerId: ROUTER_ID,
          nonce,
          nonceIatUnix: IAT,
          now: NOW,
          database: ledger.db,
        })
      ).replay,
    ).toBe(false);
    expect(
      (
        await consumeCheckInNonce({
          routerId: OTHER_ROUTER,
          nonce,
          nonceIatUnix: IAT,
          now: NOW,
          database: ledger.db,
        })
      ).replay,
    ).toBe(false);
    expect(ledger.consumed.size).toBe(2);
  });

  it("fails open (replay:false) and warns once on a database error, never throwing", async () => {
    const ledger = createLedgerStub();
    ledger.failInsert(true);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const result = await consumeCheckInNonce({
      routerId: ROUTER_ID,
      nonce: "vcn1.err",
      nonceIatUnix: IAT,
      now: NOW,
      database: ledger.db,
    });

    expect(result.replay).toBe(false);
    expect(warn).toHaveBeenCalledTimes(1);
  });

  it("prunes expired rows before inserting", async () => {
    const ledger = createLedgerStub();
    await consumeCheckInNonce({
      routerId: ROUTER_ID,
      nonce: "vcn1.prune",
      nonceIatUnix: IAT,
      now: NOW,
      database: ledger.db,
    });
    // The prune delete is issued on every consume, before the insert.
    expect(ledger.deleteCalls).toBe(1);
    expect(ledger.insertCalls).toBe(1);
  });

  it("gives the ledger row a TTL strictly longer than the acceptance window so the prune cannot reopen a replay gap at the edge", async () => {
    const ledger = createLedgerStub();
    await consumeCheckInNonce({
      routerId: ROUTER_ID,
      nonce: "vcn1.ttl",
      nonceIatUnix: IAT,
      now: NOW,
      database: ledger.db,
    });
    // The verifier accepts a nonce while now - iat <= MAX_AGE, so acceptance
    // ends at (iat + MAX_AGE). The row's expiry must sit strictly beyond that,
    // by at least the clock skew, so `expiresAt < now` can never prune a nonce
    // the verifier would still accept.
    const expiresAt = ledger.lastValues?.expiresAt as Date;
    const acceptanceEndMs = (IAT + CHECKIN_NONCE_MAX_AGE_SECONDS) * 1000;
    expect(expiresAt.getTime()).toBe(
      (IAT +
        CHECKIN_NONCE_MAX_AGE_SECONDS +
        ROUTER_AUTH_CLOCK_SKEW_SECONDS +
        1) *
        1000,
    );
    expect(expiresAt.getTime()).toBeGreaterThan(acceptanceEndMs);
  });
});
