import { afterEach, describe, expect, it } from "vitest";

import {
  AUTO_RESCUE_FLEET_SNAPSHOT_MAX_AGE_MS as RESCUE_MAX,
  loadSharedFleetMonitoringSnapshot,
  PUSH_MONITOR_FLEET_SNAPSHOT_MAX_AGE_MS as PUSH_MAX,
  resetSharedFleetMonitoringSnapshotForTest,
} from "./fleet-monitoring-data";

/** An empty fleet; counts how often the fleet is actually read. */
function countingDatabase(options: { fail?: boolean } = {}) {
  const database = {
    reads: 0,
    select() {
      const chain = {
        from: () => chain,
        orderBy: async () => {
          database.reads += 1;
          if (options.fail) {
            throw new Error("db down");
          }
          return [];
        },
      };
      return chain;
    },
  };
  return database;
}

type Client = Parameters<typeof loadSharedFleetMonitoringSnapshot>[0];
const T0 = new Date("2026-10-05T12:00:00.000Z");
const at = (ms: number) => new Date(T0.getTime() + ms);

describe("loadSharedFleetMonitoringSnapshot", () => {
  afterEach(() => resetSharedFleetMonitoringSnapshotForTest());

  it("the push monitor 15 s after auto-rescue joins its read; the next minute reads again", async () => {
    const database = countingDatabase() as unknown as Client & { reads: number };
    const first = loadSharedFleetMonitoringSnapshot(database, T0, RESCUE_MAX);
    const joined = loadSharedFleetMonitoringSnapshot(database, at(15_000), PUSH_MAX);
    expect(joined).toBe(first);
    // The snapshot is the fleet as of the first caller's instant.
    expect((await joined).generatedAt).toBe(T0.toISOString());
    expect(database.reads).toBe(1);

    const nextTick = loadSharedFleetMonitoringSnapshot(database, at(60_000), RESCUE_MAX);
    expect(nextTick).not.toBe(first);
    await nextTick;
    expect(database.reads).toBe(2);
  });

  it("timers that drifted apart: each monitor reads its own instead of taking an old snapshot", async () => {
    const database = countingDatabase() as unknown as Client & { reads: number };
    await loadSharedFleetMonitoringSnapshot(database, T0, RESCUE_MAX);
    // Push monitor drifted to 21 s after auto-rescue.
    await loadSharedFleetMonitoringSnapshot(database, at(21_000), PUSH_MAX);
    expect(database.reads).toBe(2);
    // Auto-rescue drifted to 11 s after the push monitor's read: fresh too.
    await loadSharedFleetMonitoringSnapshot(database, at(32_000), RESCUE_MAX);
    expect(database.reads).toBe(3);
    // Push monitor 9 s later: within its 20 s, reused.
    await loadSharedFleetMonitoringSnapshot(database, at(41_000), PUSH_MAX);
    expect(database.reads).toBe(3);
    // Push monitor now ahead of auto-rescue: auto-rescue reuses its read
    // within 10 s, and not a moment longer.
    await loadSharedFleetMonitoringSnapshot(database, at(70_000), PUSH_MAX);
    expect(database.reads).toBe(4);
    await loadSharedFleetMonitoringSnapshot(database, at(80_000), RESCUE_MAX);
    expect(database.reads).toBe(4);
    await loadSharedFleetMonitoringSnapshot(database, at(90_001), RESCUE_MAX);
    expect(database.reads).toBe(5);
  });

  it("never hands one database's snapshot to another, nor an older caller a newer snapshot", async () => {
    const a = countingDatabase() as unknown as Client & { reads: number };
    const b = countingDatabase() as unknown as Client & { reads: number };
    await loadSharedFleetMonitoringSnapshot(a, T0, PUSH_MAX);
    await loadSharedFleetMonitoringSnapshot(b, T0, PUSH_MAX);
    expect([a.reads, b.reads]).toEqual([1, 1]);
    await loadSharedFleetMonitoringSnapshot(b, at(-1), PUSH_MAX);
    expect(b.reads).toBe(2);
  });

  it("does not keep a failed read: the next caller reads again", async () => {
    const failing = countingDatabase({ fail: true }) as unknown as Client & { reads: number };
    await expect(loadSharedFleetMonitoringSnapshot(failing, T0, PUSH_MAX)).rejects.toThrow("db down");
    await expect(loadSharedFleetMonitoringSnapshot(failing, at(1_000), PUSH_MAX)).rejects.toThrow("db down");
    expect(failing.reads).toBe(2);
  });
});
