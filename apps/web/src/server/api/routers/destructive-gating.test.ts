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
      packageVersions: {
        "sing-box": "1.13.5-r1",
      },
    },
  };
}

function createPasswallPackageArtifact(
  name: string,
  version: string,
  options?: { required?: boolean },
) {
  return {
    id: `artifact-${name}`,
    type: "passwall_package",
    channel: "stable",
    name,
    version,
    architecture: name === "luci-app-passwall2" ? null : "aarch64_cortex-a53",
    boardName: null,
    layoutFamily: null,
    downloadUrl: `https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/${name}_${version}.ipk`,
    checksumSha256: `sha-${name}`,
    signatureUrl: null,
    metadata: {
      source: "vectra",
      required: options?.required ?? true,
      downloadSizeBytes: 1024,
      installedSizeBytes: 2048,
    },
    publishedAt: new Date("2026-04-17T09:00:00.000Z"),
  };
}

function createPasswallBundleArtifact() {
  return {
    id: "bundle-passwall",
    type: "passwall_bundle",
    channel: "stable",
    name: "passwall2-managed-stack",
    version: "26.4.10-1",
    architecture: "aarch64_cortex-a53",
    boardName: null,
    layoutFamily: null,
    downloadUrl:
      "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/manifest.json",
    checksumSha256: "sha-bundle",
    signatureUrl: null,
    metadata: {
      source: "vectra",
      releaseTag: "26.4.10-1",
      requiredPackages: [
        {
          name: "tcping",
          filename: "tcping_0.3-r1_aarch64_cortex-a53.ipk",
          version: "0.3-r1",
          downloadSizeBytes: 4339,
          installedSizeBytes: 71680,
        },
        {
          name: "xray-core",
          filename: "xray-core_26.3.27-r1_aarch64_cortex-a53.ipk",
          version: "26.3.27-r1",
          downloadSizeBytes: 10777362,
          installedSizeBytes: 30320640,
        },
        {
          name: "geoview",
          filename: "geoview_0.2.5-r1_aarch64_cortex-a53.ipk",
          version: "0.2.5-r1",
          downloadSizeBytes: 2740538,
          installedSizeBytes: 7208960,
        },
        {
          name: "v2ray-geoip",
          filename: "v2ray-geoip_202603260032.1_all.ipk",
          version: "202603260032.1",
          downloadSizeBytes: 4040459,
          installedSizeBytes: 19773440,
        },
        {
          name: "v2ray-geosite",
          filename: "v2ray-geosite_202603292224.1_all.ipk",
          version: "202603292224.1",
          downloadSizeBytes: 3456591,
          installedSizeBytes: 10536960,
        },
        {
          name: "chinadns-ng",
          filename: "chinadns-ng_2025.08.09-r1_aarch64_cortex-a53.ipk",
          version: "2025.08.09-r1",
          downloadSizeBytes: 269754,
          installedSizeBytes: 522240,
        },
        {
          name: "luci-app-passwall2",
          filename: "luci-app-passwall2_26.4.10-r1_all.ipk",
          version: "26.4.10-r1",
          downloadSizeBytes: 325772,
          installedSizeBytes: 1300480,
        },
      ],
      optionalPackages: [
        {
          name: "sing-box",
          filename: "sing-box_1.13.6-r1_aarch64_cortex-a53.ipk",
          version: "1.13.6-r1",
          downloadSizeBytes: 15947069,
          installedSizeBytes: 45209600,
        },
        {
          name: "hysteria",
          filename: "hysteria_2.8.1-r1_aarch64_cortex-a53.ipk",
          version: "2.8.1-r1",
          downloadSizeBytes: 7012046,
          installedSizeBytes: 19077120,
        },
      ],
      packageArtifacts: [
        {
          name: "tcping",
          artifactUrl:
            "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/tcping_0.3-r1_aarch64_cortex-a53.ipk",
          artifactVersion: "0.3-r1",
          sha256: "sha-tcping",
          required: true,
          source: "vectra",
          downloadSizeBytes: 4339,
          installedSizeBytes: 71680,
        },
        {
          name: "xray-core",
          artifactUrl:
            "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/xray-core_26.3.27-r1_aarch64_cortex-a53.ipk",
          artifactVersion: "26.3.27-r1",
          sha256: "sha-xray",
          required: true,
          source: "vectra",
          downloadSizeBytes: 10777362,
          installedSizeBytes: 30320640,
        },
        {
          name: "sing-box",
          artifactUrl:
            "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/sing-box_1.13.6-r1_aarch64_cortex-a53.ipk",
          artifactVersion: "1.13.6-r1",
          sha256: "sha-sing-box",
          required: false,
          source: "vectra",
          downloadSizeBytes: 15947069,
          installedSizeBytes: 45209600,
        },
        {
          name: "luci-app-passwall2",
          artifactUrl:
            "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/luci-app-passwall2_26.4.10-r1_all.ipk",
          artifactVersion: "26.4.10-r1",
          sha256: "sha-passwall2",
          required: true,
          source: "vectra",
          downloadSizeBytes: 325772,
          installedSizeBytes: 1300480,
        },
      ],
      managedPackageList: [
        "tcping",
        "xray-core",
        "v2ray-geoip",
        "v2ray-geosite",
        "geoview",
        "chinadns-ng",
        "dnsmasq-full",
        "kmod-nft-socket",
        "kmod-nft-tproxy",
        "kmod-nft-nat",
        "luci-app-passwall2",
      ],
      recoveryDependencies: [
        "dnsmasq-full",
        "kmod-nft-socket",
        "kmod-nft-tproxy",
        "kmod-nft-nat",
      ],
      installOrder: [
        "xray-core",
        "v2ray-geoip",
        "v2ray-geosite",
        "geoview",
        "sing-box",
        "hysteria",
        "chinadns-ng",
        "tcping",
        "dnsmasq-full",
        "kmod-nft-socket",
        "kmod-nft-tproxy",
        "kmod-nft-nat",
        "luci-app-passwall2",
      ],
    },
    publishedAt: new Date("2026-04-17T09:00:00.000Z"),
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
      [
        createPasswallBundleArtifact(),
        createPasswallPackageArtifact("xray-core", "26.3.27-r1"),
      ],
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
        packageArtifacts?: Array<{
          name?: string;
          artifactVersion?: string;
        }>;
        strategy?: string | null;
        targetVersion?: string | null;
        packageTargetVersion?: string | null;
        runtimeTargetVersion?: string | null;
        originSource?: string | null;
        updateScope?: string | null;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected scoped package update job insert.");
    }

    expect(inserted.payload?.packageList).toEqual(["xray-core"]);
    expect(inserted.payload?.strategy).toBe("xray-built-in-first");
    expect(inserted.payload?.targetVersion).toBe("26.3.27-r1");
    expect(inserted.payload?.packageTargetVersion).toBe("26.3.27-r1");
    expect(inserted.payload?.runtimeTargetVersion).toBeNull();
    expect(inserted.payload?.originSource).toBe("vectra");
    expect(inserted.payload?.updateScope).toBe("scoped-package");
    expect(inserted.payload?.packageArtifacts).toHaveLength(1);
    expect(inserted.payload?.packageArtifacts?.[0]).toMatchObject({
      name: "xray-core",
      artifactVersion: "26.3.27-r1",
      artifactUrl:
        "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.10-1/aarch64_cortex-a53/xray-core_26.3.27-r1.ipk",
      sha256: "sha-xray-core",
      required: true,
      source: "vectra",
    });
    expect(inserted.dedupeKey).toContain("xray-core");
  });

  it("allows scoped PassWall package updates for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [
        createPasswallBundleArtifact(),
        createPasswallPackageArtifact("xray-core", "26.3.27-r1"),
      ],
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

  it("queues managed PassWall stack updates with tcping and installed optional components", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("stock-layout")],
      [
        createPasswallBundleArtifact(),
        createPasswallPackageArtifact("tcping", "0.3-r1"),
        createPasswallPackageArtifact("xray-core", "26.3.27-r1"),
        createPasswallPackageArtifact("v2ray-geoip", "202603260032.1"),
        createPasswallPackageArtifact("v2ray-geosite", "202603292224.1"),
        createPasswallPackageArtifact("geoview", "0.2.5-r1"),
        createPasswallPackageArtifact("chinadns-ng", "2025.08.09-r1"),
        createPasswallPackageArtifact("luci-app-passwall2", "26.4.10-r1"),
        createPasswallPackageArtifact("sing-box", "1.13.6-r1", {
          required: false,
        }),
      ],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queuePasswallPackageUpdate: (input: {
        routerId: string;
        artifactChannel: "stable" | "beta";
      }) => Promise<unknown>;
    };

    await caller.queuePasswallPackageUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      artifactChannel: "stable",
    });

    const [inserted] = mock.insertedValues() as Array<{
      payload?: {
        packageList?: string[];
        strategy?: string | null;
        targetVersion?: string | null;
        packageTargetVersion?: string | null;
        runtimeTargetVersion?: string | null;
        updateScope?: string | null;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected managed stack update job insert.");
    }

    expect(inserted.payload?.packageList).toEqual([
      "xray-core",
      "v2ray-geoip",
      "v2ray-geosite",
      "geoview",
      "sing-box",
      "chinadns-ng",
      "tcping",
      "dnsmasq-full",
      "kmod-nft-socket",
      "kmod-nft-tproxy",
      "kmod-nft-nat",
      "luci-app-passwall2",
    ]);
    expect(inserted.payload?.strategy).toBe("managed-stack-package-first");
    expect(inserted.payload?.targetVersion).toBe("26.4.10-1");
    expect(inserted.payload?.packageTargetVersion).toBe("26.4.10-r1");
    expect(inserted.payload?.runtimeTargetVersion).toBeNull();
    expect(inserted.payload?.updateScope).toBe("managed-stack");
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
