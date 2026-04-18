import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  buildFleetStats,
  buildImpactLines,
  buildRouterSummary,
  describeVersionDrift,
  formatRouterStatus,
} from "./presentation";

describe("buildFleetStats", () => {
  it("summarizes fleet counters into stat cards", () => {
    const stats = buildFleetStats({
      totalRouters: 5,
      byStatus: {
        pending: 1,
        active: 3,
        offline: 0,
        direct: 1,
        rescue: 0,
        disabled: 0,
      },
      openIncidents: 2,
      queuedJobs: 4,
    });

    expect(stats).toEqual([
      { label: "Всего роутеров", value: "5" },
      { label: "Активны сейчас", value: "4", tone: "good" },
      { label: "Открытые инциденты", value: "2", tone: "warning" },
      { label: "Задания в очереди", value: "4", tone: "warning" },
    ]);
  });
});

describe("buildRouterSummary", () => {
  it("prefers display name, snapshot versions, and incident reason", () => {
    const summary = buildRouterSummary({
      id: "router-1",
      deviceIdentifier: "device-1",
      displayName: "Branch AX3000T",
      hostname: "ax3000t",
      status: "direct",
      importState: "import_review",
      controllerChannel: "stable",
      rolloutGroupId: null,
      pendingImportRevisionId: "revision-pending",
      activeRevisionId: null,
      lastAppliedRevisionId: null,
      lastConfigDigest: null,
      approvedAt: null,
      lastSeenAt: new Date(Date.now() - 2 * 60 * 1000),
      lastCheckInAt: null,
      lastDirectModeAt: null,
      lastRescueReason: null,
      model: "AX3000T",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.4",
      panelDomain: "https://panel.example.com",
      createdAt: new Date(),
      updatedAt: new Date(),
      latestSnapshot: {
        id: "snapshot-1",
        routerId: "router-1",
        source: "check_in",
        passwallEnabled: true,
        selectedNodeId: "node-us-1",
        nodeCount: 12,
        subscriptionCount: 2,
        controllerVersion: "0.1.0",
        passwallAppVersion: "26.4.2",
        createdAt: new Date(),
        payload: {
          protocolVersion: "2026-04-v1",
          deviceIdentifier: "device-1",
          devicePublicKey: "pub",
          controllerVersion: "0.1.0",
          hostname: "ax3000t",
          model: "AX3000T",
          boardName: "xiaomi,mi-router-ax3000t",
          target: "mediatek/filogic",
          architecture: "aarch64_cortex-a53",
          openwrtRelease: "24.10.4",
          passwallEnabled: true,
          selectedNodeId: "node-us-1",
          nodeCount: 12,
          subscriptionCount: 2,
          packageVersions: {
            "luci-app-passwall2": "26.4.2",
            "xray-core": "25.12.1-r1",
          },
          binaryVersions: {
            xray: "26.2.6",
          },
          rulesAssets: {},
          resources: {
            memoryTotalMb: 0,
            memoryAvailableMb: 0,
            swapTotalMb: 0,
            swapFreeMb: 0,
            overlayFreeMb: 0,
            tmpFreeMb: 0,
          },
          serviceHealth: {
            controller: "running",
            passwall: "running",
            passwallServer: "unknown",
            dnsmasq: "running",
          },
        },
      },
      latestDesiredRevision: null,
      openIncident: {
        id: "incident-1",
        routerId: "router-1",
        type: "entered_direct_mode",
        state: "open",
        reason: "Proxy health failed repeatedly.",
        metadata: {},
        openedAt: new Date(),
        resolvedAt: null,
      },
      queuedJobCount: 2,
      support: {
        state: "certified",
        title: "Сертифицировано",
        reason: "AX3000T stock-layout validated.",
      },
    } as Parameters<typeof buildRouterSummary>[0]);

    expect(summary.name).toBe("Branch AX3000T");
    expect(summary.directMode).toBe(true);
    expect(summary.offline).toBe(false);
    expect(summary.statusLabel).toBe("Прямой режим");
    expect(summary.components).toEqual({ xray: "26.2.6" });
    expect(summary.pendingChanges).toBe(2);
    expect(summary.lastRescue).toBe("Proxy health failed repeatedly.");
  });

  it("treats stale direct state as offline instead of active rescue", () => {
    const summary = buildRouterSummary({
      id: "router-stale",
      deviceIdentifier: "device-stale",
      displayName: "Remote AX3000T",
      hostname: "ax3000t-stale",
      status: "direct",
      importState: "approved",
      controllerChannel: "stable",
      rolloutGroupId: null,
      pendingImportRevisionId: null,
      activeRevisionId: null,
      lastAppliedRevisionId: null,
      lastConfigDigest: null,
      approvedAt: new Date(),
      lastSeenAt: new Date(Date.now() - 10 * 60 * 1000),
      lastCheckInAt: null,
      lastDirectModeAt: new Date(Date.now() - 10 * 60 * 1000),
      lastRescueReason: "Subscription expired or upstream proxy unavailable",
      model: "AX3000T",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.4",
      panelDomain: "https://panel.example.com",
      createdAt: new Date(),
      updatedAt: new Date(),
      latestSnapshot: {
        id: "snapshot-stale",
        routerId: "router-stale",
        source: "check_in",
        passwallEnabled: false,
        selectedNodeId: "myshunt",
        nodeCount: 15,
        subscriptionCount: 1,
        controllerVersion: "0.1.4-r1",
        passwallAppVersion: "26.3.5-r1",
        createdAt: new Date(Date.now() - 10 * 60 * 1000),
        payload: {
          protocolVersion: "2026-04-v1",
          deviceIdentifier: "device-stale",
          devicePublicKey: "pub",
          controllerVersion: "0.1.4-r1",
          hostname: "ax3000t-stale",
          model: "AX3000T",
          boardName: "xiaomi,mi-router-ax3000t",
          target: "mediatek/filogic",
          architecture: "aarch64_cortex-a53",
          openwrtRelease: "24.10.4",
          passwallEnabled: false,
          selectedNodeId: "myshunt",
          nodeCount: 15,
          subscriptionCount: 1,
          packageVersions: {
            "luci-app-passwall2": "26.3.5-r1",
          },
          binaryVersions: {},
          rulesAssets: {},
          resources: {
            memoryTotalMb: 0,
            memoryAvailableMb: 0,
            swapTotalMb: 0,
            swapFreeMb: 0,
            overlayFreeMb: 0,
            tmpFreeMb: 0,
          },
          serviceHealth: {
            controller: "unknown",
            passwall: "unknown",
            passwallServer: "unknown",
            dnsmasq: "unknown",
          },
        },
      },
      latestDesiredRevision: null,
      openIncident: null,
      queuedJobCount: 0,
      support: {
        state: "certified",
        title: "Сертифицировано",
        reason: "AX3000T stock-layout validated.",
      },
    } as Parameters<typeof buildRouterSummary>[0]);

    expect(summary.directMode).toBe(false);
    expect(summary.offline).toBe(true);
    expect(summary.statusLabel).toBe("Нет связи");
    expect(summary.lastRescue).toContain("Последнее известное rescue");
  });

  it("replaces raw unknown controller version in router summary", () => {
    const summary = buildRouterSummary({
      id: "router-unknown",
      deviceIdentifier: "device-unknown",
      displayName: "Unknown Controller",
      hostname: "unknown-controller",
      status: "active",
      importState: "approved",
      controllerChannel: "stable",
      rolloutGroupId: null,
      pendingImportRevisionId: null,
      activeRevisionId: null,
      lastAppliedRevisionId: null,
      lastConfigDigest: null,
      approvedAt: new Date(),
      lastSeenAt: new Date(),
      lastCheckInAt: null,
      lastDirectModeAt: null,
      lastRescueReason: null,
      model: "AX3000T",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.4",
      panelDomain: "https://panel.example.com",
      createdAt: new Date(),
      updatedAt: new Date(),
      latestSnapshot: {
        id: "snapshot-unknown",
        routerId: "router-unknown",
        source: "check_in",
        passwallEnabled: true,
        selectedNodeId: null,
        nodeCount: 1,
        subscriptionCount: 0,
        controllerVersion: "unknown",
        passwallAppVersion: "26.4.2",
        createdAt: new Date(),
        payload: {
          protocolVersion: "2026-04-v1",
          deviceIdentifier: "device-unknown",
          devicePublicKey: "pub",
          controllerVersion: "unknown",
          hostname: "unknown-controller",
          model: "AX3000T",
          boardName: "xiaomi,mi-router-ax3000t",
          target: "mediatek/filogic",
          architecture: "aarch64_cortex-a53",
          openwrtRelease: "24.10.4",
          passwallEnabled: true,
          selectedNodeId: null,
          nodeCount: 1,
          subscriptionCount: 0,
          packageVersions: {
            "luci-app-passwall2": "26.4.2",
          },
          binaryVersions: {},
          rulesAssets: {},
          resources: {
            memoryTotalMb: 0,
            memoryAvailableMb: 0,
            swapTotalMb: 0,
            swapFreeMb: 0,
            overlayFreeMb: 0,
            tmpFreeMb: 0,
          },
          serviceHealth: {
            controller: "running",
            passwall: "running",
            passwallServer: "unknown",
            dnsmasq: "running",
          },
        },
      },
      latestDesiredRevision: null,
      openIncident: null,
      queuedJobCount: 0,
      support: {
        state: "certified",
        title: "Сертифицировано",
        reason: "AX3000T stock-layout validated.",
      },
    } as Parameters<typeof buildRouterSummary>[0]);

    expect(summary.controllerVersion).toBe("Не удалось определить");
  });
});

describe("router detail helpers", () => {
  const config = passwallDesiredConfigSchema.parse({
    basicSettings: {
      main: {
        mainSwitch: true,
        selectedNodeId: "node-main",
        localhostProxy: true,
        clientProxy: true,
        nodeSocksPort: 1070,
        nodeSocksBindLocal: true,
        socksMainSwitch: false,
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
      },
      log: {
        enableNodeLog: true,
        level: "warning",
      },
      maintenance: {
        backupPaths: ["/etc/config/passwall2"],
      },
      socks: [],
      shuntRules: [],
    },
    nodes: [
      {
        id: "node-main",
        label: "Main node",
        protocol: "xray",
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
      items: [
        {
          id: "sub-1",
          remark: "Primary subscription",
          url: "https://example.com/subscription.txt",
          enabled: true,
          addMode: "2",
          metadata: {},
          extras: {},
        },
      ],
    },
    appUpdate: {
      binaryPaths: {
        xray: "/usr/bin/xray",
        singBox: "/usr/bin/sing-box",
        hysteria: "/usr/bin/hysteria",
        geoview: "/usr/bin/geoview",
      },
      updateStrategy: "package-preferred",
      targetVersions: {
        xray: "26.2.6",
      },
    },
    ruleManage: {
      geoipUrl: "https://example.com/geoip.dat",
      geositeUrl: "https://example.com/geosite.dat",
      assetDirectory: "/usr/share/v2ray/",
      autoUpdate: true,
      scheduleMode: "daily",
      enabledAssets: ["geoip", "geosite"],
      shuntRules: [],
    },
  });

  const detail = {
    router: {
      id: "router-1",
      deviceIdentifier: "device-1",
      displayName: null,
      hostname: "ax3000t",
      panelDomain: "https://panel.example.com",
      model: "AX3000T",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.4",
      status: "direct",
      importState: "approved",
      controllerChannel: "stable",
      rolloutGroupId: null,
      pendingImportRevisionId: null,
      activeRevisionId: "revision-1",
      lastAppliedRevisionId: "revision-1",
      lastConfigDigest: "digest-1",
      approvedAt: null,
      lastSeenAt: new Date(),
      lastCheckInAt: null,
      lastDirectModeAt: new Date(),
      lastRescueReason: "Local rescue activated.",
      createdAt: new Date(),
      updatedAt: new Date(),
    },
    latestSnapshot: {
      id: "snapshot-1",
      routerId: "router-1",
      source: "check_in",
      passwallEnabled: true,
      selectedNodeId: "node-main",
      nodeCount: 1,
      subscriptionCount: 1,
      controllerVersion: "0.1.0",
      passwallAppVersion: "26.4.2",
      createdAt: new Date(),
      payload: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "device-1",
        devicePublicKey: "pub",
        controllerVersion: "0.1.0",
        hostname: "ax3000t",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.4",
        passwallEnabled: true,
        selectedNodeId: "node-main",
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {
          "luci-app-passwall2": "26.4.2",
          "xray-core": "25.12.1-r1",
        },
        binaryVersions: {
          xray: "26.2.6",
        },
        rulesAssets: {},
        resources: {
          memoryTotalMb: 0,
          memoryAvailableMb: 0,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 0,
          tmpFreeMb: 0,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
      },
    },
    snapshots: [],
    revisions: [
      {
        id: "revision-1",
        routerId: "router-1",
        revisionNumber: 3,
        status: "queued",
        origin: "operator_draft",
        configDigest: "digest-1",
        config,
        hasRawImportedSnapshot: false,
        createdBy: "operator",
        note: null,
        approvedAt: null,
        createdAt: new Date(),
      },
    ],
    recentJobs: [],
    incidents: [],
    support: {
      state: "certified",
      title: "Сертифицировано",
      reason: "AX3000T stock-layout validated.",
    },
    applyReceipts: [],
  } as Parameters<typeof buildImpactLines>[0];

  it("builds impact lines from the newest revision", () => {
    const lines = buildImpactLines(detail);

    expect(lines).toContain("Ревизия #3: состояние queued.");
    expect(
      lines.some((line) => line.includes("Будет синхронизировано нод: 1")),
    ).toBe(true);
    expect(
      lines.some((line) => line.includes("Будет обновлено подписок: 1")),
    ).toBe(true);
  });

  it("describes package vs runtime drift and status", () => {
    const drift = describeVersionDrift(detail);

    expect(drift).toContainEqual(["xray", "25.12.1-r1", "26.2.6"]);
    expect(formatRouterStatus(detail)).toBe("Прямой доступ");
  });

  it("normalizes unknown controller drift labels", () => {
    if (!detail.latestSnapshot) {
      throw new Error("missing latest snapshot fixture");
    }

    const drift = describeVersionDrift({
      ...detail,
      latestSnapshot: {
        ...detail.latestSnapshot,
        controllerVersion: "unknown",
        payload: {
          ...detail.latestSnapshot.payload,
          controllerVersion: "unknown",
        },
      },
    });

    expect(drift[0]).toEqual([
      "Controller",
      "Не удалось определить",
      "Не удалось определить",
    ]);
  });

  it("treats stale router detail state as offline", () => {
    const staleDetail = {
      ...detail,
      router: {
        ...detail.router,
        lastSeenAt: new Date(Date.now() - 10 * 60 * 1000),
      },
    };

    expect(formatRouterStatus(staleDetail)).toBe("Нет свежей связи с роутером");
  });
});
