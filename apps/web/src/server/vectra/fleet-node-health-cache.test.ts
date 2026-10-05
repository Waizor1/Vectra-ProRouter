import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("~/server/db", () => ({ db: {} }));

/**
 * Each rebuild reads the policy configs once; every call hands out a new
 * generation, so a test can tell which rebuild a value came from and how
 * many rebuilds ran. `gate` holds a rebuild open until the test releases it.
 */
const state = vi.hoisted(() => ({
  generation: 0,
  gate: null as Promise<void> | null,
  fail: false,
}));

vi.mock("./fleet-monitoring-data", () => ({
  loadLatestSnapshots: async () => new Map(),
  loadLatestFleetPolicyConfigSummaries: async () => {
    state.generation += 1;
    const generation = state.generation;
    if (state.gate) {
      await state.gate;
    }
    if (state.fail) {
      throw new Error("db down");
    }
    return new Map([
      [
        "router-1",
        {
          id: `rev-${generation}`,
          routerId: "router-1",
          createdAt: new Date(0),
          config: { generation, nodes: [] },
        },
      ],
    ]);
  },
}));
vi.mock("./route-health-verifier", () => ({
  loadLatestRouteVerifications: async () => new Map(),
  routeVerificationToHealthSample: () => null,
}));

const {
  FIRST_BUILD_WAIT_MS,
  getFleetPolicyContext,
  resetFleetNodeHealthCache,
} = await import("./fleet-node-health-cache");

const database = {
  select: () => ({
    from: () => ({ orderBy: async () => [{ id: "router-1" }] }),
  }),
} as unknown as Parameters<typeof getFleetPolicyContext>[0];

const TTL_MS = 5 * 60 * 1000;

function generationOf(context: Awaited<ReturnType<typeof getFleetPolicyContext>>) {
  return (
    context.configByRouter.get("router-1") as { generation?: number } | undefined
  )?.generation;
}

function hold() {
  let release!: () => void;
  state.gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  return () => {
    state.gate = null;
    release();
  };
}

const flush = () => new Promise((resolve) => setImmediate(resolve));

describe("getFleetPolicyContext (stale-while-revalidate)", () => {
  beforeEach(() => {
    resetFleetNodeHealthCache();
    state.generation = 0;
    state.gate = null;
    state.fail = false;
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("awaits the first build, then serves it until it expires", async () => {
    const t0 = Date.now();
    expect(generationOf(await getFleetPolicyContext(database, t0))).toBe(1);
    expect(generationOf(await getFleetPolicyContext(database, t0 + 1000))).toBe(1);
    expect(state.generation).toBe(1);
  });

  it("an expired ledger is served at once while ONE background rebuild replaces it", async () => {
    const t0 = Date.now();
    await getFleetPolicyContext(database, t0);
    const release = hold();

    const expired = t0 + TTL_MS + 1;
    // Neither check-in waits for the rebuild, and they share one.
    const [first, second] = await Promise.all([
      getFleetPolicyContext(database, expired),
      getFleetPolicyContext(database, expired),
    ]);
    expect([generationOf(first), generationOf(second)]).toEqual([1, 1]);
    expect(state.generation).toBe(2);

    release();
    await flush();
    // The rebuilt ledger is fresh again from the moment it was built.
    expect(generationOf(await getFleetPolicyContext(database, Date.now()))).toBe(2);
    expect(state.generation).toBe(2);
  });

  it("a slow first build gives the check-in no health opinion instead of a wait", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const release = hold();
    const pending = getFleetPolicyContext(database, Date.now());
    await vi.advanceTimersByTimeAsync(FIRST_BUILD_WAIT_MS);
    const context = await pending;
    expect(context.configByRouter.size).toBe(0);
    expect(context.nodeHealth.unhealthyHosts).toEqual([]);
    // "Not known yet", not "known and empty": no directive is built from it.
    expect(context.unavailable).toBe(true);

    release();
    await vi.advanceTimersByTimeAsync(0);
    await flush();
    // The build kept going and the next check-in has it.
    const built = await getFleetPolicyContext(database, Date.now());
    expect(generationOf(built)).toBe(1);
    expect(built.unavailable).toBeUndefined();
    expect(state.generation).toBe(1);
  });

  it("a failed rebuild keeps the last value; with none, it is no opinion", async () => {
    state.fail = true;
    const t0 = Date.now();
    const failed = await getFleetPolicyContext(database, t0);
    expect(failed.configByRouter.size).toBe(0);
    // A failed build has always meant "no health opinion", directive included.
    expect(failed.unavailable).toBeUndefined();

    state.fail = false;
    expect(generationOf(await getFleetPolicyContext(database, t0))).toBe(2);

    state.fail = true;
    const expired = t0 + TTL_MS + 1;
    expect(generationOf(await getFleetPolicyContext(database, expired))).toBe(2);
    await flush();
    expect(generationOf(await getFleetPolicyContext(database, expired))).toBe(2);
  });
});
