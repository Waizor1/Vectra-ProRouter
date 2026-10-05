import { describe, expect, it } from "vitest";

import { PerRouterCache } from "./per-router-cache";

function cache(maxWeight: number, ttlMs = 600_000) {
  let now = 1_000_000;
  const instance = new PerRouterCache<string>(maxWeight, ttlMs, () => now);
  return { instance, advance: (ms: number) => (now += ms) };
}

describe("PerRouterCache", () => {
  it("keeps one entry per router: a new key replaces the old value", () => {
    const { instance } = cache(100);
    instance.set("r1", "rev-1", "a", 10);
    instance.set("r1", "rev-2", "b", 10);
    expect(instance.size).toBe(1);
    expect(instance.weight).toBe(10);
    expect(instance.get("r1", "rev-1")).toBeUndefined();
    // A miss on a stale key drops the entry altogether.
    expect(instance.size).toBe(0);
  });

  it("returns a value only for its exact key, until its TTL", () => {
    const { instance, advance } = cache(100, 1_000);
    instance.set("r1", "rev-1", "a", 10);
    expect(instance.get("r1", "rev-1")).toBe("a");
    advance(1_000);
    expect(instance.get("r1", "rev-1")).toBeUndefined();
    expect(instance.weight).toBe(0);
  });

  it("refuses a new router when full instead of evicting a hot one", () => {
    const { instance } = cache(25);
    expect(instance.set("r1", "k", "a", 10)).toBe(true);
    expect(instance.set("r2", "k", "b", 10)).toBe(true);
    expect(instance.set("r3", "k", "c", 10)).toBe(false);
    expect(instance.get("r1", "k")).toBe("a");
    expect(instance.get("r2", "k")).toBe("b");
    expect(instance.weight).toBe(20);
  });

  it("makes room from expired entries before refusing, and sweeps them at least once a minute", () => {
    const { instance, advance } = cache(25, 30_000);
    instance.set("r1", "k", "a", 10);
    instance.set("r2", "k", "b", 10);
    advance(30_000);
    expect(instance.set("r3", "k", "c", 10)).toBe(true);
    expect(instance.size).toBe(1);

    advance(60_000);
    instance.set("r4", "k", "d", 1);
    // r3 expired 30 s ago: swept by the periodic pass, not left in the heap.
    expect(instance.size).toBe(1);
  });

  it("caches nothing with a zero budget", () => {
    const { instance } = cache(0);
    expect(instance.set("r1", "k", "a", 1)).toBe(false);
    expect(instance.size).toBe(0);
  });
});
