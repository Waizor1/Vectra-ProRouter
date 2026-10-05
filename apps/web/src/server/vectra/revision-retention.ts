// Desired-revision retention.
//
// Every router check-in that reports a changed config writes a new revision,
// and each one carries three heavy payloads: a jsonb config, a jsonb
// raw_imported_snapshot larger than the config itself, and an encrypted secret
// blob. Nothing pruned them. By 2026-08-07 the table held 5208 revisions — 168
// per router over four months, 1.4 GB across the revision and secret tables,
// 61% of the entire database — and grew ~60 revisions/day for a 30-router
// fleet.
//
// Two rules, each deleting only a revision that is NOT protected (below) and
// not among the newest `keepPerRouter` revisions of its router:
//
//   1. Superseded auto-imports (since 2026-08): origin = 'router_import',
//      status = 'approved', older than `retentionHours`. 95% of the table.
//   2. The 30-day history rule (2026-10-05, the owner's decision): any origin
//      and status except an open draft or a queued apply, older than
//      `retentionDays`. This is what removes the review/out-of-sync imports
//      that rule 1 never touched (~7 MB per router by 2026-10). It honours
//      VECTRA_RETENTION_DRY_RUN: in dry-run it only counts.
//
// A revision is protected — never deleted, whatever its age — while anything
// points at it:
//
//   - a router's active / last applied / pending import revision,
//   - any job's desired_revision_id, or an unfinished job's payload
//     restoreRevisionId (subscription rescue restores to it),
//   - vectra_passwall_applied_revision: the record of what a router was told
//     to run (FK is SET NULL, so a delete would silently hollow it out),
//   - an onboarding run's active_revision_id,
//   - each router's latest router_import/operator_reimport: the config the
//     check-in route-policy directive and the node-health ledger fall back to,
//   - the previous PassWall revision of every revision a check-in can serve
//     (a router's active or last applied one, an unfinished job's): the
//     check-in diffs against it, and deleting it would change the apply
//     impact (restart/refresh flags) the agent is handed.
//
// Secret blobs cascade from the revision, so they need no separate sweep.

import { sql, type SQL } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = Pick<typeof db, "execute">;

export type RevisionRetentionResult = {
  enabled: boolean;
  deleted: number;
  /** Rule 2 in dry-run: how many revisions it would delete now. */
  wouldDelete?: number;
};

// Rows here are heavy (a TOASTed config, a raw snapshot, a cascading secret
// blob), so batches stay small whatever VECTRA_RETENTION_BATCH_SIZE says.
const REVISION_BATCH_LIMIT = 500;

/** Every revision id something still points at; see the header. */
const protectedRevisionIds = sql`
  select active_revision_id as id from vectra_router
  union select last_applied_revision_id from vectra_router
  union select pending_import_revision_id from vectra_router
  union select desired_revision_id from vectra_job
  union select payload->>'restoreRevisionId' from vectra_job
    where state in ('queued', 'delivered', 'running')
  union select desired_revision_id from vectra_passwall_applied_revision
  union select active_revision_id from vectra_router_onboarding_run
  union (
    select distinct on (router_id) id
    from vectra_passwall_desired_revision
    where origin in ('router_import', 'operator_reimport')
    order by router_id, created_at desc
  )
`;

/**
 * The revisions a check-in can hand a router as its desired revision
 * (resolveDesiredRevisionWithDb): an unfinished job's, the active one, the
 * last applied one. Their diff base is protected too.
 */
const servableRevisionIds = sql`
  select active_revision_id as id from vectra_router
  union select last_applied_revision_id from vectra_router
  union select desired_revision_id from vectra_job
    where state in ('queued', 'delivered', 'running')
`;

function candidateRevisions(rule: SQL, keepPerRouter: number) {
  return sql`
    with ranked as (
      select
        id,
        router_id,
        revision_number,
        engine_mode,
        origin,
        status,
        created_at,
        row_number() over (
          partition by router_id
          order by created_at desc
        ) as rn
      from vectra_passwall_desired_revision
    ),
    referenced as (
      select id from (${protectedRevisionIds}) refs where id is not null
    ),
    servable as (
      select id from (${servableRevisionIds}) served where id is not null
    ),
    diff_bases as (
      select (
        select prev.id
        from vectra_passwall_desired_revision prev
        where prev.router_id = cur.router_id
          and prev.revision_number < cur.revision_number
          and prev.engine_mode = 'passwall'
        order by prev.revision_number desc
        limit 1
      ) as id
      from vectra_passwall_desired_revision cur
      where cur.id in (select id from servable)
    )
    select ranked.id
    from ranked
    where rn > ${keepPerRouter}
      and (${rule})
      and not exists (select 1 from referenced r where r.id = ranked.id)
      and not exists (select 1 from diff_bases b where b.id = ranked.id)
  `;
}

function affectedRows(result: unknown) {
  // postgres.js exposes the affected-row count on the result object.
  return typeof (result as { count?: number }).count === "number"
    ? (result as { count: number }).count
    : 0;
}

async function deleteInBatches(
  database: DatabaseClient,
  candidates: SQL,
  maxPerTick: number,
) {
  let deleted = 0;
  while (deleted < maxPerTick) {
    const batch = Math.min(REVISION_BATCH_LIMIT, maxPerTick - deleted);
    // Rows are locked FOR UPDATE SKIP LOCKED before they go. A job being
    // queued for one of them right now (its FK check holds a KEY SHARE lock
    // until it commits) makes that revision skipped instead of deleted from
    // under it - the job's desired_revision_id would otherwise be nulled by
    // the SET NULL FK. A job inserted after the lock fails its FK check
    // loudly instead.
    const result = await database.execute(sql`
      delete from vectra_passwall_desired_revision
      where id in (
        select target.id
        from vectra_passwall_desired_revision target
        where target.id in (select id from (${candidates}) c)
        limit ${batch}
        for update of target skip locked
      )
    `);
    const count = affectedRows(result);
    deleted += count;
    if (count < batch) {
      break;
    }
  }
  return deleted;
}

export async function runRevisionRetentionTick(
  database: DatabaseClient = db,
  options?: {
    enabled?: boolean;
    retentionHours?: number;
    keepPerRouter?: number;
    maxPerTick?: number;
    /** Rule 2 off when null. */
    retentionDays?: number | null;
    dryRun?: boolean;
  },
): Promise<RevisionRetentionResult> {
  const enabled = options?.enabled ?? env.VECTRA_REVISION_RETENTION_ENABLED;
  if (!enabled) {
    return { enabled: false, deleted: 0 };
  }

  const retentionHours =
    options?.retentionHours ?? env.VECTRA_REVISION_RETENTION_HOURS;
  // At least three per router, whatever is configured.
  const keepPerRouter = Math.max(
    3,
    options?.keepPerRouter ?? env.VECTRA_REVISION_RETENTION_KEEP_PER_ROUTER,
  );
  const maxPerTick = options?.maxPerTick ?? 2_000;
  const retentionDays =
    options?.retentionDays === undefined
      ? env.VECTRA_HISTORY_RETENTION_ENABLED
        ? env.VECTRA_RETENTION_DAYS
        : null
      : options.retentionDays;
  const dryRun = options?.dryRun ?? env.VECTRA_RETENTION_DRY_RUN;

  const importRule = sql`
    origin = ${"router_import"}
    and status = ${"approved"}
    and created_at < now() - make_interval(hours => ${retentionHours})
  `;
  let deleted = await deleteInBatches(
    database,
    candidateRevisions(importRule, keepPerRouter),
    maxPerTick,
  );

  if (retentionDays === null || retentionDays === undefined) {
    return { enabled: true, deleted };
  }

  const historyRule = sql`
    status not in (${"draft"}, ${"queued"})
    and created_at < now() - make_interval(days => ${retentionDays})
  `;
  const historyCandidates = candidateRevisions(historyRule, keepPerRouter);
  if (dryRun) {
    const [row] = (await database.execute(sql`
      select count(*)::int as count from (${historyCandidates}) c
    `)) as unknown as Array<{ count: number }>;
    return { enabled: true, deleted, wouldDelete: row?.count ?? 0 };
  }

  deleted += await deleteInBatches(
    database,
    historyCandidates,
    Math.max(0, maxPerTick - deleted),
  );
  return { enabled: true, deleted };
}

const globalForRetention = globalThis as typeof globalThis & {
  __vectraRevisionRetentionTimer?: ReturnType<typeof setInterval>;
  __vectraRevisionRetentionRunning?: boolean;
};

// Same singleton shape as the snapshot retention timer: hot reloads reuse the
// existing timer instead of stacking, and the tick is gated so a slow sweep
// cannot overlap itself.
export function startRevisionRetention() {
  if (env.NODE_ENV === "test" || !env.VECTRA_REVISION_RETENTION_ENABLED) {
    return false;
  }
  if (globalForRetention.__vectraRevisionRetentionTimer) {
    return true;
  }

  const run = async () => {
    if (globalForRetention.__vectraRevisionRetentionRunning) {
      return;
    }
    globalForRetention.__vectraRevisionRetentionRunning = true;
    try {
      const result = await runRevisionRetentionTick(db);
      if (result.deleted > 0) {
        console.warn(
          "[revision-retention] pruned %d desired revision(s)",
          result.deleted,
        );
      }
      if (result.wouldDelete !== undefined) {
        console.warn(
          "[revision-retention] dry-run: the %d-day rule would delete %d unreferenced revision(s); set VECTRA_RETENTION_DRY_RUN=false to apply",
          env.VECTRA_RETENTION_DAYS,
          result.wouldDelete,
        );
      }
    } catch (error) {
      console.error("[revision-retention]", error);
    } finally {
      globalForRetention.__vectraRevisionRetentionRunning = false;
    }
  };

  void run();

  globalForRetention.__vectraRevisionRetentionTimer = setInterval(
    () => void run(),
    env.VECTRA_REVISION_RETENTION_INTERVAL_SECONDS * 1000,
  );
  globalForRetention.__vectraRevisionRetentionTimer.unref?.();
  return true;
}

export function stopRevisionRetentionForTest() {
  if (globalForRetention.__vectraRevisionRetentionTimer) {
    clearInterval(globalForRetention.__vectraRevisionRetentionTimer);
    globalForRetention.__vectraRevisionRetentionTimer = undefined;
  }
  globalForRetention.__vectraRevisionRetentionRunning = false;
}
