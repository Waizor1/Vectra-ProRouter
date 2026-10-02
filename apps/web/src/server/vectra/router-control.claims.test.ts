import {
  eventLog,
  healthIncidents,
  jobs,
  partnerWebhooks,
  passwallDesiredRevisions,
  routerCredentials,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { generateKeyPairSync, type KeyObject, sign } from "node:crypto";

import { beforeEach, describe, expect, it, vi } from "vitest";

import { buildTerminalRouterHostnameUpdatePayload } from "~/lib/router-hostname-jobs";
import * as dbModule from "~/server/db";

import {
  checkInRouter,
  recordJobResult,
  registerRouter,
  resolveRegisteredEngineMode,
  selectDeliverableJobsForCheckIn,
} from "./router-control";
import {
  hashClaimCode,
  registerProofMessage,
  sealClaimCodeHash,
} from "./router-claim-state";
import type { FakeDb } from "./testing/fake-db";

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});

const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const REVISION_ID = "0c9a8b7d-6e5f-4a3b-8c2d-1e0f9a8b7c6d";
const JOB_ID = "1d2c3b4a-5f6e-4d7c-9b8a-0f1e2d3c4b5a";
const DEVICE_KEY = Buffer.alloc(32, 9).toString("base64");
const CLAIM_PUBKEY = Buffer.alloc(32, 7).toString("base64");

/** A real ed25519 device key pair; the public half as vctl sends it. */
function deviceKeyPair() {
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  return {
    privateKey,
    publicKey: publicKey
      .export({ format: "der", type: "spki" })
      .subarray(12)
      .toString("base64"),
  };
}

/** A register proof as vctl makes it (claim.RegisterProof). */
function proofFor(
  privateKey: KeyObject,
  deviceIdentifier: string,
  timestamp = Math.floor(Date.now() / 1000),
) {
  return {
    timestamp,
    signature: sign(
      null,
      registerProofMessage(deviceIdentifier, timestamp),
      privateKey,
    ).toString("base64"),
  };
}

const PRECLAIMED_DEVICE = deviceKeyPair();

function inventory(overrides: Record<string, unknown> = {}) {
  return {
    protocolVersion: "2026-04-v1",
    deviceIdentifier: "vectra-07bf0887f662",
    devicePublicKey: DEVICE_KEY,
    controllerVersion: "0.4.0-r1",
    hostname: "1111111111",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    passwallEnabled: false,
    engineMode: "xray-direct",
    nodeCount: 0,
    subscriptionCount: 0,
    resources: {},
    serviceHealth: {},
    ...overrides,
  };
}

function routerRow(overrides: Record<string, unknown> = {}) {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "vectra-07bf0887f662",
    displayName: null,
    hostname: "1111111111",
    panelDomain: "https://router.vectra-pro.net",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "pending",
    importState: "awaiting_import",
    controllerChannel: "stable",
    engineMode: "xray-direct",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: null,
    lastSeenAt: new Date("2026-09-28T09:59:00.000Z"),
    lastCheckInAt: new Date("2026-09-28T09:59:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    ownerRef: null,
    ownerLabel: null,
    claimCodeHash: null,
    claimExpiresAt: null,
    previousClaimCodeHash: null,
    previousClaimExpiresAt: null,
    claimedAt: null,
    releasedAt: null,
    createdAt: new Date("2026-09-28T09:00:00.000Z"),
    updatedAt: new Date("2026-09-28T09:59:00.000Z"),
    ...overrides,
  };
}

function checkInPayload(extra: Record<string, unknown> = {}) {
  return {
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    inventory: inventory(),
    health: {},
    ...extra,
  };
}

beforeEach(() => {
  envMock.env = {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "router-control-claims-test-key-0123456789",
    VECTRA_POLLING_INTERVAL_SECONDS: "45",
    VECTRA_DEFAULT_CONTROL_DOMAIN: "https://router.vectra-pro.net",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET: "router-control-claims-webhook-0123456789",
  };
  fake.reset();
});

describe("registerRouter", () => {
  it("records engine_mode xray-direct for a NEW router that reports it", async () => {
    fake.reset({
      selects: [[routers, [[]]]],
      insertReturns: [[routers, [[routerRow()]]]],
    });

    const response = await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory(),
    });

    expect(fake.inserts(routers)[0]).toMatchObject({
      deviceIdentifier: "vectra-07bf0887f662",
      engineMode: "xray-direct",
    });
    expect(response.pendingApproval).toBe(true);
    expect(response.owner).toBeNull();
  });

  it("keeps a new router that reports no engine on passwall", async () => {
    fake.reset({
      selects: [[routers, [[]]]],
      insertReturns: [[routers, [[routerRow({ engineMode: "passwall" })]]]],
    });

    await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory({ engineMode: undefined }),
    });

    expect(fake.inserts(routers)[0]).toMatchObject({ engineMode: "passwall" });
    expect(resolveRegisteredEngineMode(undefined)).toBe("passwall");
    expect(resolveRegisteredEngineMode("xray-direct")).toBe("xray-direct");
  });

  it("never flips the engine of an existing router on re-registration", async () => {
    const existing = routerRow({ engineMode: "passwall" });
    fake.reset({
      selects: [[routers, [[existing]]]],
      updateReturns: [[routers, [[existing]]]],
    });

    await registerRouter(
      { protocolVersion: "2026-04-v1", inventory: inventory() },
      { authenticatedRouterId: ROUTER_ID },
    );

    expect(fake.updates(routers)[0]).not.toHaveProperty("engineMode");
  });

  it("carries the claim key, bot and owner in the answer", async () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = CLAIM_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "VectraConnectBot";
    fake.reset({
      selects: [[routers, [[]]]],
      insertReturns: [[routers, [[routerRow()]]]],
    });

    const response = await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory(),
    });

    expect(response).toMatchObject({
      claimKey: { kid: 1, publicKey: CLAIM_PUBKEY },
      botUsername: "VectraConnectBot",
      owner: null,
    });
  });

  const preclaimed = () =>
    routerRow({
      lastSeenAt: null,
      lastCheckInAt: null,
      approvedAt: new Date("2026-09-28T09:30:00.000Z"),
      importState: "approved",
      ownerRef: "acct-42",
      ownerLabel: "iv***",
    });
  const reservedCredential = {
    type: "bootstrap",
    devicePublicKey: PRECLAIMED_DEVICE.publicKey,
    revokedAt: null,
  };

  it("lets a router that PROVES it holds the key register into its pre-claimed record", async () => {
    const record = preclaimed();
    fake.reset({
      selects: [
        [routers, [[record]]],
        [routerCredentials, [[reservedCredential]]],
      ],
      updateReturns: [
        [routers, [[{ ...record, status: "active", lastSeenAt: new Date() }]]],
      ],
    });

    const response = await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory({ devicePublicKey: PRECLAIMED_DEVICE.publicKey }),
      proof: proofFor(PRECLAIMED_DEVICE.privateKey, record.deviceIdentifier),
    });

    expect(response).toMatchObject({
      routerId: ROUTER_ID,
      status: "active",
      pendingApproval: false,
      owner: { label: "iv***" },
    });
    expect(fake.updates(routers)[0]).toMatchObject({ status: "active" });
    expect(fake.inserts(eventLog)[0]).toMatchObject({
      type: "router.registered",
      metadata: { enrollmentMode: "partner_preclaimed" },
    });
    // The reserved credential is revoked and a real token issued.
    expect(fake.updates(routerCredentials)[0]?.revokedAt).toBeInstanceOf(Date);
    expect(fake.inserts(routerCredentials)[0]).toMatchObject({
      type: "agent_token",
      devicePublicKey: PRECLAIMED_DEVICE.publicKey,
    });
  });

  const other = deviceKeyPair();
  it.each([
    ["no proof", PRECLAIMED_DEVICE.publicKey, () => undefined, "proof_missing"],
    [
      "a signature that is not the key's",
      PRECLAIMED_DEVICE.publicKey,
      () => ({
        timestamp: Math.floor(Date.now() / 1000),
        signature: Buffer.alloc(64, 1).toString("base64"),
      }),
      "proof_invalid",
    ],
    [
      "a stale timestamp",
      PRECLAIMED_DEVICE.publicKey,
      () =>
        proofFor(
          PRECLAIMED_DEVICE.privateKey,
          "vectra-07bf0887f662",
          Math.floor(Date.now() / 1000) - 301,
        ),
      "proof_stale",
    ],
    [
      "a proof by another key",
      PRECLAIMED_DEVICE.publicKey,
      () => proofFor(other.privateKey, "vectra-07bf0887f662"),
      "proof_invalid",
    ],
    [
      "another key, even with its own valid proof",
      other.publicKey,
      () => proofFor(other.privateKey, "vectra-07bf0887f662"),
      "key_mismatch",
    ],
  ])(
    "refuses to hand the pre-claimed record to a router with %s (403, never adoption)",
    async (_name, presentedKey, proof, reason) => {
      fake.reset({
        selects: [
          [routers, [[preclaimed()]]],
          [routerCredentials, [[reservedCredential]]],
        ],
      });

      await expect(
        registerRouter({
          protocolVersion: "2026-04-v1",
          inventory: inventory({ devicePublicKey: presentedKey }),
          proof: proof(),
        }),
      ).rejects.toMatchObject({ status: 403 });

      expect(fake.updates(routers)).toEqual([]);
      expect(fake.inserts(routerCredentials)).toEqual([]);
      expect(fake.inserts(eventLog)[0]).toMatchObject({
        type: "router.reregister_blocked",
        metadata: { preclaimAdoption: reason },
      });
    },
  );

  it("never needs a proof for an ordinary new router, and ignores a malformed one", async () => {
    fake.reset({
      selects: [[routers, [[]]]],
      insertReturns: [[routers, [[routerRow()]]]],
    });

    const response = await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory(),
      // Older vctl sends none; a garbled one must not fail the registration.
      proof: { timestamp: "soon", signature: 42 },
    });

    expect(response.routerId).toBe(ROUTER_ID);
    expect(fake.inserts(routers)).toHaveLength(1);
  });

  it("tells a released router so when it registers again", async () => {
    const released = routerRow({
      releasedAt: new Date("2026-09-28T09:30:00.000Z"),
    });
    fake.reset({
      selects: [[routers, [[released]]]],
      updateReturns: [[routers, [[released]]]],
    });

    const response = await registerRouter(
      { protocolVersion: "2026-04-v1", inventory: inventory() },
      { authenticatedRouterId: ROUTER_ID },
    );

    expect(response).toMatchObject({ owner: null, released: true });
    // Registration is not a claim: it must not end the release.
    expect(fake.updates(routers)[0]).not.toHaveProperty("releasedAt");
  });
});

describe("checkInRouter claim", () => {
  function scriptCheckIn(router = routerRow()) {
    fake.reset({
      selects: [
        [routers, [[router]]],
        [healthIncidents, [[]]],
        [jobs, [[]]],
      ],
      updateReturns: [[routers, [[router]]]],
    });
  }

  it("persists measured Connect telemetry and emits observed transitions on real check-in", async () => {
    const router = routerRow({ownerRef: "acct-42", approvedAt: new Date(), status: "active"});
    fake.reset({selects: [[routers, [[router]]], [routerInventorySnapshots, [[{payload: inventory({connect: {verdict: "ok"}})}]]], [healthIncidents, [[]]], [jobs, [[]]]], updateReturns: [[routers, [[router], [router]]]]});
    await checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory({connect: {verdict: "down", exitCountry: null, lanClients: 2}})}));
    expect(fake.inserts(routerInventorySnapshots)[0]).toMatchObject({payload: {connect: {verdict: "down", lanClients: 2}}});
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual(expect.arrayContaining([expect.objectContaining({event: "router.vpn_down", ownerRef: "acct-42", detail: {verdict: "down"}})]));
  });

  it("binds confidential telemetry to the authenticated device key", async () => {
    scriptCheckIn(routerRow({ownerRef: "acct-42"}));
    await expect(checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory({connect: {ownerRef: "acct-42", wifi: [{band: "5G", ssid: "Fake guest", password: "fake-guest-pass-123"}]}})}), {devicePublicKey: Buffer.alloc(32, 8).toString("base64")})).rejects.toThrow("device identity mismatch");
    expect(fake.inserts(routerInventorySnapshots)).toEqual([]);
  });

  it("encrypts confidential wire telemetry before real check-in persistence", async () => {
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), status: "active"});
    fake.reset({selects: [[routers, [[owner], [owner]]], [healthIncidents, [[]]], [jobs, [[]]]], updateReturns: [[routers, [[owner], [owner]]]]});
    await checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory({connect: {ownerRef: "acct-42", wifi: [{band: "5G", ssid: "Fake guest", password: "fake-guest-pass-123"}]}})}), {devicePublicKey: DEVICE_KEY});
    const stored = fake.inserts(routerInventorySnapshots)[0];
    expect(JSON.stringify(stored)).not.toContain("fake-guest-pass-123");
    expect(stored?.connectSecretCiphertext).toBeTruthy();
  });

  it("hydrates a current-owner typed WiFi job only on authenticated delivery", async () => {
    const {protectPartnerParams} = await import("./partner-router-secrets");
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const payload = {origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "set_wifi", ...protectPartnerParams("set_wifi", {ssid: "Fake guest", password: "fake-guest-pass-123"}, {routerId: ROUTER_ID, ownerRef: "acct-42", actionId: JOB_ID})};
    fake.reset({selects: [[routers, [[owner]]], [healthIncidents, [[]]], [jobs, [[{id: JOB_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "queued", desiredRevisionId: null, payload, createdAt: new Date()}]]]], updateReturns: [[routers, [[owner], [owner], [owner]]]]});
    const response = await checkInRouter(ROUTER_ID, checkInPayload());
    expect(response.jobs[0]?.payload).toEqual({origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "set_wifi", params: {ssid: "Fake guest", password: "fake-guest-pass-123"}});
    expect(JSON.stringify(fake.calls.map(({table: _table, ...call}) => call))).not.toContain("fake-guest-pass-123");
  });

  it("stores the latest claim the router reports", async () => {
    scriptCheckIn();
    const codeHash = hashClaimCode("7KQ4M9XD");

    const response = await checkInRouter(
      ROUTER_ID,
      checkInPayload({
        claim: { codeHash: codeHash.toUpperCase(), expiresAt: "2026-09-28T10:12:00Z" },
      }),
    );

    expect(fake.updates(routers)[0]).toMatchObject({
      claimCodeHash: sealClaimCodeHash(codeHash),
      claimExpiresAt: new Date("2026-09-28T10:12:00Z"),
    });
    expect(response.owner).toBeNull();
  });

  it("keeps the code a new one replaced, with its own expiry (the 2-minute grace)", async () => {
    const replacedExpiry = new Date("2026-09-28T10:02:00.000Z");
    const showing = routerRow({
      claimCodeHash: sealClaimCodeHash(hashClaimCode("7KQ4M9XD")),
      claimExpiresAt: replacedExpiry,
    });
    scriptCheckIn(showing);
    // Stay inside the replaced code's life, whatever the wall clock says.
    vi.useFakeTimers({ now: new Date("2026-09-28T10:00:30.000Z"), toFake: ["Date"] });
    try {
      await checkInRouter(
        ROUTER_ID,
        checkInPayload({
          claim: {
            codeHash: hashClaimCode("HJKMNPQR"),
            expiresAt: "2026-09-28T10:12:00Z",
          },
        }),
      );
    } finally {
      vi.useRealTimers();
    }

    expect(fake.updates(routers)[0]).toMatchObject({
      claimCodeHash: sealClaimCodeHash(hashClaimCode("HJKMNPQR")),
      claimExpiresAt: new Date("2026-09-28T10:12:00Z"),
      previousClaimCodeHash: sealClaimCodeHash(hashClaimCode("7KQ4M9XD")),
      previousClaimExpiresAt: replacedExpiry,
    });
  });

  it("clears the stored claim once the router reports none", async () => {
    scriptCheckIn(routerRow({ claimCodeHash: "sealed-earlier" }));

    await checkInRouter(ROUTER_ID, checkInPayload({ claim: null }));

    expect(fake.updates(routers)[0]).toMatchObject({
      claimCodeHash: null,
      claimExpiresAt: null,
      previousClaimCodeHash: null,
      previousClaimExpiresAt: null,
    });
  });

  it("tells a claimed router who owns it", async () => {
    scriptCheckIn(routerRow({ ownerRef: "acct-42", ownerLabel: "iv***" }));

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.owner).toEqual({ ownerRef: "acct-42", label: "iv***" });
    expect(response).not.toHaveProperty("released");
  });

  it("tells a released router so, until someone claims it", async () => {
    scriptCheckIn(
      routerRow({ releasedAt: new Date("2026-09-28T09:30:00.000Z") }),
    );

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.owner).toBeNull();
    expect(response.released).toBe(true);
  });

  it("never sends released to an ordinary fleet router", async () => {
    scriptCheckIn(routerRow({ engineMode: "passwall" }));

    const response = await checkInRouter(
      ROUTER_ID,
      checkInPayload({ inventory: inventory({ engineMode: undefined }) }),
    );

    expect(response).not.toHaveProperty("released");
    expect(JSON.parse(JSON.stringify(response))).not.toHaveProperty("released");
  });
});

describe("recordJobResult reports a claim's first apply to the backend", () => {
  function scriptResult(options: { type?: string; payload?: Record<string, unknown>; ownerRef?: string | null } = {}) {
    const router = routerRow({
      ownerRef: options.ownerRef === undefined ? "acct-42" : options.ownerRef,
      approvedAt: new Date(),
      importState: "approved",
      status: "active",
    });
    fake.reset({
      selects: [
        [
          jobs,
          [
            [
              {
                id: JOB_ID,
                routerId: ROUTER_ID,
                type: options.type ?? "apply_xray_config",
                state: "running",
                payload: options.payload ?? {
                  desiredRevisionId: REVISION_ID,
                  origin: "partner_claim",
                },
                desiredRevisionId: options.type ? null : REVISION_ID,
                dedupeKey: `apply:${ROUTER_ID}:${REVISION_ID}`,
                deliveredAt: new Date(),
                createdAt: new Date(),
              },
            ],
          ],
        ],
        [routers, [[router]]],
        [
          passwallDesiredRevisions,
          [[{ id: REVISION_ID, engineMode: "xray-direct", configDigest: "d", config: {} }]],
        ],
      ],
      updateReturns: [[routers, [[router]]]],
    });
  }

  const result = (status: "accepted" | "success" | "failure", extra = {}) => ({
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    jobId: JOB_ID,
    status,
    ...extra,
  });

  it("routes native partner action results through the real job-result handler", async () => {
    scriptResult({type: "reload_xray_outbound", payload: {origin: "partner_action", actionId: JOB_ID, ownerRef: "acct-42", action: "restart_vpn"}});
    await recordJobResult(ROUTER_ID, result("success"));
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual([expect.objectContaining({event: "router.action", ownerRef: "acct-42", detail: {actionId: JOB_ID, state: "applied"}})]);
  });

  it("never persists arbitrary partner action output or echoed credentials", async () => {
    scriptResult({type: "connect_router_action", payload: {origin: "partner_action", actionId: JOB_ID, ownerRef: "acct-42", action: "set_wifi"}});
    await recordJobResult(ROUTER_ID, result("failure", {stdout: "fake-secret-echo-123", stderr: "fake-secret-echo-123", result: {password: "fake-secret-echo-123"}}));
    expect(JSON.stringify(fake.calls.map(({table: _table, ...call}) => call))).not.toContain("fake-secret-echo-123");
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual([expect.objectContaining({event: "router.action", detail: {actionId: JOB_ID, state: "failed"}})]);
  });

  it("queues router.ready when it succeeded", async () => {
    scriptResult();

    await recordJobResult(ROUTER_ID, result("success", { appliedRevisionId: REVISION_ID }));

    expect(fake.inserts(partnerWebhooks).map((row) => row.payload)).toEqual([
      expect.objectContaining({
        event: "router.ready",
        routerId: ROUTER_ID,
        ownerRef: "acct-42",
      }),
    ]);
  });

  it("queues router.failed with the router's reason when it failed", async () => {
    scriptResult();

    await recordJobResult(
      ROUTER_ID,
      result("failure", { result: { error: "provider config: http 403" } }),
    );

    expect(fake.inserts(partnerWebhooks).map((row) => row.payload)).toEqual([
      expect.objectContaining({
        event: "router.failed",
        detail: "provider config: http 403",
      }),
    ]);
  });

  it("stays quiet for an operator's apply, an ack, or an unowned router", async () => {
    scriptResult({ payload: { desiredRevisionId: REVISION_ID } });
    await recordJobResult(ROUTER_ID, result("success"));
    expect(fake.inserts(partnerWebhooks)).toEqual([]);

    scriptResult();
    await recordJobResult(ROUTER_ID, result("accepted"));
    expect(fake.inserts(partnerWebhooks)).toEqual([]);

    scriptResult({ ownerRef: null });
    await recordJobResult(ROUTER_ID, result("success"));
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });
});

describe("selectDeliverableJobsForCheckIn — rename on xray-direct", () => {
  const hostnameJob = {
    id: "rename",
    type: "run_terminal_command",
    state: "queued",
    payload: buildTerminalRouterHostnameUpdatePayload("new-name"),
  };
  const operatorShell = {
    id: "shell",
    type: "run_terminal_command",
    state: "queued",
    payload: { command: "uptime", timeoutSeconds: 30 },
  };

  it("delivers fleet.renameRouter's hostname job to an xray-direct router", () => {
    const deliverable = selectDeliverableJobsForCheckIn(
      "approved",
      [hostnameJob, operatorShell] as never,
      "xray-direct",
    );
    expect(deliverable.map((job) => job.id)).toEqual(["rename"]);
  });

  it("delivers it to a pending xray-direct router too (rename needs no approval)", () => {
    expect(
      selectDeliverableJobsForCheckIn(
        "awaiting_import",
        [hostnameJob] as never,
        "xray-direct",
      ).map((job) => job.id),
    ).toEqual(["rename"]);
  });

  it("leaves PassWall delivery as it was", () => {
    expect(
      selectDeliverableJobsForCheckIn(
        "approved",
        [hostnameJob, operatorShell] as never,
        "passwall",
      ).map((job) => job.id),
    ).toEqual(["rename", "shell"]);
  });
});
