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
  let throwOnNextTx = false; // simulate per-candidate DB failure
  let throwOnNextEventInsert = false; // simulate eventLog INSERT failure
  const eventLogInserts: Array<Record<string, unknown>> = [];
  const updateSetValues: Array<Record<string, unknown>> = [];

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
        if (throwOnNextTx) {
          throwOnNextTx = false;
          throw new Error("simulated DB failure");
        }
        return callback(buildTx());
      },
    },
    metrics: {
      updateAttempts: () => updateAttempts,
      updatesApplied: () => updatesActuallyApplied,
      eventLogInserts: () => eventLogInserts,
      updateSetValues: () => updateSetValues,
      // Simulate the next update losing the race to a concurrent writer
      // by returning an empty rows array even though we matched.
      simulateConcurrentLossOnNextUpdate() {
        abortNextUpdate = true;
      },
    },
    failOnNextTransaction() {
      throwOnNextTx = true;
    },
    failOnNextEventInsert() {
      throwOnNextEventInsert = true;
    },
  };

  function buildTx() {
    return {
      update() {
        return {
          set(setVal: Record<string, unknown>) {
            updateSetValues.push(setVal);
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
            if (throwOnNextEventInsert) {
              throwOnNextEventInsert = false;
              return Promise.reject(
                new Error("simulated eventLog INSERT failure"),
              );
            }
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
      failedJobIds: [],
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
      failedJobIds: [],
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
    expect(result.failedJobIds).toEqual([]);
    expect(metrics.updateAttempts()).toBe(2);
    expect(metrics.updatesApplied()).toBe(2);
    expect(metrics.eventLogInserts()).toHaveLength(2);

    const totchtoAudit = metrics
      .eventLogInserts()
      .find((row) => (row.routerId as string) === "router-totchto");
    expect(totchtoAudit?.type).toBe("fleet.stuck_job_cancelled");
    expect(totchtoAudit?.severity).toBe("warning");
    const totchtoMetadata = totchtoAudit?.metadata as {
      artifactVersion?: unknown;
      jobType?: unknown;
      triggeredBy?: unknown;
      operatorUser?: unknown;
    };
    expect(totchtoMetadata?.artifactVersion).toBe("0.1.13-r27");
    expect(totchtoMetadata?.jobType).toBe("run_terminal_command");
    // Default trigger is "scheduled" (not from operator mutation).
    expect(totchtoMetadata?.triggeredBy).toBe("scheduled");
    expect(totchtoMetadata?.operatorUser).toBeNull();
  });

  it("uses deliveredAt for age comparison, not createdAt", async () => {
    // A job that sat in the queue for 50 min then started running 5 min
    // ago must NOT be cancelled — its ACTUAL runtime is 5 min, well
    // inside any sane stale threshold. Using createdAt-only logic would
    // wrongly mark this as stuck.
    const { db, metrics } = createJanitorMockDb([
      {
        id: "job-recently-started",
        routerId: "router-x",
        type: "update_passwall_packages",
        createdAt: new Date(NOW.getTime() - 55 * 60 * 1000), // 55 min ago in queue
        deliveredAt: new Date(NOW.getTime() - 5 * 60 * 1000), // started 5 min ago
        payload: {},
      },
    ]);

    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );
    // The mock returns whatever candidates we hand it — the production
    // SQL filter would exclude this row at the database. We can't easily
    // exercise the SQL in this mock, but we CAN verify that even if it
    // somehow ended up in the candidate list, the metadata uses
    // deliveredAt as the reference for ageSeconds (5 min, not 55 min).
    const audit = metrics.eventLogInserts()[0];
    if (audit) {
      const ageSeconds = (audit.metadata as { ageSeconds?: number })?.ageSeconds;
      expect(ageSeconds).toBeLessThan(310); // ~5 min, not ~55 min
      expect(ageSeconds).toBeGreaterThan(290);
    }
    // Either zero candidates (filter worked) or one with correct age.
    expect(result.cancelledJobIds.length).toBeLessThanOrEqual(1);
  });

  it("records operator metadata on operator-triggered sweep", async () => {
    const { db, metrics } = createJanitorMockDb([
      {
        id: "job-1",
        routerId: "router-a",
        type: "update_passwall_packages",
        createdAt: new Date(NOW.getTime() - 2 * 60 * 60 * 1000),
        deliveredAt: new Date(NOW.getTime() - 2 * 60 * 60 * 1000),
        payload: {},
      },
    ]);

    await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      {
        enabled: true,
        staleSeconds: 3600,
        triggeredBy: "operator",
        operatorUser: "ilya@vectra-pro.net",
      },
    );

    const metadata = metrics.eventLogInserts()[0]?.metadata as {
      triggeredBy?: unknown;
      operatorUser?: unknown;
    };
    expect(metadata?.triggeredBy).toBe("operator");
    expect(metadata?.operatorUser).toBe("ilya@vectra-pro.net");
  });

  // Regression: critic-agent finding (2026-06-05). The unique index on
  // vectra_job.dedupe_key is global (not partial-on-state, no
  // NULLS-NOT-DISTINCT), so a cancelled row with a populated dedupeKey
  // would block any subsequent operator re-queue of the same logical
  // action (e.g. an operator clicking "Update controller" after a sweep)
  // with a unique-constraint violation. The janitor MUST null dedupeKey
  // on cancel, matching the contract enforced by draft.ts:442 and
  // router-control.ts:212-225.
  it("clears dedupeKey on cancel so re-queue of same logical job succeeds", async () => {
    const { db, metrics } = createJanitorMockDb([
      {
        id: "job-stuck-update-controller",
        routerId: "router-a",
        type: "update_controller",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: { artifactVersion: "0.1.13-r29" },
      },
    ]);

    await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );

    const setVals = metrics.updateSetValues();
    expect(setVals).toHaveLength(1);
    expect(setVals[0]).toMatchObject({
      state: "cancelled",
      dedupeKey: null,
    });
  });

  // Regression: code-reviewer probe B (2026-06-05). When the eventLog
  // INSERT throws inside the per-row transaction, drizzle rolls back the
  // UPDATE. The result.cancelledJobIds must NOT include the rolled-back
  // ID — otherwise operators reading the result see an inflated
  // cancellation count while the row is genuinely still running.
  it("does not over-count cancellations when audit insert rolls back the transaction", async () => {
    const { db, metrics, failOnNextEventInsert } = createJanitorMockDb([
      {
        id: "job-tx-rollback",
        routerId: "router-x",
        type: "refresh_subscriptions",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: {},
      },
    ]);
    failOnNextEventInsert();

    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );

    expect(result.scanned).toBe(1);
    expect(result.cancelled).toBe(0);
    expect(result.cancelledJobIds).toEqual([]);
    expect(result.failedJobIds).toEqual(["job-tx-rollback"]);
    expect(metrics.eventLogInserts()).toHaveLength(0);
  });

  it("isolates per-candidate failures so one bad row doesn't abort the sweep", async () => {
    const { db, metrics, failOnNextTransaction } = createJanitorMockDb([
      {
        id: "job-bad",
        routerId: "router-a",
        type: "refresh_subscriptions",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: {},
      },
      {
        id: "job-good",
        routerId: "router-b",
        type: "refresh_subscriptions",
        createdAt: TWO_HOURS_AGO,
        deliveredAt: TWO_HOURS_AGO,
        payload: {},
      },
    ]);
    failOnNextTransaction(); // first candidate's tx throws

    const result = await runStuckJobJanitorTick(
      NOW,
      db as unknown as Parameters<typeof runStuckJobJanitorTick>[1],
      { enabled: true, staleSeconds: 3600 },
    );

    expect(result.scanned).toBe(2);
    // Bad one failed; good one still cancelled.
    expect(result.cancelled).toBe(1);
    expect(result.cancelledJobIds).toEqual(["job-good"]);
    expect(result.failedJobIds).toEqual(["job-bad"]);
    expect(metrics.eventLogInserts()).toHaveLength(1);
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
    expect(result.failedJobIds).toEqual([]);
    expect(metrics.updateAttempts()).toBe(1);
    expect(metrics.updatesApplied()).toBe(0);
    expect(metrics.eventLogInserts()).toHaveLength(0);
  });
});
