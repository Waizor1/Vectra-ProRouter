import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * VECTRA_BACKGROUND_MODE=worker-separate: the web must not start a single
 * background loop (the worker runs them), and /api/health must still report
 * each loop truthfully — from the worker's presence locks, not from a wish.
 */
const state = vi.hoisted(() => ({
  presence: new Set<string>(),
  ticks: {} as Record<string, { completedAt: number | null; busyAt: number | null }>,
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
    return { running: state.presence, ticks: state.ticks };
  },
  loopTickTimes: () => {
    throw new Error("the web has no loop ticks of its own in worker-separate mode");
  },
}));

const { GET } = await import("./route");
const { resetHealthProbeForTest } = await import("~/server/vectra/health-probe");

type HealthBody = {
  ok: boolean;
  backgroundMode: string;
  checks: Record<string, boolean>;
  loopTicks: Record<string, { completedSecondsAgo: number | null; busyElsewhereSecondsAgo: number | null }>;
};

async function call() {
  const response = await GET();
  return { status: response.status, body: (await response.json()) as HealthBody };
}

describe("/api/health with the loops in a separate worker", () => {
  beforeEach(() => {
    resetHealthProbeForTest();
    state.presence = new Set();
    state.ticks = {};
    state.presenceError = null;
    startBackgroundLoops.mockClear();
  });

  it("starts no loop and reports the ones a live worker holds", async () => {
    state.presence = new Set(["autoRescueMonitor", "stuckJobJanitor"]);
    state.ticks = {
      autoRescueMonitor: { completedAt: Date.now() - 42_000, busyAt: null },
      stuckJobJanitor: { completedAt: null, busyAt: Date.now() - 7_000 },
    };
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
    // Up is not the same as ticking: the worker's last tick ages ride along.
    expect(body.loopTicks).toEqual({
      autoRescueMonitor: { completedSecondsAgo: 42, busyElsewhereSecondsAgo: null },
      stuckJobJanitor: { completedSecondsAgo: null, busyElsewhereSecondsAgo: 7 },
    });
  });

  it("reports how many hung partner deliveries the worker replaced", async () => {
    state.presence = new Set(["partnerWebhookDispatcher"]);
    state.ticks = {
      partnerWebhookDispatcher: { completedAt: Date.now() - 5_000, busyAt: null, staleFlights: 2 },
    } as typeof state.ticks;
    const { body } = await call();

    expect(body.loopTicks).toEqual({
      partnerWebhookDispatcher: {
        completedSecondsAgo: 5,
        busyElsewhereSecondsAgo: null,
        staleFlightsReplaced: 2,
      },
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
