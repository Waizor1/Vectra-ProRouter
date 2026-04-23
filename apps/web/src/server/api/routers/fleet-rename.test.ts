import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { fleetRouter } from "./fleet";

const ROUTER_ID = "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9";

function createRouterRow(
  overrides: Partial<ReturnType<typeof baseRouterRow>> = {},
) {
  return {
    ...baseRouterRow(),
    ...overrides,
  };
}

function createSnapshotRow(
  overrides: Partial<ReturnType<typeof baseSnapshotRow>> = {},
) {
  return {
    ...baseSnapshotRow(),
    ...overrides,
  };
}

function baseRouterRow() {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "router-test-rename-1",
    displayName: null as string | null,
    hostname: "openwrt-host",
    panelDomain: "https://panel.example.com",
    model: "AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    controllerChannel: "stable",
    rolloutGroupId: null,
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

function baseSnapshotRow() {
  return {
    id: "snapshot-1",
    routerId: ROUTER_ID,
    createdAt: new Date("2026-04-08T09:10:00.000Z"),
    controllerVersion: "0.1.12-r9",
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily: "stock-layout",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      hostname: "openwrt-host",
      controllerVersion: "0.1.12-r9",
      packageVersions: {
        "vectra-controller-agent": "0.1.12-r9",
        "luci-app-vectra-controller": "0.1.12-r9",
      },
    },
  };
}

function createMockDb({
  selectResponses,
  insertResponses = [],
}: {
  selectResponses: unknown[][];
  insertResponses?: unknown[][];
}) {
  let selectIndex = 0;
  let insertIndex = 0;
  let insertCalls = 0;
  let updateCalls = 0;
  const insertedValues: unknown[] = [];

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
        insertCalls += 1;
        return {
          values(value: unknown) {
            insertedValues.push(value);
            const result = nextInsertResult();
            return {
              returning() {
                return Promise.resolve(result);
              },
              then<TResult1 = unknown, TResult2 = never>(
                onfulfilled?:
                  | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
                  | null,
                onrejected?:
                  | ((reason: unknown) => TResult2 | PromiseLike<TResult2>)
                  | null,
              ) {
                return Promise.resolve(result).then(onfulfilled, onrejected);
              },
            };
          },
        };
      },
      update() {
        updateCalls += 1;
        return {
          set() {
            return {
              where() {
                return {
                  returning() {
                    return Promise.resolve([]);
                  },
                };
              },
            };
          },
        };
      },
    },
    counts() {
      return { insertCalls, updateCalls };
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

describe("fleet.renameRouter", () => {
  it("queues a real OpenWrt hostname update through the terminal lane", async () => {
    const router = createRouterRow();
    const snapshot = createSnapshotRow();
    const queuedJob = {
      id: "job-hostname-1",
      routerId: ROUTER_ID,
      type: "run_terminal_command",
      state: "queued",
      dedupeKey: "router_hostname_update:test:andrey-livingroom",
      payload: {
        purpose: "router-hostname-update",
        hostname: "andrey-livingroom",
      },
      desiredRevisionId: null,
      deliverAfter: null,
      deliveredAt: null,
      completedAt: null,
      createdAt: new Date("2026-04-22T12:00:00.000Z"),
    };
    const mock = createMockDb({
      selectResponses: [[router], [snapshot], []],
      insertResponses: [[queuedJob], []],
    });
    const caller = createProtectedCaller(fleetRouter, mock.db) as {
      renameRouter: (input: {
        routerId: string;
        hostname: string;
      }) => Promise<typeof queuedJob>;
    };

    const result = await caller.renameRouter({
      routerId: ROUTER_ID,
      hostname: "  Andrey-LivingRoom  ",
    });

    expect(result).toMatchObject({
      id: "job-hostname-1",
      type: "run_terminal_command",
      state: "queued",
    });
    expect(mock.counts()).toEqual({
      insertCalls: 2,
      updateCalls: 0,
    });

    const [insertedJob, insertedEvent] = mock.insertedValues() as Array<{
      type?: string;
      dedupeKey?: string;
      payload?: Record<string, unknown>;
      metadata?: Record<string, unknown>;
    }>;

    expect(insertedJob?.type).toBe("run_terminal_command");
    expect(insertedJob?.dedupeKey).toBe(
      `router_hostname_update:${ROUTER_ID}:andrey-livingroom`,
    );
    expect(insertedJob?.payload).toMatchObject({
      purpose: "router-hostname-update",
      hostname: "andrey-livingroom",
      timeoutSeconds: 30,
    });
    expect(String(insertedJob?.payload?.command)).toContain(
      'uci set system.@system[0].hostname="$new_hostname"',
    );
    expect(String(insertedJob?.payload?.command)).toContain("uci commit system");
    expect(String(insertedJob?.payload?.command)).toContain("reload_config");
    expect(String(insertedJob?.payload?.command)).toContain(
      "/etc/init.d/system reload",
    );

    expect(insertedEvent?.type).toBe("router.hostname.update.requested");
    expect(insertedEvent?.metadata).toMatchObject({
      requestedHostname: "andrey-livingroom",
      previousHostname: "openwrt-host",
      deviceIdentifier: "router-test-rename-1",
      jobId: "job-hostname-1",
    });
  });

  it("rejects invalid hostnames before queueing a router-side command", async () => {
    const mock = createMockDb({
      selectResponses: [],
    });
    const caller = createProtectedCaller(fleetRouter, mock.db) as {
      renameRouter: (input: {
        routerId: string;
        hostname: string;
      }) => Promise<unknown>;
    };

    await expect(
      caller.renameRouter({
        routerId: ROUTER_ID,
        hostname: "Андрей / гостиная",
      }),
    ).rejects.toMatchObject({
      code: "BAD_REQUEST",
    });

    expect(mock.counts()).toEqual({
      insertCalls: 0,
      updateCalls: 0,
    });
  });

  it("returns not found before mutating when the router does not exist", async () => {
    const mock = createMockDb({
      selectResponses: [[]],
    });
    const caller = createProtectedCaller(fleetRouter, mock.db) as {
      renameRouter: (input: {
        routerId: string;
        hostname: string;
      }) => Promise<unknown>;
    };

    await expect(
      caller.renameRouter({
        routerId: ROUTER_ID,
        hostname: "andrey-livingroom",
      }),
    ).rejects.toMatchObject({
      code: "NOT_FOUND",
    });

    expect(mock.counts()).toEqual({
      insertCalls: 0,
      updateCalls: 0,
    });
  });
});
