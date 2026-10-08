import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

// The notice itself is real here, so its silence rules (Vectra's own and
// unclaimed routers) are exercised too; only the outbox write is replaced.
const { enqueueWebhook } = vi.hoisted(() => ({
  enqueueWebhook: vi.fn(async (..._args: unknown[]): Promise<string | null> => "w1"),
}));
vi.mock("~/server/vectra/partner-webhooks", () => ({
  enqueuePartnerWebhookWithDb: enqueueWebhook,
  schedulePartnerWebhookDelivery: vi.fn(),
}));

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
                return Object.assign(Promise.resolve([]), {
                  returning() {
                    return Promise.resolve([]);
                  },
                });
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
  payloadOverrides: Record<string, unknown> = {},
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
      ...payloadOverrides,
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

function createCertifiedLikeRouter(options?: {
  activeRevisionId?: string | null;
  importState?: string;
  lastConfigDigest?: string | null;
  status?: string;
  lastCheckInAt?: Date | null;
}) {
  return {
    id: CERTIFIED_LIKE_ROUTER_ID,
    lastCheckInAt:
      options?.lastCheckInAt === undefined ? new Date() : options.lastCheckInAt,
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    importState: options?.importState ?? "approved",
    status: options?.status ?? "active",
    activeRevisionId: options?.activeRevisionId ?? null,
    lastConfigDigest: options?.lastConfigDigest ?? null,
  };
}

function createDesiredRevision(options?: {
  id?: string;
  origin?: string;
  status?: string;
  revisionNumber?: number;
  configDigest?: string | null;
  createdAt?: Date;
}) {
  return {
    id: options?.id ?? CERTIFIED_LIKE_REVISION_ID,
    routerId: CERTIFIED_LIKE_ROUTER_ID,
    revisionNumber: options?.revisionNumber ?? 31,
    origin: options?.origin ?? "operator_draft",
    status: options?.status ?? "draft",
    configDigest: options?.configDigest ?? "digest-draft",
    createdAt: options?.createdAt ?? new Date("2026-04-29T12:00:00.000Z"),
  };
}

describe("destructive route gating", () => {
  it("allows draft apply queueing for pilot Filogic layouts", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot()],
      [createDesiredRevision()],
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

  it("blocks apply queueing for an operator draft superseded by the active live baseline", async () => {
    const activeRevisionId = "8a9af7ac-9d0f-4b13-a064-28b6b15f7b75";
    const mock = createMockDb([
      [
        createCertifiedLikeRouter({
          activeRevisionId,
          lastConfigDigest: "digest-live",
        }),
      ],
      [createPilotLayoutSnapshot()],
      [
        createDesiredRevision({
          id: activeRevisionId,
          origin: "router_import",
          status: "approved",
          revisionNumber: 19,
          configDigest: "digest-live",
          createdAt: new Date("2026-04-28T21:00:00.000Z"),
        }),
        createDesiredRevision({
          id: CERTIFIED_LIKE_REVISION_ID,
          origin: "operator_draft",
          status: "draft",
          revisionNumber: 13,
          configDigest: "digest-stale",
          createdAt: new Date("2026-04-23T12:00:00.000Z"),
        }),
      ],
    ]);
    const caller = createProtectedCaller(draftRouter, mock.db) as {
      queueApply: (input: {
        routerId: string;
        desiredRevisionId: string;
      }) => Promise<unknown>;
    };

    await expect(
      caller.queueApply({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        desiredRevisionId: CERTIFIED_LIKE_REVISION_ID,
      }),
    ).rejects.toMatchObject({
      code: "PRECONDITION_FAILED",
    });

    expect(mock.counts()).toEqual({
      insertCalls: 0,
      updateCalls: 0,
    });
  });

  it("does not demote an approved active revision to queued when re-applying the authoritative baseline", async () => {
    const activeRevisionId = CERTIFIED_LIKE_REVISION_ID;
    const mock = createMockDb([
      [createCertifiedLikeRouter({ activeRevisionId })],
      [createPilotLayoutSnapshot()],
      [
        createDesiredRevision({
          id: activeRevisionId,
          origin: "operator_draft",
          status: "approved",
          revisionNumber: 31,
        }),
      ],
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
      desiredRevisionId: activeRevisionId,
    });

    expect(mock.counts()).toEqual({
      insertCalls: 1,
      updateCalls: 0,
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
      "controller self-update resource guard:",
    );
    expect(inserted.payload?.command).not.toContain("then;");
    expect(inserted.payload?.command).toContain("then\n  echo");
    expect(inserted.payload?.command).toContain(
      "/tmp/vectra-skip-postinst-restart",
    );
    expect(inserted.payload?.command).toContain(
      'actual_sha="$(sha256sum "$1" | awk \'{print $1}\')"',
    );
    expect(inserted.payload?.command).toContain(
      "pkg_ok luci-app-vectra-controller",
    );
    expect(inserted.payload?.command).toContain("/usr/lib/opkg/status");
    expect(inserted.payload?.command).not.toContain("opkg status");
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
    // 4000 → 8000 (r28, verify_ipk_has_agent / verify_agent_on_disk) →
    // 48000 (r47: the embedded rollback guard, ~27 KB).
    expect(inserted.payload?.command?.length).toBeLessThanOrEqual(48000);
    expect(inserted.payload?.command).toContain('sh "$guard" prepare');
    expect(inserted.payload?.command).toContain('sh "$guard" launch');
  });

  it("uses a compat terminal purpose for pre-r20 controllers when the controller update is resource-safe", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [
        createPilotLayoutSnapshot(
          "ubootmod",
          {
            "vectra-controller-agent": "0.1.13-r18",
            "luci-app-vectra-controller": "0.1.13-r18",
          },
          {
            resources: {
              memoryAvailableMb: 74,
              overlayFreeMb: 12,
              tmpFreeMb: 114,
            },
          },
        ),
      ],
      [
        createControllerArtifact("vectra-controller-agent", "0.1.13-r20"),
        createControllerArtifact("luci-app-vectra-controller", "0.1.13-r20"),
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
      payload?: { purpose?: string | null; artifactVersion?: string | null };
    }>;

    expect(inserted?.type).toBe("run_terminal_command");
    expect(inserted?.payload?.purpose).toBe("controller-self-update-compat");
    expect(inserted?.payload?.artifactVersion).toBe("0.1.13-r20");
  });

  it("does not use the compat bridge for pre-r20 routers already in direct rescue mode", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter({ status: "direct" })],
      [
        createPilotLayoutSnapshot(
          "ubootmod",
          {
            "vectra-controller-agent": "0.1.13-r18",
            "luci-app-vectra-controller": "0.1.13-r18",
          },
          {
            resources: {
              memoryAvailableMb: 74,
              overlayFreeMb: 12,
              tmpFreeMb: 114,
            },
            lastRescue: {
              mode: "direct",
              reason: "proxy outage",
              happenedAt: "2026-05-12T00:00:00.000Z",
            },
          },
        ),
      ],
      [
        createControllerArtifact("vectra-controller-agent", "0.1.13-r20"),
        createControllerArtifact("luci-app-vectra-controller", "0.1.13-r20"),
      ],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueControllerUpdate: (input: {
        routerId: string;
        channel: "stable" | "beta";
        force?: boolean;
      }) => Promise<unknown>;
    };

    await caller.queueControllerUpdate({
      routerId: CERTIFIED_LIKE_ROUTER_ID,
      channel: "stable",
      // A router in direct mode is refused by the rollout guard unless forced.
      force: true,
    });

    const [inserted] = mock.insertedValues() as Array<{
      type?: string;
      payload?: { purpose?: string | null; artifactVersion?: string | null };
    }>;

    expect(inserted?.type).toBe("run_terminal_command");
    expect(inserted?.payload?.purpose).toBe("controller-self-update");
    expect(inserted?.payload?.artifactVersion).toBe("0.1.13-r20");
  });

  it("keeps the normal self-update purpose when a pre-r20 controller is below bridge floors", async () => {
    const mock = createMockDb([
      [createCertifiedLikeRouter()],
      [
        createPilotLayoutSnapshot(
          "ubootmod",
          {
            "vectra-controller-agent": "0.1.13-r18",
            "luci-app-vectra-controller": "0.1.13-r18",
          },
          {
            resources: {
              memoryAvailableMb: 74,
              overlayFreeMb: 7,
              tmpFreeMb: 114,
            },
          },
        ),
      ],
      [
        createControllerArtifact("vectra-controller-agent", "0.1.13-r20"),
        createControllerArtifact("luci-app-vectra-controller", "0.1.13-r20"),
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
      payload?: { purpose?: string | null; artifactVersion?: string | null };
    }>;

    expect(inserted?.type).toBe("run_terminal_command");
    expect(inserted?.payload?.purpose).toBe("controller-self-update");
    expect(inserted?.payload?.artifactVersion).toBe("0.1.13-r20");
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
    // Scoped xray-core no longer forces the built-in updater: api.to_move()
    // would overwrite the /usr/sbin/vectra-xray-wrapper heap-cap shim.
    expect(inserted.payload?.strategy).toBe("managed-stack-package-first");
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

  describe("controller update rollout guard", () => {
    type ControllerCaller = {
      queueControllerUpdate: (input: {
        routerId: string;
        channel: "stable" | "beta";
        force?: boolean;
      }) => Promise<unknown>;
      queueBulkControllerUpdate: (input: {
        routerIds: string[];
        channel: "stable" | "beta";
        force?: boolean;
      }) => Promise<{
        results: Array<{ routerId: string; status: string; reason: string | null }>;
      }>;
    };

    // router, snapshot, artifacts, existing job, server_unreachable incidents
    function controllerUpdateMock(options: {
      router?: Parameters<typeof createCertifiedLikeRouter>[0];
      incidents?: unknown[];
      existingJob?: unknown;
    }) {
      return createMockDb([
        [createCertifiedLikeRouter(options.router)],
        [
          createPilotLayoutSnapshot("ubootmod", {
            "vectra-controller-agent": "0.1.13-r46",
            "luci-app-vectra-controller": "0.1.13-r46",
          }),
        ],
        [
          createControllerArtifact("vectra-controller-agent", "0.1.13-r47"),
          createControllerArtifact("luci-app-vectra-controller", "0.1.13-r47"),
        ],
        options.existingJob ? [options.existingJob] : [],
        options.incidents ?? [],
      ]);
    }

    function queuedSelfUpdateJob(state: string, command: string) {
      return {
        id: "job-1",
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        type: "run_terminal_command",
        state,
        dedupeKey: `update_controller:${CERTIFIED_LIKE_ROUTER_ID}:stable:0.1.13-r47`,
        payload: { purpose: "controller-self-update", command },
      };
    }

    it("a forced request upgrades a queued unforced job instead of returning it", async () => {
      const mock = controllerUpdateMock({
        existingJob: queuedSelfUpdateJob("queued", 'set -eu\nsh "$guard" prepare "$target_version"'),
      });
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      await caller.queueControllerUpdate({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        channel: "stable",
        force: true,
      });
      expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 1 });
      const [set] = mock.updatedValues() as Array<{ payload?: { command?: string } }>;
      expect(set?.payload?.command).toContain('VECTRA_GUARD_FORCE=1 sh "$guard" prepare');
    });

    it("leaves a job already delivered, or already forced, as it is", async () => {
      for (const job of [
        queuedSelfUpdateJob("delivered", 'sh "$guard" prepare'),
        queuedSelfUpdateJob("queued", '\nVECTRA_GUARD_FORCE=1 sh "$guard" prepare "$target_version"'),
      ]) {
        const mock = controllerUpdateMock({ existingJob: job });
        const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;
        await caller.queueControllerUpdate({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          channel: "stable",
          force: true,
        });
        expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 0 });
      }
    });

    it("reports the version a controller update would install now", async () => {
      const mock = createMockDb([
        [createControllerArtifact("vectra-controller-agent", "0.1.13-r47")],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as {
        controllerTargetVersion: (input: { channel: "stable" | "beta" }) => Promise<{ version: string | null }>;
      };
      await expect(caller.controllerTargetVersion({ channel: "stable" })).resolves.toEqual({
        version: "0.1.13-r47",
      });
    });

    it("refuses a router with an open server_unreachable incident", async () => {
      const mock = controllerUpdateMock({
        incidents: [
          {
            type: "server_unreachable",
            state: "open",
            openedAt: new Date(Date.now() - 2 * 24 * 3600 * 1000),
          },
        ],
      });
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      await expect(
        caller.queueControllerUpdate({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          channel: "stable",
        }),
      ).rejects.toThrow(/server_unreachable.*--force/);
      expect(mock.counts().insertCalls).toBe(0);
    });

    it("refuses a router whose last check-in is older than 10 minutes", async () => {
      const mock = controllerUpdateMock({
        router: { lastCheckInAt: new Date(Date.now() - 11 * 60 * 1000) },
      });
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      await expect(
        caller.queueControllerUpdate({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          channel: "stable",
        }),
      ).rejects.toThrow(/check-in 11 мин назад/);
      expect(mock.counts().insertCalls).toBe(0);
    });

    it("queues it anyway when the operator forces the update", async () => {
      const mock = controllerUpdateMock({
        router: { status: "rescue", lastCheckInAt: null },
        incidents: [
          { type: "server_unreachable", state: "open", openedAt: new Date() },
        ],
      });
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      await caller.queueControllerUpdate({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        channel: "stable",
        force: true,
      });
      expect(mock.counts().insertCalls).toBe(1);
      const [inserted] = mock.insertedValues() as Array<{
        payload?: { command?: string };
      }>;
      // ...and past the guard's "rolled back from this version" refusal (75).
      expect(inserted?.payload?.command).toContain(
        'VECTRA_GUARD_FORCE=1 sh "$guard" prepare',
      );
    });

    it("queues a healthy router", async () => {
      const mock = controllerUpdateMock({});
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      await caller.queueControllerUpdate({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        channel: "stable",
      });
      expect(mock.counts().insertCalls).toBe(1);
      const [inserted] = mock.insertedValues() as Array<{
        payload?: { command?: string };
      }>;
      expect(inserted?.payload?.command).not.toContain("VECTRA_GUARD_FORCE=1 sh");
    });

    it("bulk update skips and reports a shaky router instead of failing it", async () => {
      const mock = controllerUpdateMock({
        incidents: [
          {
            type: "server_unreachable",
            state: "resolved",
            openedAt: new Date(Date.now() - 3600 * 1000),
          },
        ],
      });
      const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

      const result = await caller.queueBulkControllerUpdate({
        routerIds: [CERTIFIED_LIKE_ROUTER_ID],
        channel: "stable",
      });
      expect(result.results).toEqual([
        expect.objectContaining({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          status: "skipped",
          reason: expect.stringContaining("последние 24 ч") as unknown,
        }),
      ]);
      expect(mock.counts().insertCalls).toBe(0);
    });
  });
});

describe("vendor-access notice at the operator's entry points", () => {
  beforeEach(() => {
    enqueueWebhook.mockClear();
  });

  const OTHER_ROUTER_ID = "c2a8d6e4-7b1f-4f3a-9d2e-5a6b7c8d9e0f";
  const partnerRouter = (overrides: Record<string, unknown> = {}) => ({
    ...createCertifiedLikeRouter(),
    ownerRef: "bc_1",
    partnerId: "bloopcat",
    ...overrides,
  });
  // What the partner would be told, one entry per notice queued.
  const visits = () =>
    enqueueWebhook.mock.calls.map(([, input]) => {
      const { routerId, partnerId, detail } = input as {
        routerId: string;
        partnerId: string;
        detail: Record<string, unknown>;
      };
      return { routerId, partnerId, ...detail };
    });
  const visit = (kind: string, routerId = CERTIFIED_LIKE_ROUTER_ID) => ({
    routerId,
    partnerId: "bloopcat",
    kind,
    by: "operator",
  });

  it("direct mode tells the router's partner", async () => {
    const mock = createMockDb([[partnerRouter()], [createPilotLayoutSnapshot()]]);
    const caller = createProtectedCaller(rescueRouter, mock.db) as {
      triggerDirectMode: (input: { routerId: string }) => Promise<unknown>;
    };

    await caller.triggerDirectMode({ routerId: CERTIFIED_LIKE_ROUTER_ID });

    expect(mock.counts().insertCalls).toBe(1);
    expect(visits()).toEqual([visit("direct_mode")]);
  });

  it("reconnect tells the router's partner", async () => {
    const mock = createMockDb([[partnerRouter()], [createPilotLayoutSnapshot()]]);
    const caller = createProtectedCaller(rescueRouter, mock.db) as {
      triggerReconnect: (input: { routerId: string }) => Promise<unknown>;
    };

    await caller.triggerReconnect({ routerId: CERTIFIED_LIKE_ROUTER_ID });

    expect(mock.counts().insertCalls).toBe(1);
    expect(visits()).toEqual([visit("reconnect")]);
  });

  it("a refused rescue action tells nobody", async () => {
    const mock = createMockDb([[partnerRouter()], [createBlockedSnapshot()]]);
    const caller = createProtectedCaller(rescueRouter, mock.db) as {
      triggerDirectMode: (input: { routerId: string }) => Promise<unknown>;
    };

    await expect(
      caller.triggerDirectMode({ routerId: CERTIFIED_LIKE_ROUTER_ID }),
    ).rejects.toMatchObject({ code: "FORBIDDEN" });
    expect(visits()).toEqual([]);
  });

  it("a single reboot tells the router's partner", async () => {
    const mock = createMockDb([
      [partnerRouter()],
      [createPilotLayoutSnapshot("ubootmod")],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueRouterReboot: (input: { routerId: string }) => Promise<unknown>;
    };

    await caller.queueRouterReboot({ routerId: CERTIFIED_LIKE_ROUTER_ID });

    expect(mock.counts().insertCalls).toBe(1);
    expect(visits()).toEqual([visit("reboot")]);
  });

  it("a bulk reboot of a mixed batch tells only the BloopCat router's partner", async () => {
    const mock = createMockDb([
      // Vectra's own router: queued, but nobody to tell.
      [createCertifiedLikeRouter()],
      [createPilotLayoutSnapshot("ubootmod")],
      [],
      // A BloopCat customer's router.
      [partnerRouter({ id: OTHER_ROUTER_ID })],
      [createPilotLayoutSnapshot("ubootmod")],
      [],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueBulkRouterReboot: (input: {
        routerIds: string[];
      }) => Promise<{ results: Array<{ status: string }> }>;
    };

    const { results } = await caller.queueBulkRouterReboot({
      routerIds: [CERTIFIED_LIKE_ROUTER_ID, OTHER_ROUTER_ID],
    });

    expect(results.map((result) => result.status)).toEqual(["queued", "queued"]);
    expect(mock.counts().insertCalls).toBe(2);
    expect(visits()).toEqual([visit("reboot", OTHER_ROUTER_ID)]);
  });

  it("a reboot already waiting is not a new visit", async () => {
    const waiting = { id: "job-0", type: "run_terminal_command", state: "queued" };
    const mock = createMockDb([
      [partnerRouter()],
      [createPilotLayoutSnapshot("ubootmod")],
      [waiting],
    ]);
    const caller = createProtectedCaller(updateRouter, mock.db) as {
      queueRouterReboot: (input: { routerId: string }) => Promise<unknown>;
    };

    await caller.queueRouterReboot({ routerId: CERTIFIED_LIKE_ROUTER_ID });

    expect(mock.counts().insertCalls).toBe(0);
    expect(visits()).toEqual([]);
  });

  describe("config_apply", () => {
    it("a PassWall apply queued from the draft screen", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot()],
        [createDesiredRevision()],
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

      expect(visits()).toEqual([visit("config_apply")]);
    });

    it("an apply that is already queued is not a new visit", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot()],
        [createDesiredRevision()],
        [{ id: "job-0", type: "apply_passwall_config", state: "queued" }],
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

      expect(mock.counts().insertCalls).toBe(0);
      expect(visits()).toEqual([]);
    });
  });

  describe("software_update", () => {
    it("a controller update", async () => {
      const mock = createMockDb([
        [partnerRouter()],
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
      expect(visits()).toEqual([visit("software_update")]);
    });

    it("a PassWall package update", async () => {
      const mock = createMockDb([
        [partnerRouter()],
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

      expect(visits()).toEqual([visit("software_update")]);
    });

    it("a bulk xray update", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot()],
        [
          createPasswallBundleArtifact(),
          createPasswallPackageArtifact("xray-core", "26.3.27-r1"),
        ],
        [],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as {
        queueBulkXrayUpdate: (input: { routerIds: string[] }) => Promise<unknown>;
      };

      await caller.queueBulkXrayUpdate({ routerIds: [CERTIFIED_LIKE_ROUTER_ID] });

      expect(visits()).toEqual([visit("software_update")]);
    });

    it("Vectra's own router is updated silently", async () => {
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
      expect(visits()).toEqual([]);
    });

    it("a refused update tells nobody", async () => {
      const mock = createMockDb([[partnerRouter()], [createBlockedSnapshot()]]);
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
      ).rejects.toMatchObject({ code: "FORBIDDEN" });
      expect(visits()).toEqual([]);
    });

    // The rollout guard (#73-#75): a shaky control plane refuses unless the
    // operator forces it; a forced request upgrades a still-queued job.
    describe("at the controller rollout guard", () => {
      type ControllerCaller = {
        queueControllerUpdate: (input: {
          routerId: string;
          channel: "stable" | "beta";
          force?: boolean;
        }) => Promise<unknown>;
        queueBulkControllerUpdate: (input: {
          routerIds: string[];
          channel: "stable" | "beta";
          force?: boolean;
        }) => Promise<{ results: Array<{ status: string }> }>;
      };
      // router, snapshot, artifacts, existing job, server_unreachable incidents
      function guardedUpdateMock(options: { shaky?: boolean; existingJob?: unknown }) {
        return createMockDb([
          [partnerRouter()],
          [
            createPilotLayoutSnapshot("ubootmod", {
              "vectra-controller-agent": "0.1.13-r46",
              "luci-app-vectra-controller": "0.1.13-r46",
            }),
          ],
          [
            createControllerArtifact("vectra-controller-agent", "0.1.13-r47"),
            createControllerArtifact("luci-app-vectra-controller", "0.1.13-r47"),
          ],
          options.existingJob ? [options.existingJob] : [],
          options.shaky
            ? [{ type: "server_unreachable", state: "open", openedAt: new Date() }]
            : [],
        ]);
      }

      it("an update the guard refuses tells nobody", async () => {
        const mock = guardedUpdateMock({ shaky: true });
        const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

        await expect(
          caller.queueControllerUpdate({
            routerId: CERTIFIED_LIKE_ROUTER_ID,
            channel: "stable",
          }),
        ).rejects.toThrow(/server_unreachable/);
        expect(mock.counts().insertCalls).toBe(0);
        expect(visits()).toEqual([]);
      });

      it("a bulk update that skips the router tells nobody", async () => {
        const mock = guardedUpdateMock({ shaky: true });
        const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

        const { results } = await caller.queueBulkControllerUpdate({
          routerIds: [CERTIFIED_LIKE_ROUTER_ID],
          channel: "stable",
        });
        expect(results.map((result) => result.status)).toEqual(["skipped"]);
        expect(visits()).toEqual([]);
      });

      it("an update the operator forces past the guard tells the router's partner", async () => {
        const mock = guardedUpdateMock({ shaky: true });
        const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

        await caller.queueControllerUpdate({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          channel: "stable",
          force: true,
        });
        expect(mock.counts().insertCalls).toBe(1);
        expect(visits()).toEqual([visit("software_update")]);
      });

      it("forcing an update that is already waiting is not a new visit", async () => {
        // Announced when it was queued; forcing it changes how it runs, not
        // whether Vectra reaches the router.
        const mock = guardedUpdateMock({
          existingJob: {
            id: "job-1",
            routerId: CERTIFIED_LIKE_ROUTER_ID,
            type: "run_terminal_command",
            state: "queued",
            dedupeKey: `update_controller:${CERTIFIED_LIKE_ROUTER_ID}:stable:0.1.13-r47`,
            payload: {
              purpose: "controller-self-update",
              command: 'set -eu\nsh "$guard" prepare "$target_version"',
            },
          },
        });
        const caller = createProtectedCaller(updateRouter, mock.db) as ControllerCaller;

        await caller.queueControllerUpdate({
          routerId: CERTIFIED_LIKE_ROUTER_ID,
          channel: "stable",
          force: true,
        });
        expect(mock.counts()).toEqual({ insertCalls: 0, updateCalls: 1 });
        expect(visits()).toEqual([]);
      });
    });
  });

  describe("maintenance", () => {
    it.each([
      ["clearing the ipsets", "queuePasswallClearIpsets"],
      ["a subscriptions refresh", "queueSubscriptionsRefresh"],
      ["a rules refresh", "queueRulesRefresh"],
    ])("%s", async (_label, procedure) => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot("ubootmod")],
        [],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as Record<
        string,
        (input: { routerId: string }) => Promise<unknown>
      >;

      await caller[procedure]!({ routerId: CERTIFIED_LIKE_ROUTER_ID });

      expect(mock.counts().insertCalls).toBe(1);
      expect(visits()).toEqual([visit("maintenance")]);
    });

    it("an xray runtime repair", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [
          createPilotLayoutSnapshot("ubootmod", {
            "vectra-controller-agent": "0.1.13-r45",
          }),
        ],
        [],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as {
        queueXrayRuntimeRepair: (input: { routerId: string }) => Promise<unknown>;
      };

      await caller.queueXrayRuntimeRepair({ routerId: CERTIFIED_LIKE_ROUTER_ID });

      expect(mock.counts().insertCalls).toBe(1);
      expect(visits()).toEqual([visit("maintenance")]);
    });

    it("a firmware validation", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot("ubootmod")],
        [
          {
            id: "5d2f4e6a-8b1c-4d3e-9f5a-7b9c1d3e5f70",
            artifactId: "artifact-fw",
            channel: "stable",
            version: "24.10.6",
            validationCommand: "sysupgrade -T /tmp/firmware.bin",
          },
        ],
        [
          {
            id: "artifact-fw",
            downloadUrl: "https://example.test/firmware.bin",
            checksumSha256: "sha-fw",
            signatureUrl: null,
            version: "24.10.6",
          },
        ],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as {
        queueFirmwareValidation: (input: {
          routerId: string;
          manifestId: string;
        }) => Promise<unknown>;
      };

      await caller.queueFirmwareValidation({
        routerId: CERTIFIED_LIKE_ROUTER_ID,
        manifestId: "5d2f4e6a-8b1c-4d3e-9f5a-7b9c1d3e5f70",
      });

      expect(mock.counts().insertCalls).toBe(1);
      expect(visits()).toEqual([visit("maintenance")]);
    });
  });

  describe("diagnostics", () => {
    it("a subscriptions inspection", async () => {
      const mock = createMockDb([
        [partnerRouter()],
        [createPilotLayoutSnapshot("ubootmod")],
        [],
      ]);
      const caller = createProtectedCaller(updateRouter, mock.db) as {
        queueSubscriptionsInspect: (input: { routerId: string }) => Promise<unknown>;
      };

      await caller.queueSubscriptionsInspect({ routerId: CERTIFIED_LIKE_ROUTER_ID });

      expect(mock.counts().insertCalls).toBe(1);
      expect(visits()).toEqual([visit("diagnostics")]);
    });
  });
});
