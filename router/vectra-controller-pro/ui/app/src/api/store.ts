// A tiny keyed store for the read methods. It owns three guarantees the router
// needs from a page that may stay open for hours:
//   - never two in-flight requests for the same method (a slow router must not
//     be buried under a queue of identical polls);
//   - subscribers of one method are not re-rendered by another method's data;
//   - once disposed, late answers are dropped instead of touching dead components.

import { describeError, type ErrInfo } from './errors';
import { normalize } from './normalize';
import type { CallFn, ReadData, ReadMethod } from './types';

export interface Res<T> {
  data: T | null;
  error: ErrInfo | null;
  /** Date.now() of the last successful answer. */
  at: number | null;
}

type Key = ReadMethod | 'fetching';
const EMPTY: Res<never> = { data: null, error: null, at: null };
/** A call that has not settled by then is abandoned (its late answer is ignored) so polling can retry. */
export const CALL_TIMEOUT_MS = 30_000;

export const READ_PARAMS: Partial<Record<ReadMethod, Record<string, unknown>>> = { logs: { lines: 200 } };

export type Store = ReturnType<typeof createStore>;

/**
 * A read method the router refused answers with an action object instead of
 * its data (contract/README.md, "Operator lock"). That is an error, never an
 * empty list marked fresh.
 */
function refusal(m: ReadMethod, raw: unknown): Error | null {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const r = raw as { ok?: unknown; code?: unknown };
  return r.ok === false && typeof r.code === 'string' ? new Error(`vectra/${m} refused: ${r.code}`) : null;
}

export function createStore(call: CallFn) {
  const res: Partial<Record<ReadMethod, Res<unknown>>> = {};
  const inflight: Partial<Record<ReadMethod, Promise<void>>> = {};
  const subs: Partial<Record<Key, Set<() => void>>> = {};
  const timers = new Set<ReturnType<typeof setTimeout>>();
  let running = 0;
  let dead = false;

  const emit = (key: Key) => {
    if (!dead) subs[key]?.forEach((fn) => fn());
  };
  const get = <M extends ReadMethod>(m: M): Res<ReadData[M]> => (res[m] as Res<ReadData[M]> | undefined) ?? EMPTY;
  const put = (m: ReadMethod, r: Res<unknown>) => {
    if (dead) return;
    res[m] = r;
    emit(m);
  };

  return {
    get,
    /** True while any read request is on the wire. */
    busy: () => running > 0,
    /** Fetch unless the same request is already running or the data is younger than maxAgeMs. */
    fetch(m: ReadMethod, maxAgeMs = 0): Promise<void> {
      const cur = get(m);
      if (inflight[m]) return inflight[m]!;
      if (maxAgeMs > 0 && cur.at !== null && Date.now() - cur.at < maxAgeMs) return Promise.resolve();
      const fail = (e: unknown) => put(m, { data: get(m).data, error: describeError(e), at: get(m).at });
      running++;
      emit('fetching');
      let timer: ReturnType<typeof setTimeout> | undefined;
      const answer = Promise.resolve().then(() => call(m, READ_PARAMS[m] ?? {}));
      const expiry = new Promise<never>((_, reject) => {
        timers.add((timer = setTimeout(() => reject(new Error(`vectra/${m}: request timed out`)), CALL_TIMEOUT_MS)));
      });
      const p = Promise.race([answer, expiry])
        .then((raw) => {
          const refused = refusal(m, raw);
          if (refused) throw refused;
          put(m, { data: normalize(m, raw), error: null, at: Date.now() });
        })
        .catch(fail)
        .then(done, done);
      function done() {
        clearTimeout(timer);
        timers.delete(timer!);
        delete inflight[m];
        running--;
        emit('fetching');
      }
      return (inflight[m] = p);
    },
    on(key: Key, fn: () => void): () => void {
      const set = (subs[key] = subs[key] ?? new Set());
      set.add(fn);
      return () => {
        set.delete(fn);
      };
    },
    dispose() {
      dead = true;
      timers.forEach(clearTimeout);
    },
  };
}
