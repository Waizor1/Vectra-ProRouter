import { sql } from "drizzle-orm";

import { db } from "~/server/db";
import { startAutoRescueMonitor } from "~/server/vectra/auto-rescue";
import { startBrowserPushMonitor } from "~/server/vectra/browser-push-monitor";
import { startRevisionRetention } from "~/server/vectra/revision-retention";
import { startRouteHealthVerifier } from "~/server/vectra/route-health-verifier";
import { startSnapshotRetention } from "~/server/vectra/snapshot-retention";
import { startStuckJobJanitor } from "~/server/vectra/stuck-job-janitor";

export const dynamic = "force-dynamic";

export async function GET() {
  const checkedAt = new Date().toISOString();
  const checks = {
    browserPushMonitor: false,
    autoRescueMonitor: false,
    stuckJobJanitor: false,
    snapshotRetention: false,
    revisionRetention: false,
    routeHealthVerifier: false,
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
    checks.browserPushMonitor = startBrowserPushMonitor();
    checks.autoRescueMonitor = startAutoRescueMonitor();
    checks.stuckJobJanitor = startStuckJobJanitor();
    checks.snapshotRetention = startSnapshotRetention();
    checks.revisionRetention = startRevisionRetention();
    checks.routeHealthVerifier = startRouteHealthVerifier();
    await db.execute(sql`select 1`);
    checks.dbRead = true;
    const probeId = crypto.randomUUID();
    // The insert and the delete must be SEPARATE statements. A data-modifying
    // CTE and the outer statement share one snapshot, so a `delete` wrapped
    // around `insert ... returning` never sees the row it just inserted and
    // silently deletes nothing — that leaked one probe row per health check
    // and grew vectra_event_log to millions of rows. The transaction keeps the
    // pair atomic so a crash between them cannot leak a row either.
    await db.transaction(async (tx) => {
      await tx.execute(sql`
        insert into vectra_event_log (id, type, severity, message)
        values (${probeId}, 'health.db_write_probe', 'info', 'health route db write probe')
      `);
      await tx.execute(sql`
        delete from vectra_event_log where id = ${probeId}
      `);
    });
    checks.dbWriteProbe = true;

    return Response.json(
      {
        ok: true,
        service: "vectra-web",
        checkedAt,
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
        checks,
        error: error instanceof Error ? error.message : "health check failed",
      },
      { status: 503 },
    );
  }
}
