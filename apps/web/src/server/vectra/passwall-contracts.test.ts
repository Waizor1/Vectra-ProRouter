import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  summarizePasswallRevisionDiff,
} from "@vectra/contracts";

function createMinimalPasswallConfig() {
  return passwallDesiredConfigSchema.parse({
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
        level: "warning",
        extras: {},
      },
      maintenance: {
        backupPaths: ["/etc/config/passwall2"],
        extras: {},
      },
      socks: [],
      shuntRules: [
        {
          id: "DiscordVoiceUdp",
          label: "DiscordVoiceUdp",
          outboundNodeId: "node-main",
          domainRules: [],
          ipRules: [],
          extras: {},
        },
      ],
    },
    nodes: [
      {
        id: "shunt-main",
        label: "Main shunt",
        protocol: "shunt",
        enabled: true,
        group: "default",
        tags: [],
        extras: {},
      },
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
      shuntRules: [
        {
          id: "DiscordVoiceUdp",
          label: "DiscordVoiceUdp",
          outboundNodeId: "node-main",
          domainRules: [],
          ipRules: [],
          extras: {},
        },
      ],
      extras: {},
    },
  });
}

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

  it("accepts imported Socks protocol nodes", () => {
    const config = passwallDesiredConfigSchema.parse({
      basicSettings: {
        main: {
          mainSwitch: true,
          selectedNodeId: "node-socks",
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
          id: "node-socks",
          label: "Local Socks",
          protocol: "socks",
          enabled: true,
          group: "default",
          address: "127.0.0.1",
          port: 1080,
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

    expect(config.nodes[0]?.protocol).toBe("socks");
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

  it("includes shunt rule extras such as inbound network and port in UCI preview", () => {
    const previous = createMinimalPasswallConfig();
    const next = structuredClone(previous);
    const extras = {
      protocol: "http quic",
      inbound: "tproxy",
      network: "udp",
      port: "19294-19344,50000-50100",
      source: "geoip:private",
      invert: "1",
    };
    next.basicSettings.shuntRules[0]!.extras = extras;
    next.ruleManage.shuntRules[0]!.extras = extras;

    const summary = summarizePasswallRevisionDiff(previous, next);
    const nodeSync = summary.operationPreview.find(
      (operation) => operation.kind === "node_sync",
    );

    expect(nodeSync?.uciCommands).toEqual(
      expect.arrayContaining([
        "set passwall2.DiscordVoiceUdp.protocol='http quic'",
        "set passwall2.DiscordVoiceUdp.inbound='tproxy'",
        "set passwall2.DiscordVoiceUdp.network='udp'",
        "set passwall2.DiscordVoiceUdp.port='19294-19344,50000-50100'",
        "set passwall2.DiscordVoiceUdp.source='geoip:private'",
        "set passwall2.DiscordVoiceUdp.invert='1'",
      ]),
    );
  });

  it("preserves latest PassWall node and subscription extras in UCI preview", () => {
    const previous = createMinimalPasswallConfig();
    const next = structuredClone(previous);
    next.nodes[1]!.extras = {
      mkcp_mtu: 1400,
      tls_pinSHA256: "sha256-fingerprint",
    };
    next.subscriptions.items.push({
      id: "sub-main",
      remark: "Main sub",
      url: "https://example.com/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {
        domain_resolver: "https",
        domain_resolver_dns_https: "https://dns.example/dns-query",
        domain_strategy: "UseIPv4",
      },
    });

    const summary = summarizePasswallRevisionDiff(previous, next);
    const nodeSync = summary.operationPreview.find(
      (operation) => operation.kind === "node_sync",
    );
    const subscriptionSync = summary.operationPreview.find(
      (operation) => operation.kind === "subscription_sync",
    );

    expect(nodeSync?.uciCommands).toEqual(
      expect.arrayContaining([
        "set passwall2.node_main.mkcp_mtu='1400'",
        "set passwall2.node_main.tls_pinSHA256='sha256-fingerprint'",
      ]),
    );
    expect(subscriptionSync?.uciCommands).toEqual(
      expect.arrayContaining([
        "set passwall2.vectra_sub_sub_main.domain_resolver='https'",
        "set passwall2.vectra_sub_sub_main.domain_resolver_dns_https='https://dns.example/dns-query'",
        "set passwall2.vectra_sub_sub_main.domain_strategy='UseIPv4'",
      ]),
    );
  });

  it("includes shunt node bindings and extras in UCI preview without stale duplicate shunt targets", () => {
    const previous = createMinimalPasswallConfig();
    const next = structuredClone(previous);
    next.nodes[0]!.extras = {
      DiscordVoiceUdp: "stale-node",
      default_node: "_direct",
      custom_marker: "keep-me",
    };
    next.basicSettings.shuntRules[0]!.outboundNodeId = "node-main";
    next.ruleManage.shuntRules[0]!.outboundNodeId = "node-main";
    next.subscriptions.items.push({
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

    const summary = summarizePasswallRevisionDiff(previous, next);
    const nodeSync = summary.operationPreview.find(
      (operation) => operation.kind === "node_sync",
    );
    const subscriptionSync = summary.operationPreview.find(
      (operation) => operation.kind === "subscription_sync",
    );

    expect(nodeSync?.uciCommands).toEqual(
      expect.arrayContaining([
        "set passwall2.shunt_main.DiscordVoiceUdp='node-main'",
        "set passwall2.shunt_main.default_node='_direct'",
        "set passwall2.shunt_main.custom_marker='keep-me'",
      ]),
    );
    expect(nodeSync?.uciCommands).not.toContain(
      "set passwall2.shunt_main.DiscordVoiceUdp='stale-node'",
    );
    expect(subscriptionSync?.uciCommands).toContain(
      "set passwall2.vectra_sub_sub_main.auto_update='1'",
    );
  });

  it("keeps preview UCI names and DNS redirect aligned with controller apply", () => {
    const previous = createMinimalPasswallConfig();
    const next = structuredClone(previous);
    next.basicSettings.dns.dnsRedirect = false;
    next.basicSettings.socks.push({
      id: "@socks[0]",
      enabled: true,
      nodeId: "node-main",
      port: 1080,
      bindLocal: true,
      autoswitchBackupNodeIds: [],
      extras: {},
    });
    next.subscriptions.items.push({
      id: "@subscribe_list[0]",
      remark: "Imported sub",
      url: "https://example.com/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {},
    });

    const summary = summarizePasswallRevisionDiff(previous, next);
    const globalApply = summary.operationPreview.find(
      (operation) => operation.kind === "uci_apply",
    );
    const nodeSync = summary.operationPreview.find(
      (operation) => operation.kind === "node_sync",
    );
    const subscriptionSync = summary.operationPreview.find(
      (operation) => operation.kind === "subscription_sync",
    );

    expect(globalApply?.uciCommands).toContain(
      "set passwall2.vectra_global.dns_redirect='0'",
    );
    expect(nodeSync?.uciCommands).toContain(
      "set passwall2.vectra_socks_socks_0=socks",
    );
    expect(subscriptionSync?.uciCommands).toContain(
      "set passwall2.vectra_sub_subscribe_list_0=subscribe_list",
    );
  });

  it("does not compound existing Vectra subscription section prefixes", () => {
    const previous = createMinimalPasswallConfig();
    const next = structuredClone(previous);
    next.subscriptions.items.push({
      id: "vectra_sub_vectra_sub_vectra_sub_subscribe_list1",
      remark: "Imported sub",
      url: "https://example.com/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {},
    });

    const summary = summarizePasswallRevisionDiff(previous, next);
    const subscriptionSync = summary.operationPreview.find(
      (operation) => operation.kind === "subscription_sync",
    );

    expect(subscriptionSync?.uciCommands).toContain(
      "set passwall2.vectra_sub_subscribe_list1=subscribe_list",
    );
    expect(subscriptionSync?.uciCommands).not.toContain(
      "set passwall2.vectra_sub_vectra_sub_vectra_sub_vectra_sub_subscribe_list1=subscribe_list",
    );
  });
});
