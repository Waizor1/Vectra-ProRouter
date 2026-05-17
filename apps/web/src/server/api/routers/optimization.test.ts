import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { optimizationRouter } from "./optimization";

const ROUTER_ID = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf";

function createMockDb({
  selectResponses,
  insertResponses = [],
}: {
  selectResponses: unknown[][];
  insertResponses?: unknown[][];
}) {
  let selectIndex = 0;
  let insertIndex = 0;
  const insertedValues: unknown[] = [];
  let conflictTarget: unknown = null;

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];
  const nextInsertResult = () => insertResponses[insertIndex++] ?? [];

  const makeSelectChain = () => ({
    from() {
      return this;
    },
    where() {
      return this;
    },
    orderBy() {
      return this;
    },
    limit() {
      return Promise.resolve(nextSelectResult());
    },
  });

  return {
    db: {
      select() {
        return makeSelectChain();
      },
      insert() {
        return {
          values(value: unknown) {
            insertedValues.push(value);
            const result = nextInsertResult();
            return {
              onConflictDoNothing(options: unknown) {
                conflictTarget = options;
                return {
                  returning() {
                    return Promise.resolve(result);
                  },
                };
              },
            };
          },
        };
      },
    },
    insertedValues() {
      return insertedValues;
    },
    conflictTarget() {
      return conflictTarget;
    },
  };
}

function createProtectedCaller(db: unknown) {
  return createCallerFactory(optimizationRouter)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

describe("optimizationRouter.queueBaseline", () => {
  it("queues a stable-dedupe baseline job so completed jobs can clear dedupe and future snapshots can run", async () => {
    const insertedJob = {
      id: "job-1",
      routerId: ROUTER_ID,
      type: "collect_optimization_baseline",
      state: "queued",
      dedupeKey: `collect_optimization_baseline:${ROUTER_ID}`,
      payload: {},
      createdAt: new Date("2026-05-15T08:00:00.000Z"),
      completedAt: null,
    };
    const mock = createMockDb({
      selectResponses: [[{ id: ROUTER_ID }], []],
      insertResponses: [[insertedJob]],
    });
    const caller = createProtectedCaller(mock.db);

    const job = await caller.queueBaseline({
      routerId: ROUTER_ID,
      logSource: "passwall",
      logLines: 120,
      includeLogs: true,
      includeRoutes: false,
    });

    expect(job).toBe(insertedJob);
    expect(mock.insertedValues()[0]).toMatchObject({
      routerId: ROUTER_ID,
      type: "collect_optimization_baseline",
      state: "queued",
      dedupeKey: `collect_optimization_baseline:${ROUTER_ID}`,
      payload: {
        logSource: "passwall",
        logLines: 120,
        includeLogs: true,
        includeRoutes: false,
      },
    });
    expect(mock.conflictTarget()).toBeTruthy();
  });

  it("reuses the active job after an insert conflict race", async () => {
    const reusedJob = {
      id: "job-race",
      routerId: ROUTER_ID,
      type: "collect_optimization_baseline",
      state: "queued",
      dedupeKey: `collect_optimization_baseline:${ROUTER_ID}`,
      payload: {},
      createdAt: new Date("2026-05-15T08:01:00.000Z"),
      completedAt: null,
    };
    const mock = createMockDb({
      selectResponses: [[{ id: ROUTER_ID }], [], [reusedJob]],
      insertResponses: [[]],
    });
    const caller = createProtectedCaller(mock.db);

    await expect(caller.queueBaseline({ routerId: ROUTER_ID })).resolves.toBe(
      reusedJob,
    );
  });

  it("does not return a stale completed job if the unique dedupe key is unexpectedly still occupied", async () => {
    const mock = createMockDb({
      selectResponses: [[{ id: ROUTER_ID }], [], []],
      insertResponses: [[]],
    });
    const caller = createProtectedCaller(mock.db);

    await expect(caller.queueBaseline({ routerId: ROUTER_ID })).rejects.toThrow(
      "Optimization baseline request could not be queued.",
    );
  });
});
