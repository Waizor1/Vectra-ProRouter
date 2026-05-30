import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import {
  evaluateFleetRoutePolicy,
  normalizeFleetRoutePolicy,
} from "./fleet-route-policy";

function buildConfig(
  overrides: {
    bindings?: Partial<
      Record<
        "WorldProxy" | "YouTube" | "Special" | "Tiktok" | "DiscordVoiceUdp",
        string
      >
    >;
    discordNodeExtras?: Record<string, unknown>;
    discordRuleExtras?: Record<string, unknown>;
    nodeIds?: Partial<
      Record<"world" | "youtube" | "special" | "tiktok" | "discord", string>
    >;
  } = {},
): PasswallDesiredConfig {
  const nodeIds = {
    world: overrides.nodeIds?.world ?? "node-world-1",
    youtube: overrides.nodeIds?.youtube ?? "node-youtube-1",
    special: overrides.nodeIds?.special ?? "node-special-1",
    tiktok: overrides.nodeIds?.tiktok ?? "node-tiktok-1",
    discord: overrides.nodeIds?.discord ?? "node-discord-1",
  };
  const bindings = {
    WorldProxy: nodeIds.world,
    YouTube: nodeIds.youtube,
    Special: nodeIds.special,
    Tiktok: nodeIds.tiktok,
    DiscordVoiceUdp: nodeIds.discord,
    ...overrides.bindings,
  };

  return passwallDesiredConfigSchema.parse({
    schemaVersion: 1,
    basicSettings: {
      main: {
        mainSwitch: true,
        selectedNodeId: "myshunt",
        localhostProxy: true,
        clientProxy: true,
        nodeSocksBindLocal: true,
        socksMainSwitch: false,
        extras: {},
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
        extras: {},
      },
      log: { enableNodeLog: true, level: "warning", extras: {} },
      maintenance: { backupPaths: [], extras: {} },
      socks: [],
      shuntRules: [
        {
          id: "WorldProxy",
          label: "WorldProxy",
          outboundNodeId: bindings.WorldProxy,
          extras: {},
        },
        {
          id: "YouTube",
          label: "YouTube",
          outboundNodeId: bindings.YouTube,
          extras: {},
        },
        {
          id: "Special",
          label: "Special",
          outboundNodeId: bindings.Special,
          extras: {},
        },
        {
          id: "Tiktok",
          label: "Tiktok",
          outboundNodeId: bindings.Tiktok,
          extras: {},
        },
        {
          id: "DiscordVoiceUdp",
          label: "DiscordVoiceUdp",
          outboundNodeId: bindings.DiscordVoiceUdp,
          extras: {
            network: "udp",
            port: "19294-19344,50000-50100",
            ...overrides.discordRuleExtras,
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
        group: "default",
        extras: bindings,
      },
      {
        id: nodeIds.world,
        label: "🇷🇺🇩🇪⚡Германия YouTube 🚫Ad🚫",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "ru4.nfnpx.online",
        port: 50052,
        transport: "grpc",
        extras: {},
      },
      {
        id: nodeIds.youtube,
        label: "🇷🇺⚡Россия YouTube 🚫Ad🚫",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "ru5.nfnpx.online",
        port: 50051,
        transport: "grpc",
        extras: {},
      },
      {
        id: nodeIds.special,
        label: "🇳🇱 Нидерланды",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "nl2.nfnpx.online",
        port: 443,
        transport: "tcp",
        extras: {},
      },
      {
        id: nodeIds.tiktok,
        label: "🇧🇾 Беларусь",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "by2.nfnpx.online",
        port: 443,
        transport: "tcp",
        extras: {},
      },
      {
        id: nodeIds.discord,
        label: "🇷🇺🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "ru3.nfnpx.online",
        port: 50053,
        transport: "grpc",
        extras: {
          mux: "1",
          mux_concurrency: "-1",
          xudp_concurrency: "16",
          ...overrides.discordNodeExtras,
        },
      },
      {
        id: "node-kz",
        label: "🇷🇺🇰🇿 Казахстан",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "ru5.nfnpx.online",
        port: 50056,
        transport: "grpc",
        extras: {},
      },
      {
        id: "node-us",
        label: "🇷🇺🇺🇸 США",
        protocol: "vless",
        enabled: true,
        group: "default",
        address: "ru3.nfnpx.online",
        port: 50058,
        transport: "grpc",
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
          remark: "Main subscription",
          url: "https://secret.example/sub",
          enabled: true,
          addMode: "2",
          metadata: {},
          extras: { auto_update: "1" },
        },
      ],
    },
    appUpdate: {
      binaryPaths: {},
      updateStrategy: "package-preferred",
      targetVersions: {},
      extras: {},
    },
    ruleManage: {
      geoipUrl: "https://example.com/geoip.dat",
      geositeUrl: "https://example.com/geosite.dat",
      autoUpdate: true,
      scheduleMode: "daily",
      scheduleHour: 6,
      enabledAssets: ["geoip", "geosite"],
      shuntRules: [],
      extras: {},
    },
  });
}

describe("fleet route policy", () => {
  it("accepts a refreshed subscription node id when semantic server fingerprint is unchanged", () => {
    const config = buildConfig({
      nodeIds: {
        world: "fresh-world-id",
        youtube: "fresh-youtube-id",
        special: "fresh-special-id",
        tiktok: "fresh-tiktok-id",
        discord: "fresh-discord-id",
      },
    });

    const compliance = evaluateFleetRoutePolicy(config, {
      name: "normal-router",
    });

    expect(compliance.status).toBe("compliant");
    expect(compliance.mismatches).toEqual([]);
    expect(compliance.matchedSlots.map((slot) => slot.slot)).toEqual([
      "WorldProxy",
      "YouTube",
      "Special",
      "Tiktok",
      "DiscordVoiceUdp",
    ]);
  });

  it("flags country drift even when configTrust can still be live-import", () => {
    const config = buildConfig({
      bindings: {
        WorldProxy: "node-us",
        DiscordVoiceUdp: "node-kz",
      },
    });

    const compliance = evaluateFleetRoutePolicy(config, { name: "AndreyVK" });

    expect(compliance.status).toBe("violation");
    expect(compliance.canNormalize).toBe(true);
    expect(compliance.mismatches.map((mismatch) => mismatch.slot)).toEqual([
      "WorldProxy",
      "DiscordVoiceUdp",
    ]);
    expect(compliance.mismatches[0]?.actualFingerprint).toContain("США");
  });

  it("keeps hh as an explicit no-touch exception", () => {
    const compliance = evaluateFleetRoutePolicy(buildConfig(), {
      name: "hh",
      hostname: "hh",
    });

    expect(compliance.status).toBe("exempt");
    expect(compliance.exempt).toBe(true);
    expect(compliance.canNormalize).toBe(false);
  });

  it("normalizes only shunt bindings and Discord tuning while preserving subscription URLs", () => {
    const config = buildConfig({
      bindings: {
        WorldProxy: "node-us",
        DiscordVoiceUdp: "node-kz",
      },
      discordNodeExtras: {
        mux: "0",
        mux_concurrency: "8",
        xudp_concurrency: "4",
      },
      discordRuleExtras: {
        network: "tcp",
        port: "443",
      },
    });
    const subscriptionsBefore = structuredClone(config.subscriptions);

    const result = normalizeFleetRoutePolicy(config, { name: "normal-router" });

    expect(result.changed).toBe(true);
    expect(result.after.status).toBe("compliant");
    expect(result.config.subscriptions).toEqual(subscriptionsBefore);
    expect(
      result.config.basicSettings.shuntRules.find(
        (rule) => rule.id === "WorldProxy",
      )?.outboundNodeId,
    ).toBe("node-world-1");
    const discordRule = result.config.basicSettings.shuntRules.find(
      (rule) => rule.id === "DiscordVoiceUdp",
    );
    expect(discordRule?.outboundNodeId).toBe("node-discord-1");
    expect(discordRule?.extras).toMatchObject({
      network: "udp",
      port: "19294-19344,50000-50100",
    });
    const discordNode = result.config.nodes.find(
      (node) => node.id === "node-discord-1",
    );
    expect(discordNode?.extras).toMatchObject({
      mux: "1",
      mux_concurrency: "-1",
      xudp_concurrency: "16",
    });
    expect(
      result.config.nodes.find((node) => node.id === "myshunt")?.extras,
    ).toMatchObject({
      WorldProxy: "node-world-1",
      DiscordVoiceUdp: "node-discord-1",
    });
  });

  it("prefers the live-good RU-entry Netherlands fallback for Special when it is available", () => {
    const config = buildConfig({
      bindings: {
        Special: "node-kz",
      },
    });
    config.nodes.push({
      id: "node-special-ru-entry",
      label: "🇷🇺🇳🇱 Нидерланды YouTube 🚫Ad🚫",
      protocol: "vless",
      enabled: true,
      group: "default",
      tags: [],
      address: "ru6.nfnpx.online",
      port: 50055,
      transport: "grpc",
      extras: {},
    });

    const result = normalizeFleetRoutePolicy(config, { name: "normal-router" });

    expect(result.changed).toBe(true);
    expect(result.after.status).toBe("compliant");
    expect(
      result.config.basicSettings.shuntRules.find(
        (rule) => rule.id === "Special",
      )?.outboundNodeId,
    ).toBe("node-special-ru-entry");
    expect(
      result.config.nodes.find((node) => node.id === "myshunt")?.extras,
    ).toMatchObject({
      Special: "node-special-ru-entry",
    });
    expect(
      result.changes.find((change) => change.slot === "Special")?.nextNodeId,
    ).toBe("node-special-ru-entry");
  });

  it("never binds YouTube to an entry-flag-only node that fails the real service", () => {
    // "🇷🇺🇦🇪 ОАЭ" carries a leading 🇷🇺 ENTRY flag and a genuine RU-entry host, but
    // it is a UAE exit: it passes a google-204 healthcheck yet fails real
    // youtube.com. It must never qualify for the YouTube slot off the entry marker
    // alone — this mirrors the Go scorer guard in fleet_policy_test.go.
    const config = buildConfig({ bindings: { YouTube: "node-uae" } });
    config.nodes.push({
      id: "node-uae",
      label: "🇷🇺🇦🇪 ОАЭ",
      protocol: "vless",
      enabled: true,
      group: "default",
      tags: [],
      address: "ru4.nfnpx.online",
      port: 50061,
      transport: "grpc",
      extras: {},
    });

    const compliance = evaluateFleetRoutePolicy(config, {
      name: "normal-router",
    });
    expect(compliance.status).toBe("violation");
    expect(compliance.canNormalize).toBe(true);
    expect(compliance.mismatches.map((mismatch) => mismatch.slot)).toContain(
      "YouTube",
    );

    const result = normalizeFleetRoutePolicy(config, { name: "normal-router" });
    expect(result.changed).toBe(true);
    const youtubeRule = result.config.basicSettings.shuntRules.find(
      (rule) => rule.id === "YouTube",
    );
    expect(youtubeRule?.outboundNodeId).toBe("node-youtube-1");
    expect(youtubeRule?.outboundNodeId).not.toBe("node-uae");
  });
});
