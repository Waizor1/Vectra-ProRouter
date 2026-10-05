import { afterEach, describe, expect, it } from "vitest";

import {
  loadSharedFleetMonitoringSnapshot,
  resetSharedFleetMonitoringSnapshotForTest,
  SHARED_FLEET_SNAPSHOT_MAX_AGE_MS,
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

describe("loadSharedFleetMonitoringSnapshot", () => {
  afterEach(() => resetSharedFleetMonitoringSnapshotForTest());

  it("reads the fleet once for both monitors within the window, then again on the next tick", async () => {
    const database = countingDatabase();
    const t0 = new Date("2026-10-05T12:00:00.000Z");
    const first = loadSharedFleetMonitoringSnapshot(database as unknown as Client, t0);
    const joined = loadSharedFleetMonitoringSnapshot(
      database as unknown as Client,
      new Date(t0.getTime() + 15_000),
    );
    expect(joined).toBe(first);
    // The snapshot is the fleet as of the first caller's instant.
    expect((await joined).generatedAt).toBe(t0.toISOString());
    expect(database.reads).toBe(1);

    const nextTick = loadSharedFleetMonitoringSnapshot(
      database as unknown as Client,
      new Date(t0.getTime() + SHARED_FLEET_SNAPSHOT_MAX_AGE_MS),
    );
    expect(nextTick).not.toBe(first);
    await nextTick;
    expect(database.reads).toBe(2);
  });

  it("never hands one database's snapshot to another, nor an older caller a newer snapshot", async () => {
    const a = countingDatabase();
    const b = countingDatabase();
    const t0 = new Date("2026-10-05T12:00:00.000Z");
    await loadSharedFleetMonitoringSnapshot(a as unknown as Client, t0);
    await loadSharedFleetMonitoringSnapshot(b as unknown as Client, t0);
    expect([a.reads, b.reads]).toEqual([1, 1]);
    await loadSharedFleetMonitoringSnapshot(
      b as unknown as Client,
      new Date(t0.getTime() - 1),
    );
    expect(b.reads).toBe(2);
  });

  it("does not keep a failed read: the next caller reads again", async () => {
    const failing = countingDatabase({ fail: true });
    const t0 = new Date("2026-10-05T12:00:00.000Z");
    await expect(
      loadSharedFleetMonitoringSnapshot(failing as unknown as Client, t0),
    ).rejects.toThrow("db down");
    await expect(
      loadSharedFleetMonitoringSnapshot(
        failing as unknown as Client,
        new Date(t0.getTime() + 1_000),
      ),
    ).rejects.toThrow("db down");
    expect(failing.reads).toBe(2);
  });
});
