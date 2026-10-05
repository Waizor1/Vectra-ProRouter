import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * VECTRA_BACKGROUND_MODE=worker-separate: the web must not start a single
 * background loop (the worker runs them), and /api/health must still report
 * each loop truthfully — from the worker's presence locks, not from a wish.
 */
const state = vi.hoisted(() => ({
  presence: new Set<string>(),
  presenceError: null as Error | null,
}));

vi.mock("~/env", () => ({ env: { VECTRA_BACKGROUND_MODE: "worker-separate" } }));
vi.mock("~/server/db", () => ({
  db: {
    execute: async () => [],
    transaction: async (run: (tx: { execute: () => Promise<unknown> }) => Promise<void>) =>
      run({ execute: async () => [] }),
  },
}));
const startBackgroundLoops = vi.fn(() => {
  throw new Error("the web must not start background loops in worker-separate mode");
});
vi.mock("~/server/vectra/background-loops", () => ({ startBackgroundLoops }));
vi.mock("~/server/vectra/background-lock", () => ({
  loadRunningLoopPresence: async () => {
    if (state.presenceError) throw state.presenceError;
    return state.presence;
  },
}));

const { GET } = await import("./route");
const { resetHealthProbeForTest } = await import("~/server/vectra/health-probe");

type HealthBody = {
  ok: boolean;
  backgroundMode: string;
  checks: Record<string, boolean>;
};

async function call() {
  const response = await GET();
  return { status: response.status, body: (await response.json()) as HealthBody };
}

describe("/api/health with the loops in a separate worker", () => {
  beforeEach(() => {
    resetHealthProbeForTest();
    state.presence = new Set();
    state.presenceError = null;
    startBackgroundLoops.mockClear();
  });

  it("starts no loop and reports the ones a live worker holds", async () => {
    state.presence = new Set(["autoRescueMonitor", "stuckJobJanitor"]);
    const { status, body } = await call();

    expect(startBackgroundLoops).not.toHaveBeenCalled();
    expect(status).toBe(200);
    expect(body.backgroundMode).toBe("worker-separate");
    expect(body.checks).toEqual({
      browserPushMonitor: false,
      autoRescueMonitor: true,
      stuckJobJanitor: true,
      snapshotRetention: false,
      revisionRetention: false,
      historyRetention: false,
      routeHealthVerifier: false,
      partnerWebhookDispatcher: false,
      dbRead: true,
      dbWriteProbe: true,
    });
  });

  it("reports every loop down when no worker is alive, and stays healthy itself", async () => {
    const { status, body } = await call();
    expect(status).toBe(200);
    expect(Object.entries(body.checks).filter(([, up]) => up).map(([name]) => name)).toEqual([
      "dbRead",
      "dbWriteProbe",
    ]);
  });

  it("does not fail the web's health when the presence read fails", async () => {
    const quiet = vi.spyOn(console, "error").mockImplementation(() => undefined);
    state.presenceError = new Error("pg_locks unavailable");
    const { status, body } = await call();
    expect(status).toBe(200);
    expect(body.checks.autoRescueMonitor).toBe(false);
    expect(startBackgroundLoops).not.toHaveBeenCalled();
    quiet.mockRestore();
  });
});
