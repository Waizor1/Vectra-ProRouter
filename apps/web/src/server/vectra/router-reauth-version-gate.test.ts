import { eventLog, routerCredentials, routers } from "@vectra/db";
import { generateKeyPairSync, type KeyObject, sign } from "node:crypto";

import { beforeEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";

import {
  buildRouterReauthMessage,
  buildRouterReauthV1Message,
  registerRouter,
  resetReauthReplayStateForTest,
} from "./router-control";
import type { FakeDb } from "./testing/fake-db";

// VECTRA_REQUIRE_REAUTH_V2 gate. The panel moved the re-auth proof from v1 to
// v2; the ONLY client that signs re-auth proofs in production is the legacy
// router/vectra-controller-agent, which signs v1. So the gate must keep the v1
// proof working while the flag is off (inert for the existing fleet), and
// require v2 only when the flag is explicitly on. This exercises the owned
// router device-key-change path, which verifies a proof directly.

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});

const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const DEVICE_IDENTIFIER = "vectra-07bf0887f662";

/** A real ed25519 device key pair; the public half as the agent sends it. */
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

/** The v2 proof: binds routerId AND the new key, signed by the key on record. */
function v2Proof(recordPrivateKey: KeyObject, newDevicePublicKey: string) {
  const signedAt = new Date().toISOString();
  return {
    signedAt,
    signature: sign(
      null,
      Buffer.from(
        buildRouterReauthMessage(
          ROUTER_ID,
          DEVICE_IDENTIFIER,
          newDevicePublicKey,
          signedAt,
        ),
        "utf8",
      ),
      recordPrivateKey,
    ).toString("base64"),
  };
}

/** The legacy v1 proof the old Go agent signs: deviceIdentifier + signedAt. */
function v1Proof(recordPrivateKey: KeyObject) {
  const signedAt = new Date().toISOString();
  return {
    signedAt,
    signature: sign(
      null,
      Buffer.from(
        buildRouterReauthV1Message(DEVICE_IDENTIFIER, signedAt),
        "utf8",
      ),
      recordPrivateKey,
    ).toString("base64"),
  };
}

function ownedRouterRow() {
  return {
    id: ROUTER_ID,
    deviceIdentifier: DEVICE_IDENTIFIER,
    displayName: null,
    hostname: "1111111111",
    panelDomain: "https://router.vectra-pro.net",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    controllerChannel: "stable",
    engineMode: "xray-direct",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: new Date("2026-09-28T09:30:00.000Z"),
    lastSeenAt: new Date("2026-09-28T09:59:00.000Z"),
    lastCheckInAt: new Date("2026-09-28T09:59:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    ownerRef: "acct-42",
    ownerLabel: "iv***",
    claimCodeHash: null,
    claimExpiresAt: null,
    previousClaimCodeHash: null,
    previousClaimExpiresAt: null,
    claimedAt: null,
    releasedAt: null,
    createdAt: new Date("2026-09-28T09:00:00.000Z"),
    updatedAt: new Date("2026-09-28T09:59:00.000Z"),
  };
}

function inventory(devicePublicKey: string) {
  return {
    protocolVersion: "2026-04-v1",
    deviceIdentifier: DEVICE_IDENTIFIER,
    devicePublicKey,
    controllerVersion: "0.4.0-r1",
    hostname: "1111111111",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    passwallEnabled: false,
    nodeCount: 0,
    subscriptionCount: 0,
    resources: {},
    serviceHealth: {},
  };
}

function setEnv(flags: Record<string, string> = {}) {
  envMock.env = {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "router-reauth-version-gate-test-key-0123456789",
    VECTRA_POLLING_INTERVAL_SECONDS: "45",
    VECTRA_DEFAULT_CONTROL_DOMAIN: "https://router.vectra-pro.net",
    ...flags,
  };
}

/**
 * Scripts the owned-router key-change flow: the record exists, its credential
 * holds `onRecordKey`, and the router update echoes the record back.
 */
function scriptKeyChange(onRecordKey: string) {
  const record = ownedRouterRow();
  fake.reset({
    selects: [
      [routers, [[record]]],
      [routerCredentials, [[{ devicePublicKey: onRecordKey }]]],
    ],
    updateReturns: [[routers, [[record]]]],
  });
}

const onRecord = deviceKeyPair();

beforeEach(() => {
  setEnv();
  fake.reset();
  resetReauthReplayStateForTest();
  vi.spyOn(console, "info").mockImplementation(() => undefined);
  vi.spyOn(console, "warn").mockImplementation(() => undefined);
});

describe("VECTRA_REQUIRE_REAUTH_V2 off (default): both proof versions accepted", () => {
  it("accepts a valid v2 key-change proof (rotation succeeds)", async () => {
    const next = deviceKeyPair();
    scriptKeyChange(onRecord.publicKey);

    const response = await registerRouter(
      {
        protocolVersion: "2026-04-v1",
        inventory: inventory(next.publicKey),
        recoveryProof: v2Proof(onRecord.privateKey, next.publicKey),
      },
      { authenticatedRouterId: ROUTER_ID },
    );

    expect(response.routerId).toBe(ROUTER_ID);
    expect(fake.inserts(routerCredentials)).toEqual([
      expect.objectContaining({ devicePublicKey: next.publicKey }),
    ]);
    expect(
      fake.inserts(eventLog).map((event) => event.type),
    ).not.toContain("router.device_key_change_blocked");
  });

  it("accepts the legacy v1 key-change proof (rotation succeeds, inert for old agents)", async () => {
    const next = deviceKeyPair();
    scriptKeyChange(onRecord.publicKey);

    const response = await registerRouter(
      {
        protocolVersion: "2026-04-v1",
        inventory: inventory(next.publicKey),
        // The old Go agent signs v1 (no routerId / new-key binding).
        recoveryProof: v1Proof(onRecord.privateKey),
      },
      { authenticatedRouterId: ROUTER_ID },
    );

    expect(response.routerId).toBe(ROUTER_ID);
    expect(fake.inserts(routerCredentials)).toEqual([
      expect.objectContaining({ devicePublicKey: next.publicKey }),
    ]);
    expect(
      fake.inserts(eventLog).map((event) => event.type),
    ).not.toContain("router.device_key_change_blocked");
  });

  it("still rejects a proof signed by neither version (no downgrade hole)", async () => {
    const next = deviceKeyPair();
    const intruder = deviceKeyPair();
    scriptKeyChange(onRecord.publicKey);

    await expect(
      registerRouter(
        {
          protocolVersion: "2026-04-v1",
          inventory: inventory(next.publicKey),
          // Signed by the intruder key, not the key on record: fails both v2
          // and the v1 fallback.
          recoveryProof: v2Proof(intruder.privateKey, next.publicKey),
        },
        { authenticatedRouterId: ROUTER_ID },
      ),
    ).rejects.toMatchObject({ status: 403 });
    expect(fake.inserts(routerCredentials)).toEqual([]);
  });
});

describe("VECTRA_REQUIRE_REAUTH_V2 on: v2 required, v1 rejected", () => {
  beforeEach(() => setEnv({ VECTRA_REQUIRE_REAUTH_V2: "true" }));

  it("accepts a valid v2 key-change proof", async () => {
    const next = deviceKeyPair();
    scriptKeyChange(onRecord.publicKey);

    const response = await registerRouter(
      {
        protocolVersion: "2026-04-v1",
        inventory: inventory(next.publicKey),
        recoveryProof: v2Proof(onRecord.privateKey, next.publicKey),
      },
      { authenticatedRouterId: ROUTER_ID },
    );

    expect(response.routerId).toBe(ROUTER_ID);
    expect(fake.inserts(routerCredentials)).toEqual([
      expect.objectContaining({ devicePublicKey: next.publicKey }),
    ]);
  });

  it("rejects the legacy v1 key-change proof (403, no rotation)", async () => {
    const next = deviceKeyPair();
    scriptKeyChange(onRecord.publicKey);

    await expect(
      registerRouter(
        {
          protocolVersion: "2026-04-v1",
          inventory: inventory(next.publicKey),
          recoveryProof: v1Proof(onRecord.privateKey),
        },
        { authenticatedRouterId: ROUTER_ID },
      ),
    ).rejects.toMatchObject({ status: 403 });

    expect(fake.inserts(routerCredentials)).toEqual([]);
    expect(fake.updates(routers)).toEqual([]);
    expect(fake.inserts(eventLog)[0]).toMatchObject({
      type: "router.device_key_change_blocked",
      metadata: { recoveryProofRejectReason: "signature_invalid" },
    });
  });
});
