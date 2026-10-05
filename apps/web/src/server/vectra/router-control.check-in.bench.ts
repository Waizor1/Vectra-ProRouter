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
import { bench, describe, vi } from "vitest";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: Buffer.alloc(32).toString("base64"),
    VECTRA_POLLING_INTERVAL_SECONDS: "45",
    VECTRA_SNAPSHOT_HEARTBEAT_MINUTES: 30,
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);

const state = vi.hoisted(() => ({
  rowsFor: (() => []) as (table: unknown, params: Set<unknown>, ordered: boolean) => unknown[],
}));

vi.mock("~/server/db", () => {
  function paramValues(node: unknown, out: Set<unknown>, seen: WeakSet<object>) {
    if (!node || typeof node !== "object" || seen.has(node)) return out;
    seen.add(node);
    const record = node as Record<string, unknown>;
    if (record.constructor?.name === "Param") out.add(record.value);
    if (Array.isArray(record.queryChunks)) {
      for (const chunk of record.queryChunks) paramValues(chunk, out, seen);
    }
    return out;
  }
  const chainFor = (table: unknown) => {
    let params = new Set<unknown>();
    let ordered = false;
    const rows = () => state.rowsFor(table, params, ordered);
    const chain = {
      where(cond: unknown) {
        params = paramValues(cond, new Set(), new WeakSet());
        return chain;
      },
      orderBy() {
        ordered = true;
        return chain;
      },
      for: () => chain,
      limit: () => Promise.resolve(rows()),
      then: (ok: (v: unknown[]) => unknown, err?: (e: unknown) => unknown) =>
        Promise.resolve().then(rows).then(ok, err),
    };
    return chain;
  };
  const db = {
    select: () => ({ from: chainFor }),
    insert: () => ({
      values: (values: Record<string, unknown>) => ({
        onConflictDoNothing() {
          return this;
        },
        returning: () => Promise.resolve([values]),
        then: (ok: (v: unknown[]) => unknown) => Promise.resolve([]).then(ok),
      }),
    }),
    update: (table: unknown) => ({
      set: (set: Record<string, unknown>) => ({
        where: () => ({
          returning: () =>
            Promise.resolve(
              table === routers ? [{ ...state.rowsFor(routers, new Set(), false)[0] as object, ...set }] : [set],
            ),
          then: (ok: (v: unknown[]) => unknown) => Promise.resolve([]).then(ok),
        }),
      }),
    }),
    delete: () => ({ where: () => Promise.resolve([]) }),
    transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
  };
  return { db };
});

const { checkInRouter } = await import("./router-control");
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

state.rowsFor = (table, params, ordered) => {
  if (table === routers) return [routerRow];
  if (table === passwallDesiredRevisions) {
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

describe("checkInRouter, legacy PassWall agent, steady state (no jobs)", () => {
  bench(
    "check-in with two ~140 KB revisions",
    async () => {
      await checkInRouter(ROUTER_ID, structuredClone(checkInPayload));
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
console.log(
  "[bench] stored revision config ≈ %d KB, secret blob ≈ %d KB, response ≈ %d KB",
  Math.round(JSON.stringify(activeRow.config).length / 1024),
  Math.round(secretRows[1]!.ciphertext.length / 1024),
  Math.round(JSON.stringify(sample).length / 1024),
);
