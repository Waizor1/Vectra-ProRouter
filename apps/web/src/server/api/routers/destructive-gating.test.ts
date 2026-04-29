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

function createPilotLayoutSnapshot(
  layoutFamily = "ubootmod",
  packageVersions?: Record<string, string>,
) {
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
      controllerVersion: packageVersions?.["vectra-controller-agent"] ?? null,
      packageVersions: {
        "sing-box": "1.13.5-r1",
        ...packageVersions,
      },
    },
  };
}

function createPasswallPackageArtifact(
  name: string,
  version: string,
  options?: {
    required?: boolean;
    releaseTag?: string;
    publishedAt?: Date;
  },
) {
  const releaseTag = options?.releaseTag ?? "26.4.10-1";
  return {
    id: `artifact-${name}`,
    type: "passwall_package",
    channel: "stable",
    name,
    version,
    architecture: name === "luci-app-passwall2" ? null : "aarch64_cortex-a53",
    boardName: null,
    layoutFamily: null,
    downloadUrl: `https://api.vectra-pro.net/artifacts/bootstrap/passwall2/${releaseTag}/aarch64_cortex-a53/${name}_${version}.ipk`,
    checksumSha256: `sha-${name}`,
    signatureUrl: null,
    metadata: {
      source: "vectra",
      releaseTag,
      required: options?.required ?? true,
      downloadSizeBytes: 1024,
      installedSizeBytes: 2048,
    },
    publishedAt: options?.publishedAt ?? new Date("2026-04-17T09:00:00.000Z"),
  };
}

function createControllerArtifact(
  name: string,
  version: string,
  options?: { architecture?: string | null },
) {
  return {
    id: `artifact-${name}`,
    type: "controller",
    channel: "stable",
    name,
    version,
    architecture:
      options?.architecture ??
      (name === "luci-app-vectra-controller" ? null : "aarch64_cortex-a53"),
    boardName: null,
    layoutFamily: null,
    downloadUrl: `https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/${name}_${version}.ipk`,
    checksumSha256: `sha-${name}`,
    signatureUrl: null,
    metadata: null,
    publishedAt: new Date("2026-04-18T09:00:00.000Z"),
  };
}

function bundleShaForPackage(name: string) {
  switch (name) {
    case "tcping":
      return "sha-tcping";
    case "xray-core":
      return "sha-xray";
    case "sing-box":
      return "sha-sing-box";
    case "luci-app-passwall2":
      return "sha-passwall2";
    default:
      return `sha-${name}`;
  }
}

function createPasswallBundleArtifact(options?: {
  releaseTag?: string;
  passwallAppVersion?: string;
  geoipVersion?: string;
  geositeVersion?: string;
  singBoxVersion?: string;
  publishedAt?: Date;
}) {
  const releaseTag = options?.releaseTag ?? "26.4.10-1";
  const passwallAppVersion = options?.passwallAppVersion ?? "26.4.10-r1";
  const geoipVersion = options?.geoipVersion ?? "202603260032.1";
  const geositeVersion = options?.geositeVersion ?? "202603292224.1";
  const singBoxVersion = options?.singBoxVersion ?? "1.13.6-r1";
  const requiredPackages = [
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
      filename: `v2ray-geoip_${geoipVersion}_all.ipk`,
      version: geoipVersion,
      downloadSizeBytes: 4040459,
      installedSizeBytes: 19773440,
    },
    {
      name: "v2ray-geosite",
      filename: `v2ray-geosite_${geositeVersion}_all.ipk`,
      version: geositeVersion,
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
      filename: `luci-app-passwall2_${passwallAppVersion}_all.ipk`,
      version: passwallAppVersion,
      downloadSizeBytes: 325772,
      installedSizeBytes: 1300480,
    },
  ] as const;
  const optionalPackages = [
    {
      name: "sing-box",
      filename: `sing-box_${singBoxVersion}_aarch64_cortex-a53.ipk`,
      version: singBoxVersion,
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
  ] as const;
  const packageArtifacts = [...requiredPackages, ...optionalPackages].map(
    (entry) => ({
      name: entry.name,
      artifactUrl: `https://api.vectra-pro.net/artifacts/bootstrap/passwall2/${releaseTag}/aarch64_cortex-a53/${entry.filename}`,
      artifactVersion: entry.version,
      sha256: bundleShaForPackage(entry.name),
      required: !["sing-box", "hysteria"].includes(entry.name),
      source: "vectra" as const,
      downloadSizeBytes: entry.downloadSizeBytes,
      installedSizeBytes: entry.installedSizeBytes,
    }),
  );

  return {
    id: "bundle-passwall",
    type: "passwall_bundle",
    channel: "stable",
    name: "passwall2-managed-stack",
    version: releaseTag,
    architecture: "aarch64_cortex-a53",
    boardName: null,
    layoutFamily: null,
    downloadUrl: `https://api.vectra-pro.net/artifacts/bootstrap/passwall2/${releaseTag}/aarch64_cortex-a53/manifest.json`,
    checksumSha256: "sha-bundle",
    signatureUrl: null,
    metadata: {
      source: "vectra",
      releaseTag,
      runtimeTargets: {
        "xray-core": {
          componentName: "xray",
          remoteVersion: "26.4.17",
          releaseUrl: "https://github.com/XTLS/Xray-core/releases/tag/v26.4.17",
          assetName: "Xray-linux-arm64-v8a.zip",
          assetUrl:
            "https://github.com/XTLS/Xray-core/releases/download/v26.4.17/Xray-linux-arm64-v8a.zip",
          assetSizeBytes: 12345678,
        },
        "sing-box": {
          componentName: "sing-box",
          remoteVersion: "1.13.9",
          releaseUrl: "https://github.com/SagerNet/sing-box/releases/tag/v1.13.9",
          assetName: "sing-box-1.13.9-linux-arm64-musl.tar.gz",
          assetUrl:
            "https://github.com/SagerNet/sing-box/releases/download/v1.13.9/sing-box-1.13.9-linux-arm64-musl.tar.gz",
          assetSizeBytes: 9876543,
        },
        hysteria: {
          componentName: "hysteria",
          remoteVersion: "2.8.1",
          releaseUrl: "https://github.com/apernet/hysteria/releases/tag/app/v2.8.1",
          assetName: "hysteria-linux-arm64",
          assetUrl:
            "https://github.com/apernet/hysteria/releases/download/app/v2.8.1/hysteria-linux-arm64",
          assetSizeBytes: 7654321,
        },
        geoview: {
          componentName: "geoview",
          remoteVersion: "0.2.5",
          releaseUrl: "https://github.com/snowie2000/geoview/releases/tag/0.2.5",
          assetName: "geoview-linux-arm64",
          assetUrl:
            "https://github.com/snowie2000/geoview/releases/download/0.2.5/geoview-linux-arm64",
          assetSizeBytes: 1234567,
        },
      },
      requiredPackages,
      optionalPackages,
      packageArtifacts,
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
    publishedAt: options?.publishedAt ?? new Date("2026-04-17T09:00:00.000Z"),
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

  it("uses terminal self-update as the primary lane for terminal-capable controllers", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [
        createPilotLayoutSnapshot("ubootmod", {
          "vectra-controller-agent": "0.1.12-r11",
          "luci-app-vectra-controller": "0.1.12-r11",
        }),
      ],
      [
        createControllerArtifact("vectra-controller-agent", "0.1.13-r1"),
        createControllerArtifact("luci-app-vectra-controller", "0.1.13-r1"),
      ],
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

    const [inserted] = mock.insertedValues() as Array<{
      type?: string;
      dedupeKey?: string;
      payload?: {
        purpose?: string | null;
        artifactVersion?: string | null;
        timeoutSeconds?: number;
        command?: string;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected controller update job insert.");
    }

    expect(inserted.type).toBe("run_terminal_command");
    expect(inserted.dedupeKey).toContain("0.1.13-r1");
    expect(inserted.payload?.purpose).toBe("controller-self-update");
    expect(inserted.payload?.artifactVersion).toBe("0.1.13-r1");
    expect(inserted.payload?.timeoutSeconds).toBe(120);
    expect(inserted.payload?.command).toContain(
      "VECTRA_SKIP_POSTINST_RESTART=1 opkg install --force-reinstall",
    );
    expect(inserted.payload?.command).toContain(
      "/tmp/vectra-skip-postinst-restart",
    );
    expect(inserted.payload?.command).toContain(
      'actual_sha="$(sha256sum "$1" | awk \'{print $1}\')"',
    );
    expect(inserted.payload?.command).toContain(
      "pkg_ok luci-app-vectra-controller",
    );
    expect(inserted.payload?.command).toContain(
      '^Status: install (ok|user) installed$',
    );
    expect(inserted.payload?.command).toContain(
      "need_file /www/luci-static/resources/view/vectra-controller/status.js",
    );
    expect(inserted.payload?.command).toContain(
      "controller self-update failed:",
    );
    expect(inserted.payload?.command).toContain(
      "controller self-update to 0.1.13-r1 installed",
    );
    expect(inserted.payload?.command).not.toContain("LuCI reinstall failed");
    expect(inserted.payload?.command?.length).toBeLessThanOrEqual(4000);
  });

  it("queues bulk router reboot through the terminal lane for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("ubootmod")],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueBulkRouterReboot: (input: {
        routerIds: string[];
      }) => Promise<unknown>;
    };

    await caller.queueBulkRouterReboot({
      routerIds: [CERTIFIED_LIKE_ROUTER_ID],
    });

    const [inserted] = mock.insertedValues() as Array<{
      type?: string;
      dedupeKey?: string;
      payload?: {
        purpose?: string | null;
        timeoutSeconds?: number;
        command?: string;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected router reboot job insert.");
    }

    expect(inserted.type).toBe("run_terminal_command");
    expect(inserted.dedupeKey).toBe(
      `router_reboot:${CERTIFIED_LIKE_ROUTER_ID}`,
    );
    expect(inserted.payload?.purpose).toBe("router-reboot");
    expect(inserted.payload?.timeoutSeconds).toBe(15);
    expect(inserted.payload?.command).toContain("sleep 5");
    expect(inserted.payload?.command).toContain("/sbin/reboot");
    expect(inserted.payload?.command).toContain("router reboot scheduled");
  });

  it("queues PassWall Clear IPSET/NFTSet through the terminal lane", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("ubootmod")],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queuePasswallClearIpsets: (input: {
        routerId: string;
      }) => Promise<unknown>;
    };

    await caller.queuePasswallClearIpsets({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
    });

    const [inserted] = mock.insertedValues() as Array<{
      type?: string;
      dedupeKey?: string;
      payload?: {
        purpose?: string | null;
        timeoutSeconds?: number;
        command?: string;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected Clear IPSET/NFTSet job insert.");
    }

    expect(inserted.type).toBe("run_terminal_command");
    expect(inserted.dedupeKey).toBe(
      `passwall_clear_ipsets:${CERTIFIED_LIKE_ROUTER_ID}`,
    );
    expect(inserted.payload?.purpose).toBe("passwall-clear-ipsets");
    expect(inserted.payload?.timeoutSeconds).toBe(90);
    expect(inserted.payload?.command).toContain(
      "uci -q set passwall2.@global[0].flush_set='1'",
    );
    expect(inserted.payload?.command).toContain("/etc/init.d/passwall2 restart");
  });

  it("falls back to legacy update_controller for too-old controller versions", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [
        createPilotLayoutSnapshot("ubootmod", {
          "vectra-controller-agent": "0.1.11-r1",
          "luci-app-vectra-controller": "0.1.11-r1",
        }),
      ],
      [
        createControllerArtifact("vectra-controller-agent", "0.1.13-r1"),
        createControllerArtifact("luci-app-vectra-controller", "0.1.13-r1"),
      ],
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

    const [inserted] = mock.insertedValues() as Array<{
      type?: string;
      payload?: {
        artifactVersion?: string | null;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected controller update job insert.");
    }

    expect(inserted.type).toBe("update_controller");
    expect(inserted.payload?.artifactVersion).toBe("0.1.13-r1");
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
    expect(inserted.payload?.runtimeTargetVersion).toBe("26.4.17");
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

  it("skips unpinned recovery deps when the router already has them installed", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [
        createPilotLayoutSnapshot("stock-layout", {
          "dnsmasq-full": "2.90-r4",
          "kmod-nft-socket": "6.6.119-r1",
          "kmod-nft-tproxy": "6.6.119-r1",
          "kmod-nft-nat": "6.6.119-r1",
        }),
      ],
      [
        createPasswallBundleArtifact(),
        createPasswallPackageArtifact("tcping", "0.3-r1"),
        createPasswallPackageArtifact("xray-core", "26.3.27-r1"),
        createPasswallPackageArtifact("v2ray-geoip", "202603260032.1"),
        createPasswallPackageArtifact("v2ray-geosite", "202603292224.1"),
        createPasswallPackageArtifact("geoview", "0.2.5-r1"),
        createPasswallPackageArtifact("chinadns-ng", "2025.08.09-r1"),
        createPasswallPackageArtifact("sing-box", "1.13.6-r1", {
          required: false,
        }),
        createPasswallPackageArtifact("luci-app-passwall2", "26.4.10-r1"),
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
      "luci-app-passwall2",
    ]);
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

  it("uses published runtime target metadata for scoped sing-box updates", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [
        createPasswallBundleArtifact(),
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
        packages: ["sing-box"];
      }) => Promise<unknown>;
    };

    await caller.queuePasswallPackageUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      artifactChannel: "stable",
      packages: ["sing-box"],
    });

    const [inserted] = mock.insertedValues() as Array<{
      payload?: {
        packageList?: string[];
        strategy?: string | null;
        packageTargetVersion?: string | null;
        runtimeTargetVersion?: string | null;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected scoped sing-box update job insert.");
    }

    expect(inserted.payload?.packageList).toEqual(["sing-box"]);
    expect(inserted.payload?.strategy).toBe("managed-stack-package-first");
    expect(inserted.payload?.packageTargetVersion).toBe("1.13.6-r1");
    expect(inserted.payload?.runtimeTargetVersion).toBe("1.13.9");
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

  it("keeps managed-stack package targets on the selected bundle release instead of older package rows", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("stock-layout")],
      [
        createPasswallBundleArtifact({
          releaseTag: "26.4.20-1",
          passwallAppVersion: "26.4.20-r1",
          geoipVersion: "202604090028.1",
          geositeVersion: "202604122227.1",
          singBoxVersion: "1.13.8-r1",
          publishedAt: new Date("2026-04-22T06:53:28.888Z"),
        }),
        createPasswallPackageArtifact("tcping", "0.3-r1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:07.045Z"),
        }),
        createPasswallPackageArtifact("xray-core", "26.3.27-r1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:07.045Z"),
        }),
        createPasswallPackageArtifact("v2ray-geoip", "202603260032.1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:06.992Z"),
        }),
        createPasswallPackageArtifact("v2ray-geosite", "202603292224.1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:06.986Z"),
        }),
        createPasswallPackageArtifact("geoview", "0.2.5-r1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:06.980Z"),
        }),
        createPasswallPackageArtifact("chinadns-ng", "2025.08.09-r1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:06.974Z"),
        }),
        createPasswallPackageArtifact("luci-app-passwall2", "26.4.10-r1", {
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:06.963Z"),
        }),
        createPasswallPackageArtifact("sing-box", "1.13.6-r1", {
          required: false,
          releaseTag: "26.4.10-1",
          publishedAt: new Date("2026-04-19T12:50:07.045Z"),
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
        targetVersion?: string | null;
        packageTargetVersion?: string | null;
        packageArtifacts?: Array<{
          name?: string | null;
          artifactVersion?: string | null;
          artifactUrl?: string | null;
        }>;
      };
    }>;

    expect(inserted).toBeDefined();
    if (!inserted) {
      throw new Error("Expected managed stack update job insert.");
    }

    expect(inserted.payload?.targetVersion).toBe("26.4.20-1");
    expect(inserted.payload?.packageTargetVersion).toBe("26.4.20-r1");
    expect(
      inserted.payload?.packageArtifacts?.find(
        (artifact) => artifact.name === "luci-app-passwall2",
      ),
    ).toMatchObject({
      name: "luci-app-passwall2",
      artifactVersion: "26.4.20-r1",
      artifactUrl:
        "https://api.vectra-pro.net/artifacts/bootstrap/passwall2/26.4.20-1/aarch64_cortex-a53/luci-app-passwall2_26.4.20-r1_all.ipk",
    });
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
