import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  summarizePasswallRevisionDiff,
} from "@vectra/contracts";

describe("passwall contracts", () => {
  it("accepts xhttp nodes and managed extras for imported baselines", () => {
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
          extras: {
            custom_marker: "keep-me",
          },
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
          extras: {
            dns_redirect: "1",
          },
        },
        log: {
          enableNodeLog: true,
          level: "warning",
          extras: {},
        },
        maintenance: {
          backupPaths: ["/etc/config/passwall2"],
          extras: {},
        },
        socks: [],
        shuntRules: [],
      },
      nodes: [
        {
          id: "node-main",
          label: "Main node",
          protocol: "vless",
          enabled: true,
          group: "default",
          address: "example.com",
          port: 443,
          transport: "xhttp",
          tags: [],
          extras: {
            xhttp_mode: "auto",
            xhttp_path: "/",
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

    expect(config.nodes[0]?.transport).toBe("xhttp");
    expect(config.nodes[0]?.extras.xhttp_mode).toBe("auto");
    expect(config.basicSettings.main.extras.custom_marker).toBe("keep-me");
    expect(config.basicSettings.dns.extras.dns_redirect).toBe("1");
  });

  it("returns an empty operation preview for an identical draft", () => {
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
          level: "warning",
          extras: {},
        },
        maintenance: {
          backupPaths: ["/etc/config/passwall2"],
          extras: {},
        },
        socks: [],
        shuntRules: [],
      },
      nodes: [
        {
          id: "node-main",
          label: "Main node",
          protocol: "vless",
          enabled: true,
          group: "default",
          address: "example.com",
          port: 443,
          transport: "tcp",
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
        shuntRules: [],
        extras: {},
      },
    });

    const summary = summarizePasswallRevisionDiff(config, config);

    expect(summary.changedSections).toEqual([]);
    expect(summary.requiresRestart).toBe(false);
    expect(summary.refreshSubscriptions).toBe(false);
    expect(summary.refreshRules).toBe(false);
    expect(summary.packageInstall).toBe(false);
    expect(summary.operationPreview).toEqual([]);
  });
});
