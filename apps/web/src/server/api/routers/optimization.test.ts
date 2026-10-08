import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

const { notifyVendorAccessWithDb } = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
}));
vi.mock("~/server/vectra/vendor-access", () => ({ notifyVendorAccessWithDb }));

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

describe("optimizationRouter.queueBaseline and the partner's vendor-access notice", () => {
  beforeEach(() => {
    notifyVendorAccessWithDb.mockClear();
  });

  const partnerRouter = { id: ROUTER_ID, ownerRef: "bc_1", partnerId: "bloopcat" };

  it("tells the router's partner when a baseline is queued", async () => {
    const mock = createMockDb({
      selectResponses: [[partnerRouter], []],
      insertResponses: [[{ id: "job-1" }]],
    });

    await createProtectedCaller(mock.db).queueBaseline({ routerId: ROUTER_ID });

    expect(notifyVendorAccessWithDb).toHaveBeenCalledTimes(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledWith(mock.db, partnerRouter, {
      kind: "diagnostics",
      by: "operator",
    });
  });

  it("says nothing when an active baseline is returned instead", async () => {
    const mock = createMockDb({
      selectResponses: [[partnerRouter], [{ id: "job-0" }]],
    });

    await createProtectedCaller(mock.db).queueBaseline({ routerId: ROUTER_ID });

    expect(mock.insertedValues()).toHaveLength(0);
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });

  it("says nothing when the insert lost a race and reused the other job", async () => {
    const mock = createMockDb({
      selectResponses: [[partnerRouter], [], [{ id: "job-race" }]],
      insertResponses: [[]],
    });

    await createProtectedCaller(mock.db).queueBaseline({ routerId: ROUTER_ID });

    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });
});
