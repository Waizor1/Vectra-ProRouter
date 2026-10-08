import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("~/env", () => ({ env: { DATABASE_URL: "postgres://unused@127.0.0.1/unused" } }));

const {
  BACKGROUND_LOOP_IDS,
  loadRunningLoopPresence,
  loopTickTimes,
  presencePingQuery,
  recordLoopStaleFlight,
  recordLoopTick,
  setBackgroundLockClientForTest,
} = await import("./background-lock");

// What the web reads back (pg_locks joined to pg_stat_activity) for a worker
// whose presence session last ran `query`.
const seenAs = (query: string) =>
  ({
    execute: async () => [{ id: BACKGROUND_LOOP_IDS.partnerWebhookDispatcher, query }],
  }) as never;

// A partner delivery that hung is replaced by the dispatcher itself (its tick
// completes either way, so the stall watchdog never sees it); the count is
// what tells an operator it happened.
describe("hung partner deliveries on the loop health surface", () => {
  beforeEach(() => {
    setBackgroundLockClientForTest(null);
  });

  it("counts each replaced delivery next to the loop's tick times", () => {
    recordLoopTick("partnerWebhookDispatcher", "completed", 1_000);
    recordLoopStaleFlight("partnerWebhookDispatcher");
    recordLoopStaleFlight("partnerWebhookDispatcher");

    expect(loopTickTimes()).toEqual({
      partnerWebhookDispatcher: { completedAt: 1_000, busyAt: null, staleFlights: 2 },
    });
  });

  it("carries the count from the worker's ping to the web", async () => {
    const seen = await loadRunningLoopPresence(
      seenAs(
        presencePingQuery({
          partnerWebhookDispatcher: { completedAt: 1_000, busyAt: null, staleFlights: 3 },
          autoRescueMonitor: { completedAt: 2_000, busyAt: 3_000 },
        }),
      ),
    );

    expect(seen.ticks).toEqual({
      partnerWebhookDispatcher: { completedAt: 1_000, busyAt: null, staleFlights: 3 },
      autoRescueMonitor: { completedAt: 2_000, busyAt: 3_000 },
    });
  });

  it("reads a worker from before the count as having none", async () => {
    const seen = await loadRunningLoopPresence(
      seenAs(
        'select pg_backend_pid() as pid /* vectra-loop-ticks:{"partnerWebhookDispatcher":[1000,null]} */',
      ),
    );

    expect(seen.ticks).toEqual({
      partnerWebhookDispatcher: { completedAt: 1_000, busyAt: null },
    });
  });
});
