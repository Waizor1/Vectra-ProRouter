import { describe, expect, it } from "vitest";

import { buildRouterOptimizationBaselineHistory } from "./router-optimization";

describe("buildRouterOptimizationBaselineHistory", () => {
  it("keeps an active collection separate from the latest completed baseline", () => {
    const history = buildRouterOptimizationBaselineHistory({
      jobs: [
        {
          id: "job-running",
          state: "running",
          payload: {
            logSource: "passwall",
            logLines: 120,
            includeLogs: true,
            includeRoutes: false,
          },
          createdAt: new Date("2026-05-15T08:05:00.000Z"),
          completedAt: null,
        },
        {
          id: "job-success",
          state: "succeeded",
          payload: {
            logSource: "all",
            logLines: 160,
            includeLogs: true,
            includeRoutes: true,
          },
          createdAt: new Date("2026-05-15T08:00:00.000Z"),
          completedAt: new Date("2026-05-15T08:00:20.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-running",
          status: "accepted",
          payload: {},
          reportedAt: new Date("2026-05-15T08:05:01.000Z"),
        },
        {
          jobId: "job-success",
          status: "success",
          payload: {
            baselineVersion: "2026-05-15-v1",
            collectedAt: "2026-05-15T08:00:10.000Z",
            ok: true,
            passwallEnabled: true,
            resources: {
              memoryAvailableMb: 96,
            },
            serviceHealth: {
              passwall: "running",
            },
            safetyEvents: [],
            packageVersions: {},
            binaryVersions: {},
            processes: [
              {
                pid: 1234,
                role: "xray",
                command: "/tmp/etc/passwall2/bin/xray run",
                vmRssKb: 49680,
              },
            ],
            conntrack: {
              count: 313,
              max: 15360,
            },
            warnings: [],
            errors: [],
          },
          reportedAt: new Date("2026-05-15T08:00:11.000Z"),
        },
      ],
    });

    expect(history.activeRequest?.jobId).toBe("job-running");
    expect(history.latestBaseline?.jobId).toBe("job-success");
    expect(history.latestBaseline?.baselineVersion).toBe("2026-05-15-v1");
    expect(history.latestBaseline?.processCount).toBe(1);
    expect(history.latestBaseline?.processes[0]?.role).toBe("xray");
    expect(history.latestBaseline?.resources.memoryAvailableMb).toBe(96);
    expect(history.latestBaseline?.result?.conntrack.count).toBe(313);
  });

  it("falls back to safe request defaults for malformed payloads", () => {
    const history = buildRouterOptimizationBaselineHistory({
      jobs: [
        {
          id: "job-defaults",
          state: "failed",
          payload: {},
          createdAt: new Date("2026-05-15T09:00:00.000Z"),
          completedAt: new Date("2026-05-15T09:00:05.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-defaults",
          status: "failure",
          payload: {
            baselineVersion: "2026-05-15-v1",
            collectedAt: "2026-05-15T09:00:04.000Z",
            ok: false,
            passwallEnabled: false,
            resources: {},
            serviceHealth: {},
            safetyEvents: [],
            packageVersions: {},
            binaryVersions: {},
            processes: [],
            conntrack: {},
            warnings: ["resource guard blocked"],
            errors: [],
          },
          reportedAt: new Date("2026-05-15T09:00:04.000Z"),
        },
      ],
    });

    expect(history.history[0]?.request.logSource).toBe("all");
    expect(history.history[0]?.request.logLines).toBe(160);
    expect(history.history[0]?.request.includeLogs).toBe(true);
    expect(history.history[0]?.warnings).toEqual(["resource guard blocked"]);
  });

  it("preserves router resource guard failure evidence", () => {
    const history = buildRouterOptimizationBaselineHistory({
      jobs: [
        {
          id: "job-resource-guard",
          state: "failed",
          payload: {
            includeLogs: true,
            includeRoutes: true,
          },
          createdAt: new Date("2026-05-15T09:30:00.000Z"),
          completedAt: new Date("2026-05-15T09:30:02.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-resource-guard",
          status: "failure",
          payload: {
            code: "router_resource_guard",
            error: "Router resources are below safe diagnostic floors.",
            jobType: "collect_optimization_baseline",
            safetyClass: "diagnostic",
            retryable: true,
            reasons: ["MemAvailable 37 MB is below diagnostic floor 40 MB."],
            resources: {
              memoryAvailableMb: 37,
              memoryTotalMb: 256,
              overlayFreeMb: 24,
              tmpFreeMb: 64,
            },
            checkedAt: "2026-05-15T09:30:01Z",
          },
          reportedAt: new Date("2026-05-15T09:30:01.000Z"),
        },
      ],
    });

    expect(history.latestBaseline?.jobId).toBe("job-resource-guard");
    expect(history.latestBaseline?.resultStatus).toBe("failure");
    expect(history.latestBaseline?.ok).toBe(false);
    expect(history.latestBaseline?.collectedAt).toBe("2026-05-15T09:30:01Z");
    expect(history.latestBaseline?.resources.memoryAvailableMb).toBe(37);
    expect(history.latestBaseline?.warnings).toEqual([
      "MemAvailable 37 MB is below diagnostic floor 40 MB.",
    ]);
    expect(history.latestBaseline?.errors[0]).toContain(
      "router_resource_guard",
    );
    expect(history.latestBaseline?.result).toBeNull();
  });
});
