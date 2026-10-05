import { readFileSync } from "node:fs";

import {
  passwallDesiredConfigSchema,
  routerCheckInRequestSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import {
  jobs,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routers,
} from "@vectra/db";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StaticDbQuery } from "./testing/static-db";

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
  rowsFor: (() => []) as (query: StaticDbQuery) => unknown[],
  queries: [] as StaticDbQuery[],
}));
vi.mock("~/server/db", async () => {
  const { createStaticDb } = await import("./testing/static-db");
  const { routers: routersTable } = await import("@vectra/db");
  const created = createStaticDb((query) => state.rowsFor(query), { routersTable });
  state.queries = created.queries;
  return { db: created.db };
});

const {
  checkInRouter,
  resetRevisionSummaryCacheForTest,
  routerAlreadyHoldsDesiredRevision,
  vctlIgnoresNullDesiredRevision,
} = await import("./router-control");
const { resetFleetNodeHealthCache } = await import("./fleet-node-health-cache");

/** What each controller really puts on the wire (see the Go contract tests). */
function fixture(path: string) {
  return JSON.parse(
    readFileSync(new URL(`../../../../../router/${path}`, import.meta.url), "utf8"),
  ) as {
    routerId: string;
    claim?: unknown;
    inventory: Record<string, unknown>;
  };
}
const VCTL_FIXTURE = "vectra-controller-pro/testdata/contract/check-in-request.json";
const AGENT_FIXTURE = "vectra-controller-agent/testdata/contract/check-in-request.json";

const ROUTER_ID = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf";
const XRAY_REVISION = "1a2b3c4d-0000-4000-8000-0000000000a1";
const PASSWALL_REVISION = "1a2b3c4d-0000-4000-8000-000000000042";

const xrayConfig = {
  schema: 1,
  instance: { name: "r" },
  process: {
    xrayBinary: "/usr/bin/xray",
    workDir: "/var/run/vectra-controller-pro",
    oomScoreAdj: -500,
    restartBackoff: { initialMs: 500, factor: 2, maxMs: 60_000 },
  },
  inbounds: {
    tproxy: { listenIP: "0.0.0.0", port: 12345, udpEnabled: true, sniffing: { enabled: true } },
  },
  geo: {
    assetDir: "/usr/share/v2ray",
    geoipUrl: "https://example.test/geoip.dat",
    geositeUrl: "https://example.test/geosite.dat",
    updateOnStart: false,
  },
};

const passwallConfig: PasswallDesiredConfig = passwallDesiredConfigSchema.parse({
  basicSettings: { main: { mainSwitch: true, selectedNodeId: "myshunt" }, dns: {}, log: {}, maintenance: {} },
  nodes: [{ id: "node-pl2", label: "PL2 :443", protocol: "vless", address: "pl2.example", port: 443 }],
  subscriptions: { items: [] },
  appUpdate: {},
  ruleManage: { geoipUrl: "https://example.test/geoip.dat", geositeUrl: "https://example.test/geosite.dat" },
});

function routerRow(engineMode: "passwall" | "xray-direct", revisionId: string) {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "vectra-07bf0887f662",
    displayName: null,
    hostname: "1111111111",
    panelDomain: null,
    status: "active",
    importState: "approved",
    engineMode,
    pendingImportRevisionId: null,
    activeRevisionId: revisionId,
    lastAppliedRevisionId: revisionId,
    lastConfigDigest: "digest",
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
}

function revisionRow(id: string, engineMode: "passwall" | "xray-direct") {
  return {
    id,
    routerId: ROUTER_ID,
    revisionNumber: 42,
    status: "approved",
    origin: "operator_draft",
    engineMode,
    configDigest: "digest",
    config: engineMode === "xray-direct" ? xrayConfig : passwallConfig,
    secretBlobId: null,
  };
}

let world: {
  router: ReturnType<typeof routerRow>;
  revision: ReturnType<typeof revisionRow>;
  queuedJobs: unknown[];
};

function revisionReads() {
  return state.queries.filter(
    (query) =>
      (query.table === passwallDesiredRevisions && query.params.has(world.revision.id)) ||
      query.table === passwallSecretBlobs,
  ).length;
}

/** A released vctl build (the fixture itself carries a dev version). */
const VCTL_RELEASE = "0.7.0-r20";

function payloadFrom(
  path: string,
  appliedRevisionId: string | null,
  controllerVersion?: string,
) {
  const raw = fixture(path);
  // The claim is irrelevant here and needs claim keys this test does not set.
  delete raw.claim;
  return {
    ...raw,
    routerId: ROUTER_ID,
    inventory: {
      ...raw.inventory,
      appliedRevisionId: appliedRevisionId ?? undefined,
      ...(controllerVersion ? { controllerVersion } : {}),
    },
  };
}

beforeEach(() => {
  resetRevisionSummaryCacheForTest();
  resetFleetNodeHealthCache();
  state.rowsFor = ({ table, params }) => {
    if (table === routers) return [world.router];
    if (table === passwallDesiredRevisions) {
      return params.has(world.revision.id) ? [world.revision] : [];
    }
    if (table === jobs) return world.queuedJobs;
    return [];
  };
});

describe("both controllers' real check-in payloads", () => {
  it("are accepted by the panel, and only vctl's says it is vctl", () => {
    const vctl = routerCheckInRequestSchema.parse(fixture(VCTL_FIXTURE));
    const agent = routerCheckInRequestSchema.parse(fixture(AGENT_FIXTURE));
    expect(vctl.inventory.engineMode).toBe("xray-direct");
    expect(agent.inventory.engineMode).toBeUndefined();
    expect(agent.inventory.appliedRevisionId).toBe(PASSWALL_REVISION);
  });
});

describe("check-in fast path: vctl that already runs the desired revision", () => {
  beforeEach(() => {
    world = {
      router: routerRow("xray-direct", XRAY_REVISION),
      revision: revisionRow(XRAY_REVISION, "xray-direct"),
      queuedJobs: [],
    };
  });

  it("answers without the revision and without reading it, otherwise identically", async () => {
    const full = await checkInRouter(ROUTER_ID, payloadFrom(VCTL_FIXTURE, null, VCTL_RELEASE));
    expect(full.desiredRevision?.id).toBe(XRAY_REVISION);

    state.queries.length = 0;
    const fast = await checkInRouter(ROUTER_ID, payloadFrom(VCTL_FIXTURE, XRAY_REVISION, VCTL_RELEASE));
    expect(fast.desiredRevision).toBeNull();
    expect(revisionReads()).toBe(0);

    // vctl's loop ignores a null desiredRevision (keeps its stored copy) and
    // nothing else in the answer differs.
    expect({ ...fast, desiredRevision: undefined }).toEqual({ ...full, desiredRevision: undefined });
  });

  it("still sends the revision to a vctl build not known to ignore null (dev, pre-0.6)", async () => {
    for (const version of [undefined /* the fixture's own 0.1.0-alpha */, "0.5.9-r40", "dev"]) {
      const response = await checkInRouter(ROUTER_ID, payloadFrom(VCTL_FIXTURE, XRAY_REVISION, version));
      expect(response.desiredRevision?.id).toBe(XRAY_REVISION);
    }
  });

  it("knows which vctl builds ignore a null desiredRevision", () => {
    expect(vctlIgnoresNullDesiredRevision("0.6.0-r1")).toBe(true);
    expect(vctlIgnoresNullDesiredRevision("0.6.0-r36")).toBe(true);
    expect(vctlIgnoresNullDesiredRevision("0.7.0-r20")).toBe(true);
    expect(vctlIgnoresNullDesiredRevision("1.0.0-r1")).toBe(true);
    expect(vctlIgnoresNullDesiredRevision("0.6.0-r0")).toBe(false);
    expect(vctlIgnoresNullDesiredRevision("0.5.9-r99")).toBe(false);
    expect(vctlIgnoresNullDesiredRevision("0.2.3")).toBe(false);
    expect(vctlIgnoresNullDesiredRevision("0.1.0-alpha")).toBe(false);
    expect(vctlIgnoresNullDesiredRevision("dev")).toBe(false);
    expect(vctlIgnoresNullDesiredRevision(undefined)).toBe(false);
  });

  it("still sends the revision when vctl reports another applied revision", async () => {
    const response = await checkInRouter(
      ROUTER_ID,
      payloadFrom(VCTL_FIXTURE, "1a2b3c4d-0000-4000-8000-0000000000ff", VCTL_RELEASE),
    );
    expect(response.desiredRevision?.id).toBe(XRAY_REVISION);
  });

  it("still sends the revision with any job, so a job never runs on vctl's stored copy", async () => {
    world.queuedJobs = [
      {
        id: "0d2c3b4a-5f6e-4d7c-9b8a-0f1e2d3c4b5a",
        routerId: ROUTER_ID,
        type: "apply_xray_config",
        state: "queued",
        payload: {},
        desiredRevisionId: XRAY_REVISION,
        dedupeKey: null,
        deliverAfter: null,
        deliveredAt: null,
        completedAt: null,
        createdAt: new Date(),
      },
    ];
    const response = await checkInRouter(ROUTER_ID, payloadFrom(VCTL_FIXTURE, XRAY_REVISION, VCTL_RELEASE));
    expect(response.jobs.map((job) => job.type)).toEqual(["apply_xray_config"]);
    expect(response.desiredRevision?.id).toBe(XRAY_REVISION);
  });
});

describe("legacy PassWall agent keeps today's behaviour", () => {
  beforeEach(() => {
    world = {
      router: routerRow("passwall", PASSWALL_REVISION),
      revision: revisionRow(PASSWALL_REVISION, "passwall"),
      queuedJobs: [],
    };
  });

  it("gets the full desired revision on every check-in, even when it applied it", async () => {
    for (let i = 0; i < 3; i += 1) {
      const response = await checkInRouter(ROUTER_ID, payloadFrom(AGENT_FIXTURE, PASSWALL_REVISION));
      expect(response.desiredRevision?.id).toBe(PASSWALL_REVISION);
      expect((response.desiredRevision?.config as PasswallDesiredConfig).nodes).toHaveLength(1);
    }
  });

  it("is never fast-pathed, whatever it reports", () => {
    const router = routerRow("xray-direct", XRAY_REVISION) as never;
    // A legacy agent woken on an xray router (dead-man hand-back) reports no engineMode.
    expect(
      routerAlreadyHoldsDesiredRevision({
        router,
        reportedEngineMode: undefined,
        reportedControllerVersion: "0.1.13-r42",
        reportedAppliedRevisionId: XRAY_REVISION,
        deliverableJobs: [],
      }),
    ).toBe(false);
    expect(
      routerAlreadyHoldsDesiredRevision({
        router: routerRow("passwall", PASSWALL_REVISION) as never,
        reportedEngineMode: "xray-direct",
        reportedControllerVersion: VCTL_RELEASE,
        reportedAppliedRevisionId: PASSWALL_REVISION,
        deliverableJobs: [],
      }),
    ).toBe(false);
  });
});
