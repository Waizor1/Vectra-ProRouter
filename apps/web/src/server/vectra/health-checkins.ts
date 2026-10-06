import { sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

type DatabaseClient = Pick<typeof appDb, "execute">;

/** A router counts as "checked in" when last_seen_at is this fresh. */
export const HEALTH_CHECKIN_WINDOW_SECONDS = 300;

/**
 * /api/health is called ~90 times a minute, so the count is computed at most
 * once per window and shared between concurrent calls.
 */
export const HEALTH_CHECKIN_CACHE_MS = 30_000;

export type HealthCheckinCounts = {
  /** Routers seen within HEALTH_CHECKIN_WINDOW_SECONDS. */
  checkedInLast5m: number;
  /** Fleet routers that should be checking in: everything except pending/disabled. */
  active: number;
};

let cached: { at: number; value: HealthCheckinCounts } | null = null;
let inFlight: Promise<HealthCheckinCounts> | null = null;

export function resetHealthCheckinsForTest() {
  cached = null;
  inFlight = null;
}

async function queryCounts(database: DatabaseClient): Promise<HealthCheckinCounts> {
  const rows = await database.execute<{ checked_in: number; active: number }>(sql`
    select
      count(*) filter (
        where last_seen_at >= now() - make_interval(secs => ${HEALTH_CHECKIN_WINDOW_SECONDS})
      )::int as checked_in,
      count(*)::int as active
    from vectra_router
    where status not in ('pending', 'disabled')
  `);
  const row = Array.from(rows)[0];
  return {
    checkedInLast5m: Number(row?.checked_in ?? 0),
    active: Number(row?.active ?? 0),
  };
}

/**
 * Read-only router check-in freshness for external monitors. Returns null when
 * the count cannot be read: it is informational and must never fail /api/health.
 */
export async function loadHealthCheckinCounts(
  database: DatabaseClient,
  now = Date.now(),
): Promise<HealthCheckinCounts | null> {
  if (cached && now >= cached.at && now - cached.at < HEALTH_CHECKIN_CACHE_MS) {
    return cached.value;
  }
  inFlight ??= queryCounts(database)
    .then((value) => {
      cached = { at: Date.now(), value };
      return value;
    })
    .finally(() => {
      inFlight = null;
    });
  return inFlight.catch((error: unknown) => {
    console.error("[health] checkin counts", error);
    return null;
  });
}
