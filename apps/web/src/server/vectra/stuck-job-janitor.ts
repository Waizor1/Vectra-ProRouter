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
// Threshold tuning: default `staleSeconds` is 3600 (1 hour) — a
// conservative upper bound. No observed production job in the daily-notes
// archive (2026-04 through 2026-06) exceeded ~10 min of runtime, but a
// stressed low-RAM router (AX3000T with RAM~80MB during subscription
// parse) could plausibly slow further. 1 h leaves comfortable headroom
// for legitimate work; an incident is still caught within one full
// sweep + threshold (≤70 min total). When per-job-type SLAs land in r31
// this static threshold becomes per-type expiry.

import { and, eq, isNull, lt, sql } from "drizzle-orm";

import { eventLog, jobs } from "@vectra/db";

import { env } from "~/env";
import { db } from "~/server/db";

type DatabaseClient = typeof db;

export type StuckJobJanitorResult = {
  enabled: boolean;
  scanned: number;
  cancelled: number;
  cancelledJobIds: string[];
  // Per-candidate failures are isolated from the rest of the sweep so a
  // single bad row doesn't strand the others. Surface the failed IDs so
  // an operator can investigate; they'll be retried on the next sweep.
  failedJobIds: string[];
};

const buildEmptyResult = (enabled: boolean): StuckJobJanitorResult => ({
  enabled,
  scanned: 0,
  cancelled: 0,
  cancelledJobIds: [],
  failedJobIds: [],
});

// Single sweep. Idempotent — running this twice back-to-back is safe; the
// second call finds nothing because the first already moved everything out
// of the 'running' state.
export async function runStuckJobJanitorTick(
  now: Date,
  database: DatabaseClient = db,
  options?: {
    staleSeconds?: number;
    enabled?: boolean;
    // Forensic metadata recorded on each cancelled job's audit row so an
    // operator-triggered sweep is distinguishable from the scheduled tick
    // when reading vectra_event_log later.
    triggeredBy?: "scheduled" | "operator";
    operatorUser?: string;
  },
): Promise<StuckJobJanitorResult> {
  const enabled = options?.enabled ?? env.VECTRA_STUCK_JOB_JANITOR_ENABLED;
  if (!enabled) {
    return buildEmptyResult(false);
  }

  const staleSeconds =
    options?.staleSeconds ?? env.VECTRA_STUCK_JOB_STALE_SECONDS;
  const threshold = new Date(now.getTime() - staleSeconds * 1000);

  // Find candidates first so we can return a list of what was cancelled. A
  // single combined UPDATE...RETURNING would be one round-trip, but the
  // intermediate read makes auditing / event-log writes straightforward.
  //
  // Age comparison uses coalesce(deliveredAt, createdAt) — NOT createdAt
  // alone. A job can sit in state='queued' for an extended window
  // (deliver_after delay, router offline, panel backlog) before it
  // transitions to 'running'. Using createdAt would mistakenly cancel a
  // legitimately-running job whose queue time was large but whose actual
  // execution time is well within bounds. Falling back to createdAt is a
  // belt-and-suspenders guard for data drift where deliveredAt is null
  // even though state='running'.
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
        lt(
          sql`coalesce(${jobs.deliveredAt}, ${jobs.createdAt})`,
          threshold,
        ),
      ),
    );

  if (candidates.length === 0) {
    return buildEmptyResult(true);
  }

  const cancelledIds: string[] = [];
  const failedCandidateIds: string[] = [];
  for (const candidate of candidates) {
    // Reference time for "how long has this job been live" is
    // deliveredAt when present (controller accepted it then) falling back
    // to createdAt for data-drift safety. Mirrors the WHERE clause above.
    const referenceTime = candidate.deliveredAt ?? candidate.createdAt;
    const ageSeconds = Math.round(
      (now.getTime() - referenceTime.getTime()) / 1000,
    );
    // Defense-in-depth: payload schemas don't cap artifactVersion length
    // (runTerminalCommandJobPayloadSchema accepts arbitrary strings). Clamp
    // here so an operator-curated payload with an outsized version string
    // can't bloat the eventLog metadata column.
    const rawArtifactVersion =
      typeof candidate.payload?.artifactVersion === "string"
        ? candidate.payload.artifactVersion
        : null;
    const artifactVersion = rawArtifactVersion
      ? rawArtifactVersion.slice(0, 200)
      : null;

    try {
      // Cancel and record in a single transaction so we never have an event
      // row pointing at a job we failed to update (or vice versa).
      await database.transaction(async (tx) => {
        const updated = await tx
          .update(jobs)
          .set({
            state: "cancelled",
            completedAt: now,
            // Clear dedupeKey so a future operator-triggered re-queue of
            // the same logical job (same dedupeKey, e.g.
            // `update_controller:<routerId>:stable:<version>`) doesn't
            // collide with this cancelled row on the unique index.
            // Without this, the recovery flow this PR enables would
            // immediately fail with a unique-constraint violation on the
            // next operator click — defeating the purpose. The canonical
            // operator-cancel path (draft.ts:442) and the normal
            // job-completion path (router-control.ts:212-225) already
            // null dedupeKey for the same reason; we mirror that
            // contract.
            dedupeKey: null,
          })
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

        // The audit row goes INSIDE the same transaction. If it throws
        // (disk full, JSONB size limit, future schema regression), the
        // entire transaction rolls back — the row stays in 'running' and
        // the next sweep retries. We push to cancelledIds AFTER the
        // insert succeeds, so a rolled-back tx doesn't over-report
        // cancellations in the operator-facing result shape.
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
            triggeredBy: options?.triggeredBy ?? "scheduled",
            operatorUser: options?.operatorUser ?? null,
          },
        });
        cancelledIds.push(candidate.id);
      });
    } catch (error) {
      // Per-candidate isolation: one bad row (deadlock, FK violation,
      // network blip) must not abort the sweep — the remaining stuck jobs
      // would stay un-cancelled until the next 10-min tick and continue
      // poisoning their routers' check-ins meanwhile.
      failedCandidateIds.push(candidate.id);
      console.error(
        "[stuck-job-janitor] failed to cancel job %s: %s",
        candidate.id,
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  return {
    enabled: true,
    scanned: candidates.length,
    cancelled: cancelledIds.length,
    cancelledJobIds: cancelledIds,
    failedJobIds: failedCandidateIds,
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
