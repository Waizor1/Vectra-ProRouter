import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { draftRouter } from "./draft";
import { rescueRouter } from "./rescue";
import { updateRouter } from "./update";

const CERTIFIED_LIKE_ROUTER_ID = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf";
const CERTIFIED_LIKE_REVISION_ID = "a02ee206-3ff6-40db-b23e-c036a48463be";

function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;
  let insertCalls = 0;
  let updateCalls = 0;
  const insertedValues: unknown[] = [];

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
                return Promise.resolve([]);
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

function createPilotLayoutSnapshot(layoutFamily = "ubootmod") {
  return {
    id: "snapshot-1",
    routerId: CERTIFIED_LIKE_ROUTER_ID,
    createdAt: new Date("2026-04-07T12:00:00.000Z"),
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily,
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
    },
  };
}

function createBlockedSnapshot() {
  return {
    id: "snapshot-blocked",
    routerId: CERTIFIED_LIKE_ROUTER_ID,
    createdAt: new Date("2026-04-07T12:00:00.000Z"),
    payload: {
      boardName: "tplink,tl-wr841n-v13",
      layoutFamily: "stock-layout",
      target: "ath79/generic",
      architecture: "mips_24kc",
      openwrtRelease: "24.10.6",
    },
  };
}

function createCertifiedLikeRouter() {
  return {
    id: CERTIFIED_LIKE_ROUTER_ID,
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    importState: "approved",
    status: "active",
  };
}

describe("destructive route gating", () => {
  it("allows draft apply queueing for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [],
      [],
      [],
    ]);
    const caller = createProtectedCaller(draftRouter, mock.db) as {
      queueApply: (input: {
        routerId: string;
        desiredRevisionId: string;
      }) => Promise<unknown>;
    };

    await caller.queueApply({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      desiredRevisionId: CERTIFIED_LIKE_REVISION_ID,
    });

    expect(mock.counts()).toEqual({
      insertCalls: 1,
      updateCalls: 1,
    });
  });

  it("allows controller update queueing for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueControllerUpdate: (input: {
        routerId: string;
        channel: "stable" | "beta";
      }) => Promise<unknown>;
    };

    await caller.queueControllerUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      channel: "stable",
    });

    expect(mock.counts().insertCalls).toBe(1);
  });

  it("allows rescue jobs for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
    ]);
    const caller = createProtectedCaller(rescueRouter, mock.db) as {
      triggerDirectMode: (input: {
        routerId: string;
        reason: string;
      }) => Promise<unknown>;
    };

    await caller.triggerDirectMode({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      reason: "operator-test",
    });

    expect(mock.counts().insertCalls).toBe(1);
  });

  it("queues scoped PassWall package updates without the full recovery package list", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("stock-layout")],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queuePasswallPackageUpdate: (input: {
        routerId: string;
        artifactChannel: "stable" | "beta";
        packages: ["xray-core"];
      }) => Promise<unknown>;
    };

    await caller.queuePasswallPackageUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      artifactChannel: "stable",
      packages: ["xray-core"],
    });

    const [inserted] = mock.insertedValues() as Array<{
      dedupeKey?: string;
      payload?: {
        packageList?: string[];
        artifactUrl?: string | null;
        artifactVersion?: string | null;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected scoped package update job insert.");
    }

    expect(inserted.payload?.packageList).toEqual(["xray-core"]);
    expect(inserted.payload?.artifactUrl).toBeNull();
    expect(inserted.payload?.artifactVersion).toBeNull();
    expect(inserted.dedupeKey).toContain("xray-core");
  });

  it("allows scoped PassWall package updates for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queuePasswallPackageUpdate: (input: {
        routerId: string;
        artifactChannel: "stable" | "beta";
        packages: ["xray-core"];
      }) => Promise<unknown>;
    };

    await caller.queuePasswallPackageUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      artifactChannel: "stable",
      packages: ["xray-core"],
    });

    expect(mock.counts().insertCalls).toBe(1);
  });

  it("allows subscription refresh for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueSubscriptionsRefresh: (input: {
        routerId: string;
      }) => Promise<unknown>;
    };

    await caller.queueSubscriptionsRefresh({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
    });

    expect(mock.counts().insertCalls).toBe(1);
  });

  it("still blocks controller update queueing for unsupported non-Filogic snapshots", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createBlockedSnapshot()],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueControllerUpdate: (input: {
        routerId: string;
        channel: "stable" | "beta";
      }) => Promise<unknown>;
    };

    await expect(
      caller.queueControllerUpdate({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        channel: "stable",
      }),
    ).rejects.toMatchObject({
      code: "FORBIDDEN",
    });

    expect(mock.counts().insertCalls).toBe(0);
  });
});
