import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  jobResultRequestSchema,
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
      import.meta.url
    ),
    "utf8"
  )
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
      nodes: [{ id: "node-a", label: "Node A", protocol: "xray" as const, group: "default", enabled: true, tags: [], extras: {} }],
      subscriptions: {
        ...previous.subscriptions,
        items: [{ id: "sub-1", remark: "Primary", url: "https://example.com/sub", enabled: true, addMode: "2" as const, metadata: {}, extras: {} }],
      },
      appUpdate: {
        ...previous.appUpdate,
        targetVersions: { xray: "26.3.0" },
      },
      ruleManage: {
        ...previous.ruleManage,
        autoUpdate: true,
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
      expect(() => routerJobSchema.parse(fixture.value), fixture.name).not.toThrow();
    }
  });

  it("rejects malformed router jobs consistently", () => {
    for (const fixture of jobFixtures.routerJobs.rejected) {
      expect(() => routerJobSchema.parse(fixture.value), fixture.name).toThrow();
    }
  });

  it("accepts the curated job result corpus", () => {
    for (const fixture of jobFixtures.jobResults.accepted) {
      expect(() => jobResultRequestSchema.parse(fixture.value), fixture.name).not.toThrow();
    }
  });

  it("rejects malformed job results consistently", () => {
    for (const fixture of jobFixtures.jobResults.rejected) {
      expect(() => jobResultRequestSchema.parse(fixture.value), fixture.name).toThrow();
    }
  });
});
