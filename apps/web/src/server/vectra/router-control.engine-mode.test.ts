import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";
import { passwallDesiredRevisions, passwallSecretBlobs } from "@vectra/db";

import { resolveDesiredRevisionWithDb } from "./router-control";

// ---------------------------------------------------------------------------
// The engine guard in resolveDesiredRevisionWithDb is what made fleet.setEngineMode
// necessary: while `routers.engine_mode` had no writer it was pinned to its
// 'passwall' default, so an xray revision could never be delivered to anything.
// These tests pin both halves of the guard — that it still blocks a mismatch,
// and that it actually resolves once the two modes agree.
// ---------------------------------------------------------------------------

const ROUTER_ID = "5c2b7e10-9d4a-4f83-a1b6-0e73c5d8f924";
const PASSWALL_REVISION_ID = "1a2b3c4d-0000-4000-8000-000000000001";
const XRAY_REVISION_ID = "1a2b3c4d-0000-4000-8000-000000000002";

const passwallConfig = passwallDesiredConfigSchema.parse({
  basicSettings: {
    main: { mainSwitch: true, selectedNodeId: "node-pl1", nodeSocksPort: 1070 },
    dns: { remoteDns: "1.1.1.1", remoteDnsProtocol: "doh" },
    log: {},
    maintenance: {},
  },
  nodes: [
    {
      id: "node-pl1",
      label: "PL1 :443",
      remark: "PL1 :443",
      type: "Xray",
      protocol: "vless",
      address: "pl1.example.online",
      port: 443,
    },
  ],
  subscriptions: { items: [] },
  appUpdate: {},
  ruleManage: {
    geoipUrl: "https://example.test/geoip.dat",
    geositeUrl: "https://example.test/geosite.dat",
  },
});

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
    tproxy: {
      listenIP: "0.0.0.0",
      port: 12345,
      udpEnabled: true,
      sniffing: { enabled: true },
    },
  },
  geo: {
    assetDir: "/usr/share/v2ray",
    geoipUrl: "https://example.test/geoip.dat",
    geositeUrl: "https://example.test/geosite.dat",
    updateOnStart: false,
  },
};

function createRevisionRow(engineMode: "passwall" | "xray-direct") {
  const isXray = engineMode === "xray-direct";
  return {
    id: isXray ? XRAY_REVISION_ID : PASSWALL_REVISION_ID,
    routerId: ROUTER_ID,
    revisionNumber: isXray ? 8 : 7,
    origin: "operator_draft",
    status: "approved",
    engineMode,
    configDigest: isXray ? "digest-xray" : "digest-passwall",
    config: isXray ? xrayConfig : passwallConfig,
    note: null,
    rawImportedSnapshot: null,
    approvedAt: new Date("2026-04-08T09:00:00.000Z"),
    createdAt: new Date("2026-04-08T09:00:00.000Z"),
    updatedAt: new Date("2026-04-08T09:00:00.000Z"),
  };
}

function createRouterRow(
  engineMode: "passwall" | "xray-direct",
  activeRevisionId: string,
) {
  return {
    id: ROUTER_ID,
    importState: "approved",
    engineMode,
    activeRevisionId,
    lastAppliedRevisionId: null,
  };
}

// getRevisionSummaryWithDb reads the revision list (awaited straight after
// .orderBy()), then getSecretCiphertextForRevisionWithDb reads the secret blob
// (awaited after .limit()). Dispatch on the table so both shapes work; an empty
// blob result means "no ciphertext", and hydration returns the config as-is.
function createMockDb(revisionRows: unknown[]) {
  const makeChain = (rows: unknown[]) => {
    const chain = {
      where() {
        return chain;
      },
      orderBy() {
        return chain;
      },
      limit() {
        return Promise.resolve(rows);
      },
      then<TResult1 = unknown, TResult2 = never>(
        onfulfilled?:
          | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
          | null,
        onrejected?: ((reason: unknown) => TResult2 | PromiseLike<TResult2>) | null,
      ) {
        return Promise.resolve(rows).then(onfulfilled, onrejected);
      },
    };
    return chain;
  };

  return {
    select() {
      return {
        from(table: unknown) {
          if (table === passwallSecretBlobs) {
            return makeChain([]);
          }
          if (table === passwallDesiredRevisions) {
            return makeChain(revisionRows);
          }
          throw new Error("Unexpected table in resolveDesiredRevision mock.");
        },
      };
    },
  };
}

describe("resolveDesiredRevisionWithDb engine guard", () => {
  it("resolves a passwall revision for a passwall router", async () => {
    const summary = await resolveDesiredRevisionWithDb(
      createMockDb([createRevisionRow("passwall")]) as never,
      createRouterRow("passwall", PASSWALL_REVISION_ID) as never,
      [],
    );

    expect(summary?.id).toBe(PASSWALL_REVISION_ID);
    expect(summary?.engineMode).toBe("passwall");
  });

  it("resolves an xray revision once the router is switched to xray-direct", async () => {
    // This is the case fleet.setEngineMode unlocks. Before it existed,
    // routers.engineMode was stuck at 'passwall' and this returned null.
    const summary = await resolveDesiredRevisionWithDb(
      createMockDb([createRevisionRow("xray-direct")]) as never,
      createRouterRow("xray-direct", XRAY_REVISION_ID) as never,
      [],
    );

    expect(summary?.id).toBe(XRAY_REVISION_ID);
    expect(summary?.engineMode).toBe("xray-direct");
  });

  it("withholds an xray revision from a router still on passwall", async () => {
    const summary = await resolveDesiredRevisionWithDb(
      createMockDb([createRevisionRow("xray-direct")]) as never,
      createRouterRow("passwall", XRAY_REVISION_ID) as never,
      [],
    );

    expect(summary).toBeNull();
  });

  it("withholds a passwall revision from a router moved to xray-direct", async () => {
    // The flip side operators must understand: switching the engine stops
    // passwall delivery immediately, before any xray revision exists.
    const summary = await resolveDesiredRevisionWithDb(
      createMockDb([createRevisionRow("passwall")]) as never,
      createRouterRow("xray-direct", PASSWALL_REVISION_ID) as never,
      [],
    );

    expect(summary).toBeNull();
  });
});
