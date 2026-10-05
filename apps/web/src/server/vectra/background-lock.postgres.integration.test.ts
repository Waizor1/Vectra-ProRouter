import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
vi.mock("~/server/db", () => ({ db: {} }));
vi.mock("server-only", () => ({}));
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";

import * as schema from "@vectra/db";

import type * as BackgroundLock from "./background-lock";

type LockModule = typeof BackgroundLock;

/**
 * The background loops' single-runner guarantee, against a real PostgreSQL.
 * Two module instances (vi.resetModules) on two separate clients stand in for
 * two processes — the worker and the web — each with its own in-memory state
 * and its own database sessions.
 *
 * Runs only against a throwaway local cluster named by PANEL_PERF_PG_PORT on
 * 127.0.0.1 (see panel-perf.postgres.integration.test.ts for the setup). It
 * creates and drops its own database, so it never races the other suites.
 */
const port = process.env.PANEL_PERF_PG_PORT;

describe.skipIf(!port)("background loop locks on a real PostgreSQL", () => {
  const database = `bg_lock_test_${process.pid}_${Date.now()}`;
  const connection = {
    host: "127.0.0.1",
    port: Number(port),
    username: "panel_test",
    onnotice: () => undefined,
  };
  const admin = postgres({ ...connection, database: "postgres", max: 1 });
  // Each "process" has its lock session (max 1, as in production) and the
  // web an application pool besides.
  let worker: postgres.Sql;
  let web: postgres.Sql;
  let webApp: postgres.Sql;
  let workerLock: LockModule;
  let webLock: LockModule;

  beforeAll(async () => {
    await admin.unsafe(`create database ${database}`);
    const lockSession = { ...connection, database, max: 1, idle_timeout: 0, max_lifetime: 0 };
    worker = postgres(lockSession);
    web = postgres(lockSession);
    webApp = postgres({ ...connection, database, max: 4 });
    const migrations = resolve(process.cwd(), "..", "..", "packages", "db", "drizzle");
    for (const file of readdirSync(migrations).filter((name) => name.endsWith(".sql")).sort()) {
      await webApp.unsafe(readFileSync(resolve(migrations, file), "utf8"));
    }

    workerLock = await import("./background-lock");
    workerLock.setBackgroundLockClientForTest(worker);
    vi.resetModules();
    webLock = await import("./background-lock");
    webLock.setBackgroundLockClientForTest(web);
  });

  afterAll(async () => {
    await worker?.end();
    await web?.end();
    await webApp?.end();
    await admin.unsafe(`drop database if exists ${database} with (force)`);
    await admin.end();
  });

  function gatedTick<T>(value: T) {
    let open!: () => void;
    let entered!: () => void;
    const gate = new Promise<void>((resolve) => (open = resolve));
    const started = new Promise<void>((resolve) => (entered = resolve));
    return {
      fn: async () => {
        entered();
        await gate;
        return value;
      },
      started,
      open: () => open(),
    };
  }

  it("runs one tick of a loop at a time across processes", async () => {
    const tick = gatedTick("worker");
    const running = workerLock.withLoopLock("autoRescueMonitor", tick.fn);
    await tick.started;

    // The other process is refused while the tick runs …
    expect(await webLock.withLoopLock("autoRescueMonitor", async () => "web")).toEqual({
      acquired: false,
    });
    // … so is a second tick in the same process (session locks are re-entrant) …
    expect(await workerLock.withLoopLock("autoRescueMonitor", async () => "again")).toEqual({
      acquired: false,
    });
    // … and another loop is not affected.
    expect(await webLock.withLoopLock("historyRetention", async () => "other")).toEqual({
      acquired: true,
      value: "other",
    });

    tick.open();
    expect(await running).toEqual({ acquired: true, value: "worker" });
    expect(await webLock.withLoopLock("autoRescueMonitor", async () => "web")).toEqual({
      acquired: true,
      value: "web",
    });

    // Both outcomes count as a settled tick for the stall watchdog.
    expect(typeof workerLock.loopTickTimes().autoRescueMonitor?.completedAt).toBe("number");
    const webTimes = webLock.loopTickTimes().autoRescueMonitor;
    expect([typeof webTimes?.completedAt, typeof webTimes?.busyAt]).toEqual(["number", "number"]);
  });

  it("releases the lock when the holder's connection dies mid-tick", async () => {
    const quiet = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const tick = gatedTick("lost");
    const running = workerLock.withLoopLock("snapshotRetention", tick.fn);
    await tick.started;

    const [holder] = await webApp<{ pid: number }[]>`
      select pid from pg_locks
      where locktype = 'advisory'
        and classid::bigint = ${workerLock.LOOP_TICK_LOCK_CLASS}
        and objid::bigint = ${workerLock.BACKGROUND_LOOP_IDS.snapshotRetention}
        and granted
    `;
    expect(holder).toBeDefined();
    await webApp`select pg_terminate_backend(${holder!.pid})`;
    // pg_terminate_backend only signals; wait until the backend is gone.
    for (let i = 0; i < 100; i += 1) {
      const [alive] = await webApp`select 1 from pg_stat_activity where pid = ${holder!.pid}`;
      if (!alive) break;
      await new Promise((resolve) => setTimeout(resolve, 20));
    }

    // A crashed worker must not wedge the loop: the lock went with the session.
    expect(await webLock.withLoopLock("snapshotRetention", async () => "taken over")).toEqual({
      acquired: true,
      value: "taken over",
    });

    tick.open();
    expect(await running).toEqual({ acquired: true, value: "lost" });
    // … and the loss is reported, not swallowed.
    expect(quiet.mock.calls).toContainEqual(
      expect.arrayContaining([
        expect.stringContaining("lock session was lost"),
        "snapshotRetention",
      ]),
    );
    quiet.mockRestore();
  });

  it("lets the web read which loops a live worker runs", async () => {
    const webDb = drizzle(webApp, { schema });
    const presence = await workerLock.holdLoopPresence(
      ["autoRescueMonitor", "stuckJobJanitor"],
      `postgres://panel_test@127.0.0.1:${port}/${database}`,
    );
    await presence.ping({
      autoRescueMonitor: { completedAt: 1_700_000_000_000, busyAt: null },
      stuckJobJanitor: { completedAt: 1_700_000_001_000, busyAt: 1_700_000_002_000 },
    });

    const seen = await webLock.loadRunningLoopPresence(webDb);
    expect([...seen.running].sort()).toEqual(["autoRescueMonitor", "stuckJobJanitor"]);
    // The tick times travel in the ping's own SQL (pg_stat_activity.query).
    expect(seen.ticks).toEqual({
      autoRescueMonitor: { completedAt: 1_700_000_000_000, busyAt: null },
      stuckJobJanitor: { completedAt: 1_700_000_001_000, busyAt: 1_700_000_002_000 },
    });

    await presence.release();
    const gone = await webLock.loadRunningLoopPresence(webDb);
    expect(gone.running.size).toBe(0);
    expect(gone.ticks).toEqual({});
  });

  it("refuses the operator's stuck-job sweep while the worker is running one", async () => {
    // The web side: a fresh module graph whose background-lock instance talks
    // to the web's client, exactly as fleet.ts would see it in production.
    vi.resetModules();
    const lockInWeb = await import("~/server/vectra/background-lock");
    lockInWeb.setBackgroundLockClientForTest(web);
    const { fleetRouter } = await import("~/server/api/routers/fleet");
    const { createCallerFactory } = await import("~/server/api/trpc");
    const caller = createCallerFactory(fleetRouter as never)({
      db: drizzle(webApp, { schema }),
      operatorSession: { subject: "operator", user: "operator" } as never,
      headers: new Headers(),
    }) as unknown as {
      cancelStuckJobs: (input?: { staleSeconds?: number }) => Promise<{ enabled: boolean; scanned: number }>;
    };

    const tick = gatedTick("worker sweep");
    const running = workerLock.withLoopLock("stuckJobJanitor", tick.fn);
    await tick.started;

    await expect(caller.cancelStuckJobs()).rejects.toMatchObject({ code: "CONFLICT" });

    tick.open();
    await running;
    await expect(caller.cancelStuckJobs({ staleSeconds: 86400 })).resolves.toMatchObject({ enabled: true, scanned: 0 });
  });
});
