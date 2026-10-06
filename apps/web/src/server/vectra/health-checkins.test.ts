import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  HEALTH_CHECKIN_CACHE_MS,
  loadHealthCheckinCounts,
  resetHealthCheckinsForTest,
} from "./health-checkins";

function fakeDb(impl: () => Promise<unknown[]>) {
  const execute = vi.fn(impl);
  return { database: { execute } as never, execute };
}

describe("loadHealthCheckinCounts", () => {
  beforeEach(() => {
    resetHealthCheckinsForTest();
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  it("maps the row to counts", async () => {
    const { database } = fakeDb(async () => [{ checked_in: 25, active: 28 }]);
    expect(await loadHealthCheckinCounts(database, 1_000)).toEqual({
      checkedInLast5m: 25,
      active: 28,
    });
  });

  it("caches within the window, shares concurrent calls, and refreshes after it", async () => {
    const { database, execute } = fakeDb(async () => [{ checked_in: 1, active: 2 }]);
    await Promise.all([
      loadHealthCheckinCounts(database, 1_000),
      loadHealthCheckinCounts(database, 1_000),
    ]);
    expect(execute).toHaveBeenCalledTimes(1);
    await loadHealthCheckinCounts(database, Date.now());
    expect(execute).toHaveBeenCalledTimes(1);
    await loadHealthCheckinCounts(database, Date.now() + HEALTH_CHECKIN_CACHE_MS + 1);
    expect(execute).toHaveBeenCalledTimes(2);
  });

  it("returns null instead of throwing when the query fails, and does not cache the failure", async () => {
    let fail = true;
    const { database, execute } = fakeDb(async () => {
      if (fail) throw new Error("boom");
      return [{ checked_in: 3, active: 4 }];
    });
    expect(await loadHealthCheckinCounts(database)).toBeNull();
    fail = false;
    expect(await loadHealthCheckinCounts(database)).toEqual({ checkedInLast5m: 3, active: 4 });
    expect(execute).toHaveBeenCalledTimes(2);
  });
});
