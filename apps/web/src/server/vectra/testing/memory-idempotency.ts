import {
  type IdempotencyRecord,
  type PartnerApiDeps,
  partnerHashMatches,
} from "../partner-api";

type Row = IdempotencyRecord & { locked: boolean };

/**
 * In-memory twin of reserveIdempotencyKeyWithDb / finishIdempotencyKeyWithDb
 * for tests: same states (pending + lease, released, final), no database.
 */
export function createMemoryIdempotency() {
  const store = new Map<string, Row>();
  const reserveIdempotent: PartnerApiDeps["reserveIdempotent"] = async (
    key,
    hashes,
  ) => {
    const row = store.get(key);
    if (!row) {
      store.set(key, {
        requestHash: hashes.requestHash,
        statusCode: 0,
        response: {},
        locked: true,
      });
      return { kind: "run" };
    }
    if (!partnerHashMatches(row.requestHash, hashes)) return { kind: "mismatch" };
    if (row.statusCode !== 0) {
      return {
        kind: "replay",
        record: {
          requestHash: row.requestHash,
          statusCode: row.statusCode,
          response: row.response,
        },
      };
    }
    if (row.locked) return { kind: "busy" };
    row.locked = true;
    return { kind: "run" };
  };
  const finishIdempotent: PartnerApiDeps["finishIdempotent"] = async (
    key,
    requestHash,
    record,
  ) => {
    const row = store.get(key);
    if (row?.requestHash !== requestHash || row.statusCode !== 0) return;
    if (record) {
      row.statusCode = record.statusCode;
      row.response = record.response;
    }
    row.locked = false;
  };
  return { store, reserveIdempotent, finishIdempotent };
}
