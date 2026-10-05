import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import { passwallDesiredRevisions, routerInventorySnapshots } from "@vectra/db";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  loadLatestFleetPolicyConfigRows,
  loadLatestFleetPolicyConfigSummaries,
  loadLatestSnapshots,
  resetFleetPolicyConfigSummaryCacheForTest,
} from "./fleet-monitoring-data";
import { buildFleetNodeHealth } from "./fleet-node-health";
import {
  buildFleetRoutePolicyDirective,
  collectFleetNodeHealthSample,
  evaluateFleetRoutePolicy,
  findStrandedSlots,
} from "./fleet-route-policy";
import { routeVerificationToHealthSample, subscriptionHasHardwareId } from "./route-health-verifier";
import { createStaticDb, type StaticDbQuery } from "./testing/static-db";

const ROUTER_A = "router-a";
const ROUTER_B = "router-b";

function liveConfig(worldNode: string): PasswallDesiredConfig {
  const slots = ["WorldProxy", "YouTube", "Special", "Tiktok", "DiscordVoiceUdp"];
  return passwallDesiredConfigSchema.parse({
    basicSettings: {
      main: { mainSwitch: true, selectedNodeId: "myshunt" },
      dns: {},
      log: {},
      maintenance: {},
      shuntRules: slots.map((slot) => ({
        id: slot,
        label: slot,
        domainRules: [`geosite:${slot.toUpperCase()}`],
      })),
    },
    nodes: [
      {
        id: "myshunt",
        label: "myshunt",
        protocol: "shunt",
        // YouTube on its own host, so a sample has a control success.
        extras: Object.fromEntries(
          slots.map((slot) => [slot, slot === "YouTube" ? "node-pl2" : worldNode]),
        ),
      },
      { id: "node-pl2", label: "🇵🇱 Польша pl2 :443", protocol: "vless", address: "pl2.example.online", port: 443 },
      { id: "node-de", label: "🇩🇪 Германия ru3 :50052", protocol: "vless", address: "ru3.example.online", port: 50052 },
      { id: "node-nl", label: "🇳🇱 Нидерланды nl1 :443", protocol: "vless", address: "nl1.example.online", port: 443 },
    ],
    subscriptions: {
      items: [{ id: "sub", remark: "Vectra", url: "https://sub.example/x", extras: { hwid: "1" } }],
    },
    appUpdate: {},
    ruleManage: { geoipUrl: "https://example.test/geoip.dat", geositeUrl: "https://example.test/geosite.dat" },
  });
}

type Revision = { id: string; routerId: string; origin: string; createdAt: Date; config: PasswallDesiredConfig };

function createDb(revisions: Revision[], revisionsTable: unknown = passwallDesiredRevisions) {
  const configReads: string[][] = [];
  const { db } = createStaticDb(({ table, params, fields }: StaticDbQuery) => {
    if (table !== revisionsTable) return [];
    if (fields && "config" in fields) {
      const ids = revisions.filter((revision) => params.has(revision.id)).map((revision) => revision.id);
      configReads.push(ids);
      return revisions.filter((revision) => params.has(revision.id));
    }
    // Newest first, for the routers asked about: whole rows for a bare
    // select() (the full loader), metadata for a projection.
    const rows = revisions
      .filter((revision) => params.has(revision.routerId))
      .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    return fields ? rows.map(({ config: _config, ...metadata }) => metadata) : rows;
  });
  return { db: db as never, configReads };
}

beforeEach(() => resetFleetPolicyConfigSummaryCacheForTest());

describe("loadLatestFleetPolicyConfigSummaries", () => {
  it("reads each revision's config once, then only revisions that are new", async () => {
    const revisions: Revision[] = [
      { id: "rev-a1", routerId: ROUTER_A, origin: "router_import", createdAt: new Date(1_000), config: liveConfig("node-pl2") },
      { id: "rev-b1", routerId: ROUTER_B, origin: "router_import", createdAt: new Date(1_000), config: liveConfig("node-de") },
    ];
    const { db, configReads } = createDb(revisions);

    const first = await loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A, ROUTER_B]);
    expect(configReads).toEqual([["rev-a1", "rev-b1"]]);
    expect(first.get(ROUTER_A)?.id).toBe("rev-a1");

    // A minute later nothing changed: no config is read at all.
    await loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A, ROUTER_B]);
    expect(configReads).toHaveLength(1);

    // Router B imports a new config: only that revision is read.
    revisions.push({ id: "rev-b2", routerId: ROUTER_B, origin: "router_import", createdAt: new Date(2_000), config: liveConfig("node-nl") });
    const third = await loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A, ROUTER_B]);
    expect(configReads).toEqual([["rev-a1", "rev-b1"], ["rev-b2"]]);
    expect(third.get(ROUTER_B)?.id).toBe("rev-b2");
    expect(third.get(ROUTER_B)?.config.nodes[0]?.extras.WorldProxy).toBe("node-nl");
  });

  it("ignores operator drafts, exactly as the full loader does", async () => {
    const revisions: Revision[] = [
      { id: "rev-a1", routerId: ROUTER_A, origin: "router_import", createdAt: new Date(1_000), config: liveConfig("node-pl2") },
      { id: "rev-a2", routerId: ROUTER_A, origin: "operator_draft", createdAt: new Date(2_000), config: liveConfig("node-nl") },
    ];
    const { db } = createDb(revisions);
    const summaries = await loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A]);
    expect(summaries.get(ROUTER_A)?.id).toBe("rev-a1");
  });

  it("gives every monitor the same answers as the full config", async () => {
    const full = liveConfig("node-nl");
    const { db } = createDb([
      { id: "rev-a1", routerId: ROUTER_A, origin: "router_import", createdAt: new Date(1_000), config: full },
    ]);
    const [summaryRow, fullRow] = await Promise.all([
      loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A]).then((rows) => rows.get(ROUTER_A)),
      loadLatestFleetPolicyConfigRows(db, [ROUTER_A]).then((rows) => rows.get(ROUTER_A)),
    ]);
    const summary = summaryRow!.config;
    expect(fullRow?.config).toEqual(full);
    expect({ ...summaryRow, config: undefined }).toEqual({ ...fullRow, config: undefined });

    const identity = { id: ROUTER_A, hostname: "router-a", deviceIdentifier: "vectra-a" };
    const now = new Date("2026-10-05T10:00:00Z");
    const probes = {
      telegram: { status: "blocked", checkedAt: "2026-10-05T09:59:00Z" },
      youtube: { status: "reachable", checkedAt: "2026-10-05T09:59:00Z" },
    };
    const nodeHealth = buildFleetNodeHealth([
      collectFleetNodeHealthSample(ROUTER_A, full, probes, new Date(0), now)!,
      collectFleetNodeHealthSample("router-z", full, probes, new Date(0), now)!,
    ]);

    // Not a vacuous comparison: the ledger condemns a host and the directive
    // binds slots.
    expect(nodeHealth.unhealthyHosts.length).toBeGreaterThan(0);
    expect(buildFleetRoutePolicyDirective(full, identity, { nodeHealth })?.slots.length).toBeGreaterThan(0);

    expect(collectFleetNodeHealthSample(ROUTER_A, summary, probes, new Date(0), now)).toEqual(
      collectFleetNodeHealthSample(ROUTER_A, full, probes, new Date(0), now),
    );
    expect(evaluateFleetRoutePolicy(summary, identity, { nodeHealth })).toEqual(
      evaluateFleetRoutePolicy(full, identity, { nodeHealth }),
    );
    expect(buildFleetRoutePolicyDirective(summary, identity, { nodeHealth })).toEqual(
      buildFleetRoutePolicyDirective(full, identity, { nodeHealth }),
    );
    expect(findStrandedSlots(summary, identity, { nodeHealth })).toEqual(
      findStrandedSlots(full, identity, { nodeHealth }),
    );
    const verification = {
      checkedAt: "2026-10-05T09:58:00Z",
      exempt: false,
      slots: [{ slot: "WorldProxy", nodeId: "node-nl", ok: false, code: "000" }],
    } as never;
    expect(routeVerificationToHealthSample(ROUTER_A, summary.nodes, verification)).toEqual(
      routeVerificationToHealthSample(ROUTER_A, full.nodes, verification),
    );
    expect(subscriptionHasHardwareId(summary)).toBe(subscriptionHasHardwareId(full));
    expect(subscriptionHasHardwareId(summary)).toBe(true);
  });
});

describe("loadLatestSnapshots for the monitors", () => {
  it("leaves out only Connect telemetry and a raw UCI snapshot", async () => {
    const payload = {
      hostname: "router-a",
      telegramReachability: { status: "reachable" },
      packageVersions: { "luci-app-passwall2": "26.8.10-r1" },
      connect: { entries: Array.from({ length: 300 }, (_, i) => ({ id: `e${i}` })) },
      rawSnapshot: { passwall2: "x".repeat(10_000) },
    };
    const { db } = createStaticDb(({ table }) =>
      table === routerInventorySnapshots
        ? [{ id: "snap", routerId: ROUTER_A, source: "check_in", payload, createdAt: new Date() }]
        : [],
    );
    const slim = (await loadLatestSnapshots(db as never, [ROUTER_A], { monitoringPayload: true })).get(ROUTER_A);
    const fullSnapshot = (await loadLatestSnapshots(db as never, [ROUTER_A])).get(ROUTER_A);
    expect(fullSnapshot?.payload).toEqual(payload);
    expect(slim?.payload).toEqual({
      hostname: payload.hostname,
      telegramReachability: payload.telegramReachability,
      packageVersions: payload.packageVersions,
    });
  });
});

describe("loadLatestFleetPolicyConfigSummaries with no cache room", () => {
  it("still gives every router its config for the tick that read it", async () => {
    vi.stubEnv("VECTRA_CONFIG_CACHE_MB", "0");
    vi.resetModules();
    try {
      const fresh = await import("./fleet-monitoring-data");
      // A fresh module graph has its own table objects.
      const freshSchema = await import("@vectra/db");
      const { db, configReads } = createDb([
        { id: "rev-a1", routerId: ROUTER_A, origin: "router_import", createdAt: new Date(1_000), config: liveConfig("node-pl2") },
        { id: "rev-b1", routerId: ROUTER_B, origin: "router_import", createdAt: new Date(1_000), config: liveConfig("node-de") },
      ], freshSchema.passwallDesiredRevisions);
      const summaries = await fresh.loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A, ROUTER_B]);
      expect([...summaries.keys()].sort()).toEqual([ROUTER_A, ROUTER_B]);
      // Nothing was cached, so the next tick reads both again.
      await fresh.loadLatestFleetPolicyConfigSummaries(db, [ROUTER_A, ROUTER_B]);
      expect(configReads).toHaveLength(2);
    } finally {
      vi.unstubAllEnvs();
      vi.resetModules();
    }
  });
});
