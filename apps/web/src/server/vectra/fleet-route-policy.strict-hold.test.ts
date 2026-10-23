import { describe, expect, it } from "vitest";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import {
  buildFleetNodeHealth,
  type FleetNodeHealthSample,
} from "./fleet-node-health";
import {
  buildFleetRoutePolicyDirective,
  evaluateFleetRoutePolicy,
  normalizeFleetRoutePolicy,
} from "./fleet-route-policy";

// zhenya13911, 2026-10-23: WorldProxy sat on a working ru16:50052. The spread
// hashed the router to ru5:50052, a host no router was bound to and therefore
// one the ledger knew nothing about. Controller r49 binds the exact node the
// panel names, so it moved there, and Telegram and Instagram were down for
// ~40 minutes until the router's own failing probes got ru5 condemned.

const GERMANY = "🇷🇺🇩🇪⚡Германия YouTube 🚫Ad🚫";

function vless(
  id: string,
  label: string,
  address: string,
  port: number,
  extras: Record<string, unknown> = {},
) {
  return {
    id,
    label,
    protocol: "vless",
    enabled: true,
    group: "default",
    address,
    port,
    transport: "grpc",
    extras,
  };
}

const discordTuning = { mux: "1", mux_concurrency: "-1", xudp_concurrency: "16" };

function config(bindings: {
  WorldProxy: string;
  DiscordVoiceUdp?: string;
  extraNodes?: ReturnType<typeof vless>[];
}): PasswallDesiredConfig {
  const shunt = {
    WorldProxy: bindings.WorldProxy,
    YouTube: "node-yt",
    Special: "node-nl",
    Tiktok: "node-by",
    DiscordVoiceUdp: bindings.DiscordVoiceUdp ?? bindings.WorldProxy,
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
        { id: "WorldProxy", label: "WorldProxy", outboundNodeId: shunt.WorldProxy, extras: {} },
        { id: "YouTube", label: "YouTube", outboundNodeId: shunt.YouTube, extras: {} },
        { id: "Special", label: "Special", outboundNodeId: shunt.Special, extras: {} },
        { id: "Tiktok", label: "Tiktok", outboundNodeId: shunt.Tiktok, extras: {} },
        {
          id: "DiscordVoiceUdp",
          label: "DiscordVoiceUdp",
          outboundNodeId: shunt.DiscordVoiceUdp,
          extras: { network: "udp", port: "19294-19344,50000-50100" },
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
        extras: shunt,
      },
      vless("node-de-ru16", GERMANY, "ru16.nfnpx.online", 50052, discordTuning),
      vless("node-de-ru5", GERMANY, "ru5.nfnpx.online", 50052, discordTuning),
      vless("node-yt", "🇷🇺⚡Россия YouTube 🚫Ad🚫", "ru20.nfnpx.online", 50051),
      { ...vless("node-nl", "🇳🇱 Нидерланды", "nl2.nfnpx.online", 443), transport: "tcp" },
      { ...vless("node-by", "🇧🇾 Беларусь", "by2.nfnpx.online", 443), transport: "tcp" },
      ...(bindings.extraNodes ?? []),
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

// A router the spread hashes to ru5 while it sits on ru16 — found rather than
// hard-coded, so the test does not depend on the hash function's exact output.
const identity = (() => {
  const onRu16 = config({ WorldProxy: "node-de-ru16" });
  for (let index = 0; index < 64; index += 1) {
    const candidate = { deviceIdentifier: `vectra-spread-${index}` };
    const mismatch = evaluateFleetRoutePolicy(onRu16, candidate).mismatches.find(
      (entry) => entry.slot === "WorldProxy",
    );
    if (mismatch?.expectedNodeId === "node-de-ru5") {
      return candidate;
    }
  }
  throw new Error("no identity hashes to ru5; the fixture needs a second host");
})();

function ledger(...samples: FleetNodeHealthSample[]) {
  return { nodeHealth: buildFleetNodeHealth(samples) };
}

// zhenya's own evidence: Telegram/Instagram fine through ru16, YouTube fine.
const ru16Works: FleetNodeHealthSample = {
  routerId: "zhenya13911",
  observations: [
    { host: "ru16.nfnpx.online:50052", outcome: "ok" },
    { host: "ru20.nfnpx.online:50051", outcome: "ok" },
  ],
};

function slotTarget(
  directive: ReturnType<typeof buildFleetRoutePolicyDirective>,
  slot: string,
) {
  return directive?.slots.find((entry) => entry.id === slot)?.nodeId;
}

describe("strictPreferred holds a live exit against an unproven pick", () => {
  it("without a ledger the spread still moves the router (unchanged behaviour)", () => {
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-de-ru16" }),
      identity,
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru5");
  });

  it("keeps WorldProxy and DiscordVoiceUdp on the working exit while nobody has reached the pick", () => {
    const options = ledger(ru16Works);
    const current = config({ WorldProxy: "node-de-ru16" });

    const compliance = evaluateFleetRoutePolicy(current, identity, options);
    expect(compliance.status).toBe("compliant");

    const directive = buildFleetRoutePolicyDirective(current, identity, options);
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru16");
    expect(slotTarget(directive, "DiscordVoiceUdp")).toBe("node-de-ru16");

    const normalized = normalizeFleetRoutePolicy(current, identity, options);
    expect(normalized.changes.map((change) => change.slot)).not.toContain("WorldProxy");
    expect(normalized.changes.map((change) => change.slot)).not.toContain("DiscordVoiceUdp");
  });

  it("holds on an empty ledger, where nothing has been proven yet", () => {
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-de-ru16" }),
      identity,
      ledger(),
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru16");
  });

  it("moves once some router has carried traffic through the pick", () => {
    const options = ledger(ru16Works, {
      routerId: "nataliafilisiti",
      observations: [{ host: "ru5.nfnpx.online:50052", outcome: "ok" }],
    });
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-de-ru16" }),
      identity,
      options,
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru5");
    expect(slotTarget(directive, "DiscordVoiceUdp")).toBe("node-de-ru5");
  });

  it("still moves a router off a condemned exit, proven pick or not", () => {
    const options = ledger({
      routerId: "zhenya13911",
      observations: [
        { host: "ru16.nfnpx.online:50052", outcome: "fail" },
        { host: "ru20.nfnpx.online:50051", outcome: "ok" },
      ],
    });
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-de-ru16" }),
      identity,
      options,
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru5");
  });

  it("still moves the router up to a better tier (an operator canon change)", () => {
    // Parked on the RU-entry EU auto exit, which scores below the Germany
    // bridge; nobody has reached either Germany host yet. A tier change is a
    // decision, not a rebalance, and must still go through.
    const auto = vless("node-auto", "🇷🇺🇪🇺 Авто Самый стабильный", "ru3.nfnpx.online", 40051);
    const options = ledger({
      routerId: "zhenya13911",
      observations: [{ host: "ru3.nfnpx.online:40051", outcome: "ok" }],
    });
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-auto", extraNodes: [auto] }),
      identity,
      options,
    );
    expect(slotTarget(directive, "WorldProxy")).not.toBe("node-auto");
    expect(slotTarget(directive, "WorldProxy")).toMatch(/^node-de-/);
  });

  it("brings DiscordVoiceUdp back to the held WorldProxy exit", () => {
    // The pair must share one node. With WorldProxy held on ru16, a
    // DiscordVoiceUdp left on ru5 is out of place and goes to ru16, not to
    // wherever its own spread would point.
    const options = ledger(ru16Works);
    const current = config({ WorldProxy: "node-de-ru16", DiscordVoiceUdp: "node-de-ru5" });

    const compliance = evaluateFleetRoutePolicy(current, identity, options);
    const discord = compliance.mismatches.find((entry) => entry.slot === "DiscordVoiceUdp");
    expect(discord?.expectedNodeId).toBe("node-de-ru16");
    expect(slotTarget(buildFleetRoutePolicyDirective(current, identity, options), "DiscordVoiceUdp"))
      .toBe("node-de-ru16");

    const normalized = normalizeFleetRoutePolicy(current, identity, options);
    const after = normalized.after.matchedSlots;
    expect(after.find((slot) => slot.slot === "WorldProxy")?.targetNodeId).toBe("node-de-ru16");
    expect(after.find((slot) => slot.slot === "DiscordVoiceUdp")?.targetNodeId).toBe("node-de-ru16");
  });

  it("lets DiscordVoiceUdp follow WorldProxy onto its pick instead of holding on its own", () => {
    // WorldProxy already sits on the (unproven) pick, so nothing is held. A
    // DiscordVoiceUdp still on another live exit must join it there, not keep
    // its own exit — the pair shares one node.
    const options = ledger(ru16Works);
    const current = config({ WorldProxy: "node-de-ru5", DiscordVoiceUdp: "node-de-ru16" });

    const directive = buildFleetRoutePolicyDirective(current, identity, options);
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru5");
    expect(slotTarget(directive, "DiscordVoiceUdp")).toBe("node-de-ru5");
  });

  it("does not hold a slot whose bound node is gone from the config", () => {
    // A re-minted id leaves nothing to hold on to; the slot goes to the pick.
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-re-minted-away" }),
      identity,
      ledger(ru16Works),
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru5");
  });

  it("only takes proof for the pick's own port", () => {
    // ru5:50051 (YouTube) answering says nothing about ru5:50052 (Germany).
    const options = ledger(ru16Works, {
      routerId: "someone",
      observations: [{ host: "ru5.nfnpx.online:50051", outcome: "ok" }],
    });
    const directive = buildFleetRoutePolicyDirective(
      config({ WorldProxy: "node-de-ru16" }),
      identity,
      options,
    );
    expect(slotTarget(directive, "WorldProxy")).toBe("node-de-ru16");
  });
});
