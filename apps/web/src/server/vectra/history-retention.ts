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
//                       so those rows stay. Also kept: jobs an attempt cap
//                       still counts (created since an open incident opened or
//                       an active rescue case started, same router), a job an
//                       onboarding run points at (last_job_id), and the newest
//                       job of each type per router. Results go with their
//                       job (FK cascade).
//   health_incident     resolved before the window
//   operator_push_alert resolved before the window (its dedupe key carries the
//                       episode start, so a later episode is a new key)
//
//   rescue_case         resolved before the window (resolved_at, or
//                       updated_at for a row without one), beyond the newest
//                       50 cases of its router (the rescue list shows the
//                       newest 50 fleet-wide), and not referenced by any job:
//                       a job carrying the case id in its payload
//                       (run_rescue_repair) or keyed by it (auto_rescue_repair:
//                       / auto_rescue_logs:). Nothing has a foreign key into
//                       the table; the code reads only active cases, one case
//                       by id, and the newest 50. A job's results go with the
//                       job, so they cannot outlive the reference.
//
// Open incidents, unresolved alerts, unfinished jobs and their results, and
// active rescue cases are never touched. Applied-revision records and partner
// webhooks are not swept here. Desired revisions are pruned by
// revision-retention.ts.
//
// VECTRA_RETENTION_DRY_RUN (default true) makes a tick count and log instead
// of delete. Rescue cases have a second switch,
// VECTRA_RESCUE_CASE_RETENTION_DRY_RUN (default true): they are deleted only
// when both are false, and counted otherwise.

import { sql, type SQL } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = Pick<typeof db, "execute">;

export type HistoryRetentionTable =
  | "event_log"
  | "job"
  | "health_incident"
  | "operator_push_alert"
  | "rescue_case";

/** Rescue cases a router keeps however old: the newest this many. */
export const RESCUE_CASE_KEEP_PER_ROUTER = 50;

export type HistoryRetentionResult = {
  enabled: boolean;
  dryRun: boolean;
  /** Rescue cases were only counted (either switch still on). */
  rescueCaseDryRun: boolean;
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
      select j.id from vectra_job j
      where j.state in ${TERMINAL_JOB_STATES}
        and j.dedupe_key is null
        and j.created_at < ${cutoff}
        and (j.completed_at is null or j.completed_at < ${cutoff})
        -- Attempt caps count job rows since an anchor, not by age: auto-rescue's
        -- reconnect cap since the open incident's opened_at, its repair cap
        -- since the active rescue case's started_at. Those rows stay for as
        -- long as the incident / case does, or the cap would reset.
        and not exists (
          select 1 from vectra_health_incident i
          where i.router_id = j.router_id
            and i.state = 'open'
            and j.created_at >= i.opened_at
        )
        and not exists (
          select 1 from vectra_rescue_case c
          where c.router_id = j.router_id
            and c.state in ('open', 'repairing', 'escalated', 'silenced')
            and j.created_at >= c.started_at
        )
        -- The FK from an onboarding run (SET NULL) would hollow it out.
        and not exists (
          select 1 from vectra_router_onboarding_run r where r.last_job_id = j.id
        )
        -- The newest job of each type per router stays: "the last X" reads
        -- (latest route verification, last refresh/apply for cooldowns).
        and exists (
          select 1 from vectra_job newer
          where newer.router_id = j.router_id
            and newer.type = j.type
            and newer.created_at > j.created_at
        )
      order by j.created_at
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
    rescue_case: sql`
      select c.id from (
        select id, state, coalesce(resolved_at, updated_at) as closed_at,
          row_number() over (
            partition by router_id order by started_at desc, id desc
          ) as recency
        from vectra_rescue_case
      ) c
      where c.state = 'resolved'
        and c.closed_at < ${cutoff}
        and c.recency > ${RESCUE_CASE_KEEP_PER_ROUTER}
        and not exists (
          select 1 from vectra_job j where j.payload ->> 'caseId' = c.id
        )
        and not exists (
          select 1 from vectra_job j
          where j.dedupe_key in ('auto_rescue_repair:' || c.id, 'auto_rescue_logs:' || c.id)
        )
      order by c.closed_at
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
    /** Rescue cases only; deleted when this and `dryRun` are both false. */
    rescueCaseDryRun?: boolean;
  },
): Promise<HistoryRetentionResult> {
  const enabled = options?.enabled ?? env.VECTRA_HISTORY_RETENTION_ENABLED;
  const dryRun = options?.dryRun ?? env.VECTRA_RETENTION_DRY_RUN;
  // Anything but an explicit false keeps the rescue cases.
  const rescueCaseDryRun =
    dryRun ||
    (options?.rescueCaseDryRun ??
      env.VECTRA_RESCUE_CASE_RETENTION_DRY_RUN !== false);
  const counts: Record<HistoryRetentionTable, number> = {
    event_log: 0,
    job: 0,
    health_incident: 0,
    operator_push_alert: 0,
    rescue_case: 0,
  };
  if (!enabled) {
    return { enabled: false, dryRun, rescueCaseDryRun, counts, jobResults: 0 };
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
    return { enabled: true, dryRun, rescueCaseDryRun, counts, jobResults };
  }

  let jobResults = 0;
  for (const table of Object.keys(expired) as HistoryRetentionTable[]) {
    if (table === "rescue_case" && rescueCaseDryRun) {
      counts[table] = await countOf(database, expired[table]);
      continue;
    }
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
  return { enabled: true, dryRun, rescueCaseDryRun, counts, jobResults };
}

function summarize(result: HistoryRetentionResult) {
  return `event_log=${result.counts.event_log} job=${result.counts.job} job_result=${result.jobResults} health_incident=${result.counts.health_incident} operator_push_alert=${result.counts.operator_push_alert} rescue_case=${result.counts.rescue_case}${!result.dryRun && result.rescueCaseDryRun ? " (rescue_case dry-run: counted, not deleted)" : ""}`;
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
