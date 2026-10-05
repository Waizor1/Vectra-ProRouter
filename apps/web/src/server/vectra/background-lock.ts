import { sql } from "drizzle-orm";
import postgres from "postgres";

import { env } from "~/env";
import type { db as appDb } from "~/server/db";

/**
 * Cross-process single-runner guard for the background loops.
 *
 * The loops were guarded only by globalThis flags, which is exactly one
 * process's view. With a separate worker process — or two web containers
 * during a deploy — the same tick could run twice at once: two auto-rescue
 * sweeps opening the same case, two retention sweeps deleting the same batch.
 *
 * Each tick now holds a PostgreSQL session-level advisory lock for its loop
 * (pg_try_advisory_lock), taken and released on one dedicated connection per
 * process. A tick that does not get the lock is skipped, not queued: another
 * process is running the same sweep right now, and the next interval comes
 * round anyway. If the process dies, its session ends and PostgreSQL drops
 * the lock — nothing stale to clean up. The tick's own queries go through the
 * application pool; the lock connection only ever runs these one-line
 * statements, so it is never busy and never takes a slot from check-ins.
 *
 * Why not sql.reserve() or sql.begin() (pg_try_advisory_xact_lock): in
 * postgres.js 3.4.8 a query issued on a reserved or in-transaction connection
 * after its socket closed throws an uncaught TypeError (connection.js
 * nextWrite) and kills the process — in in-web mode, the web. Reproduced
 * against PostgreSQL 16 with pg_terminate_backend. A plain pooled connection
 * just reconnects. The cost: if the lock session dies mid-tick, the lock is
 * gone while the tick runs on; withLoopLock notices (the backend pid changed)
 * and logs it, and the tick's own queries were failing in that moment anyway.
 */

/** Stable per-loop ids; never renumber — a mixed old/new deploy shares them. */
export const BACKGROUND_LOOP_IDS = {
  autoRescueMonitor: 1,
  browserPushMonitor: 2,
  stuckJobJanitor: 3,
  snapshotRetention: 4,
  revisionRetention: 5,
  historyRetention: 6,
  routeHealthVerifier: 7,
  partnerWebhookDispatcher: 8,
} as const;

export type BackgroundLoopName = keyof typeof BACKGROUND_LOOP_IDS;

export const BACKGROUND_LOOP_NAMES = Object.keys(
  BACKGROUND_LOOP_IDS,
) as BackgroundLoopName[];

/** Two-int4 advisory key space ("VT" + 1): one tick at a time per loop. */
export const LOOP_TICK_LOCK_CLASS = 0x56540001;
/** ("VT" + 2): held by the worker for its lifetime per loop it runs. */
export const LOOP_PRESENCE_LOCK_CLASS = 0x56540002;

type LockClient = postgres.Sql;

let lockClient: LockClient | null = null;
const heldLocally = new Set<BackgroundLoopName>();
let inFlight = 0;
let draining = false;

function getLockClient(): LockClient {
  lockClient ??= postgres(env.DATABASE_URL, {
    // One session holds every loop's lock; the statements are one-liners.
    max: 1,
    // Never recycled or idled out between a lock and its unlock.
    idle_timeout: 0,
    max_lifetime: 0,
    // "you don't own a lock of type ExclusiveLock" after a lost session is
    // reported below as such; no need for PostgreSQL's warning too.
    onnotice: () => undefined,
    connection: { application_name: "vectra-loop-locks" },
  });
  return lockClient;
}

/** Tests point the guard at their own (throwaway) PostgreSQL. */
export function setBackgroundLockClientForTest(client: LockClient | null) {
  lockClient = client;
  heldLocally.clear();
  inFlight = 0;
  draining = false;
}

export type LoopLockResult<T> =
  | { acquired: true; value: T }
  | { acquired: false };

/**
 * Run `fn` only if no other tick of `loop` is running in ANY process.
 * Advisory locks are re-entrant within a session, and every tick of this
 * process shares the one lock session, so the same-process case is refused
 * here first — the timer tick and the operator trigger in one process must
 * exclude each other too.
 *
 * Errors from `fn` propagate; failing to reach PostgreSQL to take the lock
 * propagates as well (the tick could not have run anyway).
 */
export async function withLoopLock<T>(
  loop: BackgroundLoopName,
  fn: () => Promise<T>,
  client: LockClient = getLockClient(),
): Promise<LoopLockResult<T>> {
  if (draining || heldLocally.has(loop)) {
    return { acquired: false };
  }
  heldLocally.add(loop);
  const objid = BACKGROUND_LOOP_IDS[loop];
  try {
    const [lock] = await client<{ locked: boolean; pid: number }[]>`
      select pg_try_advisory_lock(${LOOP_TICK_LOCK_CLASS}::int4, ${objid}::int4) as locked,
             pg_backend_pid() as pid
    `;
    if (!lock?.locked) {
      return { acquired: false };
    }
    inFlight += 1;
    try {
      return { acquired: true, value: await fn() };
    } finally {
      inFlight -= 1;
      try {
        const [unlock] = await client<{ unlocked: boolean; pid: number }[]>`
          select pg_advisory_unlock(${LOOP_TICK_LOCK_CLASS}::int4, ${objid}::int4) as unlocked,
                 pg_backend_pid() as pid
        `;
        if (!unlock?.unlocked || unlock.pid !== lock.pid) {
          console.error(
            "[background-lock] %s: the lock session was lost during the tick",
            loop,
          );
        }
      } catch (error) {
        // The session is gone, and the lock went with it.
        console.error(
          "[background-lock] %s: the lock session was lost during the tick",
          loop,
          error,
        );
      }
    }
  } finally {
    heldLocally.delete(loop);
  }
}

/** Ticks currently inside withLoopLock in this process. */
export function backgroundTicksInFlight() {
  return inFlight;
}

/**
 * Stop taking new ticks (SIGTERM): withLoopLock refuses from now on, and the
 * caller waits for backgroundTicksInFlight() to reach zero before exiting.
 */
export function beginBackgroundDrain() {
  draining = true;
}

/**
 * The worker holds one presence lock per loop it started, on a connection of
 * its own, for as long as it lives. The web's /api/health reads them back
 * from pg_locks in worker-separate mode, so the health report stays truthful
 * about which loops are running without a heartbeat table: a dead worker's
 * connection closes and its presence vanishes with it.
 *
 * Returns a ping that throws once that session is gone (postgres.js would
 * silently reconnect to a new one without the locks) — the worker exits then
 * and is restarted, rather than run on with nobody able to see it.
 */
export async function holdLoopPresence(
  loops: BackgroundLoopName[],
  databaseUrl: string = env.DATABASE_URL,
) {
  const client = postgres(databaseUrl, {
    max: 1,
    // Never recycled or idled out: the locks live exactly as long as it.
    idle_timeout: 0,
    max_lifetime: 0,
    connection: { application_name: "vectra-worker-presence" },
  });
  const [session] = await client<{ pid: number }[]>`select pg_backend_pid() as pid`;
  const backendPid = session!.pid;
  for (const loop of loops) {
    await client`
      select pg_try_advisory_lock(${LOOP_PRESENCE_LOCK_CLASS}::int4, ${BACKGROUND_LOOP_IDS[loop]}::int4)
    `;
  }
  return {
    async ping() {
      const [row] = await client<{ pid: number }[]>`select pg_backend_pid() as pid`;
      if (row?.pid !== backendPid) {
        throw new Error("presence session was replaced; its locks are gone");
      }
    },
    async release() {
      await client.end({ timeout: 2 });
    },
  };
}

/** Which loops some live worker process reports as running (via pg_locks). */
export async function loadRunningLoopPresence(
  database: Pick<typeof appDb, "execute">,
): Promise<Set<BackgroundLoopName>> {
  const result = (await database.execute(sql`
    select objid::bigint as id
    from pg_locks
    where locktype = 'advisory'
      and classid::bigint = ${LOOP_PRESENCE_LOCK_CLASS}
      and objsubid = 2
      and granted
  `)) as unknown as Array<{ id: number | string }>;
  const ids = new Set(Array.from(result, (row) => Number(row.id)));
  return new Set(
    BACKGROUND_LOOP_NAMES.filter((loop) => ids.has(BACKGROUND_LOOP_IDS[loop])),
  );
}
