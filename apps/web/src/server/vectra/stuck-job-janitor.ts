// Stuck-job janitor — periodically cancels jobs that have been in state
// 'running' for longer than the threshold and have no completed_at.
//
// The post-mortem failure mode this guard fixes (totchto-filiciy, 2026-06-04):
//
//   1. Panel queues update_controller / run_terminal_command(controller-self-update)
//      with artifactVersion=N+1.
//   2. Job is delivered → controller persists CurrentJob with
//      ExpectedControllerVersion=N+1 → starts the install.
//   3. Install kills the controller process before it can submit a terminal
//      job result (success or failure). Job state on the panel stays 'running'
//      forever.
//   4. A freshly-installed controller (manual recovery, different version M)
//      polls jobs. Panel returns the still-running job. Controller flushes
//      its now-stale PendingJobResult, fails the version check
//      (got=M ≠ want=N+1), aborts the run_once cycle BEFORE sending check-in.
//   5. Router goes silent from the panel's perspective. No remote remediation
//      is possible because the controller never reaches the check-in step.
//
// totchto stayed in this loop for nine days because no one realised the job
// state on the panel side was the actual root cause. This janitor catches
// the pattern within one sweep interval: every job that's been 'running'
// for longer than `staleSeconds` becomes 'cancelled', `completed_at=NOW()`,
// and an event-log row records the auto-cancellation. The controller's next
// poll returns no jobs → recoverJobJournal returns nil → check-in resumes.
//
// Threshold tuning: refresh_subscriptions can take 30+ minutes on a stressed
// AX3000T (RAM ~80MB during subscription parse). Default `staleSeconds` is
// 3600 (1 hour) — well beyond any legitimate job runtime but short enough
// that an incident is caught within one full sweep + threshold (≤70 min).

import { and, isNull, lt, eq, sql } from "drizzle-orm";

import { eventLog, jobs } from "@vectra/db";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = typeof db;

export type StuckJobJanitorResult = {
  enabled: boolean;
  scanned: number;
  cancelled: number;
  cancelledJobIds: string[];
};

const NOOP_RESULT: StuckJobJanitorResult = {
  enabled: false,
  scanned: 0,
  cancelled: 0,
  cancelledJobIds: [],
};

// Single sweep. Idempotent — running this twice back-to-back is safe; the
// second call finds nothing because the first already moved everything out
// of the 'running' state.
export async function runStuckJobJanitorTick(
  now: Date,
  database: DatabaseClient = db,
  options?: {
    staleSeconds?: number;
    enabled?: boolean;
  },
): Promise<StuckJobJanitorResult> {
  const enabled = options?.enabled ?? env.VECTRA_STUCK_JOB_JANITOR_ENABLED;
  if (!enabled) {
    return NOOP_RESULT;
  }

  const staleSeconds =
    options?.staleSeconds ?? env.VECTRA_STUCK_JOB_STALE_SECONDS;
  const threshold = new Date(now.getTime() - staleSeconds * 1000);

  // Find candidates first so we can return a list of what was cancelled. A
  // single combined UPDATE...RETURNING would be one round-trip, but the
  // intermediate read makes auditing / event-log writes straightforward.
  const candidates = await database
    .select({
      id: jobs.id,
      routerId: jobs.routerId,
      type: jobs.type,
      createdAt: jobs.createdAt,
      deliveredAt: jobs.deliveredAt,
      payload: jobs.payload,
    })
    .from(jobs)
    .where(
      and(
        eq(jobs.state, "running"),
        isNull(jobs.completedAt),
        lt(jobs.createdAt, threshold),
      ),
    );

  if (candidates.length === 0) {
    return { enabled: true, scanned: 0, cancelled: 0, cancelledJobIds: [] };
  }

  const cancelledIds: string[] = [];
  for (const candidate of candidates) {
    const ageSeconds = Math.round(
      (now.getTime() - candidate.createdAt.getTime()) / 1000,
    );
    const artifactVersion =
      typeof candidate.payload?.artifactVersion === "string"
        ? candidate.payload.artifactVersion
        : null;

    // Cancel and record in a single transaction so we never have an event
    // row pointing at a job we failed to update (or vice versa).
    await database.transaction(async (tx) => {
      const updated = await tx
        .update(jobs)
        .set({ state: "cancelled", completedAt: now })
        .where(
          and(
            eq(jobs.id, candidate.id),
            eq(jobs.state, "running"),
            isNull(jobs.completedAt),
          ),
        )
        .returning({ id: jobs.id });

      if (updated.length === 0) {
        // Someone else (concurrent janitor tick, manual SQL, controller
        // finishing the job right now) already moved this job to terminal
        // state. That's fine — skip the audit row.
        return;
      }

      cancelledIds.push(candidate.id);
      await tx.insert(eventLog).values({
        type: "fleet.stuck_job_cancelled",
        severity: "warning",
        message: `Auto-cancelled job ${candidate.id} stuck in 'running' for ${ageSeconds}s`,
        routerId: candidate.routerId,
        metadata: {
          jobId: candidate.id,
          jobType: candidate.type,
          ageSeconds,
          createdAt: candidate.createdAt.toISOString(),
          deliveredAt: candidate.deliveredAt?.toISOString() ?? null,
          artifactVersion,
          staleSecondsThreshold: staleSeconds,
        },
      });
    });
  }

  return {
    enabled: true,
    scanned: candidates.length,
    cancelled: cancelledIds.length,
    cancelledJobIds: cancelledIds,
  };
}

const globalForJanitor = globalThis as typeof globalThis & {
  __vectraStuckJobJanitorTimer?: ReturnType<typeof setInterval>;
  __vectraStuckJobJanitorRunning?: boolean;
};

// Singleton timer setup. Mirrors startAutoRescueMonitor — hot-reloads in
// dev re-use the existing timer instead of stacking, and the tick is gated
// on `__vectraStuckJobJanitorRunning` so a slow database can't pile up
// concurrent sweeps.
export function startStuckJobJanitor() {
  if (env.NODE_ENV === "test" || !env.VECTRA_STUCK_JOB_JANITOR_ENABLED) {
    return;
  }
  if (globalForJanitor.__vectraStuckJobJanitorTimer) {
    return;
  }

  const run = async () => {
    if (globalForJanitor.__vectraStuckJobJanitorRunning) {
      return;
    }
    globalForJanitor.__vectraStuckJobJanitorRunning = true;
    try {
      const result = await runStuckJobJanitorTick(new Date(), db);
      if (result.cancelled > 0) {
        console.warn(
          "[stuck-job-janitor] auto-cancelled %d stuck job(s): %o",
          result.cancelled,
          result.cancelledJobIds,
        );
      }
    } catch (error) {
      console.error("[stuck-job-janitor]", error);
    } finally {
      globalForJanitor.__vectraStuckJobJanitorRunning = false;
    }
  };

  // Don't wait for the first interval tick — sweep once on startup so an
  // already-stuck job is cleared during the next /healthz hit, not 10 min
  // later.
  void run();

  globalForJanitor.__vectraStuckJobJanitorTimer = setInterval(
    () => void run(),
    env.VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS * 1000,
  );
  // Don't keep the process alive for the timer; Next.js owns the lifecycle.
  globalForJanitor.__vectraStuckJobJanitorTimer.unref?.();
}

// Stop hook for tests. Production code never calls this — the timer lives
// for the lifetime of the Node process and is unref'd so it doesn't block
// shutdown.
export function stopStuckJobJanitorForTest() {
  if (globalForJanitor.__vectraStuckJobJanitorTimer) {
    clearInterval(globalForJanitor.__vectraStuckJobJanitorTimer);
    globalForJanitor.__vectraStuckJobJanitorTimer = undefined;
  }
  globalForJanitor.__vectraStuckJobJanitorRunning = false;
}

// Silence the unused-import warning when the SQL helper isn't needed in
// the current implementation but might be re-introduced in a future tick
// variant (e.g. metric collection via raw SQL).
void sql;
