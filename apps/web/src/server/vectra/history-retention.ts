// History retention: the tables that only ever grew.
//
// By 2026-10-05 the event journal held 163 MB and job results 151 MB, and no
// row of either, nor of jobs, health incidents or push alerts, had ever been
// deleted (since April). At 38 routers that is already a fifth of the
// database; at 1000 routers it would be most of the VPS disk.
//
// The owner's decision (2026-10-05) is 30 days. Each tick deletes, oldest
// first, in batches of VECTRA_RETENTION_BATCH_SIZE (≤ 10k) with a pause between
// batches so a large first backlog never holds one long transaction or
// starves the check-in path:
//
//   event_log           created_at older than the window
//   job (+ job_result)  succeeded/failed/cancelled, finished (or, if it never
//                       recorded completion, created) before the window, and
//                       without a dedupe key. A finished job keeps its key only
//                       for onboarding and partner actions, where the key is an
//                       idempotency guarantee (resolveJobDedupeKeyAfterResult),
//                       so those rows stay. Results go with their job (FK
//                       cascade).
//   health_incident     resolved before the window
//   operator_push_alert resolved before the window (its dedupe key carries the
//                       episode start, so a later episode is a new key)
//
// Open incidents, unresolved alerts, unfinished jobs and their results are
// never touched. Rescue cases, applied-revision records and partner webhooks
// are not swept here. Desired revisions are pruned by revision-retention.ts.
//
// VECTRA_RETENTION_DRY_RUN (default true) makes a tick count and log instead
// of delete.

import { sql, type SQL } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = Pick<typeof db, "execute">;

export type HistoryRetentionTable =
  | "event_log"
  | "job"
  | "health_incident"
  | "operator_push_alert";

export type HistoryRetentionResult = {
  enabled: boolean;
  dryRun: boolean;
  /** Rows deleted (or, in dry-run, that would be) per table. */
  counts: Record<HistoryRetentionTable, number>;
  /** Job results that go with the jobs above (FK cascade). */
  jobResults: number;
};

const TERMINAL_JOB_STATES = sql`('succeeded', 'failed', 'cancelled')`;

/** The id of every row of each table that is past retention. */
function expiredRows(cutoffDate: Date): Record<HistoryRetentionTable, SQL> {
  // postgres.js binds only strings here; a Date param fails at bind time.
  const cutoff = sql`${cutoffDate.toISOString()}::timestamptz`;
  return {
    event_log: sql`
      select id from vectra_event_log
      where created_at < ${cutoff}
      order by created_at
    `,
    job: sql`
      select id from vectra_job
      where state in ${TERMINAL_JOB_STATES}
        and dedupe_key is null
        and created_at < ${cutoff}
        and (completed_at is null or completed_at < ${cutoff})
      order by created_at
    `,
    health_incident: sql`
      select id from vectra_health_incident
      where state = 'resolved'
        and resolved_at < ${cutoff}
    `,
    operator_push_alert: sql`
      select id from vectra_operator_push_alert
      where resolved_at is not null
        and resolved_at < ${cutoff}
    `,
  };
}

function affectedRows(result: unknown) {
  return typeof (result as { count?: number }).count === "number"
    ? (result as { count: number }).count
    : 0;
}

async function countOf(database: DatabaseClient, query: SQL) {
  const [row] = (await database.execute(
    sql`select count(*)::int as count from (${query}) expired`,
  )) as unknown as Array<{ count: number }>;
  return row?.count ?? 0;
}

const sleep = (ms: number) =>
  ms > 0 ? new Promise((resolve) => setTimeout(resolve, ms)) : Promise.resolve();

export async function runHistoryRetentionTick(
  database: DatabaseClient = db,
  options?: {
    enabled?: boolean;
    retentionDays?: number;
    dryRun?: boolean;
    batchSize?: number;
    /** Per table per tick; a larger backlog drains over later ticks. */
    maxBatchesPerTable?: number;
    pauseMs?: number;
    now?: Date;
  },
): Promise<HistoryRetentionResult> {
  const enabled = options?.enabled ?? env.VECTRA_HISTORY_RETENTION_ENABLED;
  const dryRun = options?.dryRun ?? env.VECTRA_RETENTION_DRY_RUN;
  const counts: Record<HistoryRetentionTable, number> = {
    event_log: 0,
    job: 0,
    health_incident: 0,
    operator_push_alert: 0,
  };
  if (!enabled) {
    return { enabled: false, dryRun, counts, jobResults: 0 };
  }

  const retentionDays = options?.retentionDays ?? env.VECTRA_RETENTION_DAYS;
  const batchSize = Math.min(
    10_000,
    options?.batchSize ?? env.VECTRA_RETENTION_BATCH_SIZE,
  );
  const maxBatchesPerTable = options?.maxBatchesPerTable ?? 20;
  const pauseMs = options?.pauseMs ?? 200;
  const now = options?.now ?? new Date();
  const cutoff = new Date(now.getTime() - retentionDays * 24 * 60 * 60 * 1000);
  const expired = expiredRows(cutoff);

  if (dryRun) {
    for (const table of Object.keys(expired) as HistoryRetentionTable[]) {
      counts[table] = await countOf(database, expired[table]);
    }
    const jobResults = await countOf(
      database,
      sql`select id from vectra_job_result where job_id in (${expired.job})`,
    );
    return { enabled: true, dryRun, counts, jobResults };
  }

  let jobResults = 0;
  for (const table of Object.keys(expired) as HistoryRetentionTable[]) {
    for (let batch = 0; batch < maxBatchesPerTable; batch += 1) {
      const ids = sql`select id from (${expired[table]}) expired limit ${batchSize}`;
      if (table === "job") {
        // Counted before the cascade removes them, for the log line.
        jobResults += await countOf(
          database,
          sql`select id from vectra_job_result where job_id in (${ids})`,
        );
      }
      const result = await database.execute(
        sql`delete from ${sql.raw(`vectra_${table}`)} where id in (${ids})`,
      );
      const deleted = affectedRows(result);
      counts[table] += deleted;
      if (deleted < batchSize) {
        break;
      }
      await sleep(pauseMs);
    }
  }
  return { enabled: true, dryRun, counts, jobResults };
}

function summarize(result: HistoryRetentionResult) {
  return `event_log=${result.counts.event_log} job=${result.counts.job} job_result=${result.jobResults} health_incident=${result.counts.health_incident} operator_push_alert=${result.counts.operator_push_alert}`;
}

const globalForRetention = globalThis as typeof globalThis & {
  __vectraHistoryRetentionTimer?: ReturnType<typeof setInterval>;
  __vectraHistoryRetentionRunning?: boolean;
};

// Same singleton shape as the snapshot and revision retention timers.
export function startHistoryRetention() {
  if (env.NODE_ENV === "test" || !env.VECTRA_HISTORY_RETENTION_ENABLED) {
    return false;
  }
  if (globalForRetention.__vectraHistoryRetentionTimer) {
    return true;
  }

  const run = async () => {
    if (globalForRetention.__vectraHistoryRetentionRunning) {
      return;
    }
    globalForRetention.__vectraHistoryRetentionRunning = true;
    try {
      const result = await runHistoryRetentionTick(db);
      if (result.dryRun) {
        console.warn(
          "[history-retention] dry-run: older than %d days, would delete %s; set VECTRA_RETENTION_DRY_RUN=false to apply",
          env.VECTRA_RETENTION_DAYS,
          summarize(result),
        );
      } else if (Object.values(result.counts).some((count) => count > 0)) {
        console.warn(
          "[history-retention] pruned rows older than %d days: %s",
          env.VECTRA_RETENTION_DAYS,
          summarize(result),
        );
      }
    } catch (error) {
      console.error("[history-retention]", error);
    } finally {
      globalForRetention.__vectraHistoryRetentionRunning = false;
    }
  };

  void run();

  globalForRetention.__vectraHistoryRetentionTimer = setInterval(
    () => void run(),
    env.VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS * 1000,
  );
  globalForRetention.__vectraHistoryRetentionTimer.unref?.();
  return true;
}

export function stopHistoryRetentionForTest() {
  if (globalForRetention.__vectraHistoryRetentionTimer) {
    clearInterval(globalForRetention.__vectraHistoryRetentionTimer);
    globalForRetention.__vectraHistoryRetentionTimer = undefined;
  }
  globalForRetention.__vectraHistoryRetentionRunning = false;
}
