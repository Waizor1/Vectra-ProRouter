import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { fleetRouter } from "./fleet";

const ROUTER_ID = "3f1c9a04-1d2e-4a7b-9c3f-8b21d4e6a710";

function baseRouterRow() {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "router-test-engine-1",
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
    engineMode: "passwall",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: "9a7c1f30-5b6d-4e21-8a44-2c9f7d1e3b05",
    lastAppliedRevisionId: "9a7c1f30-5b6d-4e21-8a44-2c9f7d1e3b05",
    lastConfigDigest: "digest-live",
    approvedAt: new Date("2026-04-08T09:00:00.000Z"),
    lastSeenAt: new Date("2026-04-08T09:10:00.000Z"),
    lastCheckInAt: new Date("2026-04-08T09:10:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    createdAt: new Date("2026-04-08T08:00:00.000Z"),
    updatedAt: new Date("2026-04-08T09:10:00.000Z"),
  };
}

function createRouterRow(
  overrides: Partial<ReturnType<typeof baseRouterRow>> = {},
) {
  return { ...baseRouterRow(), ...overrides };
}

function createSnapshotRow(payloadOverrides: Record<string, unknown> = {}) {
  return {
    id: "snapshot-1",
    routerId: ROUTER_ID,
    createdAt: new Date("2026-04-08T09:10:00.000Z"),
    controllerVersion: "0.1.13-r36",
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily: "stock-layout",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      ...payloadOverrides,
    },
  };
}

function createQueuedApplyJob() {
  return {
    id: "job-apply-1",
    routerId: ROUTER_ID,
    type: "apply_passwall_config",
    state: "queued",
    dedupeKey: `apply_passwall_config:${ROUTER_ID}`,
    payload: {},
    desiredRevisionId: "9a7c1f30-5b6d-4e21-8a44-2c9f7d1e3b05",
    deliverAfter: null,
    deliveredAt: null,
    completedAt: null,
    createdAt: new Date("2026-04-08T09:05:00.000Z"),
  };
}

function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;
  let insertCalls = 0;
  let updateCalls = 0;
  const insertedValues: unknown[] = [];
  const updatedValues: unknown[] = [];

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];

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
            return {
              returning() {
                return Promise.resolve([]);
              },
              then<TResult1 = unknown, TResult2 = never>(
                onfulfilled?:
                  | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
                  | null,
                onrejected?:
                  | ((reason: unknown) => TResult2 | PromiseLike<TResult2>)
                  | null,
              ) {
                return Promise.resolve([]).then(onfulfilled, onrejected);
              },
            };
          },
        };
      },
      update() {
        updateCalls += 1;
        return {
          set(value: unknown) {
            updatedValues.push(value);
            return {
              where() {
                return {
                  returning() {
                    // Echo the write back the way the driver would, so the
                    // procedure's return value reflects the new engine mode.
                    return Promise.resolve([
                      { ...baseRouterRow(), ...(value as object) },
                    ]);
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
    updatedValues() {
      return updatedValues;
    },
  };
}

function createProtectedCaller(db: unknown) {
  return createCallerFactory(fleetRouter as never)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  }) as unknown as {
    setEngineMode: (input: {
      routerId: string;
      engineMode: "passwall" | "xray-direct";
    }) => Promise<{ engineMode: string }>;
  };
}

describe("fleet.setEngineMode", () => {
  it("switches a passwall router onto the xray-direct engine", async () => {
    const mock = createMockDb([
      [createRouterRow({ engineMode: "passwall" })],
      [createSnapshotRow()],
      [],
    ]);

    const result = await createProtectedCaller(mock.db).setEngineMode({
      routerId: ROUTER_ID,
      engineMode: "xray-direct",
    });

    expect(mock.counts().updateCalls).toBe(1);
    expect(mock.updatedValues()).toEqual([{ engineMode: "xray-direct" }]);
    expect(result.engineMode).toBe("xray-direct");
  });

  it("switches an xray-direct router back to passwall", async () => {
    const mock = createMockDb([
      [createRouterRow({ engineMode: "xray-direct" })],
      [createSnapshotRow()],
      [],
    ]);

    const result = await createProtectedCaller(mock.db).setEngineMode({
      routerId: ROUTER_ID,
      engineMode: "passwall",
    });

    expect(mock.counts().updateCalls).toBe(1);
    expect(mock.updatedValues()).toEqual([{ engineMode: "passwall" }]);
    expect(result.engineMode).toBe("passwall");
  });

  it("journals the switch as a warning with both the old and new engine", async () => {
    const mock = createMockDb([
      [createRouterRow({ engineMode: "passwall" })],
      [createSnapshotRow()],
      [],
    ]);

    await createProtectedCaller(mock.db).setEngineMode({
      routerId: ROUTER_ID,
      engineMode: "xray-direct",
    });

    const [event] = mock.insertedValues() as Array<{
      type?: string;
      severity?: string;
      metadata?: {
        previousEngineMode?: string;
        engineMode?: string;
        activeRevisionId?: string | null;
      };
    }>;

    expect(event?.type).toBe("router.engine_mode.changed");
    expect(event?.severity).toBe("warning");
    expect(event?.metadata?.previousEngineMode).toBe("passwall");
    expect(event?.metadata?.engineMode).toBe("xray-direct");
    expect(event?.metadata?.activeRevisionId).toBe(
      "9a7c1f30-5b6d-4e21-8a44-2c9f7d1e3b05",
    );
  });

  it("rejects switching a router to the engine it already runs", async () => {
    const mock = createMockDb([
      [createRouterRow({ engineMode: "passwall" })],
      [createSnapshotRow()],
      [],
    ]);

    await expect(
      createProtectedCaller(mock.db).setEngineMode({
        routerId: ROUTER_ID,
        engineMode: "passwall",
      }),
    ).rejects.toMatchObject({ code: "BAD_REQUEST" });

    expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
  });

  it("refuses to switch the engine on an unsupported board", async () => {
    const mock = createMockDb([
      [
        createRouterRow({
          engineMode: "passwall",
          boardName: "tplink,tl-wr841n-v13",
          target: "ath79/generic",
          architecture: "mips_24kc",
        }),
      ],
      [
        createSnapshotRow({
          boardName: "tplink,tl-wr841n-v13",
          layoutFamily: "stock-layout",
          target: "ath79/generic",
          architecture: "mips_24kc",
        }),
      ],
      [],
    ]);

    await expect(
      createProtectedCaller(mock.db).setEngineMode({
        routerId: ROUTER_ID,
        engineMode: "xray-direct",
      }),
    ).rejects.toMatchObject({ code: "FORBIDDEN" });

    expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
  });

  it("refuses to switch the engine while a job is still in flight", async () => {
    // A queued apply_passwall_config would be filtered out of every future
    // check-in by selectDeliverableJobsForCheckIn the moment the router moves
    // to xray-direct, stranding it in `queued` instead of failing it.
    const mock = createMockDb([
      [createRouterRow({ engineMode: "passwall" })],
      [createSnapshotRow()],
      [createQueuedApplyJob()],
    ]);

    await expect(
      createProtectedCaller(mock.db).setEngineMode({
        routerId: ROUTER_ID,
        engineMode: "xray-direct",
      }),
    ).rejects.toMatchObject({ code: "CONFLICT" });

    expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
  });

  it("blocks the in-flight-job case in the reverse direction too", async () => {
    const mock = createMockDb([
      [createRouterRow({ engineMode: "xray-direct" })],
      [createSnapshotRow()],
      [createQueuedApplyJob()],
    ]);

    await expect(
      createProtectedCaller(mock.db).setEngineMode({
        routerId: ROUTER_ID,
        engineMode: "passwall",
      }),
    ).rejects.toMatchObject({ code: "CONFLICT" });

    expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
  });

  it("reports a missing router instead of writing", async () => {
    const mock = createMockDb([[]]);

    await expect(
      createProtectedCaller(mock.db).setEngineMode({
        routerId: ROUTER_ID,
        engineMode: "xray-direct",
      }),
    ).rejects.toMatchObject({ code: "NOT_FOUND" });

    expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
  });
});
