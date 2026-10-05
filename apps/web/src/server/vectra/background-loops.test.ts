import { describe, expect, it, vi } from "vitest";

vi.mock("~/server/vectra/auto-rescue", () => ({ startAutoRescueMonitor: () => false }));
vi.mock("~/server/vectra/browser-push-monitor", () => ({
  BROWSER_PUSH_MONITOR_OFFSET_MS: 15_000,
  startBrowserPushMonitor: () => false,
}));
vi.mock("~/server/vectra/history-retention", () => ({ startHistoryRetention: () => false }));
vi.mock("~/server/vectra/partner-webhooks", () => ({
  PARTNER_WEBHOOK_SWEEP_INTERVAL_MS: 30_000,
  startPartnerWebhookDispatcher: () => false,
}));
vi.mock("~/server/vectra/revision-retention", () => ({ startRevisionRetention: () => false }));
vi.mock("~/server/vectra/route-health-verifier", () => ({
  ROUTE_HEALTH_VERIFIER_INTERVAL_MS: 15 * 60_000,
  startRouteHealthVerifier: () => false,
}));
vi.mock("~/server/vectra/snapshot-retention", () => ({ startSnapshotRetention: () => false }));
vi.mock("~/server/vectra/stuck-job-janitor", () => ({ startStuckJobJanitor: () => false }));
vi.mock("~/env", () => ({
  env: {
    VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS: 60,
    VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS: 60,
    VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS: 120,
    VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS: 3600,
    VECTRA_REVISION_RETENTION_INTERVAL_SECONDS: 3600,
    VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS: 3600,
  },
}));

const { findStalledLoops, loopStallThresholdMs } = await import("./background-loops");

const MIN = 60_000;

describe("worker stall watchdog", () => {
  it("allows max(3 intervals, 5 minutes) per loop", () => {
    expect(loopStallThresholdMs("autoRescueMonitor")).toBe(5 * MIN);
    expect(loopStallThresholdMs("stuckJobJanitor")).toBe(6 * MIN);
    expect(loopStallThresholdMs("historyRetention")).toBe(180 * MIN);
    expect(loopStallThresholdMs("routeHealthVerifier")).toBe(45 * MIN);
    expect(loopStallThresholdMs("partnerWebhookDispatcher")).toBe(5 * MIN);
  });

  it("flags only a loop that has settled no tick within its threshold", () => {
    const startedAt = 0;
    const now = 20 * MIN;
    const stalled = findStalledLoops(
      ["autoRescueMonitor", "stuckJobJanitor", "snapshotRetention", "routeHealthVerifier"],
      {
        // Hung: last completed 16 minutes ago.
        autoRescueMonitor: { completedAt: 4 * MIN, busyAt: null },
        // Another process has been doing the sweep: settled, not stuck.
        stuckJobJanitor: { completedAt: null, busyAt: 18 * MIN },
        // Hourly: 20 minutes without a tick is fine.
      },
      startedAt,
      now,
    );
    expect(stalled).toEqual([
      { loop: "autoRescueMonitor", ageMs: 16 * MIN, thresholdMs: 5 * MIN },
    ]);
  });

  it("counts a loop that never ticked from the worker's start", () => {
    expect(findStalledLoops(["autoRescueMonitor"], {}, 10 * MIN, 14 * MIN)).toEqual([]);
    expect(findStalledLoops(["autoRescueMonitor"], {}, 10 * MIN, 16 * MIN)).toHaveLength(1);
  });
});
