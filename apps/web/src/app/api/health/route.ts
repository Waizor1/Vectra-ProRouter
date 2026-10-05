import { db } from "~/server/db";
import { startAutoRescueMonitor } from "~/server/vectra/auto-rescue";
import { startBrowserPushMonitor } from "~/server/vectra/browser-push-monitor";
import {
  checkDatabaseRead,
  checkDatabaseWrite,
} from "~/server/vectra/health-probe";
import { startPartnerWebhookDispatcher } from "~/server/vectra/partner-webhooks";
import { startHistoryRetention } from "~/server/vectra/history-retention";
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
    checks.browserPushMonitor = startBrowserPushMonitor();
    checks.autoRescueMonitor = startAutoRescueMonitor();
    checks.stuckJobJanitor = startStuckJobJanitor();
    checks.snapshotRetention = startSnapshotRetention();
    checks.revisionRetention = startRevisionRetention();
    checks.historyRetention = startHistoryRetention();
    checks.routeHealthVerifier = startRouteHealthVerifier();
    checks.partnerWebhookDispatcher = startPartnerWebhookDispatcher();
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
