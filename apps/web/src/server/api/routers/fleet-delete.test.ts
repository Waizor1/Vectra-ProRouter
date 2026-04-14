import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { fleetRouter } from "./fleet";

const ROUTER_ID = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf";

function createRouterRow() {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "device-test-1",
    displayName: "Test Router",
    hostname: "ax3000t-test",
    panelDomain: "https://panel.example.com",
    model: "AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    controllerChannel: "stable",
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: new Date("2026-04-08T09:00:00.000Z"),
    lastSeenAt: new Date("2026-04-08T09:10:00.000Z"),
    lastCheckInAt: new Date("2026-04-08T09:10:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    createdAt: new Date("2026-04-08T08:00:00.000Z"),
    updatedAt: new Date("2026-04-08T09:10:00.000Z"),
  };
}

function createMockDb({
  selectResponses,
  deleteResponses = [],
}: {
  selectResponses: unknown[][];
  deleteResponses?: unknown[][];
}) {
  let selectIndex = 0;
  let deleteIndex = 0;
  let insertCalls = 0;
  let deleteCalls = 0;
  const insertedValues: unknown[] = [];

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];
  const nextDeleteResult = () => deleteResponses[deleteIndex++] ?? [];

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
        insertCalls += 1;
        return {
          values(value: unknown) {
            insertedValues.push(value);
            return Promise.resolve([]);
          },
        };
      },
      delete() {
        deleteCalls += 1;
        return {
          where() {
            return {
              returning() {
                return Promise.resolve(nextDeleteResult());
              },
            };
          },
        };
      },
    },
    counts() {
      return { insertCalls, deleteCalls };
    },
    insertedValues() {
      return insertedValues;
    },
  };
}

function createProtectedCaller<T>(router: T, db: unknown) {
  return createCallerFactory(router as never)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

describe("fleet.deleteRouter", () => {
  it("deletes the router and records an audit event", async () => {
    const router = createRouterRow();
    const mock = createMockDb({
      selectResponses: [[router]],
      deleteResponses: [[router]],
    });
    const caller = createProtectedCaller(fleetRouter, mock.db) as {
      deleteRouter: (input: { routerId: string }) => Promise<{
        router: typeof router;
      }>;
    };

    const result = await caller.deleteRouter({ routerId: ROUTER_ID });

    expect(result.router.id).toBe(ROUTER_ID);
    expect(mock.counts()).toEqual({
      insertCalls: 1,
      deleteCalls: 1,
    });
    const [insertedEvent] = mock.insertedValues() as Array<{
      routerId: string;
      type: string;
      severity: string;
      metadata: Record<string, unknown>;
    }>;

    expect(insertedEvent).toBeDefined();
    if (!insertedEvent) {
      throw new Error("Expected router deletion audit event.");
    }

    expect(insertedEvent.routerId).toBe(ROUTER_ID);
    expect(insertedEvent.type).toBe("router.deleted");
    expect(insertedEvent.severity).toBe("warning");
    expect(insertedEvent.metadata).toMatchObject({
      routerId: ROUTER_ID,
      deviceIdentifier: "device-test-1",
      displayName: "Test Router",
    });
  });

  it("returns not found before mutating when the router does not exist", async () => {
    const mock = createMockDb({
      selectResponses: [[]],
    });
    const caller = createProtectedCaller(fleetRouter, mock.db) as {
      deleteRouter: (input: { routerId: string }) => Promise<unknown>;
    };

    await expect(
      caller.deleteRouter({ routerId: ROUTER_ID }),
    ).rejects.toMatchObject({
      code: "NOT_FOUND",
    });

    expect(mock.counts()).toEqual({
      insertCalls: 0,
      deleteCalls: 0,
    });
  });
});
