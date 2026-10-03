import {
  eventLog,
  healthIncidents,
  jobResults,
  jobs,
  partnerWebhooks,
  passwallAppliedRevisions,
  passwallDesiredRevisions,
  routerCredentials,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { generateKeyPairSync, type KeyObject, sign } from "node:crypto";

import { beforeEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";

import { buildTerminalRouterHostnameUpdatePayload } from "~/lib/router-hostname-jobs";
import * as dbModule from "~/server/db";

import {
  buildRouterReauthMessage,
  checkInRouter,
  describeUndeliverableError,
  recordJobResult,
  registerRouter,
  resolveRegisteredEngineMode,
  selectDeliverableJobsForCheckIn,
  SUPPORT_SHELL_OFF_MESSAGE,
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

  describe("device key pinning of an owned router", () => {
    const owned = () => routerRow({ownerRef: "acct-42", approvedAt: new Date("2026-09-28T09:30:00.000Z"), importState: "approved", status: "active"});
    const onRecord = deviceKeyPair();
    const reauthProof = (privateKey: KeyObject, deviceIdentifier = "vectra-07bf0887f662") => {
      const signedAt = new Date().toISOString();
      return {signedAt, signature: sign(null, Buffer.from(buildRouterReauthMessage(deviceIdentifier, signedAt), "utf8"), privateKey).toString("base64")};
    };

    it("refuses a token re-registration that swaps the key of an owned router", async () => {
      const record = owned();
      fake.reset({selects: [[routers, [[record]]], [routerCredentials, [[{devicePublicKey: onRecord.publicKey}]]]], updateReturns: [[routers, [[record]]]]});

      await expect(registerRouter({protocolVersion: "2026-04-v1", inventory: inventory({devicePublicKey: DEVICE_KEY})}, {authenticatedRouterId: ROUTER_ID})).rejects.toMatchObject({status: 403});

      expect(fake.updates(routers)).toEqual([]);
      expect(fake.inserts(routerCredentials)).toEqual([]);
      expect(fake.inserts(eventLog)).toHaveLength(1);
      expect(fake.inserts(eventLog)[0]).toMatchObject({type: "router.device_key_change_blocked", metadata: {recoveryProofRejectReason: "missing_proof"}});
    });

    it("refuses a key swap whose proof is signed by the NEW key, not the one on record", async () => {
      const record = owned();
      const intruder = deviceKeyPair();
      fake.reset({selects: [[routers, [[record]]], [routerCredentials, [[{devicePublicKey: onRecord.publicKey}]]]], updateReturns: [[routers, [[record]]]]});

      await expect(registerRouter({protocolVersion: "2026-04-v1", inventory: inventory({devicePublicKey: intruder.publicKey}), recoveryProof: reauthProof(intruder.privateKey)}, {authenticatedRouterId: ROUTER_ID})).rejects.toMatchObject({status: 403});
      expect(fake.inserts(routerCredentials)).toEqual([]);
    });

    it("lets an owned router rotate its key with a proof signed by the key on record", async () => {
      const record = owned();
      const next = deviceKeyPair();
      fake.reset({selects: [[routers, [[record]]], [routerCredentials, [[{devicePublicKey: onRecord.publicKey}]]]], updateReturns: [[routers, [[record]]]]});

      const response = await registerRouter({protocolVersion: "2026-04-v1", inventory: inventory({devicePublicKey: next.publicKey}), recoveryProof: reauthProof(onRecord.privateKey)}, {authenticatedRouterId: ROUTER_ID});

      expect(response.routerId).toBe(ROUTER_ID);
      expect(fake.inserts(routerCredentials)).toEqual([expect.objectContaining({devicePublicKey: next.publicKey})]);
    });

    it("re-registers an owned router that keeps its key without any proof", async () => {
      const record = owned();
      fake.reset({selects: [[routers, [[record]]], [routerCredentials, [[{devicePublicKey: onRecord.publicKey}]]]], updateReturns: [[routers, [[record]]]]});

      await registerRouter({protocolVersion: "2026-04-v1", inventory: inventory({devicePublicKey: onRecord.publicKey})}, {authenticatedRouterId: ROUTER_ID});

      expect(fake.inserts(routerCredentials)).toEqual([expect.objectContaining({devicePublicKey: onRecord.publicKey})]);
    });

    it("does not pin the key of a router nobody owns", async () => {
      const record = routerRow();
      fake.reset({selects: [[routers, [[record]]], [routerCredentials, [[{devicePublicKey: onRecord.publicKey}]]]], updateReturns: [[routers, [[record]]]]});

      await registerRouter({protocolVersion: "2026-04-v1", inventory: inventory({devicePublicKey: DEVICE_KEY})}, {authenticatedRouterId: ROUTER_ID});

      expect(fake.inserts(routerCredentials)).toEqual([expect.objectContaining({devicePublicKey: DEVICE_KEY})]);
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

  // The agent re-sends its check-in (and import) until a 2xx: an oversized
  // field must be cut down, never refused.
  it("accepts a check-in with an oversized raw snapshot and logs the truncation", async () => {
    scriptCheckIn();

    await checkInRouter(
      ROUTER_ID,
      checkInPayload({
        inventory: inventory({ rawSnapshot: { blob: "x".repeat(70_000) } }),
      }),
    );

    expect(fake.inserts(routerInventorySnapshots)[0]).toMatchObject({
      payload: { rawSnapshot: { truncated: true } },
    });
    expect(fake.inserts(eventLog)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          type: "router.payload_truncated",
          metadata: { endpoint: "check_in", fields: ["inventory.rawSnapshot"] },
        }),
      ]),
    );
  });

  // A dropped import still counts as sent: asking for it again would have
  // the agent re-send >1 MB on every check-in and skip its self-heals.
  it("does not ask again for an import it dropped as oversized", async () => {
    scriptCheckIn(
      routerRow({ importState: "awaiting_import", engineMode: "passwall" }),
    );

    const response = await checkInRouter(
      ROUTER_ID,
      checkInPayload({
        inventory: inventory({ engineMode: "passwall" }),
        passwallImport: {
          config: { blob: "x".repeat(1024 * 1024 + 1) },
          configDigest: "big-digest",
        },
      }),
    );

    expect(response.configSyncState.requestImport).toBe(false);
  });

  it("logs the same truncation at most once an hour", async () => {
    const oversized = checkInPayload({
      inventory: inventory({
        configDigest: "digest-throttle",
        rawSnapshot: { blob: "x".repeat(70_000) },
      }),
    });
    scriptCheckIn();
    await checkInRouter(ROUTER_ID, oversized);
    const first = fake
      .inserts(eventLog)
      .filter((row) => row.type === "router.payload_truncated");
    scriptCheckIn();
    await checkInRouter(ROUTER_ID, oversized);
    const second = fake
      .inserts(eventLog)
      .filter((row) => row.type === "router.payload_truncated");

    expect(first).toHaveLength(1);
    expect(second).toHaveLength(0);
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
    const job = {id: JOB_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "queued", desiredRevisionId: null, payload, createdAt: new Date()};
    // The second read is the re-read under the router lock.
    fake.reset({selects: [[routers, [[owner]]], [healthIncidents, [[]]], [jobs, [[job], [job]]]], updateReturns: [[routers, [[owner], [owner], [owner]]]]});
    const response = await checkInRouter(ROUTER_ID, checkInPayload());
    expect(response.jobs[0]?.payload).toEqual({origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "set_wifi", params: {ssid: "Fake guest", password: "fake-guest-pass-123"}});
    expect(JSON.stringify(fake.calls.map(({table: _table, ...call}) => call))).not.toContain("fake-guest-pass-123");
  });

  // Live 2026-10-03: auto-rescue queued collect_router_logs (a PassWall agent
  // job) for 1111 on vctl; it sat in `queued` forever, filled the delivery
  // candidates and blocked the engine switch. vctl reports xray-direct, so it
  // is failed at check-in with engine_mismatch and owner actions still go out.
  it("fails a job vctl can never run and still delivers the owner's action", async () => {
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active", engineMode: "xray-direct"});
    const STUCK_ID = "3f4e5d6c-7b8a-4f9e-8d0c-2b1a0f3e4d5c";
    const stuck = {id: STUCK_ID, routerId: ROUTER_ID, type: "collect_router_logs", state: "queued", desiredRevisionId: null, dedupeKey: "auto_rescue_logs:case-1", createdAt: new Date(Date.now() - 60_000), payload: {source: "all", lines: 200}};
    const action = {id: JOB_ID, routerId: ROUTER_ID, type: "reload_xray_outbound", state: "queued", desiredRevisionId: null, createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "restart_vpn"}};
    fake.reset({selects: [[routers, [[owner], [owner]]], [healthIncidents, [[]]], [jobs, [[stuck, action], [action], [{...stuck, state: "running"}]]]], updateReturns: [[routers, [[owner], [owner], [owner], [owner]]], [jobs, [[{...action}], [{...stuck, state: "running"}]]]]});

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.jobs.map(job => job.id)).toEqual([JOB_ID]);
    expect(fake.inserts(eventLog)).toEqual(expect.arrayContaining([expect.objectContaining({type: "job.undeliverable", metadata: {jobId: STUCK_ID, jobType: "collect_router_logs", code: "engine_mismatch"}})]));
    expect(fake.updates(jobs).map(update => update.state).filter(Boolean)).toEqual(["running", "failed"]);
  });

  // Live 2026-10-03 (artem-lutfulin): an operator's support command never
  // reached vctl; it runs it when the owner allows the support shell, which
  // vctl reports as inventory remoteShell on every check-in.
  describe("operator support shell on vctl", () => {
    const SHELL_ID = "4a5b6c7d-8e9f-4a0b-9c1d-2e3f4a5b6c7d";
    const LOGS_ID = "5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e";
    const owner = () => routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active", engineMode: "xray-direct"});
    const shell = {id: SHELL_ID, routerId: ROUTER_ID, type: "run_terminal_command", state: "queued", desiredRevisionId: null, dedupeKey: `run_terminal_command:${ROUTER_ID}`, createdAt: new Date(Date.now() - 30_000), payload: {command: "uptime", timeoutSeconds: 30}};
    const logs = {id: LOGS_ID, routerId: ROUTER_ID, type: "collect_router_logs", state: "queued", desiredRevisionId: null, dedupeKey: "auto_rescue_logs:case-2", createdAt: new Date(Date.now() - 60_000), payload: {source: "all", lines: 200}};
    const undeliverable = () => fake.inserts(eventLog).filter(event => event.type === "job.undeliverable");

    it("delivers it when remoteShell is true; a PassWall job still fails as before", async () => {
      const router = owner();
      fake.reset({selects: [[routers, [[router], [router]]], [healthIncidents, [[]]], [jobs, [[logs, shell], [{...logs, state: "running"}]]]], updateReturns: [[routers, [[router], [router], [router]]], [jobs, [[{...logs, state: "running"}]]]]});

      const response = await checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory({remoteShell: true})}));

      expect(response.jobs.map(job => job.id)).toEqual([SHELL_ID]);
      expect(undeliverable()).toEqual([expect.objectContaining({
        message: `Job ${LOGS_ID} (collect_router_logs) cannot run on this router's engine and was failed.`,
        metadata: {jobId: LOGS_ID, jobType: "collect_router_logs", code: "engine_mismatch"},
      })]);
    });

    it.each([
      ["false", {remoteShell: false}],
      ["absent", {}],
    ])("fails it with engine_mismatch and a shell-off message when remoteShell is %s", async (_label, extra) => {
      const router = owner();
      fake.reset({selects: [[routers, [[router], [router]]], [healthIncidents, [[]]], [jobs, [[shell], [{...shell, state: "running"}]]]], updateReturns: [[routers, [[router], [router], [router]]], [jobs, [[{...shell, state: "running"}]]]]});

      const response = await checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory(extra)}));

      expect(response.jobs).toEqual([]);
      const events = undeliverable();
      expect(events).toHaveLength(1);
      expect(events[0]?.metadata).toEqual({jobId: SHELL_ID, jobType: "run_terminal_command", code: "engine_mismatch"});
      expect(String(events[0]?.message)).toContain("owner has the support shell switched off");
      // The result reads as a terminal result, so the operator's history shows why.
      const results = fake.inserts(jobResults);
      expect(results).toHaveLength(1);
      expect(results[0]).toMatchObject({jobId: SHELL_ID, status: "failure"});
      expect(results[0]?.payload).toMatchObject({code: "engine_mismatch", error: SUPPORT_SHELL_OFF_MESSAGE, command: "uptime", timeoutSeconds: 30});
      expect(fake.updates(jobs).map(update => update.state).filter(Boolean)).toEqual(["running", "failed"]);
    });

    it("leaves it queued for vctl when a legacy agent (hand-back) checks in", async () => {
      const router = owner();
      fake.reset({selects: [[routers, [[router], [router]]], [healthIncidents, [[]]], [jobs, [[shell]]]], updateReturns: [[routers, [[router], [router], [router]]]]});

      const response = await checkInRouter(ROUTER_ID, checkInPayload({inventory: inventory({engineMode: undefined, remoteShell: true})}));

      expect(response.jobs).toEqual([]);
      expect(undeliverable()).toEqual([]);
      expect(fake.updates(jobs)).toEqual([]);
    });
  });

  it("fails one undecryptable owner job and still delivers the rest of the check-in", async () => {
    const {protectPartnerParams} = await import("./partner-router-secrets");
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const BROKEN_ID = "2e3d4c5b-6a7f-4e8d-8c9b-1a0f2e3d4c5b";
    const good = {id: JOB_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "queued", desiredRevisionId: null, createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "set_wifi", ...protectPartnerParams("set_wifi", {ssid: "Fake guest", password: "fake-guest-pass-123"}, {routerId: ROUTER_ID, ownerRef: "acct-42", actionId: JOB_ID})}};
    // A ciphertext that no longer opens (key rotated, row corrupted).
    const broken = {id: BROKEN_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "queued", desiredRevisionId: null, createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: BROKEN_ID, action: "set_wifi", paramsCiphertext: "{\"v\":2,\"iv\":\"AAAA\",\"tag\":\"AAAA\",\"data\":\"AAAA\"}"}};
    // The failure is recorded through the router's own result path, which
    // reads the job and the router again.
    fake.reset({selects: [[routers, [[owner], [owner]]], [healthIncidents, [[]]], [jobs, [[broken, good], [broken, good], [{...broken, state: "running"}]]]], updateReturns: [[routers, [[owner], [owner], [owner], [owner]]], [jobs, [[{...broken, state: "running"}]]]]});

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.jobs.map(job => job.id)).toEqual([JOB_ID]);
    // Only the delivered job is stamped; the broken one goes to the result path.
    expect(fake.updates(jobs).map(({deliveredAt, ...rest}) => ({...rest, stamped: deliveredAt instanceof Date}))).toEqual([
      {stamped: true},
      {state: "running", stamped: false},
      expect.objectContaining({state: "failed", stamped: true}),
    ]);
    expect(fake.updates(jobs)[2]?.completedAt).toBeInstanceOf(Date);
    expect(fake.inserts(eventLog)).toEqual(expect.arrayContaining([expect.objectContaining({type: "job.undeliverable", metadata: {jobId: BROKEN_ID, jobType: "connect_router_action", code: "payload_unavailable"}})]));
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual(expect.arrayContaining([expect.objectContaining({event: "router.action", ownerRef: "acct-42", detail: {actionId: BROKEN_ID, state: "failed", detail: "payload_unavailable"}})]));
    expect(JSON.stringify(fake.calls.map(({table: _table, ...call}) => call))).not.toContain("fake-guest-pass-123");
  });

  // Review 2026-10-02: a non-partner job that failed at check-in skipped the
  // result path, so a claim's first apply never told the backend it failed.
  it("fails an undeliverable claim apply through the result path: router.failed reaches the backend", async () => {
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const apply = {id: JOB_ID, routerId: ROUTER_ID, type: "apply_xray_config", state: "queued", desiredRevisionId: REVISION_ID, dedupeKey: `apply:${ROUTER_ID}:${REVISION_ID}`, deliveredAt: null, payload: {desiredRevisionId: REVISION_ID, origin: "partner_claim"}, createdAt: new Date(Number.NaN)};
    fake.reset({
      selects: [
        [routers, [[owner], [owner]]],
        [healthIncidents, [[]]],
        [jobs, [[apply], [{...apply, state: "running"}]]],
        // The check-in's revision summary finds none; the result path finds the revision.
        [passwallDesiredRevisions, [[], [{id: REVISION_ID, engineMode: "xray-direct", configDigest: "d", config: {}}]]],
      ],
      updateReturns: [[routers, [[owner], [owner], [owner]]], [jobs, [[{...apply, state: "running"}]]]],
    });
    const errors = vi.spyOn(console, "error").mockImplementation(() => undefined);

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.jobs).toEqual([]);
    expect(fake.updates(jobs).map(update => update.state)).toEqual(["running", "failed"]);
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual(expect.arrayContaining([expect.objectContaining({event: "router.failed", ownerRef: "acct-42", detail: "payload_unavailable"})]));
    // Logged: which job, and the error's class — nothing from the payload.
    expect(errors).toHaveBeenCalledWith("[router-control] job could not be prepared for delivery", {jobId: JOB_ID, jobType: "apply_xray_config", error: "RangeError"});
    errors.mockRestore();
  });

  it("logs only the class and the issue paths of a schema failure, never its message or data", () => {
    const parsed = z.object({password: z.number(), nested: z.object({ssid: z.number()})}).safeParse({password: "fake-guest-pass-123", nested: {ssid: "Fake guest"}});
    expect(parsed.success).toBe(false);
    const described = describeUndeliverableError(parsed.error);
    expect(described).toEqual({error: "ZodError", issuePaths: ["password", "nested.ssid"]});
    expect(JSON.stringify(described)).not.toMatch(/fake-guest-pass-123|Fake guest|Expected/);
    expect(describeUndeliverableError(new TypeError("fake-guest-pass-123"))).toEqual({error: "TypeError"});
  });

  // Review 2026-10-02 (HIGH): check-in never recorded a delivery, so a
  // partner's cancel could cancel a job the router had already received and
  // run. Now the delivery is stamped (the job stays queued for redelivery)
  // and the cancel sees it.
  it("stamps a delivered owner action without leaving queued, and the cancel then refuses it", async () => {
    const {cancelPartnerActionWithDb} = await import("./partner-routers");
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const job = {id: JOB_ID, routerId: ROUTER_ID, type: "reload_xray_outbound", state: "queued", deliveredAt: null, desiredRevisionId: null, dedupeKey: "partner-action:rb7-key", createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "restart_vpn", idempotencyKey: "rb7-key"}};
    fake.reset({selects: [[routers, [[owner]]], [healthIncidents, [[]]], [jobs, [[job], [job]]]], updateReturns: [[routers, [[owner], [owner], [owner]]]]});

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.jobs.map(item => item.id)).toEqual([JOB_ID]);
    const stamps = fake.updates(jobs);
    expect(stamps).toHaveLength(1);
    expect(stamps[0]).not.toHaveProperty("state");
    expect(stamps[0]?.deliveredAt).toBeInstanceOf(Date);

    // The cancel reads the job as the stamp left it.
    const stamped = {...job, ...stamps[0]};
    fake.reset({selects: [[jobs, [[stamped]]]], updateReturns: [[routers, [[owner]]]]});
    expect(await cancelPartnerActionWithDb(fake.db as never, {routerId: ROUTER_ID, ownerRef: "acct-42", idempotencyKey: "rb7-key"})).toMatchObject({status: 409, body: {state: "delivered"}});
    expect(fake.updates(jobs)).toEqual([]);
  });

  it("does not hand out an owner action cancelled after the candidates were read", async () => {
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const job = {id: JOB_ID, routerId: ROUTER_ID, type: "reload_xray_outbound", state: "queued", deliveredAt: null, desiredRevisionId: null, createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: JOB_ID, action: "restart_vpn"}};
    // Under the lock the job is no longer queued.
    fake.reset({selects: [[routers, [[owner]]], [healthIncidents, [[]]], [jobs, [[job], []]]], updateReturns: [[routers, [[owner], [owner], [owner]]]]});

    const response = await checkInRouter(ROUTER_ID, checkInPayload());

    expect(response.jobs).toEqual([]);
    expect(fake.updates(jobs)).toEqual([]);
  });

  it("still tells the backend when an undeliverable job's result path itself fails", async () => {
    const {protectPartnerParams} = await import("./partner-router-secrets");
    const owner = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    const BROKEN_ID = "2e3d4c5b-6a7f-4e8d-8c9b-1a0f2e3d4c5b";
    const broken = {id: BROKEN_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "queued", desiredRevisionId: null, createdAt: new Date(), payload: {origin: "partner_action", ownerRef: "acct-42", actionId: BROKEN_ID, action: "set_wifi", ...protectPartnerParams("set_wifi", {ssid: "Fake guest", password: "fake-guest-pass-123"}, {routerId: ROUTER_ID, ownerRef: "acct-42", actionId: "other-action"})}};
    // recordJobResult finds no router (only the check-in's own read is scripted) and throws.
    fake.reset({selects: [[routers, [[owner]]], [healthIncidents, [[]]], [jobs, [[broken], [broken], [{...broken, state: "running"}]]]], updateReturns: [[routers, [[owner], [owner], [owner], [owner], [owner]]], [jobs, [[{...broken, state: "running"}], [{...broken, state: "failed"}]]]]});
    const errors = vi.spyOn(console, "error").mockImplementation(() => undefined);

    await checkInRouter(ROUTER_ID, checkInPayload());

    expect(fake.updates(jobs).map(update => update.state)).toEqual(["running", "failed"]);
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual(expect.arrayContaining([expect.objectContaining({event: "router.action", detail: expect.objectContaining({actionId: BROKEN_ID, state: "failed", detail: "payload_unavailable"})})]));
    // The fallback frees the key like any result would.
    expect(fake.updates(jobs)[1]).toMatchObject({state: "failed", dedupeKey: null});
    errors.mockRestore();
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
          [[{ id: REVISION_ID, routerId: ROUTER_ID, engineMode: "xray-direct", configDigest: "d", config: {} }]],
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

  // Review 2026-10-02: a result for a cancelled owner action was dropped
  // silently. It cannot happen once deliveries are stamped, but if a router
  // runs one anyway its owner hears the real result.
  it("reports a result that arrives for a cancelled owner action and logs that it ran", async () => {
    const router = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    fake.reset({
      selects: [
        [jobs, [[{id: JOB_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "cancelled", desiredRevisionId: null, dedupeKey: "partner-action:rb7-key", payload: {origin: "partner_action", actionId: JOB_ID, ownerRef: "acct-42", action: "reboot", idempotencyKey: "rb7-key", cancelledBy: "partner"}, createdAt: new Date()}]]],
        [routers, [[router]]],
      ],
      updateReturns: [[routers, [[router]]]],
    });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const answer = await recordJobResult(ROUTER_ID, result("failure", {result: {code: "no_route"}}));

    expect(answer.acknowledged).toBe(true);
    expect(warn).toHaveBeenCalledWith("[router-control] cancelled job ran", expect.objectContaining({jobId: JOB_ID}));
    expect(fake.inserts(partnerWebhooks).map(row => row.payload)).toEqual([expect.objectContaining({event: "router.action", detail: {actionId: JOB_ID, idempotencyKey: "rb7-key", state: "failed", detail: "no_route"}})]);
    // The job itself stays cancelled; it only records that its run was reported.
    expect(fake.updates(jobs)).toEqual([{payload: expect.objectContaining({cancelledBy: "partner", cancelledRunReported: true})}]);
    warn.mockRestore();
  });

  // Review 2026-10-03: every retried finish of a cancelled job sent another webhook.
  it("reports a cancelled owner action's run only once", async () => {
    const router = routerRow({ownerRef: "acct-42", approvedAt: new Date(), importState: "approved", status: "active"});
    fake.reset({
      selects: [
        [jobs, [[{id: JOB_ID, routerId: ROUTER_ID, type: "connect_router_action", state: "cancelled", desiredRevisionId: null, dedupeKey: "partner-action:rb7-key", payload: {origin: "partner_action", actionId: JOB_ID, ownerRef: "acct-42", action: "reboot", idempotencyKey: "rb7-key", cancelledBy: "partner", cancelledRunReported: true}, createdAt: new Date()}]]],
        [routers, [[router]]],
      ],
      // The claim finds the run already reported.
      updateReturns: [[jobs, [[]]], [routers, [[router]]]],
    });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const answer = await recordJobResult(ROUTER_ID, result("success"));

    expect(answer.acknowledged).toBe(true);
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
    warn.mockRestore();
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

  it("delivers an operator's command too when vctl reports remoteShell", () => {
    expect(
      selectDeliverableJobsForCheckIn(
        "approved",
        [hostnameJob, operatorShell] as never,
        "xray-direct",
        "xray-direct",
        true,
      ).map((job) => job.id),
    ).toEqual(["rename", "shell"]);
  });

  it("keeps an operator's command from a legacy agent (hand-back)", () => {
    expect(
      selectDeliverableJobsForCheckIn(
        "approved",
        [operatorShell] as never,
        "xray-direct",
        "other",
      ),
    ).toEqual([]);
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

// appliedRevisionId in a job result comes from the router. Another router's
// revision must be ignored: recording it would make that router's config (and
// its secrets) this router's active revision.
describe("recordJobResult and revision ownership", () => {
  const OTHER_ROUTER_ID = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f2a3b4c5d";
  const FOREIGN_REVISION_ID = "3e4f5a6b-7c8d-4e9f-8a0b-1c2d3e4f5a6b";

  function scriptApply(revisionOwner: string, revisionId: string) {
    const router = routerRow({
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
                type: "apply_passwall_config",
                state: "running",
                payload: {},
                desiredRevisionId: null,
                dedupeKey: null,
                deliveredAt: new Date(),
                createdAt: new Date(),
              },
            ],
          ],
        ],
        [routers, [[router]]],
        [
          passwallDesiredRevisions,
          [
            [
              {
                id: revisionId,
                routerId: revisionOwner,
                engineMode: "passwall",
                configDigest: "digest",
                config: {},
              },
            ],
          ],
        ],
      ],
      updateReturns: [[routers, [[router]]]],
    });
  }

  const success = (appliedRevisionId: string) => ({
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    jobId: JOB_ID,
    status: "success",
    appliedRevisionId,
  });

  it("ignores a revision that belongs to another router", async () => {
    scriptApply(OTHER_ROUTER_ID, FOREIGN_REVISION_ID);

    const answer = await recordJobResult(
      ROUTER_ID,
      success(FOREIGN_REVISION_ID),
    );

    expect(answer.acknowledged).toBe(true);
    expect(fake.inserts(passwallAppliedRevisions)).toEqual([]);
    expect(fake.updates(passwallDesiredRevisions)).toEqual([]);
    expect(
      fake.updates(routers).filter((set) => "activeRevisionId" in set),
    ).toEqual([]);
  });

  it("records the router's own revision as applied", async () => {
    scriptApply(ROUTER_ID, REVISION_ID);

    await recordJobResult(ROUTER_ID, success(REVISION_ID));

    expect(fake.inserts(passwallAppliedRevisions)).toEqual([
      expect.objectContaining({
        routerId: ROUTER_ID,
        desiredRevisionId: REVISION_ID,
        result: "applied",
      }),
    ]);
    expect(
      fake.updates(routers).filter((set) => "activeRevisionId" in set),
    ).toEqual([expect.objectContaining({ activeRevisionId: REVISION_ID })]);
  });

  // The PassWall agent re-sends an unacknowledged result before every
  // check-in; refusing an oversized one would wedge the router for good.
  it("accepts an oversized result, storing a size marker in its place", async () => {
    scriptApply(ROUTER_ID, REVISION_ID);

    const answer = await recordJobResult(ROUTER_ID, {
      protocolVersion: "2026-04-v1",
      routerId: ROUTER_ID,
      jobId: JOB_ID,
      status: "failure",
      result: { stdout: "z".repeat(1024 * 1024 + 1) },
      stderr: "e".repeat(20_000),
    });

    expect(answer.acknowledged).toBe(true);
    expect(fake.inserts(jobResults)).toEqual([
      expect.objectContaining({
        payload: expect.objectContaining({
          truncated: true,
          bytes: expect.any(Number) as number,
        }) as object,
      }),
    ]);
    expect(fake.inserts(eventLog)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          type: "router.payload_truncated",
          metadata: { endpoint: "job_result", fields: ["result", "stderr"] },
        }),
      ]),
    );
  });
});
