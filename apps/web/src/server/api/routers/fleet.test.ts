import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { fleetRouter } from "./fleet";

const ROUTER_ID = "f0f2437a-7786-4a3b-be38-5c02af2f4461";

function createRouterRow() {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "router-test-1",
    displayName: "VagrandRouter",
    hostname: "vagrand-router",
    panelDomain: "router.vectra-pro.net",
    model: "WR3000E",
    boardName: "cudy,wr3000e-v1",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.5",
    status: "active",
    importState: "approved",
    controllerChannel: "stable",
    pendingImportRevisionId: null,
    activeRevisionId: "revision-live-1",
    lastAppliedRevisionId: null,
    lastConfigDigest: "digest-live",
    approvedAt: new Date("2026-04-19T10:00:00.000Z"),
    lastSeenAt: new Date("2026-04-20T09:00:00.000Z"),
    lastCheckInAt: new Date("2026-04-20T09:00:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    createdAt: new Date("2026-04-10T10:00:00.000Z"),
    updatedAt: new Date("2026-04-20T09:00:00.000Z"),
  };
}

function createSnapshotRow() {
  return {
    id: "snapshot-1",
    routerId: ROUTER_ID,
    source: "check_in",
    payload: {
      boardName: "cudy,wr3000e-v1",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.5",
      hostname: "vagrand-router",
      configDigest: "digest-live",
      packageVersions: {
        "luci-app-passwall2": "26.4.10-r1",
      },
      binaryVersions: {},
    },
    passwallEnabled: true,
    selectedNodeId: "myshunt",
    nodeCount: 17,
    subscriptionCount: 1,
    controllerVersion: "0.1.12-r13",
    passwallAppVersion: "26.4.10-r1",
    createdAt: new Date("2026-04-20T09:00:00.000Z"),
  };
}

function createRevisionMetadata() {
  return {
    id: "revision-live-1",
    routerId: ROUTER_ID,
    revisionNumber: 17,
    status: "approved",
    origin: "router_import",
    configDigest: "digest-live",
    createdBy: "router",
    note: "Imported from live router PassWall2 state.",
    approvedAt: new Date("2026-04-19T10:00:00.000Z"),
    createdAt: new Date("2026-04-19T10:00:00.000Z"),
    hasRawImportedSnapshot: true,
  };
}

function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];

  const makeSelectChain = () => {
    const chain = {
      from() {
        return chain;
      },
      where() {
        return chain;
      },
      orderBy() {
        return chain;
      },
      limit() {
        return Promise.resolve(nextSelectResult());
      },
      then<TResult1 = unknown, TResult2 = never>(
        onfulfilled?:
          | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
          | null,
        onrejected?:
          | ((reason: unknown) => TResult2 | PromiseLike<TResult2>)
          | null,
      ) {
        return Promise.resolve(nextSelectResult()).then(onfulfilled, onrejected);
      },
    };

    return chain;
  };

  return {
    select() {
      return makeSelectChain();
    },
  };
}

function createProtectedCaller(db: unknown) {
  return createCallerFactory(fleetRouter as never)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  }) as {
    list: () => Promise<
      Array<{
        id: string;
        latestDesiredRevision: Record<string, unknown> | null;
        configTrust: {
          configSourceMode: string;
          requiresReimport: boolean;
          digestMismatch: boolean;
        };
      }>
    >;
  };
}

describe("fleet.list", () => {
  it("returns compact latest revision metadata while preserving config trust", async () => {
    const caller = createProtectedCaller(
      createMockDb([
        [createRouterRow()],
        [createSnapshotRow()],
        [createRevisionMetadata()],
        [],
        [],
      ]),
    );

    const result = await caller.list();

    expect(result).toHaveLength(1);
    expect(result[0]?.configTrust).toMatchObject({
      configSourceMode: "live-import",
      requiresReimport: false,
      digestMismatch: false,
    });
    expect(result[0]?.latestDesiredRevision).toMatchObject({
      id: "revision-live-1",
      revisionNumber: 17,
      status: "approved",
      origin: "router_import",
      configDigest: "digest-live",
      hasRawImportedSnapshot: true,
    });
    expect(result[0]?.latestDesiredRevision).not.toHaveProperty("config");
  });
});
