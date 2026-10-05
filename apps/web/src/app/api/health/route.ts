import { env } from "~/env";
import { db } from "~/server/db";
import { loadRunningLoopPresence } from "~/server/vectra/background-lock";
import { startBackgroundLoops } from "~/server/vectra/background-loops";
import {
  checkDatabaseRead,
  checkDatabaseWrite,
} from "~/server/vectra/health-probe";

export const dynamic = "force-dynamic";

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
      const running = await loadRunningLoopPresence(db).catch((error) => {
        console.error("[health] worker presence", error);
        return new Set<string>();
      });
      for (const loop of running) {
        checks[loop as keyof typeof checks] = true;
      }
    } else {
      Object.assign(checks, startBackgroundLoops());
    }
    await checkDatabaseRead(db);
    checks.dbRead = true;
    // At most one insert+delete probe per 30 s (see health-probe.ts); a call
    // in between reports the last successful probe.
    await checkDatabaseWrite(db);
    checks.dbWriteProbe = true;

    return Response.json(
      {
        ok: true,
        service: "vectra-web",
        checkedAt,
        backgroundMode: env.VECTRA_BACKGROUND_MODE,
        checks,
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
        error: error instanceof Error ? error.message : "health check failed",
      },
      { status: 503 },
    );
  }
}
