import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  jobResultRequestSchema,
  passwallDesiredConfigSchema,
  routerJobSchema,
  type PasswallDesiredConfig,
  createDefaultRescuePolicy,
  evaluateRescueMode,
  summarizePasswallRevisionDiff,
} from "@vectra/contracts";

const jobFixtures = JSON.parse(
  readFileSync(
    new URL(
      "../../../packages/contracts/fixtures/job-contract-fixtures.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as {
  routerJobs: {
    accepted: Array<{ name: string; value: unknown }>;
    rejected: Array<{ name: string; value: unknown }>;
  };
  jobResults: {
    accepted: Array<{ name: string; value: unknown }>;
    rejected: Array<{ name: string; value: unknown }>;
  };
};

describe("summarizePasswallRevisionDiff", () => {
  it("flags restart, subscriptions, rules, and packages based on changed sections", () => {
    const previous: PasswallDesiredConfig = {
      schemaVersion: 1 as const,
      basicSettings: {
        main: {
          mainSwitch: true,
          selectedNodeId: "node-a",
          localhostProxy: true,
          clientProxy: true,
          nodeSocksPort: 1070,
          nodeSocksBindLocal: true,
          socksMainSwitch: false,
          extras: {},
        },
        dns: {
          directQueryStrategy: "UseIP" as const,
          remoteDnsProtocol: "tcp" as const,
          remoteDns: "1.1.1.1",
          remoteDnsDoh: "https://1.1.1.1/dns-query",
          remoteDnsDetour: "remote" as const,
          remoteFakeDns: false,
          remoteDnsQueryStrategy: "UseIPv4" as const,
          dnsHosts: [],
          dnsRedirect: true,
          extras: {},
        },
        log: { enableNodeLog: true, level: "warning" as const, extras: {} },
        maintenance: { backupPaths: ["/etc/config/passwall2"], extras: {} },
        socks: [],
        shuntRules: [],
      },
      nodes: [],
      subscriptions: {
        filterKeywordMode: "0" as const,
        discardList: [],
        keepList: [],
        typePreferences: {},
        domainStrategy: "auto" as const,
        items: [],
      },
      appUpdate: {
        binaryPaths: {
          xray: "/usr/bin/xray",
          singBox: "/usr/bin/sing-box",
          hysteria: "/usr/bin/hysteria",
          geoview: "/usr/bin/geoview",
        },
        updateStrategy: "package-preferred" as const,
        targetVersions: {},
        extras: {},
      },
      ruleManage: {
        geoipUrl: "https://example.com/geoip.dat",
        geositeUrl: "https://example.com/geosite.dat",
        assetDirectory: "/usr/share/v2ray/",
        autoUpdate: false,
        scheduleMode: "daily" as const,
        enabledAssets: ["geoip", "geosite"],
        shuntRules: [],
        extras: {},
      },
    };

    const next: PasswallDesiredConfig = {
      ...previous,
      nodes: [
        {
          id: "node-a",
          label: "Node A",
          protocol: "xray" as const,
          group: "default",
          enabled: true,
          tags: [],
          extras: {},
        },
      ],
      subscriptions: {
        ...previous.subscriptions,
        items: [
          {
            id: "sub-1",
            remark: "Primary",
            url: "https://example.com/sub",
            enabled: true,
            addMode: "2" as const,
            metadata: {},
            extras: {},
          },
        ],
      },
      appUpdate: {
        ...previous.appUpdate,
        targetVersions: { xray: "26.3.0" },
      },
      ruleManage: {
        ...previous.ruleManage,
        autoUpdate: true,
        geoipUrl: "https://example.com/geoip-new.dat",
      },
    };

    const summary = summarizePasswallRevisionDiff(previous, next);

    expect(summary.changedSections).toEqual([
      "nodes",
      "subscriptions",
      "appUpdate",
      "ruleManage",
    ]);
    expect(summary.requiresRestart).toBe(true);
    expect(summary.refreshSubscriptions).toBe(true);
    expect(summary.refreshRules).toBe(true);
    expect(summary.packageInstall).toBe(true);
  });

  it("does not turn unrelated edits into subscription or rule refreshes", () => {
    const previous = passwallDesiredConfigSchema.parse({
      basicSettings: {
        main: {
          mainSwitch: true,
          selectedNodeId: "myshunt",
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
        log: { enableNodeLog: true, level: "warning", extras: {} },
        maintenance: { backupPaths: ["/etc/config/passwall2"], extras: {} },
        socks: [],
        shuntRules: [],
      },
      nodes: [
        {
          id: "myshunt",
          label: "myshunt",
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
        items: [
          {
            id: "sub-1",
            remark: "BloopCat",
            url: "https://example.com/sub",
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
        shuntRules: [],
        extras: {},
      },
    });
    const next = passwallDesiredConfigSchema.parse({
      ...previous,
      basicSettings: {
        ...previous.basicSettings,
        log: {
          ...previous.basicSettings.log,
          level: "debug",
        },
      },
    });

    const summary = summarizePasswallRevisionDiff(previous, next);

    expect(summary.changedSections).toEqual(["basicSettings"]);
    expect(summary.requiresRestart).toBe(true);
    expect(summary.refreshSubscriptions).toBe(false);
    expect(summary.refreshRules).toBe(false);
    expect(summary.operationPreview.map((operation) => operation.kind)).toEqual(
      ["uci_apply", "service_restart"],
    );
  });

  it("treats subscription-managed node reordering and volatile ids as no-op", () => {
    const previous = passwallDesiredConfigSchema.parse({
      basicSettings: {
        main: {
          mainSwitch: true,
          selectedNodeId: "myshunt",
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
        log: { enableNodeLog: true, level: "warning", extras: {} },
        maintenance: { backupPaths: ["/etc/config/passwall2"], extras: {} },
        socks: [],
        shuntRules: [],
      },
      nodes: [
        {
          id: "myshunt",
          label: "myshunt",
          protocol: "shunt",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
        {
          id: "old-ru-id",
          label: "🇷🇺 Россия",
          protocol: "vless",
          enabled: true,
          group: "BloopCat",
          address: "ru.example.com",
          port: 443,
          transport: "grpc",
          tls: true,
          tags: [],
          extras: {
            add_mode: "2",
            reality: "1",
            reality_publicKey: "present-but-redacted-in-ui",
            tls_serverName: "ru.example.com",
          },
        },
        {
          id: "old-fi-id",
          label: "🇫🇮 Finland",
          protocol: "vless",
          enabled: true,
          group: "BloopCat",
          address: "fi.example.com",
          port: 443,
          transport: "tcp",
          tls: true,
          tags: [],
          extras: {
            add_mode: "2",
            reality: "1",
            reality_publicKey: "present-too",
            tls_serverName: "fi.example.com",
          },
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
            id: "old-sub-id",
            remark: "BloopCat",
            url: "https://example.com/sub",
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
        shuntRules: [],
        extras: {},
      },
    });
    const next = passwallDesiredConfigSchema.parse({
      ...previous,
      nodes: [
        previous.nodes[0]!,
        {
          ...previous.nodes[2]!,
          id: "new-fi-id",
        },
        {
          ...previous.nodes[1]!,
          id: "new-ru-id",
        },
      ],
      subscriptions: {
        ...previous.subscriptions,
        items: [
          {
            ...previous.subscriptions.items[0]!,
            id: "new-sub-id",
          },
        ],
      },
    });

    const summary = summarizePasswallRevisionDiff(previous, next);

    expect(summary.changedSections).toEqual([]);
    expect(summary.fieldDiffs).toEqual([]);
    expect(summary.operationPreview).toEqual([]);
    expect(summary.refreshSubscriptions).toBe(false);
    expect(summary.refreshRules).toBe(false);
  });
});

describe("evaluateRescueMode", () => {
  it("enters direct mode after threshold failures", () => {
    const result = evaluateRescueMode({
      policy: createDefaultRescuePolicy({
        triggerFailureCount: 3,
        recoverySuccessCount: 2,
        cooldownSeconds: 300,
      }),
      currentMode: "proxy",
      failedProxyChecks: 3,
      successfulDirectChecks: 1,
      successfulProxyChecks: 0,
      lastTransitionAt: new Date(Date.now() - 600_000),
      now: new Date(),
    });

    expect(result.shouldTransition).toBe(true);
    expect(result.nextMode).toBe("direct");
  });

  it("stays in direct mode until recovery threshold is met", () => {
    const result = evaluateRescueMode({
      policy: createDefaultRescuePolicy({
        triggerFailureCount: 3,
        recoverySuccessCount: 2,
        cooldownSeconds: 300,
      }),
      currentMode: "direct",
      failedProxyChecks: 0,
      successfulDirectChecks: 1,
      successfulProxyChecks: 1,
      lastTransitionAt: new Date(Date.now() - 600_000),
      now: new Date(),
    });

    expect(result.shouldTransition).toBe(false);
    expect(result.nextMode).toBe("direct");
  });
});

describe("shared job contract fixtures", () => {
  it("accepts the curated router job corpus", () => {
    for (const fixture of jobFixtures.routerJobs.accepted) {
      expect(
        () => routerJobSchema.parse(fixture.value),
        fixture.name,
      ).not.toThrow();
    }
  });

  it("rejects malformed router jobs consistently", () => {
    for (const fixture of jobFixtures.routerJobs.rejected) {
      expect(
        () => routerJobSchema.parse(fixture.value),
        fixture.name,
      ).toThrow();
    }
  });

  it("accepts the curated job result corpus", () => {
    for (const fixture of jobFixtures.jobResults.accepted) {
      expect(
        () => jobResultRequestSchema.parse(fixture.value),
        fixture.name,
      ).not.toThrow();
    }
  });

  it("rejects malformed job results consistently", () => {
    for (const fixture of jobFixtures.jobResults.rejected) {
      expect(
        () => jobResultRequestSchema.parse(fixture.value),
        fixture.name,
      ).toThrow();
    }
  });
});
