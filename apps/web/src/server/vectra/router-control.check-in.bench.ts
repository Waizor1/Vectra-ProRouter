/**
 * CPU cost of one router check-in, with realistic ~140 KB PassWall revisions.
 *
 *   SKIP_ENV_VALIDATION=1 DATABASE_URL=postgres://synthetic@localhost/none \
 *   VECTRA_SECRETS_KEY=$(head -c 32 /dev/zero | base64) \
 *   npx vitest bench --run src/server/vectra/router-control.check-in.bench.ts
 *
 * The database is a static in-memory stand-in that answers each table with the
 * same rows every time (selected by the ids in the WHERE clause), so what is
 * measured is the handler's own work: parsing, decrypting, diffing, building
 * and validating the response. Round-trips to Postgres are not in the number.
 */
import { passwallDesiredConfigSchema, type PasswallDesiredConfig } from "@vectra/contracts";
import {
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routers,
} from "@vectra/db";
import { readFileSync } from "node:fs";
import v8 from "node:v8";
import vm from "node:vm";

import { bench, describe, vi } from "vitest";

import type { StaticDbQuery } from "./testing/static-db";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: Buffer.alloc(32).toString("base64"),
    VECTRA_POLLING_INTERVAL_SECONDS: "45",
    VECTRA_SNAPSHOT_HEARTBEAT_MINUTES: 30,
    VECTRA_CONFIG_CACHE_MB: process.env.VECTRA_CONFIG_CACHE_MB,
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);

const state = vi.hoisted(() => ({
  rowsFor: (() => []) as (query: StaticDbQuery) => unknown[],
}));

vi.mock("~/server/db", async () => {
  const { createStaticDb } = await import("./testing/static-db");
  const { routers: routersTable } = await import("@vectra/db");
  return {
    db: createStaticDb((query) => state.rowsFor(query), { routersTable }).db,
  };
});

const { checkInRouter, revisionSummaryCacheStatsForTest } = await import("./router-control");
const { createSecretPayload, sanitizePasswallConfig } = await import("./secrets");

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const PREVIOUS_ID = "0c9a8b7d-6e5f-4a3b-8c2d-1e0f9a8b7c01";
const ACTIVE_ID = "0c9a8b7d-6e5f-4a3b-8c2d-1e0f9a8b7c02";

/** A PassWall config of roughly the size production routers carry. */
function syntheticConfig(seed: number): PasswallDesiredConfig {
  const nodes = Array.from({ length: 220 }, (_, i) => ({
    id: `node-${seed}-${i}`,
    label: `ru${i % 12}.provider.example :${50050 + (i % 5)} [${i}]`,
    protocol: "vless" as const,
    address: `ru${i % 12}.provider.example`,
    port: 50050 + (i % 5),
    password: `uuid-${seed}-${i}-0000-0000-000000000000`,
    transport: "tcp" as const,
    tls: true,
    tags: ["subscription", `group-${i % 7}`],
    extras: {
      flow: "xtls-rprx-vision",
      reality: "1",
      reality_publicKey: `pk${i}`.padEnd(43, "x"),
      reality_shortId: `${i}`.padStart(8, "0"),
      tls_serverName: "www.example-cdn.com",
      fingerprint: "chrome",
      group: "provider",
      add_mode: "2",
      add_from: "Vectra",
    },
  }));
  const shuntRules = ["WorldProxy", "YouTube", "Discord", "Special", "Tiktok", "Direct"].map(
    (label, r) => ({
      id: label,
      label,
      outboundNodeId: nodes[r]!.id,
      domainRules: Array.from({ length: 60 }, (_, d) => `domain:site-${r}-${d}.example.org`),
      ipRules: Array.from({ length: 10 }, (_, d) => `10.${r}.${d}.0/24`),
      extras: {},
    }),
  );
  return passwallDesiredConfigSchema.parse({
    basicSettings: {
      main: { mainSwitch: true, selectedNodeId: "myshunt" },
      dns: {},
      log: {},
      maintenance: {},
      shuntRules,
    },
    nodes,
    subscriptions: {
      items: [
        {
          id: "sub-1",
          remark: "Vectra",
          url: `https://sub.example/${seed}`,
          extras: { hwid: "1" },
        },
      ],
    },
    appUpdate: {},
    ruleManage: {
      geoipUrl: "https://example.test/geoip.dat",
      geositeUrl: "https://example.test/geosite.dat",
      shuntRules,
    },
  });
}

function revisionRow(id: string, revisionNumber: number, config: PasswallDesiredConfig, origin: string) {
  return {
    id,
    routerId: ROUTER_ID,
    revisionNumber,
    status: "approved",
    origin,
    engineMode: "passwall",
    configDigest: `digest-${revisionNumber}`,
    config: sanitizePasswallConfig(config),
    rawImportedSnapshot: null,
    createdBy: "operator",
    note: null,
    approvedAt: new Date("2026-10-01T00:00:00Z"),
    createdAt: new Date("2026-10-01T00:00:00Z"),
    // What the metadata query's correlated subquery returns.
    secretBlobId: `secret-${revisionNumber}`,
  };
}

const previousConfig = syntheticConfig(1);
const activeConfig = syntheticConfig(2);
const previousRow = revisionRow(PREVIOUS_ID, 41, previousConfig, "router_import");
const activeRow = revisionRow(ACTIVE_ID, 42, activeConfig, "operator_draft");
const secretRows = [
  { id: "secret-41", routerId: ROUTER_ID, desiredRevisionId: PREVIOUS_ID, scope: "router_import", ciphertext: createSecretPayload(previousConfig), createdAt: new Date() },
  { id: "secret-42", routerId: ROUTER_ID, desiredRevisionId: ACTIVE_ID, scope: "desired_revision", ciphertext: createSecretPayload(activeConfig), createdAt: new Date() },
];

const routerRow = {
  id: ROUTER_ID,
  deviceIdentifier: "vectra-07bf0887f662",
  displayName: null,
  hostname: "bench-router",
  panelDomain: "https://router.vectra-pro.net",
  model: "Xiaomi Mi Router AX3000T",
  boardName: "xiaomi,mi-router-ax3000t",
  target: "mediatek/filogic",
  architecture: "aarch64_cortex-a53",
  openwrtRelease: "24.10.6",
  status: "active",
  importState: "approved",
  controllerChannel: "stable",
  engineMode: "passwall",
  rolloutGroupId: null,
  pendingImportRevisionId: null,
  activeRevisionId: ACTIVE_ID,
  lastAppliedRevisionId: ACTIVE_ID,
  lastConfigDigest: "digest-live",
  approvedAt: new Date("2026-09-01T00:00:00Z"),
  lastSeenAt: new Date(),
  lastCheckInAt: new Date(),
  lastDirectModeAt: null,
  lastRescueReason: null,
  routePolicyExempt: null,
  routePolicyExemptReason: null,
  ownerRef: null,
  ownerLabel: null,
  claimCodeHash: null,
  claimExpiresAt: null,
  previousClaimCodeHash: null,
  previousClaimExpiresAt: null,
  claimedAt: null,
  releasedAt: null,
  createdAt: new Date("2026-09-01T00:00:00Z"),
  updatedAt: new Date("2026-09-01T00:00:00Z"),
};

// vctl (xray-direct) on its applied operator config revision.
const VCTL_ROUTER_ID = "7a1d3e5f-2b4c-4d6e-8f01-23456789abcd";
const XRAY_REVISION_ID = "0c9a8b7d-6e5f-4a3b-8c2d-1e0f9a8b7c03";
const vctlRouterRow = {
  ...routerRow,
  id: VCTL_ROUTER_ID,
  deviceIdentifier: "vectra-0a1b2c3d4e5f",
  engineMode: "xray-direct",
  activeRevisionId: XRAY_REVISION_ID,
  lastAppliedRevisionId: XRAY_REVISION_ID,
};
const xrayRevisionRow = {
  ...revisionRow(XRAY_REVISION_ID, 7, activeConfig, "operator_draft"),
  routerId: VCTL_ROUTER_ID,
  engineMode: "xray-direct",
  secretBlobId: null,
  config: {
    schema: 1,
    instance: { name: "r" },
    process: {
      xrayBinary: "/usr/bin/xray",
      workDir: "/var/run/vectra-controller-pro",
      oomScoreAdj: -500,
      restartBackoff: { initialMs: 500, factor: 2, maxMs: 60_000 },
    },
    inbounds: { tproxy: { listenIP: "0.0.0.0", port: 12345, udpEnabled: true, sniffing: { enabled: true } } },
    geo: {
      assetDir: "/usr/share/v2ray",
      geoipUrl: "https://example.test/geoip.dat",
      geositeUrl: "https://example.test/geosite.dat",
      updateOnStart: false,
    },
  },
};

// BENCH_HEAP_ROUTERS=N: N more PassWall routers, each with its own ~131 KB
// desired revision (sharing one ciphertext; every decrypt still builds its own
// objects), to measure what the check-in cache holds on the heap.
const heapRouters = new Map<string, { router: typeof routerRow; revision: typeof activeRow }>();
const heapRevisions = new Map<string, typeof activeRow>();
for (let i = 0; i < Number(process.env.BENCH_HEAP_ROUTERS ?? 0); i += 1) {
  const suffix = i.toString(16).padStart(12, "0");
  const routerId = `11111111-2222-4333-8444-${suffix}`;
  const revisionId = `aaaaaaaa-bbbb-4ccc-8ddd-${suffix}`;
  const revision = { ...activeRow, id: revisionId, routerId, secretBlobId: `secret-42` };
  heapRouters.set(routerId, {
    router: { ...routerRow, id: routerId, activeRevisionId: revisionId, lastAppliedRevisionId: revisionId },
    revision,
  });
  heapRevisions.set(revisionId, revision);
}

state.rowsFor = ({ table, params, ordered }) => {
  if (table === routers) {
    for (const value of params) {
      const heapRouter = typeof value === "string" ? heapRouters.get(value) : undefined;
      if (heapRouter) return [heapRouter.router];
    }
    return [params.has(VCTL_ROUTER_ID) ? vctlRouterRow : routerRow];
  }
  if (table === passwallDesiredRevisions) {
    for (const value of params) {
      const heapRevision = typeof value === "string" ? heapRevisions.get(value) : undefined;
      if (heapRevision) return [heapRevision];
    }
    if (params.has(XRAY_REVISION_ID)) return [xrayRevisionRow];
    if (params.has(ACTIVE_ID)) return [activeRow];
    if (params.has(PREVIOUS_ID)) return [previousRow];
    // "previous passwall revision" and "latest import per router" lookups.
    return ordered ? [previousRow] : [];
  }
  if (table === passwallSecretBlobs) {
    return secretRows.filter(
      (row) => params.has(row.desiredRevisionId) || params.has(row.id),
    );
  }
  return [];
};

const checkInPayload = {
  protocolVersion: "2026-04-v1",
  routerId: ROUTER_ID,
  inventory: {
    protocolVersion: "2026-04-v1",
    deviceIdentifier: "vectra-07bf0887f662",
    devicePublicKey: Buffer.alloc(32, 9).toString("base64"),
    controllerVersion: "0.1.13-r42",
    hostname: "bench-router",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    passwallEnabled: true,
    nodeCount: 220,
    subscriptionCount: 1,
    configDigest: "digest-live",
    appliedRevisionId: ACTIVE_ID,
    packageVersions: { "luci-app-passwall2": "26.8.10-r1" },
    binaryVersions: { xray: "26.3.27" },
    resources: { memoryTotalMb: 234, memoryAvailableMb: 90 },
    serviceHealth: { controller: "running", passwall: "running" },
  },
  health: { currentMode: "proxy" },
};

// vctl's own wire payload (its contract fixture), reporting the revision it runs.
const vctlFixture = JSON.parse(
  readFileSync(
    new URL("../../../../../router/vectra-controller-pro/testdata/contract/check-in-request.json", import.meta.url),
    "utf8",
  ),
) as { claim?: unknown; inventory: Record<string, unknown> };
delete vctlFixture.claim;
const vctlPayload = {
  ...vctlFixture,
  routerId: VCTL_ROUTER_ID,
  inventory: {
    ...vctlFixture.inventory,
    deviceIdentifier: "vectra-0a1b2c3d4e5f",
    controllerVersion: "0.7.0-r20",
    appliedRevisionId: XRAY_REVISION_ID,
  },
};

// Each case includes serializing the answer, as Response.json does.
describe("checkInRouter, steady state (no jobs)", () => {
  bench(
    "legacy PassWall agent, two ~140 KB revisions",
    async () => {
      JSON.stringify(await checkInRouter(ROUTER_ID, structuredClone(checkInPayload)));
    },
    { time: 3000, warmupTime: 500 },
  );
  bench(
    "vctl on its applied revision",
    async () => {
      JSON.stringify(await checkInRouter(VCTL_ROUTER_ID, structuredClone(vctlPayload)));
    },
    { time: 3000, warmupTime: 500 },
  );
});

// Sanity: the measured path really delivers the full revision (not a short
// circuit on a mock that answered nothing).
const sample = await checkInRouter(ROUTER_ID, structuredClone(checkInPayload));
const sampleConfig = sample.desiredRevision?.config as PasswallDesiredConfig | undefined;
if (sampleConfig?.nodes.length !== 220 || !sample.routePolicy) {
  throw new Error("bench fixture did not exercise the full revision path");
}
const vctlSample = await checkInRouter(VCTL_ROUTER_ID, structuredClone(vctlPayload));
console.log(
  "[bench] vctl answer carries desiredRevision: %s, response ≈ %d B",
  vctlSample.desiredRevision ? vctlSample.desiredRevision.id : "null",
  JSON.stringify(vctlSample).length,
);
console.log(
  "[bench] stored revision config ≈ %d KB, secret blob ≈ %d KB, response ≈ %d KB",
  Math.round(JSON.stringify(activeRow.config).length / 1024),
  Math.round(secretRows[1]!.ciphertext.length / 1024),
  Math.round(JSON.stringify(sample).length / 1024),
);

if (heapRouters.size > 0) {
  v8.setFlagsFromString("--expose_gc");
  const gc = vm.runInNewContext("gc") as () => void;
  const heapMb = () => {
    gc();
    return process.memoryUsage().heapUsed / 1024 / 1024;
  };
  const before = heapMb();
  for (let round = 0; round < 2; round += 1) {
    for (const [routerId] of heapRouters) {
      await checkInRouter(routerId, { ...structuredClone(checkInPayload), routerId });
    }
  }
  const after = heapMb();
  const stats = revisionSummaryCacheStatsForTest();
  console.log(
    "[bench] heap: %d routers x ~131 KB revisions, VECTRA_CONFIG_CACHE_MB=%s: heapUsed %s MB -> %s MB (+%s MB); cache holds %d routers, weighed %s MB",
    heapRouters.size,
    process.env.VECTRA_CONFIG_CACHE_MB ?? "16 (default)",
    before.toFixed(1),
    after.toFixed(1),
    (after - before).toFixed(1),
    stats.size,
    (stats.weight / 1024 / 1024).toFixed(1),
  );
}
