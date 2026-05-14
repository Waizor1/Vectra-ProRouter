import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  createSubscriptionSecretCiphertext,
  hashOnboardingSecret,
  planNextOnboardingAction,
  sanitizeOnboardingProfileForClient,
  type RouterOnboardingContext,
} from "./router-auto-onboarding";

const now = new Date("2026-05-14T12:00:00.000Z");
const recent = new Date("2026-05-14T11:59:10.000Z");
const stale = new Date("2026-05-14T11:30:00.000Z");

type RouterCtx = NonNullable<RouterOnboardingContext["router"]>;
type ProfileCtx = NonNullable<RouterOnboardingContext["profile"]>;
type RunCtx = NonNullable<RouterOnboardingContext["run"]>;

function router(overrides: Partial<RouterCtx> = {}) {
  return {
    id: "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9",
    deviceIdentifier: "router-auto-onboarding-1",
    displayName: null,
    hostname: "openwrt",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    pendingImportRevisionId: null,
    activeRevisionId: "revision-active",
    approvedAt: new Date("2026-05-14T10:00:00.000Z"),
    lastSeenAt: recent,
    ...overrides,
  } satisfies RouterCtx;
}

function profile(overrides: Partial<ProfileCtx> = {}) {
  return {
    id: "profile-1",
    routerId: "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9",
    enabled: true,
    targetHostname: "yuranrod-msk",
    displayName: "YuranRod-msk",
    subscriptionSecretCiphertext: null,
    subscriptionUrlHash: null,
    subscriptionRemark: null,
    baseline: "standard-non-hh",
    runtimePolicy: "auto-minimal-passwall-xray",
    verifyPolicy: "route-smoke",
    notes: null,
    ...overrides,
  } satisfies ProfileCtx;
}

function run(overrides: Partial<RunCtx> = {}) {
  return {
    id: "run-1",
    routerId: "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9",
    profileId: "profile-1",
    state: "preflight",
    status: "running",
    attempt: 0,
    lastJobId: null,
    activeRevisionId: null,
    lastError: null,
    nextRunAfter: null,
    completedAt: null,
    ...overrides,
  } satisfies RunCtx;
}

function snapshot(
  overrides: Record<string, unknown> = {},
): NonNullable<RouterOnboardingContext["latestSnapshot"]> {
  return {
    createdAt: recent,
    payload: {
      protocolVersion: "2026-04-v1",
      deviceIdentifier: "router-auto-onboarding-1",
      devicePublicKey: "public-key",
      controllerVersion: "0.1.13-r21",
      hostname: "openwrt",
      model: "Xiaomi AX3000T",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      passwallEnabled: true,
      selectedNodeId: "myshunt",
      nodeCount: 8,
      subscriptionCount: 1,
      packageVersions: {},
      binaryVersions: { xray: "25.4.30" },
      rulesAssets: {},
      resources: {
        memoryTotalMb: 256,
        memoryAvailableMb: 96,
        swapTotalMb: 0,
        swapFreeMb: 0,
        overlayFreeMb: 32,
        tmpFreeMb: 64,
      },
      serviceHealth: {
        controller: "running",
        passwall: "running",
        passwallServer: "running",
        dnsmasq: "running",
      },
      safetyEvents: [],
      ...overrides,
    },
  } as NonNullable<RouterOnboardingContext["latestSnapshot"]>;
}

function context(
  overrides: Partial<RouterOnboardingContext> = {},
): RouterOnboardingContext {
  return {
    featureEnabled: true,
    router: router(),
    profile: profile(),
    run: run(),
    latestSnapshot: snapshot(),
    activeJobs: [],
    openIncidents: [],
    lastJob: null,
    lastJobResult: null,
    activeConfig: null,
    now,
    ...overrides,
  };
}

describe("router auto-onboarding planner", () => {
  it("does nothing while the feature flag is off", () => {
    expect(
      planNextOnboardingAction(context({ featureEnabled: false })),
    ).toEqual({ action: "skip", reason: "feature flag disabled" });
  });

  it("waits for offline routers without queueing work", () => {
    const plan = planNextOnboardingAction(
      context({ router: router({ lastSeenAt: stale }) }),
    );

    expect(plan).toMatchObject({
      action: "wait",
      nextState: "preflight",
      reason: "router is offline or has no recent check-in",
    });
  });

  it("blocks unsupported boards before destructive actions", () => {
    const plan = planNextOnboardingAction(
      context({
        router: router({
          boardName: "unsupported,router",
          target: "ath79/generic",
          architecture: "mips_24kc",
        }),
        latestSnapshot: snapshot({
          boardName: "unsupported,router",
          target: "ath79/generic",
          architecture: "mips_24kc",
        }),
      }),
    );

    expect(plan.action).toBe("block");
    expect(plan.reason).toContain("unsupported router");
  });

  it("auto-approves the first import when the profile owns onboarding", () => {
    expect(
      planNextOnboardingAction(
        context({
          router: router({
            importState: "import_review",
            pendingImportRevisionId: "revision-imported",
            activeRevisionId: null,
            approvedAt: null,
            status: "pending",
          }),
        }),
      ),
    ).toMatchObject({
      action: "approve_import",
      revisionId: "revision-imported",
      nextState: "rename_router",
    });
  });

  it("waits for unrelated active jobs instead of stacking onboarding work", () => {
    expect(
      planNextOnboardingAction(
        context({
          activeJobs: [
            {
              id: "job-update-1",
              type: "update_controller",
              state: "queued",
              dedupeKey: "update_controller:router",
              payload: {},
              desiredRevisionId: null,
            },
          ],
        }),
      ),
    ).toMatchObject({
      action: "wait",
      reason: "waiting for unrelated active job update_controller",
      lastJobId: "job-update-1",
    });
  });

  it("queues typed runtime repair for missing compact geodata and dnsmasq-full", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({ state: "ensure_runtime" }),
          latestSnapshot: snapshot({
            packageVersions: {
              "luci-app-passwall2": "26.5.1-r1",
              "xray-core": "26.4.25-r1",
            },
            binaryVersions: { xray: "26.4.25" },
            rulesAssets: {},
          }),
        }),
      ),
    ).toMatchObject({
      action: "queue_ensure_runtime",
      actions: ["compact_geodata", "dnsmasq_full"],
      reason: "queueing typed runtime repair: compact_geodata, dnsmasq_full",
    });
  });

  it("refuses blind full-stack installs when core PassWall/Xray runtime is absent", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({ state: "ensure_runtime" }),
          latestSnapshot: snapshot({
            packageVersions: {},
            binaryVersions: {},
            serviceHealth: {
              controller: "running",
              passwall: "unknown",
              passwallServer: "unknown",
              dnsmasq: "running",
            },
          }),
        }),
      ),
    ).toMatchObject({
      action: "block",
      nextState: "ensure_runtime",
      reason:
        "core PassWall/Xray runtime is absent; refusing blind full-stack install during onboarding",
    });
  });

  it("moves to subscription after green typed runtime repair", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({
            state: "ensure_runtime",
            lastJobId: "job-runtime-1",
          }),
          lastJob: {
            id: "job-runtime-1",
            type: "ensure_passwall_runtime",
            state: "succeeded",
            dedupeKey:
              "onboarding:run-1:ensure_passwall_runtime:compact_geodata+dnsmasq_full",
            payload: {},
            desiredRevisionId: null,
          },
          lastJobResult: {
            status: "success",
            reportedAt: recent,
            payload: {
              ok: true,
              actions: [
                { action: "compact_geodata", status: "success" },
                { action: "dnsmasq_full", status: "success" },
              ],
              services: {
                controller: "running",
                passwall: "running",
                passwallServer: "running",
                dnsmasq: "running",
              },
              resources: {
                memoryAvailableMb: 96,
                overlayFreeMb: 32,
                tmpFreeMb: 64,
              },
            },
          },
        }),
      ),
    ).toMatchObject({
      action: "transition",
      nextState: "apply_subscription",
      reason: "typed runtime repair succeeded",
    });
  });

  it("blocks after typed runtime repair when post-repair services are not green", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({
            state: "ensure_runtime",
            lastJobId: "job-runtime-1",
          }),
          lastJob: {
            id: "job-runtime-1",
            type: "ensure_passwall_runtime",
            state: "succeeded",
            dedupeKey:
              "onboarding:run-1:ensure_passwall_runtime:compact_geodata+dnsmasq_full",
            payload: {},
            desiredRevisionId: null,
          },
          lastJobResult: {
            status: "success",
            reportedAt: recent,
            payload: {
              ok: true,
              actions: [{ action: "dnsmasq_full", status: "success" }],
              services: {
                controller: "running",
                passwall: "running",
                passwallServer: "running",
                dnsmasq: "stopped",
              },
              resources: {
                memoryAvailableMb: 96,
                overlayFreeMb: 32,
                tmpFreeMb: 64,
              },
            },
          },
        }),
      ),
    ).toMatchObject({
      action: "block",
      nextState: "ensure_runtime",
      reason: "typed runtime repair finished without green proof",
    });
  });

  it("queues typed route-smoke verification before route-smoke completion", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({ state: "verify_runtime" }),
          activeConfig: standardRouteConfig(),
        }),
      ),
    ).toMatchObject({
      action: "queue_route_verification",
      reason: "queueing typed PassWall route-smoke verifier",
    });
  });

  it("moves to final import after green typed route verification", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({
            state: "verify_runtime",
            lastJobId: "job-verify-1",
          }),
          activeConfig: standardRouteConfig(),
          lastJob: {
            id: "job-verify-1",
            type: "verify_passwall_routes",
            state: "succeeded",
            dedupeKey: "onboarding:run-1:verify_passwall_routes",
            payload: {},
            desiredRevisionId: null,
          },
          lastJobResult: {
            status: "success",
            reportedAt: recent,
            payload: {
              ok: true,
              slots: [
                routeSlot("WorldProxy"),
                routeSlot("YouTube"),
                routeSlot("Special"),
                routeSlot("Tiktok"),
                routeSlot("DiscordVoiceUdp"),
              ],
            },
          },
        }),
      ),
    ).toMatchObject({
      action: "request_import",
      nextState: "final_reimport",
      reason: "typed route verifier returned green route-smoke proof",
    });
  });

  it("does not accept route verifier payloads from failed job results", () => {
    expect(
      planNextOnboardingAction(
        context({
          run: run({
            state: "verify_runtime",
            lastJobId: "job-verify-1",
          }),
          activeConfig: standardRouteConfig(),
          lastJob: {
            id: "job-verify-1",
            type: "verify_passwall_routes",
            state: "succeeded",
            dedupeKey: "onboarding:run-1:verify_passwall_routes",
            payload: {},
            desiredRevisionId: null,
          },
          lastJobResult: {
            status: "failure",
            reportedAt: recent,
            payload: {
              ok: true,
              slots: [
                routeSlot("WorldProxy"),
                routeSlot("YouTube"),
                routeSlot("Special"),
                routeSlot("Tiktok"),
                routeSlot("DiscordVoiceUdp"),
              ],
            },
          },
        }),
      ),
    ).toMatchObject({
      action: "block",
      nextState: "verify_runtime",
      reason: "typed route verifier finished without green route-smoke proof",
    });
  });
});

describe("router auto-onboarding secret hygiene", () => {
  it("returns profile metadata without raw subscription URLs", () => {
    const subscriptionUrl = "https://sub.example.invalid/api/sub/super-secret";
    const sanitized = sanitizeOnboardingProfileForClient({
      ...profile({
        subscriptionSecretCiphertext:
          createSubscriptionSecretCiphertext(subscriptionUrl),
        subscriptionUrlHash: hashOnboardingSecret(subscriptionUrl),
      }),
      createdAt: now,
      updatedAt: now,
    } as never);

    const serialized = JSON.stringify(sanitized);
    expect(serialized).not.toContain(subscriptionUrl);
    expect(serialized).not.toContain("super-secret");
    expect(sanitized).toMatchObject({
      hasSubscription: true,
      subscriptionUrlHash: hashOnboardingSecret(subscriptionUrl),
    });
  });
});

function routeSlot(slotId: string) {
  return {
    slotId,
    bindingOk: true,
    ruleExtrasOk: true,
    nodeExtrasOk: true,
    smokeOk: true,
    statusCode: 204,
  };
}

function standardRouteConfig(): NonNullable<
  RouterOnboardingContext["activeConfig"]
> {
  return passwallDesiredConfigSchema.parse({
    schemaVersion: 1,
    basicSettings: {
      main: {
        mainSwitch: true,
        selectedNodeId: "myshunt",
        localhostProxy: true,
        clientProxy: true,
        nodeSocksPort: 1070,
        nodeSocksBindLocal: true,
        socksMainSwitch: false,
      },
      dns: {
        directQueryStrategy: "UseIP",
        remoteDnsProtocol: "doh",
        remoteDns: "8.8.8.8",
        remoteDnsDoh: "https://dns.google/dns-query",
        remoteDnsDetour: "direct",
        remoteFakeDns: false,
        remoteDnsQueryStrategy: "UseIPv4",
        dnsHosts: [],
        dnsRedirect: true,
      },
      log: { enableNodeLog: true, level: "error" },
      maintenance: { backupPaths: [] },
      socks: [],
      shuntRules: [
        { id: "WorldProxy", label: "WorldProxy", outboundNodeId: "node-world" },
        { id: "YouTube", label: "YouTube", outboundNodeId: "node-youtube" },
        { id: "Special", label: "Special", outboundNodeId: "node-special" },
        { id: "Tiktok", label: "Tiktok", outboundNodeId: "node-tiktok" },
        {
          id: "DiscordVoiceUdp",
          label: "DiscordVoiceUdp",
          outboundNodeId: "node-discord",
          extras: {
            network: "udp",
            port: "19294-19344,50000-50100",
          },
        },
      ],
    },
    nodes: [
      {
        id: "myshunt",
        label: "Маршрутизатор BloopCat",
        protocol: "shunt",
        enabled: true,
        extras: {
          WorldProxy: "node-world",
          YouTube: "node-youtube",
          Special: "node-special",
          Tiktok: "node-tiktok",
          DiscordVoiceUdp: "node-discord",
        },
      },
      routeNode(
        "node-world",
        "🇩🇪 Germany YouTube RU entry",
        "ru1.example.net",
        50052,
      ),
      routeNode(
        "node-youtube",
        "🇷🇺 Russia YouTube RU entry",
        "ru1.example.net",
        50051,
      ),
      routeNode("node-special", "🇳🇱 Netherlands", "nl1.example.net", 443),
      routeNode("node-tiktok", "🇧🇾 Belarus", "by1.example.net", 443),
      {
        ...routeNode(
          "node-discord",
          "🇵🇱 Poland YouTube RU entry",
          "ru2.example.net",
          50053,
        ),
        extras: {
          mux: "1",
          mux_concurrency: "-1",
          xudp_concurrency: "16",
        },
      },
    ],
    subscriptions: {
      filterKeywordMode: "0",
      discardList: [],
      keepList: [],
      typePreferences: {},
      domainStrategy: "auto",
      items: [],
    },
    appUpdate: {
      binaryPaths: {
        xray: "/usr/bin/xray",
        singBox: "/usr/bin/sing-box",
        hysteria: "/usr/bin/hysteria",
        geoview: "/usr/bin/geoview",
      },
      updateStrategy: "package-preferred",
      targetVersions: {},
    },
    ruleManage: {
      geoipUrl:
        "https://github.com/Loyalsoldier/geoip/releases/latest/download/geoip.dat",
      geositeUrl:
        "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
      assetDirectory: "/usr/share/v2ray/",
      autoUpdate: false,
      enabledAssets: ["geoip", "geosite"],
      shuntRules: [],
    },
  });
}

function routeNode(id: string, label: string, address: string, port: number) {
  return {
    id,
    label,
    protocol: "vless",
    enabled: true,
    group: "default",
    address,
    port,
    transport: "grpc",
  };
}
