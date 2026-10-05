/**
 * Heap bytes a parsed config is weighed at per byte of its JSON. Measured
 * ~1.9x for a 135 kB PassWall config (200 parsed copies, heapUsed after GC);
 * 3x leaves headroom for the frozen wrapper objects and two-byte strings.
 */
export const CACHED_CONFIG_HEAP_FACTOR = 3;

/**
 * At most one cached value per router, under a global weight budget and a TTL.
 *
 * - One entry per router: a router's previous value is dropped the moment a
 *   new key (a new revision, a re-imported secret) is stored for it, so the
 *   cache never accumulates superseded revisions.
 * - TTL: nothing outlives `ttlMs`, so a decrypted config does not linger in
 *   the heap after its router stops checking in.
 * - Budget: when full, a new router is NOT admitted (rather than evicting the
 *   least recently used one). Check-ins arrive round-robin across the fleet,
 *   and an LRU smaller than that working set misses on every single request;
 *   refusing admission instead keeps a stable subset of routers hitting.
 *   Expired entries are swept before a refusal and at most once a minute.
 */
export class PerRouterCache<V> {
  private readonly entries = new Map<
    string,
    { key: string; value: V; weight: number; expiresAt: number }
  >();
  private totalWeight = 0;
  private lastSweep = 0;

  constructor(
    private readonly maxWeight: number,
    private readonly ttlMs: number,
    private readonly clock: () => number = Date.now,
  ) {}

  get(routerId: string, key: string): V | undefined {
    const entry = this.entries.get(routerId);
    if (!entry) {
      return undefined;
    }
    if (entry.key !== key || entry.expiresAt <= this.clock()) {
      this.delete(routerId);
      return undefined;
    }
    return entry.value;
  }

  /** Returns whether the value was admitted. */
  set(routerId: string, key: string, value: V, weight: number): boolean {
    this.delete(routerId);
    const now = this.clock();
    if (
      now - this.lastSweep >= 60_000 ||
      this.totalWeight + weight > this.maxWeight
    ) {
      this.sweepExpired(now);
    }
    if (this.totalWeight + weight > this.maxWeight) {
      return false;
    }
    this.entries.set(routerId, { key, value, weight, expiresAt: now + this.ttlMs });
    this.totalWeight += weight;
    return true;
  }

  delete(routerId: string) {
    const entry = this.entries.get(routerId);
    if (entry) {
      this.entries.delete(routerId);
      this.totalWeight -= entry.weight;
    }
  }

  clear() {
    this.entries.clear();
    this.totalWeight = 0;
    this.lastSweep = 0;
  }

  get size() {
    return this.entries.size;
  }

  get weight() {
    return this.totalWeight;
  }

  private sweepExpired(now: number) {
    this.lastSweep = now;
    for (const [routerId, entry] of this.entries) {
      if (entry.expiresAt <= now) {
        this.delete(routerId);
      }
    }
  }
}
