import { startAutoRescueMonitor } from "~/server/vectra/auto-rescue";
import { startBrowserPushMonitor } from "~/server/vectra/browser-push-monitor";
import { startHistoryRetention } from "~/server/vectra/history-retention";
import { startPartnerWebhookDispatcher } from "~/server/vectra/partner-webhooks";
import { startRevisionRetention } from "~/server/vectra/revision-retention";
import { startRouteHealthVerifier } from "~/server/vectra/route-health-verifier";
import { startSnapshotRetention } from "~/server/vectra/snapshot-retention";
import { startStuckJobJanitor } from "~/server/vectra/stuck-job-janitor";

import type { BackgroundLoopName } from "./background-lock";

/**
 * Start every background loop in THIS process and report which are actually
 * running. Each start* is idempotent and returns false when its feature flag
 * is off, so the result is the truth about the process, not a wish.
 *
 * Called by /api/health in the web (VECTRA_BACKGROUND_MODE=in-web) or once by
 * the worker entrypoint (worker-separate). The order matters only for the
 * push monitor, which offsets itself from auto-rescue to share its snapshot;
 * both must live in the same process for that, and they always do.
 */
export function startBackgroundLoops(): Record<BackgroundLoopName, boolean> {
  return {
    browserPushMonitor: startBrowserPushMonitor(),
    autoRescueMonitor: startAutoRescueMonitor(),
    stuckJobJanitor: startStuckJobJanitor(),
    snapshotRetention: startSnapshotRetention(),
    revisionRetention: startRevisionRetention(),
    historyRetention: startHistoryRetention(),
    routeHealthVerifier: startRouteHealthVerifier(),
    partnerWebhookDispatcher: startPartnerWebhookDispatcher(),
  };
}
