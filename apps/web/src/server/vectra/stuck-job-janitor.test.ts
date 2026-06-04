import { describe, expect, it } from "vitest";

import { runStuckJobJanitorTick } from "./stuck-job-janitor";

// Minimal fake DB sufficient for the janitor's query+transaction shape.
// Each select returns the configured candidate list; each transaction
// callback receives a tx with update().set().where().returning() and
// insert().values() chains. We track inserted event-log rows to verify
// the audit trail.
function createJanitorMockDb(candidates: Array<Record<string, unknown>>) {
  let updateAttempts = 0;
  let updatesActuallyApplied = 0;
  let abortNextUpdate = false; // simulate concurrent winner-takes-all
  const eventLogInserts: Array<Record<string, unknown>> = [];

  return {
    db: {
      select() {
        return {
          from() {
            return {
              where() {
                return Promise.resolve(candidates);
              },
            };
          },
        };
      },
      async transaction(
        callback: (tx: ReturnType<typeof buildTx>) => Promise<unknown>,
      ) {
        return callback(buildTx());
      },
    },
    metrics: {
      updateAttempts: () => updateAttempts,
      updatesApplied: () => updatesActuallyApplied,
      eventLogInserts: () => eventLogInserts,
      // Simulate the next update losing the race to a concurrent writer
      // by returning an empty rows array even though we matched.
      simulateConcurrentLossOnNextUpdate() {
        abortNextUpdate = true;
      },
    },
  };

  function buildTx() {
    return {
      update() {
        return {
          set() {
            return {
              where() {
                return {
                  returning() {
                    updateAttempts++;
                    if (abortNextUpdate) {
                      abortNextUpdate = false;
                      return Promise.resolve([]);
                    }
                    updatesActuallyApplied++;
                    return Promise.resolve([{ id: "ok" }]);
                  },
                };
              },
            };
          },
        };
      },
      insert() {
        return {
          values(row: Record<string, unknown>) {
            eventLogInserts.push(row);
            return Promise.resolve();
          },
        };
      },
    };
  }
}

describe("runStuckJobJanitorTick", () => {
  const NOW = new Date("2026-06-05T12:00:00Z");
  const ONE_HOUR_AGO = new Date(NOW.getTime() - 60 * 60 * 1000);
  const TWO_HOURS_AGO = new Date(NOW.getTime() - 2 * 60 * 60 * 1000);

  it("returns disabled noop when feature flag is off", async () => {
    const { db } = createJanitorMockDb([]);
    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: false },
    );
    expect(result).toEqual({
      enabled: false,
      scanned: 0,
      cancelled: 0,
      cancelledJobIds: [],
    });
  });

  it("returns zero counts when no stuck jobs match", async () => {
    const { db, metrics } = createJanitorMockDb([]);
    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );
    expect(result).toEqual({
      enabled: true,
      scanned: 0,
      cancelled: 0,
      cancelledJobIds: [],
    });
    expect(metrics.updateAttempts()).toBe(0);
    expect(metrics.eventLogInserts()).toHaveLength(0);
  });

  it("cancels stuck jobs and records audit rows", async () => {
    const { db, metrics } = createJanitorMockDb([
      {
        id: "job-old-r27",
        routerId: "router-totchto",
        type: "run_terminal_command",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: {
          artifactVersion: "0.1.13-r27",
          purpose: "controller-self-update",
        },
      },
      {
        id: "job-old-passwall",
        routerId: "router-other",
        type: "update_passwall_packages",
        createdAt: ONE_HOUR_AGO,
        deliveredAt: null,
        payload: {},
      },
    ]);

    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );

    expect(result.enabled).toBe(true);
    expect(result.scanned).toBe(2);
    expect(result.cancelled).toBe(2);
    expect(result.cancelledJobIds.sort()).toEqual(
      ["job-old-passwall", "job-old-r27"].sort(),
    );
    expect(metrics.updateAttempts()).toBe(2);
    expect(metrics.updatesApplied()).toBe(2);
    expect(metrics.eventLogInserts()).toHaveLength(2);

    const totchtoAudit = metrics
      .eventLogInserts()
      .find((row) => (row.routerId as string) === "router-totchto");
    expect(totchtoAudit?.type).toBe("fleet.stuck_job_cancelled");
    expect(totchtoAudit?.severity).toBe("warning");
    expect(
      (totchtoAudit?.details as { artifactVersion?: unknown })?.artifactVersion,
    ).toBe("0.1.13-r27");
    expect(
      (totchtoAudit?.details as { jobType?: unknown })?.jobType,
    ).toBe("run_terminal_command");
  });

  it("skips audit write when a concurrent writer wins the update race", async () => {
    const { db, metrics } = createJanitorMockDb([
      {
        id: "job-race",
        routerId: "router-x",
        type: "refresh_subscriptions",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: {},
      },
    ]);
    metrics.simulateConcurrentLossOnNextUpdate();

    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );

    expect(result.scanned).toBe(1);
    expect(result.cancelled).toBe(0);
    expect(result.cancelledJobIds).toEqual([]);
    expect(metrics.updateAttempts()).toBe(1);
    expect(metrics.updatesApplied()).toBe(0);
    expect(metrics.eventLogInserts()).toHaveLength(0);
  });
});
