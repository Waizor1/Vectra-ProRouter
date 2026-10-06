import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const database = vi.hoisted(() => ({
  reads: 0,
  writes: 0,
  readError: null as Error | null,
  writeError: null as Error | null,
  writeGate: null as Promise<void> | null,
}));

vi.mock("~/server/db", () => ({
  db: {
    execute: async () => {
      database.reads += 1;
      if (database.readError) {
        throw database.readError;
      }
      return [];
    },
    transaction: async (run: (tx: { execute: () => Promise<unknown> }) => Promise<void>) => {
      database.writes += 1;
      if (database.writeGate) {
        await database.writeGate;
      }
      if (database.writeError) {
        throw database.writeError;
      }
      await run({ execute: async () => [] });
    },
  },
}));
// The check-in count runs its own query; keep it out of the read/write probe
// accounting below (it has its own tests in health-checkins.test.ts).
vi.mock("~/server/vectra/health-checkins", () => ({
  loadHealthCheckinCounts: async () => ({ checkedInLast5m: 25, active: 28 }),
}));
vi.mock("~/server/vectra/auto-rescue", () => ({ startAutoRescueMonitor: () => true }));
vi.mock("~/server/vectra/browser-push-monitor", () => ({ startBrowserPushMonitor: () => true }));
vi.mock("~/server/vectra/partner-webhooks", () => ({ startPartnerWebhookDispatcher: () => true }));
vi.mock("~/server/vectra/history-retention", () => ({ startHistoryRetention: () => true }));
vi.mock("~/server/vectra/revision-retention", () => ({ startRevisionRetention: () => true }));
vi.mock("~/server/vectra/route-health-verifier", () => ({ startRouteHealthVerifier: () => true }));
vi.mock("~/server/vectra/snapshot-retention", () => ({ startSnapshotRetention: () => true }));
vi.mock("~/server/vectra/stuck-job-janitor", () => ({ startStuckJobJanitor: () => true }));

const { GET } = await import("./route");
const { HEALTH_WRITE_PROBE_INTERVAL_MS, resetHealthProbeForTest } = await import(
  "~/server/vectra/health-probe"
);

type HealthBody = {
  ok: boolean;
  service: string;
  checkedAt: string;
  checks: Record<string, boolean>;
  routers?: { checkedInLast5m: number; active: number } | null;
  error?: string;
};

async function call() {
  const response = await GET();
  return { status: response.status, body: (await response.json()) as HealthBody };
}

describe("/api/health", () => {
  beforeEach(() => {
    resetHealthProbeForTest();
    Object.assign(database, { reads: 0, writes: 0, readError: null, writeError: null, writeGate: null });
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-05T12:00:00.000Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps its contract while writing at most one probe per 30 s", async () => {
    const first = await call();
    expect(first.status).toBe(200);
    expect(first.body).toMatchObject({ ok: true, service: "vectra-web" });
    expect(first.body.routers).toEqual({ checkedInLast5m: 25, active: 28 });
    expect(first.body.checks).toEqual({
      browserPushMonitor: true,
      autoRescueMonitor: true,
      stuckJobJanitor: true,
      snapshotRetention: true,
      revisionRetention: true,
      historyRetention: true,
      routeHealthVerifier: true,
      partnerWebhookDispatcher: true,
      dbRead: true,
      dbWriteProbe: true,
    });
    expect([database.reads, database.writes]).toEqual([1, 1]);

    // A legacy agent ~22 s later: read checked again, no write.
    vi.setSystemTime(Date.now() + 22_000);
    const second = await call();
    expect(second.status).toBe(200);
    expect(second.body.checks.dbWriteProbe).toBe(true);
    expect([database.reads, database.writes]).toEqual([2, 1]);

    vi.setSystemTime(Date.now() + HEALTH_WRITE_PROBE_INTERVAL_MS);
    expect((await call()).status).toBe(200);
    expect([database.reads, database.writes]).toEqual([3, 2]);
  });

  it("still answers 503 when the database refuses writes, and recovers on the next call", async () => {
    database.writeError = new Error("read-only transaction");
    const failed = await call();
    expect(failed.status).toBe(503);
    expect(failed.body.ok).toBe(false);
    expect(failed.body.checks.dbRead).toBe(true);
    expect(failed.body.checks.dbWriteProbe).toBe(false);
    expect(failed.body.error).toBe("read-only transaction");

    // A failure is never cached: the very next call probes again.
    database.writeError = null;
    expect((await call()).status).toBe(200);
    expect(database.writes).toBe(2);
  });

  it("answers 503 when the database cannot be read, even right after a good probe", async () => {
    await call();
    database.readError = new Error("connection refused");
    const failed = await call();
    expect(failed.status).toBe(503);
    expect(failed.body.checks.dbRead).toBe(false);
    expect(failed.body.checks.dbWriteProbe).toBe(false);
  });

  it("concurrent calls on a slow database share one write probe", async () => {
    let release!: () => void;
    database.writeGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = Promise.all([call(), call(), call()]);
    await new Promise((resolve) => setImmediate(resolve));
    release();
    const results = await calls;
    expect(results.map((result) => result.status)).toEqual([200, 200, 200]);
    expect(database.writes).toBe(1);
  });
});
