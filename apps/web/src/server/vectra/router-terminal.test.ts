import { describe, expect, it } from "vitest";

import { buildRouterTerminalHistory } from "./router-terminal";

describe("buildRouterTerminalHistory", () => {
  it("keeps the latest active request separate from the latest terminal result", () => {
    const history = buildRouterTerminalHistory({
      jobs: [
        {
          id: "job-running",
          state: "queued",
          payload: {
            command: "logread | tail -n 20",
            timeoutSeconds: 30,
          },
          createdAt: new Date("2026-04-14T10:05:00.000Z"),
          completedAt: null,
        },
        {
          id: "job-success",
          state: "succeeded",
          payload: {
            command: "ubus call system board",
            timeoutSeconds: 30,
          },
          createdAt: new Date("2026-04-14T10:00:00.000Z"),
          completedAt: new Date("2026-04-14T10:00:05.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-running",
          status: "accepted",
          payload: {},
          reportedAt: new Date("2026-04-14T10:05:01.000Z"),
        },
        {
          jobId: "job-success",
          status: "success",
          payload: {
            command: "ubus call system board",
            timeoutSeconds: 30,
            startedAt: "2026-04-14T10:00:01.000Z",
            completedAt: "2026-04-14T10:00:02.000Z",
            durationMs: 800,
            exitCode: 0,
            timedOut: false,
            stdout: "{ }",
            stderr: "",
            stdoutTruncated: false,
            stderrTruncated: false,
          },
          reportedAt: new Date("2026-04-14T10:00:03.000Z"),
        },
      ],
    });

    expect(history.activeRequest?.jobId).toBe("job-running");
    expect(history.latestResult?.jobId).toBe("job-success");
    expect(history.latestResult?.exitCode).toBe(0);
  });

  it("falls back to safe request defaults for malformed payloads", () => {
    const history = buildRouterTerminalHistory({
      jobs: [
        {
          id: "job-defaults",
          state: "failed",
          payload: {},
          createdAt: new Date("2026-04-14T11:00:00.000Z"),
          completedAt: new Date("2026-04-14T11:00:10.000Z"),
        },
      ],
      results: [
        {
          jobId: "job-defaults",
          status: "failure",
          payload: {
            command: "badcmd",
            timeoutSeconds: 30,
            startedAt: "2026-04-14T11:00:01.000Z",
            completedAt: "2026-04-14T11:00:02.000Z",
            durationMs: 500,
            timedOut: false,
            error: "terminal command failed with exit code 127",
          },
          reportedAt: new Date("2026-04-14T11:00:03.000Z"),
        },
      ],
    });

    expect(history.history[0]?.request.command).toBe("");
    expect(history.history[0]?.request.timeoutSeconds).toBe(30);
    expect(history.history[0]?.error).toContain("exit code 127");
  });
});
