/**
 * A least-recently-used map bounded by a total "weight" (an approximate byte
 * size the caller supplies per entry), not by entry count: a fleet's cached
 * values range from a few hundred bytes to ~150 KB, so a count bound would be
 * either useless for small values or unsafe for large ones.
 *
 * A value heavier than the whole budget is not cached at all.
 */
export class BoundedLru<K, V> {
  private readonly entries = new Map<K, { value: V; weight: number }>();
  private totalWeight = 0;

  constructor(private readonly maxWeight: number) {}

  get(key: K): V | undefined {
    const entry = this.entries.get(key);
    if (!entry) {
      return undefined;
    }
    // Re-inserted so the map stays ordered oldest-use first.
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value;
  }

  set(key: K, value: V, weight = 1) {
    this.delete(key);
    if (weight > this.maxWeight) {
      return;
    }
    this.entries.set(key, { value, weight });
    this.totalWeight += weight;
    for (const [oldestKey, oldest] of this.entries) {
      if (this.totalWeight <= this.maxWeight) {
        break;
      }
      this.entries.delete(oldestKey);
      this.totalWeight -= oldest.weight;
    }
  }

  delete(key: K) {
    const entry = this.entries.get(key);
    if (entry) {
      this.entries.delete(key);
      this.totalWeight -= entry.weight;
    }
  }

  clear() {
    this.entries.clear();
    this.totalWeight = 0;
  }

  get size() {
    return this.entries.size;
  }

  get weight() {
    return this.totalWeight;
  }
}
