import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import { buildEditorSurface } from "./editor";

const baseConfig = passwallDesiredConfigSchema.parse({
  basicSettings: {
    main: {
      mainSwitch: true,
      selectedNodeId: "shunt-main",
      localhostProxy: true,
      clientProxy: true,
      nodeSocksPort: 1070,
      nodeSocksBindLocal: true,
      socksMainSwitch: false,
      extras: {},
    },
    dns: {
      directQueryStrategy: "UseIP",
      remoteDnsProtocol: "tcp",
      remoteDns: "1.1.1.1",
      remoteDnsDoh: "https://1.1.1.1/dns-query",
      remoteDnsDetour: "remote",
      remoteFakeDns: false,
      remoteDnsQueryStrategy: "UseIPv4",
      dnsHosts: [],
      dnsRedirect: true,
      extras: {},
    },
    log: {
      enableNodeLog: true,
      level: "error",
      extras: {},
    },
    maintenance: {
      backupPaths: ["/etc/config/passwall2"],
      extras: {},
    },
    socks: [],
    shuntRules: [
      {
        id: "direct",
        label: "direct",
        outboundNodeId: "_direct",
        domainRules: ["domain:example.com"],
        ipRules: ["geoip:private"],
        extras: {
          network: "tcp,udp",
        },
      },
    ],
  },
  nodes: [
    {
      id: "shunt-main",
      label: "Shunt",
      protocol: "shunt",
      enabled: true,
      group: "default",
      tags: [],
      extras: {},
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
    extras: {},
  },
  ruleManage: {
    geoipUrl: "https://example.com/geoip.dat",
    geositeUrl: "https://example.com/geosite.dat",
    assetDirectory: "/usr/share/v2ray/",
    autoUpdate: true,
    scheduleMode: "daily",
    enabledAssets: ["geoip", "geosite"],
    shuntRules: [
      {
        id: "direct",
        label: "direct",
        outboundNodeId: "_direct",
        domainRules: ["domain:example.com"],
        ipRules: ["geoip:private"],
        extras: {
          network: "tcp,udp",
        },
      },
    ],
    extras: {},
  },
});

describe("editor surface", () => {
  it("tracks PassWall Rule Manage shunt match extras", () => {
    const draftConfig = structuredClone(baseConfig);
    const rule = draftConfig.ruleManage.shuntRules[0];
    if (!rule) {
      throw new Error("missing shunt rule fixture");
    }
    rule.extras.protocol = "http tls";
    rule.extras.inbound = "tproxy";
    rule.extras.network = "tcp";
    rule.extras.source = "geoip:private";
    rule.extras.port = "443";
    rule.extras.invert = "1";
    draftConfig.basicSettings.shuntRules = structuredClone(
      draftConfig.ruleManage.shuntRules,
    );

    const surface = buildEditorSurface({
      routerRuntimeSummary: {
        status: "active",
        importState: "approved",
        lastSeenAt: null,
        passwallEnabled: true,
        selectedNodeId: "shunt-main",
        selectedNodeLabel: "Shunt",
        pendingChanges: 0,
        supportState: "certified",
        supportTitle: "Сертифицировано",
        supportReason: "test",
      },
      currentLiveConfig: baseConfig,
      authoritativeConfig: baseConfig,
      draftConfig,
      currentConfigFreshness: "live",
      configSourceMode: "live-import",
    });

    expect(surface.fieldDiffs.map((diff) => diff.path)).toEqual(
      expect.arrayContaining([
        "Управление правилами[direct].extras.protocol",
        "Управление правилами[direct].extras.inbound",
        "Управление правилами[direct].extras.network",
        "Управление правилами[direct].extras.source",
        "Управление правилами[direct].extras.port",
        "Управление правилами[direct].extras.invert",
      ]),
    );
    expect(surface.operationPreview.changedSections).toContain("ruleManage");
    expect(
      surface.operationPreview.operations.some(
        (operation) => operation.key === "Управление правилами",
      ),
    ).toBe(true);
  });

  it("tracks node subscription and global PassWall extras in the editor diff", () => {
    const draftConfig = structuredClone(baseConfig);
    draftConfig.nodes[0]!.extras.mux = "1";
    draftConfig.nodes[0]!.extras.xudp_concurrency = "16";
    draftConfig.subscriptions.items.push({
      id: "sub-main",
      remark: "Main sub",
      url: "https://example.com/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {
        auto_update: "1",
      },
    });
    draftConfig.ruleManage.extras.week_update = "7";
    draftConfig.appUpdate.extras.custom_binary_flag = "1";

    const surface = buildEditorSurface({
      routerRuntimeSummary: {
        status: "active",
        importState: "approved",
        lastSeenAt: null,
        passwallEnabled: true,
        selectedNodeId: "shunt-main",
        selectedNodeLabel: "Shunt",
        pendingChanges: 0,
        supportState: "certified",
        supportTitle: "Сертифицировано",
        supportReason: "test",
      },
      currentLiveConfig: baseConfig,
      authoritativeConfig: baseConfig,
      draftConfig,
      currentConfigFreshness: "live",
      configSourceMode: "live-import",
    });

    const paths = surface.fieldDiffs.map((diff) => diff.path);
    expect(paths.some((path) => /^Ноды\[.+\]\.extras\.mux$/.test(path))).toBe(
      true,
    );
    expect(
      paths.some((path) => /^Ноды\[.+\]\.extras\.xudp_concurrency$/.test(path)),
    ).toBe(true);
    expect(
      paths.some((path) => /^Подписки\[.+\]\.extras\.auto_update$/.test(path)),
    ).toBe(true);
    expect(paths).toEqual(
      expect.arrayContaining([
        "ruleManage.extras.week_update",
        "appUpdate.extras.custom_binary_flag",
      ]),
    );
  });

  it("marks deep config as stale-authoritative when live import was not confirmed", () => {
    const surface = buildEditorSurface({
      routerRuntimeSummary: {
        status: "active",
        importState: "approved",
        lastSeenAt: null,
        passwallEnabled: true,
        selectedNodeId: "shunt-main",
        selectedNodeLabel: "Shunt",
        pendingChanges: 0,
        supportState: "certified",
        supportTitle: "Сертифицировано",
        supportReason: "test",
      },
      currentLiveConfig: baseConfig,
      authoritativeConfig: baseConfig,
      draftConfig: baseConfig,
      currentConfigFreshness: "live",
      configSourceMode: "stale-authoritative",
    });

    expect(
      surface.fieldDiffs.find(
        (diff) => diff.path === "basicSettings.main.selectedNodeId",
      )?.source,
    ).toBe("stale-authoritative");
  });

  it("builds apply preview from fresh live config instead of stale authoritative config", () => {
    const staleAuthoritativeConfig = structuredClone(baseConfig);
    staleAuthoritativeConfig.nodes[0]!.extras.default_node = "old-ru-node";

    const surface = buildEditorSurface({
      routerRuntimeSummary: {
        status: "active",
        importState: "approved",
        lastSeenAt: new Date("2026-04-29T00:00:00Z"),
        passwallEnabled: true,
        selectedNodeId: "shunt-main",
        selectedNodeLabel: "Shunt",
        pendingChanges: 0,
        supportState: "certified",
        supportTitle: "Сертифицировано",
        supportReason: "test",
      },
      currentLiveConfig: baseConfig,
      authoritativeConfig: staleAuthoritativeConfig,
      draftConfig: baseConfig,
      currentConfigFreshness: "live",
      configSourceMode: "live-import",
    });

    expect(surface.operationPreview.changedSections).toEqual([]);
    expect(surface.operationPreview.refreshSubscriptions).toBe(false);
    expect(surface.operationPreview.refreshRules).toBe(false);
    expect(surface.operationPreview.operations).toEqual([]);
  });
});
