// Inventory-snapshot retention.
//
// Every router check-in writes a row to vectra_router_inventory_snapshot and
// nothing ever pruned them. On 2026-07-31 the table held 406,716 rows / 1.3 GB
// — by a wide margin the largest in the database — growing ~29k rows/day for a
// 30-router fleet and pushing the VPS disk to 88%.
//
// The policy has two parts and BOTH must hold before a row is deleted:
//
//   1. It is not among the newest `keepPerRouter` snapshots for its router.
//   2. It is older than `retentionHours`.
//
// Part 1 is the important one. An age-only rule would erase every trace of a
// router that has been offline for weeks (childsergeyavito had not checked in
// for 47 days), leaving the panel with no last-known state to show or diagnose
// from. The per-router floor guarantees recent history survives regardless of
// age. The floor defaults to 20 while the hungriest reader (editor-surface)
// asks for 5, so there is 4x headroom.

import { sql } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = typeof db;

export type SnapshotRetentionResult = {
  enabled: boolean;
  deleted: number;
};

export async function runSnapshotRetentionTick(
  database: DatabaseClient = db,
  options?: {
    enabled?: boolean;
    retentionHours?: number;
    keepPerRouter?: number;
    maxPerTick?: number;
  },
): Promise<SnapshotRetentionResult> {
  const enabled = options?.enabled ?? env.VECTRA_SNAPSHOT_RETENTION_ENABLED;
  if (!enabled) {
    return { enabled: false, deleted: 0 };
  }

  const retentionHours =
    options?.retentionHours ?? env.VECTRA_SNAPSHOT_RETENTION_HOURS;
  const keepPerRouter =
    options?.keepPerRouter ?? env.VECTRA_SNAPSHOT_RETENTION_KEEP_PER_ROUTER;
  const maxPerTick = options?.maxPerTick ?? 50_000;

  // Deleted in one statement rather than select-then-delete: shipping ids back
  // to the application only to send them again is exactly the kind of needless
  // allocation that already cost this service its heap once.
  //
  // Capped per tick so a large backlog drains over several sweeps instead of
  // holding one long transaction. The first production sweep had 252k rows to
  // clear; steady state is a small fraction of that, so the cap only ever
  // binds when retention has been off for a while.
  const deleted = await database.execute(sql`
    with ranked as (
      select
        id,
        row_number() over (
          partition by router_id
          order by created_at desc
        ) as rn,
        created_at
      from vectra_router_inventory_snapshot
    )
    delete from vectra_router_inventory_snapshot
    where id in (
      select id
      from ranked
      where rn > ${keepPerRouter}
        and created_at < now() - make_interval(hours => ${retentionHours})
      limit ${maxPerTick}
    )
  `);

  // postgres.js exposes the affected-row count on the result object.
  const count =
    typeof (deleted as unknown as { count?: number }).count === "number"
      ? (deleted as unknown as { count: number }).count
      : 0;

  return { enabled: true, deleted: count };
}

const globalForRetention = globalThis as typeof globalThis & {
  __vectraSnapshotRetentionTimer?: ReturnType<typeof setInterval>;
  __vectraSnapshotRetentionRunning?: boolean;
};

// Singleton timer, same shape as the stuck-job janitor: hot reloads reuse the
// existing timer instead of stacking, and the tick is gated so a slow sweep
// cannot overlap itself.
export function startSnapshotRetention() {
  if (env.NODE_ENV === "test" || !env.VECTRA_SNAPSHOT_RETENTION_ENABLED) {
    return false;
  }
  if (globalForRetention.__vectraSnapshotRetentionTimer) {
    return true;
  }

  const run = async () => {
    if (globalForRetention.__vectraSnapshotRetentionRunning) {
      return;
    }
    globalForRetention.__vectraSnapshotRetentionRunning = true;
    try {
      const result = await runSnapshotRetentionTick(db);
      if (result.deleted > 0) {
        console.warn(
          "[snapshot-retention] pruned %d inventory snapshot(s)",
          result.deleted,
        );
      }
    } catch (error) {
      console.error("[snapshot-retention]", error);
    } finally {
      globalForRetention.__vectraSnapshotRetentionRunning = false;
    }
  };

  void run();

  globalForRetention.__vectraSnapshotRetentionTimer = setInterval(
    () => void run(),
    env.VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS * 1000,
  );
  globalForRetention.__vectraSnapshotRetentionTimer.unref?.();
  return true;
}

export function stopSnapshotRetentionForTest() {
  if (globalForRetention.__vectraSnapshotRetentionTimer) {
    clearInterval(globalForRetention.__vectraSnapshotRetentionTimer);
    globalForRetention.__vectraSnapshotRetentionTimer = undefined;
  }
  globalForRetention.__vectraSnapshotRetentionRunning = false;
}
