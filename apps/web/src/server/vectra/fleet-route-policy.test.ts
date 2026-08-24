import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import {
  buildFleetRoutePolicyDirective,
  buildFleetRoutePolicyIdentity,
  canonicalFleetRoutePolicy,
  evaluateFleetRoutePolicy,
  FLEET_ROUTE_POLICY_VERSION,
  normalizeFleetRoutePolicy,
} from "./fleet-route-policy";
import { buildFleetNodeHealth } from "./fleet-node-health";

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

  it("no longer exempts VagrandRouter now that its ISP stopped filtering high ports", () => {
    // Exempt from 2026-07-29 because its line filtered ports 50051-50061, so
    // every canonical RU-entry gRPC target was unreachable while :443 worked.
    // Confirmed gone 2026-08-05 — the router's own Special slot runs on
    // ru11.nfnpx.online:50055 — so the exemption lost its cause. A hardcoded
    // exemption that outlives its reason silently freezes a router on whatever
    // bindings it happened to have.
    const compliance = evaluateFleetRoutePolicy(buildConfig(), {
      hostname: "VagrandRouter",
    });

    expect(compliance.exempt).toBe(false);
    expect(compliance.status).not.toBe("exempt");
    // `checked` is the part that matters: an exempt router short-circuits
    // before the slots are ever evaluated. (`canNormalize` stays false here
    // only because this fixture is already compliant and has nothing to fix.)
    expect(compliance.checked).toBe(true);
    expect(compliance.exceptionReason).toBeNull();
  });

  it("keeps the panel exception list in sync with the on-router one", () => {
    // The panel and the controller's self-heal both hold a hardcoded list. If
    // they drift, one silently undoes the other every 60s check-in. This test
    // fails whenever the panel list changes without the Go list following.
    expect([...canonicalFleetRoutePolicy.exceptions].sort()).toEqual(["hh"]);
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
      hostname: "hh",
      routePolicyExempt: false,
    });

    expect(directive?.exempt).toBe(false);
    expect(directive?.slots.length).toBeGreaterThan(0);
  });

  it("still honours the seed list when no operator flag is set", () => {
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "hh",
    });

    expect(directive?.exempt).toBe(true);
  });

  it("no longer seeds an exemption for VagrandRouter", () => {
    // The seed list is the offline fallback. Now that vagrandrouter is out of
    // it, the directive must carry real slots instead of exempt: true --
    // otherwise the router would keep self-exempting whenever it falls back to
    // its own scorer.
    const directive = buildFleetRoutePolicyDirective(buildConfig({}), {
      hostname: "VagrandRouter",
    });

    expect(directive?.exempt).toBe(false);
    expect(directive?.slots.length).toBeGreaterThan(0);
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

  it("binds WorldProxy to a direct Poland :443 exit when one exists", () => {
    const result = normalizeFleetRoutePolicy(
      withPolandNodes([extremePoland, directPoland]),
      { hostname: "kirill-msk" },
    );

    const world = result.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    // Which of the two direct exits wins is deliberately per-router — see
    // "exit spreading" below. The canon claim is only that the slot leaves the
    // RU-entry tier whenever a direct :443 Poland exit is available.
    expect(["node-poland-direct", "node-poland-extreme"]).toContain(
      world?.targetNodeId,
    );
    expect(world?.targetNodeId).not.toBe(ruEntryPolandId);
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
    expect(["node-poland-direct", "node-poland-extreme"]).toContain(
      discord?.targetNodeId,
    );
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

describe("exit spreading across equally good Poland nodes", () => {
  function polandNode(id: string, address: string, label: string) {
    return {
      id,
      label,
      protocol: "vless",
      enabled: true,
      group: "default",
      address,
      port: 443,
      transport: "tcp",
      extras: {},
    };
  }

  function withNodes(
    nodes: ReturnType<typeof polandNode>[],
    bindings?: Record<string, string>,
  ) {
    const base = buildConfig(bindings ? { bindings } : {});
    return passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [...base.nodes, ...nodes],
    });
  }

  const bothExits = () => [
    polandNode("node-pl1", "pl1.nfnpx.online", "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫"),
    polandNode("node-pl2", "pl2.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
  ];

  function worldTargetFor(deviceIdentifier: string) {
    const normalized = normalizeFleetRoutePolicy(withNodes(bothExits()), {
      deviceIdentifier,
    });
    return normalized.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    )?.targetNodeId;
  }

  it("hands every router the operator's exit when it is available", () => {
    // Operator decision 2026-08-12, made with the concentration risk stated:
    // the fleet converges on the exit the operator runs on 1111111111, so that
    // "it works on mine" holds for every router rather than for a hash-picked
    // half of them.
    const picks = new Set(
      Array.from({ length: 40 }, (_, index) =>
        worldTargetFor(`vectra-device-${index}`),
      ),
    );

    expect(picks).toEqual(new Set(["node-pl2"]));
  });

  it("falls back to another Poland exit when the subscription has no pl2", () => {
    // The convergence must not strand a slot. A subscription that carries no
    // pl2 node still has to land on a working Poland exit rather than score
    // below the 100 floor and leave WorldProxy unbound.
    const onlyPl1 = [
      polandNode("node-pl1", "pl1.nfnpx.online", "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫"),
    ];
    const normalized = normalizeFleetRoutePolicy(withNodes(onlyPl1), {
      deviceIdentifier: "vectra-no-pl2",
    });

    expect(
      normalized.after.matchedSlots.find((slot) => slot.slot === "WorldProxy")
        ?.targetNodeId,
    ).toBe("node-pl1");
  });

  it("still spreads routers over the remaining exits when pl2 is absent", () => {
    // The spread is inert for WorldProxy only while the canonical host is
    // present. Without it the fleet must not re-concentrate on one survivor.
    const noCanonical = () => [
      polandNode("node-pl1", "pl1.nfnpx.online", "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫"),
      polandNode("node-pl3", "pl3.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
    ];
    const picks = new Set(
      Array.from({ length: 40 }, (_, index) => {
        const normalized = normalizeFleetRoutePolicy(withNodes(noCanonical()), {
          deviceIdentifier: `vectra-device-${index}`,
        });
        return normalized.after.matchedSlots.find(
          (slot) => slot.slot === "WorldProxy",
        )?.targetNodeId;
      }),
    );

    expect(picks).toEqual(new Set(["node-pl1", "node-pl3"]));
  });

  it("picks the operator's exit by host even when its label says something else", () => {
    // The provider re-maps labels per subscription: this same host arrives as
    // "⚡Extreme Авто EU 🇪🇺" on some routers, with no Poland marker at all.
    // Matching on the label would silently drop those to the pl1 tier.
    const relabelled = [
      polandNode("node-pl1", "pl1.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
      polandNode("node-pl2", "pl2.nfnpx.online", "⚡Extreme Авто EU 🇪🇺"),
    ];
    const normalized = normalizeFleetRoutePolicy(withNodes(relabelled), {
      deviceIdentifier: "vectra-relabelled",
    });

    expect(
      normalized.after.matchedSlots.find((slot) => slot.slot === "WorldProxy")
        ?.targetNodeId,
    ).toBe("node-pl2");
  });

  it("gives one router the same exit on every evaluation", () => {
    // A router that hops exits between check-ins would rewrite its config and
    // drop connections every minute.
    const picks = Array.from({ length: 8 }, () =>
      worldTargetFor("vectra-aabbccddeeff"),
    );

    expect(new Set(picks).size).toBe(1);
  });

  it("keeps DiscordVoiceUdp on the same node as WorldProxy for every router", () => {
    // These two slots MUST share a node: the generated Xray routing chain puts
    // the WorldProxy rule above DiscordVoiceUdp, so voice packets leave through
    // whatever WorldProxy resolved to regardless.
    for (let index = 0; index < 40; index += 1) {
      const normalized = normalizeFleetRoutePolicy(withNodes(bothExits()), {
        deviceIdentifier: `vectra-device-${index}`,
      });
      const world = normalized.after.matchedSlots.find(
        (slot) => slot.slot === "WorldProxy",
      )?.targetNodeId;
      const discord = normalized.after.matchedSlots.find(
        (slot) => slot.slot === "DiscordVoiceUdp",
      )?.targetNodeId;

      expect(discord).toBe(world);
    }
  });

  it("spreads by exit host, not by duplicate labels on one host", () => {
    // Subscriptions routinely carry the same host twice under two labels.
    // Splitting those would look like spreading while leaving the whole fleet
    // on a single exit.
    const picks = new Set(
      Array.from({ length: 30 }, (_, index) => {
        const normalized = normalizeFleetRoutePolicy(
          withNodes([
            polandNode("node-a", "pl2.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
            polandNode(
              "node-b",
              "pl2.nfnpx.online",
              "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
            ),
          ]),
          { deviceIdentifier: `vectra-device-${index}` },
        );
        return normalized.after.matchedSlots.find(
          (slot) => slot.slot === "WorldProxy",
        )?.targetNodeId;
      }),
    );

    expect(picks.size).toBe(1);
  });

  it("recognises a Poland exit whose label carries no Poland marker", () => {
    // Measured 2026-08-08: the provider labels pl2 "⚡Extreme Авто EU 🇪🇺" on
    // AlekseyHorev and vladimirdrfilicity. Label-only matching made that node
    // invisible and dropped both routers to the RU-entry fallback.
    const normalized = normalizeFleetRoutePolicy(
      withNodes([
        polandNode("node-pl2", "pl2.nfnpx.online", "⚡Extreme Авто EU 🇪🇺"),
      ]),
      { deviceIdentifier: "vectra-aabbccddeeff" },
    );

    const world = normalized.after.matchedSlots.find(
      (slot) => slot.slot === "WorldProxy",
    );
    expect(world?.targetNodeId).toBe("node-pl2");
  });

  it("still refuses a node that is neither Poland-labelled nor a pl* host", () => {
    const normalized = normalizeFleetRoutePolicy(
      withNodes([
        polandNode("node-nl", "nl3.nfnpx.online", "⚡Extreme Авто EU 🇪🇺"),
      ]),
      { deviceIdentifier: "vectra-aabbccddeeff" },
    );

    const bound = normalized.after.matchedSlots
      .filter((slot) => slot.slot === "WorldProxy")
      .map((slot) => slot.targetNodeId);
    expect(bound).not.toContain("node-nl");
  });
});

describe("operator exemption survives into every panel surface", () => {
  // The database flag used to reach only the check-in path: the controller
  // honoured it while fleet.list / fleet.byId / fleet.normalizeRoutePolicy each
  // built their own identity literal and dropped it. kirill-msk therefore read
  // "violation" with canNormalize:true, and pressing normalize would have
  // rebound his slots off the hand-tuned node that took his handshake failures
  // from 78/150 to 0/150 — the flag protected him from the automation but not
  // from the operator.
  const exemptRow = {
    id: "11111111-2222-3333-4444-555555555555",
    displayName: "kirill-msk",
    hostname: "kirill-msk",
    deviceIdentifier: "vectra-aabbccddeeff",
    routePolicyExempt: true,
    routePolicyExemptReason: "CPU handshake saturation",
  };

  it("carries the flag and its reason into the identity", () => {
    const identity = buildFleetRoutePolicyIdentity(exemptRow);

    expect(identity.routePolicyExempt).toBe(true);
    expect(identity.routePolicyExemptReason).toBe("CPU handshake saturation");
  });

  it("reports an exempt router as exempt, not as a violation", () => {
    const compliance = evaluateFleetRoutePolicy(
      buildConfig({}),
      buildFleetRoutePolicyIdentity(exemptRow),
    );

    expect(compliance.status).toBe("exempt");
    expect(compliance.canNormalize).toBe(false);
    expect(compliance.exceptionReason).toBe("CPU handshake saturation");
  });

  it("makes normalization a no-op for an exempt router", () => {
    const config = buildConfig({});
    const result = normalizeFleetRoutePolicy(
      config,
      buildFleetRoutePolicyIdentity(exemptRow),
    );

    expect(result.changed).toBe(false);
    expect(result.changes).toHaveLength(0);
    expect(result.config).toEqual(config);
  });

  it("still normalizes a router whose flag is unset", () => {
    // The flag is null for every router until an operator sets it, so the
    // untouched fleet must keep behaving exactly as before.
    const compliance = evaluateFleetRoutePolicy(
      buildConfig({}),
      buildFleetRoutePolicyIdentity({
        ...exemptRow,
        displayName: "artem-lutfulin",
        hostname: "artem-lutfulin",
        routePolicyExempt: null,
        routePolicyExemptReason: null,
      }),
    );

    expect(compliance.exempt).toBe(false);
    expect(compliance.checked).toBe(true);
  });

  it("lets the flag un-exempt a router the seed list still names", () => {
    const compliance = evaluateFleetRoutePolicy(
      buildConfig({}),
      buildFleetRoutePolicyIdentity({
        ...exemptRow,
        displayName: "hh",
        hostname: "hh",
        routePolicyExempt: false,
        routePolicyExemptReason: null,
      }),
    );

    expect(compliance.exempt).toBe(false);
  });
});

describe("no churn when already on the right exit", () => {
  function polandNode(id: string, address: string, label: string) {
    return {
      id,
      label,
      protocol: "vless",
      enabled: true,
      group: "default",
      address,
      port: 443,
      transport: "tcp",
      extras: {},
    };
  }

  // One host, two labels — the shape every real subscription hands out.
  function twinLabelConfig(boundNodeId: string) {
    const base = buildConfig({ bindings: { WorldProxy: boundNodeId } });
    return passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [
        ...base.nodes,
        polandNode("node-adblock", "pl2.nfnpx.online", "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫"),
        polandNode("node-extreme", "pl2.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
      ],
    });
  }

  it("keeps a binding that is already on the chosen host", () => {
    // Both labels are the same exit, so demanding one over the other is a
    // rebind that changes no traffic — and it never sticks, because the
    // subscription re-mints node ids nightly.
    for (const bound of ["node-adblock", "node-extreme"]) {
      const compliance = evaluateFleetRoutePolicy(twinLabelConfig(bound), {
        deviceIdentifier: "vectra-aabbccddeeff",
      });
      const world = compliance.matchedSlots.find(
        (slot) => slot.slot === "WorldProxy",
      );

      expect(world?.targetNodeId).toBe(bound);
    }
  });

  it("reports such a router as compliant, not as a violation", () => {
    const compliance = evaluateFleetRoutePolicy(twinLabelConfig("node-extreme"), {
      deviceIdentifier: "vectra-aabbccddeeff",
    });

    expect(
      compliance.mismatches.filter((mismatch) => mismatch.slot === "WorldProxy"),
    ).toHaveLength(0);
  });

  it("normalization is a no-op for the WorldProxy slot in that case", () => {
    const config = twinLabelConfig("node-extreme");
    const result = normalizeFleetRoutePolicy(config, {
      deviceIdentifier: "vectra-aabbccddeeff",
    });

    expect(
      result.changes.filter((change) => change.slot === "WorldProxy"),
    ).toHaveLength(0);
  });

  it("still moves a router that sits on the wrong host", () => {
    // Stickiness must not become "never move". A router parked on pl1 while the
    // operator's exit is available in its subscription has to be relocated —
    // that is exactly the case that left kirill-msk rotting on an emergency
    // node for five days on 2026-08-07.
    const base = buildConfig({ bindings: { WorldProxy: "node-pl1" } });
    const config = passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [
        ...base.nodes,
        polandNode("node-pl1", "pl1.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
        polandNode("node-pl2", "pl2.nfnpx.online", "⚡Extreme Польша 🇵🇱"),
      ],
    });

    const picks = new Set(
      Array.from({ length: 40 }, (_, index) => {
        const result = normalizeFleetRoutePolicy(config, {
          deviceIdentifier: `vectra-device-${index}`,
        });
        return result.after.matchedSlots.find(
          (slot) => slot.slot === "WorldProxy",
        )?.targetNodeId;
      }),
    );

    expect(picks).toEqual(new Set(["node-pl2"]));
  });
});

describe("directive covers slots that have drifted", () => {
  // 2026-08-24 root cause. buildFleetRoutePolicyDirective used to echo only
  // matchedSlots, so a drifted slot got no instruction — and on the controller
  // side a directive carrying ANY binding replaces the whole slot list, which
  // silently switches off the router's own scorer for exactly the slot that
  // needed it. The drift then survives forever. The directive must therefore
  // name the node a slot should be on, never just the ones already correct.
  function driftedWorldProxy() {
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
          address: "pl1.nfnpx.online",
          port: 443,
          transport: "tcp",
          extras: {},
        },
      ],
    });
  }

  it("names the canonical node for a drifted slot instead of staying silent", () => {
    const config = driftedWorldProxy();
    const compliance = evaluateFleetRoutePolicy(config, {
      hostname: "nataliafilisiti",
    });
    expect(compliance.status).toBe("violation");

    const directive = buildFleetRoutePolicyDirective(config, {
      hostname: "nataliafilisiti",
    });
    const world = directive?.slots.find((slot) => slot.id === "WorldProxy");
    const discord = directive?.slots.find(
      (slot) => slot.id === "DiscordVoiceUdp",
    );

    expect(world?.nodeId).toBe("node-poland-direct");
    // The pair must move together or Discord voice breaks, exactly as it did
    // on 2026-08-03 when the two slots resolved to different nodes.
    expect(discord?.nodeId).toBe("node-poland-direct");
  });

  it("still covers every canonical slot while it is drifting", () => {
    const directive = buildFleetRoutePolicyDirective(driftedWorldProxy(), {
      hostname: "nataliafilisiti",
    });

    expect(directive?.slots.map((slot) => slot.id)).toEqual([
      "WorldProxy",
      "YouTube",
      "Special",
      "Tiktok",
      "DiscordVoiceUdp",
    ]);
  });
});

describe("dead provider hosts lose their slot", () => {
  // The other half of 2026-08-24: the provider lost ru9-ru12 outright. Scoring
  // by label/host/port cannot see that, so eleven routers read as "compliant"
  // while bound to a node returning nothing, and the directive pinned them
  // there. With the fleet ledger the dead host stops being a candidate.
  function twoYoutubeNodes() {
    const base = buildConfig({});
    return passwallDesiredConfigSchema.parse({
      ...base,
      nodes: [
        ...base.nodes,
        {
          id: "node-youtube-live",
          label: "🇷🇺⚡Россия YouTube 🚫Ad🚫",
          protocol: "vless",
          enabled: true,
          group: "default",
          address: "ru7.nfnpx.online",
          port: 50051,
          transport: "grpc",
          extras: {},
        },
      ],
    });
  }

  const deadRu5 = {
    nodeHealth: buildFleetNodeHealth([
      {
        routerId: "kirill",
        observations: [
          { host: "ru5.nfnpx.online", outcome: "fail" as const },
          { host: "pl2.nfnpx.online", outcome: "ok" as const },
        ],
      },
    ]),
  };

  it("counts a binding to a dead host as a violation", () => {
    const config = twoYoutubeNodes();

    expect(evaluateFleetRoutePolicy(config, { hostname: "kirill-msk" }).status)
      .toBe("compliant");
    expect(
      evaluateFleetRoutePolicy(config, { hostname: "kirill-msk" }, deadRu5)
        .status,
    ).toBe("violation");
  });

  it("points the directive at the live sibling", () => {
    const directive = buildFleetRoutePolicyDirective(
      twoYoutubeNodes(),
      { hostname: "kirill-msk" },
      deadRu5,
    );

    const youtube = directive?.slots.find((slot) => slot.id === "YouTube");
    expect(youtube?.nodeId).toBe("node-youtube-live");
  });

  it("normalisation moves the slot off the dead host", () => {
    const result = normalizeFleetRoutePolicy(
      twoYoutubeNodes(),
      { hostname: "kirill-msk" },
      deadRu5,
    );

    expect(result.changed).toBe(true);
    expect(result.after.status).toBe("compliant");
    expect(
      result.after.matchedSlots.find((slot) => slot.slot === "YouTube")
        ?.targetNodeId,
    ).toBe("node-youtube-live");
  });

  // Never strand a slot. A dead binding still recovers by itself when the
  // provider brings the host back; an unbound slot dumps that traffic
  // somewhere else entirely and nothing brings it back.
  it("keeps the only candidate even when it is dead", () => {
    // Every host that could carry the YouTube slot is condemned, so there is
    // nowhere live to go.
    const everythingDead = {
      nodeHealth: buildFleetNodeHealth([
        {
          routerId: "kirill",
          observations: [
            { host: "ru3.nfnpx.online", outcome: "fail" as const },
            { host: "ru4.nfnpx.online", outcome: "fail" as const },
            { host: "ru5.nfnpx.online", outcome: "fail" as const },
            { host: "pl2.nfnpx.online", outcome: "ok" as const },
          ],
        },
      ]),
    };
    const compliance = evaluateFleetRoutePolicy(
      buildConfig({}),
      { hostname: "kirill-msk" },
      everythingDead,
    );

    const youtube = compliance.matchedSlots.find(
      (slot) => slot.slot === "YouTube",
    );
    expect(youtube?.targetNodeId).toBe("node-youtube-1");
  });
});
