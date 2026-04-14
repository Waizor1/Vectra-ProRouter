import { describe, expect, it } from "vitest";

import { buildRouterLogHistory } from "./router-logs";

describe("buildRouterLogHistory", () => {
  it("keeps the latest active request separate from the latest terminal snapshot", () => {
    const history = buildRouterLogHistory({
      jobs: [
        {
          id: "job-running",
          state: "queued",
          payload: { source: "passwall", lines: 120 },
          createdAt: new Date("2026-04-08T10:05:00.000Z"),
          completedAt: null,
        },
        {
          id: "job-success",
          state: "succeeded",
          payload: { source: "all", lines: 160 },
          createdAt: new Date("2026-04-08T10:00:00.000Z"),
          completedAt: new Date("2026-04-08T10:00:30.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-running",
          status: "accepted",
          payload: {},
          reportedAt: new Date("2026-04-08T10:05:01.000Z"),
        },
        {
          jobId: "job-success",
          status: "success",
          payload: {
            source: "all",
            requestedLines: 160,
            collectedAt: "2026-04-08T10:00:25.000Z",
            snapshots: [
              {
                id: "controller",
                label: "Vectra Controller",
                command: "logread -e 'vectra-controller|vectra' | tail -n 160",
                content: "ok",
                truncated: false,
              },
            ],
          },
          reportedAt: new Date("2026-04-08T10:00:26.000Z"),
        },
      ],
    });

    expect(history.activeRequest?.jobId).toBe("job-running");
    expect(history.latestSnapshot?.jobId).toBe("job-success");
    expect(history.latestSnapshot?.snapshots).toHaveLength(1);
  });

  it("falls back to default request values for malformed payloads", () => {
    const history = buildRouterLogHistory({
      jobs: [
        {
          id: "job-defaults",
          state: "failed",
          payload: {},
          createdAt: new Date("2026-04-08T11:00:00.000Z"),
          completedAt: new Date("2026-04-08T11:00:05.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-defaults",
          status: "failure",
          payload: {
            source: "all",
            requestedLines: 200,
            collectedAt: "2026-04-08T11:00:04.000Z",
            snapshots: [],
            error: "router offline",
          },
          reportedAt: new Date("2026-04-08T11:00:04.000Z"),
        },
      ],
    });

    expect(history.history[0]?.request.source).toBe("all");
    expect(history.history[0]?.request.lines).toBe(200);
    expect(history.history[0]?.error).toBe("router offline");
  });
});
