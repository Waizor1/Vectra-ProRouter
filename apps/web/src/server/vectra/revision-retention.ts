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
// Of those 5208, exactly 189 had ever been applied. The other 95% were
// `router_import` rows: the panel recording that a router reported a config,
// not an operator deciding anything.
//
// Four conditions must ALL hold before a revision is deleted:
//
//   1. origin = 'router_import' — never an operator's own draft.
//   2. status = 'approved' — never a draft, review, failure or queued item.
//   3. It is not among the newest `keepPerRouter` revisions for its router.
//   4. It is older than `retentionHours`.
//
// And independently: a revision referenced by vectra_passwall_applied_revision
// is never deleted, whatever its age. That row is the record of what a router
// was actually told to run; the FK is SET NULL, so deleting the revision would
// silently hollow out the applied history rather than fail loudly.
//
// Secret blobs cascade from the revision, so they need no separate sweep.

import { sql } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = typeof db;

export type RevisionRetentionResult = {
  enabled: boolean;
  deleted: number;
};

export async function runRevisionRetentionTick(
  database: DatabaseClient = db,
  options?: {
    enabled?: boolean;
    retentionHours?: number;
    keepPerRouter?: number;
    maxPerTick?: number;
  },
): Promise<RevisionRetentionResult> {
  const enabled = options?.enabled ?? env.VECTRA_REVISION_RETENTION_ENABLED;
  if (!enabled) {
    return { enabled: false, deleted: 0 };
  }

  const retentionHours =
    options?.retentionHours ?? env.VECTRA_REVISION_RETENTION_HOURS;
  const keepPerRouter =
    options?.keepPerRouter ?? env.VECTRA_REVISION_RETENTION_KEEP_PER_ROUTER;
  // Deliberately smaller than the snapshot cap: each row here drags a TOASTed
  // config, a raw snapshot and a cascading secret blob with it, so a sweep
  // moves far more bytes per row than a snapshot sweep does.
  const maxPerTick = options?.maxPerTick ?? 2_000;

  const deleted = await database.execute(sql`
    with ranked as (
      select
        id,
        origin,
        status,
        created_at,
        row_number() over (
          partition by router_id
          order by created_at desc
        ) as rn
      from vectra_passwall_desired_revision
    )
    delete from vectra_passwall_desired_revision
    where id in (
      select id
      from ranked
      where origin = ${"router_import"}
        and status = ${"approved"}
        and rn > ${keepPerRouter}
        and created_at < now() - make_interval(hours => ${retentionHours})
        and id not in (
          select desired_revision_id
          from vectra_passwall_applied_revision
          where desired_revision_id is not null
        )
      limit ${maxPerTick}
    )
  `);

  const count =
    typeof (deleted as unknown as { count?: number }).count === "number"
      ? (deleted as unknown as { count: number }).count
      : 0;

  return { enabled: true, deleted: count };
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
    return;
  }
  if (globalForRetention.__vectraRevisionRetentionTimer) {
    return;
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
}

export function stopRevisionRetentionForTest() {
  if (globalForRetention.__vectraRevisionRetentionTimer) {
    clearInterval(globalForRetention.__vectraRevisionRetentionTimer);
    globalForRetention.__vectraRevisionRetentionTimer = undefined;
  }
  globalForRetention.__vectraRevisionRetentionRunning = false;
}
