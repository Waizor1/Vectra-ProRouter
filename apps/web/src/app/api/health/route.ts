import { env } from "~/env";
import { db } from "~/server/db";
import {
  type LoopTickTimes,
  loadRunningLoopPresence,
  loopTickTimes,
} from "~/server/vectra/background-lock";
import { startBackgroundLoops } from "~/server/vectra/background-loops";
import { loadHealthCheckinCounts } from "~/server/vectra/health-checkins";
import {
  checkDatabaseRead,
  checkDatabaseWrite,
} from "~/server/vectra/health-probe";

export const dynamic = "force-dynamic";

/**
 * Seconds since each loop last completed a tick / found it running elsewhere,
 * and — only once there were any — how many of its partner deliveries were
 * found hung and replaced (a tick completes either way).
 */
function tickAges(
  ticks: Partial<Record<string, LoopTickTimes>>,
  now: number,
) {
  const age = (at: number | null) =>
    at === null ? null : Math.max(0, Math.round((now - at) / 1000));
  return Object.fromEntries(
    Object.entries(ticks).map(([loop, times]) => [
      loop,
      {
        completedSecondsAgo: age(times?.completedAt ?? null),
        busyElsewhereSecondsAgo: age(times?.busyAt ?? null),
        ...(times?.staleFlights
          ? { staleFlightsReplaced: times.staleFlights }
          : {}),
      },
    ]),
  );
}

export async function GET() {
  const checkedAt = new Date().toISOString();
  const checks = {
    browserPushMonitor: false,
    autoRescueMonitor: false,
    stuckJobJanitor: false,
    snapshotRetention: false,
    revisionRetention: false,
    historyRetention: false,
    routeHealthVerifier: false,
    partnerWebhookDispatcher: false,
    dbRead: false,
    dbWriteProbe: false,
  };

  // "Running" is not "ticking": the age of each loop's last tick shows a loop
  // that is up but stuck. Informational only — never part of `ok`.
  let loopTicks: ReturnType<typeof tickAges> = {};

  try {
    // Each start* returns whether that lane is actually RUNNING, not merely
    // whether the call returned. Every one of these no-ops when its feature
    // flag is off, and this route used to set `true` straight after the call —
    // so health reported a healthy auto-rescue monitor for a monitor that had
    // never started. That is how VECTRA_AUTO_RESCUE_ENABLED went missing from
    // docker-compose.yml on 2026-08-24 and stayed unnoticed until 08-28: the
    // whole self-repair layer was dark while /api/health said it was up, and a
    // customer sat in direct mode for two days waiting for an unpark sweep that
    // could not run. A check that cannot report false is not a check.
    //
    // In worker-separate mode the web starts none of them; each check then
    // reports whether a live worker holds that loop's presence lock, which a
    // worker that died cannot (its connection, and the lock, are gone). The
    // web's own health does not depend on it: a failed read leaves them false.
    if (env.VECTRA_BACKGROUND_MODE === "worker-separate") {
      const presence = await loadRunningLoopPresence(db).catch(
        (error: unknown) => {
          console.error("[health] worker presence", error);
          return null;
        },
      );
      for (const loop of presence?.running ?? []) {
        checks[loop] = true;
      }
      loopTicks = tickAges(presence?.ticks ?? {}, Date.now());
    } else {
      Object.assign(checks, startBackgroundLoops());
      loopTicks = tickAges(loopTickTimes(), Date.now());
    }
    await checkDatabaseRead(db);
    checks.dbRead = true;
    // At most one insert+delete probe per 30 s (see health-probe.ts); a call
    // in between reports the last successful probe.
    await checkDatabaseWrite(db);
    checks.dbWriteProbe = true;
    // Optional, informational: lets an external monitor see fleet check-in
    // freshness without database access. null when the count is unreadable.
    const routers = await loadHealthCheckinCounts(db);

    return Response.json(
      {
        ok: true,
        service: "vectra-web",
        checkedAt,
        backgroundMode: env.VECTRA_BACKGROUND_MODE,
        checks,
        loopTicks,
        routers,
      },
      { status: 200 },
    );
  } catch (error) {
    console.error("[health]", error);
    return Response.json(
      {
        ok: false,
        service: "vectra-web",
        checkedAt,
        backgroundMode: env.VECTRA_BACKGROUND_MODE,
        checks,
        loopTicks,
        error: error instanceof Error ? error.message : "health check failed",
      },
      { status: 503 },
    );
  }
}
