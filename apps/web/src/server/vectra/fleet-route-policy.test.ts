import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import {
  buildFleetRoutePolicyDirective,
  canonicalFleetRoutePolicy,
  evaluateFleetRoutePolicy,
  FLEET_ROUTE_POLICY_VERSION,
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
    // WorldProxy now canonically shares the RU-entry Poland node with
    // DiscordVoiceUdp (DE→PL move, 2026-07-02). The node-world fixture below
    // stays a now-inert Germany node; WorldProxy defaults onto the discord
    // (RU-Poland) node so the default config is policy-compliant.
    WorldProxy: nodeIds.discord,
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

  it("exempts VagrandRouter, whose ISP blocks the RU-entry port range", () => {
    // Its line filters ports 50051-50061, so every canonical RU-entry gRPC
    // target is unreachable while :443 works. Reachability is not part of the
    // scorer, so without this exception normalization rebinds the slots to a
    // dead node on every check-in.
    const compliance = evaluateFleetRoutePolicy(buildConfig(), {
      hostname: "VagrandRouter",
    });

    expect(compliance.status).toBe("exempt");
    expect(compliance.exempt).toBe(true);
    expect(compliance.canNormalize).toBe(false);
  });

  it("keeps the panel exception list in sync with the on-router one", () => {
    // The panel and the controller's self-heal both hold a hardcoded list. If
    // they drift, one silently undoes the other every 60s check-in. This test
    // fails whenever the panel list changes without the Go list following.
    expect([...canonicalFleetRoutePolicy.exceptions].sort()).toEqual([
      "hh",
      "vagrandrouter",
    ]);
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
    ).toBe("node-discord-1");
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
      WorldProxy: "node-discord-1",
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

// --- Panel-authored directive ---------------------------------------------
//
// The directive is what removes "rebuild the controller for every policy tweak".
// These lock in the two properties that matter operationally: the panel can set
// an exemption the controller has never heard of, and it names concrete nodes.

describe("buildFleetRoutePolicyDirective", () => {
  it("names concrete node ids for every resolved slot", () => {
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "kirill-msk",
    });

    expect(directive).not.toBeNull();
    expect(directive!.exempt).toBe(false);
    expect(directive!.slots.length).toBeGreaterThan(0);
    for (const slot of directive!.slots) {
      expect(slot.nodeId).toBeTruthy();
      expect(slot.id).toBeTruthy();
    }
  });

  it("carries the UDP tuning extras on the DiscordVoiceUdp slot", () => {
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "kirill-msk",
    });
    const discord = directive!.slots.find((s) => s.id === "DiscordVoiceUdp");

    expect(discord?.ruleExtras).toMatchObject({ network: "udp" });
    expect(discord?.nodeExtras).toMatchObject({ mux: "1" });
  });

  it("emits an exempt directive from the database flag alone", () => {
    // The whole point: a router the controller's compiled-in list has never
    // heard of can still be exempted, with no rebuild and no rollout.
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "kirill-msk",
      routePolicyExempt: true,
      routePolicyExemptReason: "operator hold pending provider fix",
    });

    expect(directive).toEqual({
      version: FLEET_ROUTE_POLICY_VERSION,
      exempt: true,
      reason: "operator hold pending provider fix",
      slots: [],
    });
  });

  it("lets the database flag retire a seed-list exemption", () => {
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "VagrandRouter",
      routePolicyExempt: false,
    });

    expect(directive?.exempt).toBe(false);
    expect(directive?.slots.length).toBeGreaterThan(0);
  });

  it("still honours the seed list when no operator flag is set", () => {
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "VagrandRouter",
    });

    expect(directive?.exempt).toBe(true);
  });

  it("returns null when there is no live config to reason about", () => {
    expect(
      buildFleetRoutePolicyDirective(null, { hostname: "kirill-msk" }),
    ).toBeNull();
  });
});

describe("WorldProxy canon (2026-08-02: direct Poland :443)", () => {
  // The provider blackholed Telegram DCs and the Netflix OCA CDN on part of its
  // RU-entry fleet (ru3/ru4/ru5:50053 dead, ru7-ru12 fine), so WorldProxy moved
  // to the direct Poland exit. These assertions mirror
  // TestFleetRoutePolicyScoreWorldProxyPrefersDirectPoland in
  // router/vectra-controller-agent/internal/passwall/fleet_policy_test.go — the
  // two scorers must agree or they undo each other every check-in.
  function withPolandNodes(extra: Array<Record<string, unknown>>) {
    const base = buildConfig({});
    return passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [...base.nodes, ...extra],
    });
  }

  const directPoland = {
    id: "node-poland-direct",
    label: "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
    protocol: "vless",
    enabled: true,
    group: "default",
    address: "pl2.nfnpx.online",
    port: 443,
    transport: "tcp",
    extras: {},
  };
  const extremePoland = {
    ...directPoland,
    id: "node-poland-extreme",
    label: "⚡Extreme Польша 🇵🇱",
    address: "pl1.nfnpx.online",
  };
  // The base fixture's `node-discord-1` IS the RU-entry Poland node
  // (ru3.nfnpx.online:50053, grpc) — the shape this outage killed.
  const ruEntryPolandId = "node-discord-1";

  it("binds WorldProxy to the direct Poland :443 exit when one exists", () => {
    const result = normalizeFleetRoutePolicy(
      withPolandNodes([extremePoland, directPoland]),
      { hostname: "kirill-msk" },
    );

    const world = result.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    expect(world?.targetNodeId).toBe("node-poland-direct");
  });

  // 2026-08-03: splitting DiscordVoiceUdp off WorldProxy killed voice
  // fleet-wide. The WorldProxy rule outranks the Discord rule in the generated
  // Xray chain and already carries the Discord prefixes with network=tcp,udp,
  // so voice packets leave through the WorldProxy node no matter what this slot
  // points at — leaving the slot on RU-entry stranded its mux/xudp tuning on a
  // node no Discord packet reached. The two must move together.
  it("binds DiscordVoiceUdp to the same node as WorldProxy", () => {
    const result = normalizeFleetRoutePolicy(
      withPolandNodes([extremePoland, directPoland]),
      { hostname: "kirill-msk" },
    );

    const world = result.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    const discord = result.after.matchedSlots.find(
      (slot) => slot.slot === "DiscordVoiceUdp",
    );
    expect(discord?.targetNodeId).toBe("node-poland-direct");
    expect(discord?.targetNodeId).toBe(world?.targetNodeId);
  });

  it("keeps DiscordVoiceUdp with WorldProxy on the RU-entry fallback too", () => {
    const result = normalizeFleetRoutePolicy(buildConfig({}), {
      hostname: "kirill-msk",
    });

    const world = result.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    const discord = result.after.matchedSlots.find(
      (slot) => slot.slot === "DiscordVoiceUdp",
    );
    expect(discord?.targetNodeId).toBe(ruEntryPolandId);
    expect(discord?.targetNodeId).toBe(world?.targetNodeId);
  });

  it("falls back to RU-entry Poland when the subscription has no direct node", () => {
    const result = normalizeFleetRoutePolicy(buildConfig({}), {
      hostname: "kirill-msk",
    });

    const world = result.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    expect(world?.targetNodeId).toBe(ruEntryPolandId);
  });
});

describe("WorldProxy strictPreferred", () => {
  // Regression guard for the trap found during the 2026-08-02 rollout: the
  // directive echoes matchedSlots, and the controller obeys the directive over
  // its own scorer. If a slot parked on the RU-entry fallback counted as
  // compliant, the panel would pin every router back onto the broken exit.
  function withDirectPoland() {
    const base = buildConfig({});
    return passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [
        ...base.nodes,
        {
          id: "node-poland-direct",
          label: "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
          protocol: "vless",
          enabled: true,
          group: "default",
          address: "pl2.nfnpx.online",
          port: 443,
          transport: "tcp",
          extras: {},
        },
      ],
    });
  }

  it("reports violation while parked on the RU-entry fallback", () => {
    const compliance = evaluateFleetRoutePolicy(withDirectPoland(), {
      hostname: "kirill-msk",
    });

    const world = compliance.mismatches.find(
      (mismatch) => mismatch.slot === "WorldProxy",
    );
    expect(compliance.status).toBe("violation");
    expect(world?.reason).toBe("wrong_target");
    expect(world?.expectedNodeId).toBe("node-poland-direct");
  });

  it("never pins the fallback node through the check-in directive", () => {
    const directive = buildFleetRoutePolicyDirective(withDirectPoland(), {
      hostname: "kirill-msk",
    });

    const world = directive?.slots.find((slot) => slot.id === "WorldProxy");
    // Either the directive names the canonical node, or it stays silent and
    // lets the controller's own scorer pick it — never the stale fallback.
    expect(world?.nodeId ?? "node-poland-direct").toBe("node-poland-direct");
  });

  it("is compliant once bound to the canonical direct exit", () => {
    const config = withDirectPoland();
    const normalized = normalizeFleetRoutePolicy(config, {
      hostname: "kirill-msk",
    });

    expect(normalized.after.status).toBe("compliant");
    const world = normalized.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    expect(world?.targetNodeId).toBe("node-poland-direct");
  });

  it("still accepts the RU-entry node when no direct exit exists", () => {
    const compliance = evaluateFleetRoutePolicy(buildConfig({}), {
      hostname: "kirill-msk",
    });

    expect(compliance.status).toBe("compliant");
  });
});
