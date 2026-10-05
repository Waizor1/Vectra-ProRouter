import { env } from "~/env";
import { startAutoRescueMonitor } from "~/server/vectra/auto-rescue";
import {
  BROWSER_PUSH_MONITOR_OFFSET_MS,
  startBrowserPushMonitor,
} from "~/server/vectra/browser-push-monitor";
import { startHistoryRetention } from "~/server/vectra/history-retention";
import {
  PARTNER_WEBHOOK_SWEEP_INTERVAL_MS,
  startPartnerWebhookDispatcher,
} from "~/server/vectra/partner-webhooks";
import { startRevisionRetention } from "~/server/vectra/revision-retention";
import {
  ROUTE_HEALTH_VERIFIER_INTERVAL_MS,
  startRouteHealthVerifier,
} from "~/server/vectra/route-health-verifier";
import { startSnapshotRetention } from "~/server/vectra/snapshot-retention";
import { startStuckJobJanitor } from "~/server/vectra/stuck-job-janitor";

import type { BackgroundLoopName, LoopTickTimes } from "./background-lock";

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

function loopIntervalMs(loop: BackgroundLoopName): number {
  switch (loop) {
    case "autoRescueMonitor":
      return env.VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS * 1000;
    case "browserPushMonitor":
      // Its first tick comes only after the offset.
      return (
        env.VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS * 1000 +
        BROWSER_PUSH_MONITOR_OFFSET_MS
      );
    case "stuckJobJanitor":
      return env.VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS * 1000;
    case "snapshotRetention":
      return env.VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS * 1000;
    case "revisionRetention":
      return env.VECTRA_REVISION_RETENTION_INTERVAL_SECONDS * 1000;
    case "historyRetention":
      return env.VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS * 1000;
    case "routeHealthVerifier":
      return ROUTE_HEALTH_VERIFIER_INTERVAL_MS;
    case "partnerWebhookDispatcher":
      return PARTNER_WEBHOOK_SWEEP_INTERVAL_MS;
  }
}

/** A loop that has not settled a tick for this long is stuck: max(3 intervals, 5 min). */
export function loopStallThresholdMs(loop: BackgroundLoopName): number {
  return Math.max(3 * loopIntervalMs(loop), 5 * 60 * 1000);
}

/**
 * The running loops that have neither completed a tick nor found another
 * process doing it for longer than their threshold, counted from `startedAt`
 * for a loop that has not settled one yet.
 */
export function findStalledLoops(
  running: BackgroundLoopName[],
  ticks: Partial<Record<BackgroundLoopName, LoopTickTimes>>,
  startedAt: number,
  now: number,
  thresholdMs: (loop: BackgroundLoopName) => number = loopStallThresholdMs,
) {
  return running.flatMap((loop) => {
    const times = ticks[loop];
    const settledAt = Math.max(
      startedAt,
      times?.completedAt ?? 0,
      times?.busyAt ?? 0,
    );
    const ageMs = now - settledAt;
    const limitMs = thresholdMs(loop);
    return ageMs > limitMs ? [{ loop, ageMs, thresholdMs: limitMs }] : [];
  });
}
