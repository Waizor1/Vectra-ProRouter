import { sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

type DatabaseClient = Pick<typeof appDb, "execute" | "transaction">;

/**
 * How long a successful write probe vouches for the database.
 *
 * /api/health is called ~90 times a minute (Caddy's upstream checks, and
 * legacy agents every ~22 s, which only read the status code), and every call
 * inserted and deleted an event_log row. The write probe now runs at most
 * once per window; the calls in between still run the read check.
 */
export const HEALTH_WRITE_PROBE_INTERVAL_MS = 30_000;

let lastWriteProbeOkAt: number | null = null;
let writeProbeInFlight: Promise<void> | null = null;
let readInFlight: Promise<void> | null = null;

export function resetHealthProbeForTest() {
  lastWriteProbeOkAt = null;
  writeProbeInFlight = null;
  readInFlight = null;
}

/** Concurrent health calls share one `select 1` instead of queueing more. */
export function checkDatabaseRead(database: DatabaseClient): Promise<void> {
  readInFlight ??= database
    .execute(sql`select 1`)
    .then(() => undefined)
    .finally(() => {
      readInFlight = null;
    });
  return readInFlight;
}

async function runWriteProbe(database: DatabaseClient) {
  const probeId = crypto.randomUUID();
  // The insert and the delete must be SEPARATE statements. A data-modifying
  // CTE and the outer statement share one snapshot, so a `delete` wrapped
  // around `insert ... returning` never sees the row it just inserted and
  // silently deletes nothing — that leaked one probe row per health check
  // and grew vectra_event_log to millions of rows. The transaction keeps the
  // pair atomic so a crash between them cannot leak a row either.
  await database.transaction(async (tx) => {
    await tx.execute(sql`
      insert into vectra_event_log (id, type, severity, message)
      values (${probeId}, 'health.db_write_probe', 'info', 'health route db write probe')
    `);
    await tx.execute(sql`
      delete from vectra_event_log where id = ${probeId}
    `);
  });
}

/**
 * Resolves when the database accepted a write within the last
 * HEALTH_WRITE_PROBE_INTERVAL_MS, probing again only when that has lapsed.
 *
 * Only a success is remembered: a failed probe rejects this call and the next
 * call probes again, so health turns green as soon as writes work again, as
 * it did when every call probed. Concurrent calls share the probe in flight,
 * so a slow database never accumulates a pile of them.
 */
export function checkDatabaseWrite(
  database: DatabaseClient,
  now = Date.now(),
): Promise<void> {
  if (
    lastWriteProbeOkAt !== null &&
    now >= lastWriteProbeOkAt &&
    now - lastWriteProbeOkAt < HEALTH_WRITE_PROBE_INTERVAL_MS
  ) {
    return Promise.resolve();
  }
  writeProbeInFlight ??= runWriteProbe(database)
    .then(() => {
      lastWriteProbeOkAt = Date.now();
    })
    .finally(() => {
      writeProbeInFlight = null;
    });
  return writeProbeInFlight;
}
