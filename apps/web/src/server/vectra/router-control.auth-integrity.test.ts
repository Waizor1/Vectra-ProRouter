import {
  eventLog,
  healthIncidents,
  jobs,
  routerCredentialBindings,
  routerCredentials,
  routers,
} from "@vectra/db";
import { generateKeyPairSync, sign } from "node:crypto";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";

import { mintEnrollmentToken, verifyCheckInNonce } from "./router-auth-integrity";
import { resetRouterAuthPolicyStateForTest } from "./router-auth-policy";
import { registerProofMessage } from "./router-claim-state";
import { checkInRouter, registerRouter } from "./router-control";
import { resetCredentialBindingStateForTest } from "./router-credential-binding";
import type { FakeDb } from "./testing/fake-db";

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});
const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

// Fake keys only.
const SECRETS_KEY = "router-auth-integrity-flow-test-key-0123456789";
const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const DEVICE_ID = "vectra-07bf0887f662";
const DEVICE_KEY = Buffer.alloc(32, 9).toString("base64");
const CREDENTIAL_ID = "c0ffee00-0000-4000-8000-000000000001";
const B1 = `v1:${"1".repeat(64)}`;
const B2 = `v1:${"2".repeat(64)}`;

// What register and check-in answer a fleet router on the base, with no
// claim key or bot configured. Every switch at its default must keep these.
const BASE_REGISTER_KEYS = [
  "configSyncState",
  "issuedToken",
  "operatorMessage",
  "owner",
  "pendingApproval",
  "pollingIntervalSeconds",
  "protocolVersion",
  "rescuePolicy",
  "routerId",
  "status",
  "updatePolicy",
];
const BASE_CHECK_IN_KEYS = [
  "configSyncState",
  "desiredRevision",
  "jobs",
  "operatorMessage",
  "owner",
  "pollingIntervalSeconds",
  "protocolVersion",
  "rescuePolicy",
  "routePolicy",
  "routerId",
  "status",
  "updatePolicy",
];
const NEW_EVENT_TYPE = /^router\.(enrollment\.token_|checkin_signature\.|credential\.)/;

function inventory(overrides: Record<string, unknown> = {}) {
  return {
    protocolVersion: "2026-04-v1",
    deviceIdentifier: DEVICE_ID,
    devicePublicKey: DEVICE_KEY,
    controllerVersion: "0.1.13-r48",
    hostname: "1111111111",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    passwallEnabled: true,
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
    deviceIdentifier: DEVICE_ID,
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
    engineMode: "passwall",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: null,
    lastSeenAt: new Date("2026-10-08T09:59:00.000Z"),
    lastCheckInAt: new Date("2026-10-08T09:59:00.000Z"),
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
    createdAt: new Date("2026-10-08T09:00:00.000Z"),
    updatedAt: new Date("2026-10-08T09:59:00.000Z"),
    ...overrides,
  };
}

function bindingRow(overrides: Record<string, unknown> = {}) {
  return {
    routerId: ROUTER_ID,
    credentialId: CREDENTIAL_ID,
    boundDeviceBinding: B1,
    boundAt: new Date("2026-10-01T00:00:00.000Z"),
    lastDeviceBinding: B1,
    lastSourceCluster: "203.0.113.0/24",
    lastSeenAt: new Date(Date.now() - 45_000),
    knownSourceClusters: ["203.0.113.0/24"],
    updatedAt: new Date(Date.now() - 45_000),
    ...overrides,
  };
}

function register(extra: Record<string, unknown> = {}, options = {}) {
  return registerRouter(
    { protocolVersion: "2026-04-v1", inventory: inventory({ deviceBinding: B1 }), ...extra },
    options,
  );
}

function scriptNewRouter() {
  fake.reset({
    selects: [[routers, [[]]]],
    insertReturns: [[routers, [[routerRow()]]]],
  });
}

function scriptCheckIn(binding: Record<string, unknown> | null = null) {
  const router = routerRow();
  fake.reset({
    selects: [
      [routers, [[router]]],
      [healthIncidents, [[]]],
      [jobs, [[]]],
      [routerCredentialBindings, [binding ? [binding] : []]],
    ],
    updateReturns: [[routers, [[router]]]],
  });
}

function checkIn(
  inventoryOverrides: Record<string, unknown> = { deviceBinding: B1 },
  clientIp = "203.0.113.77",
) {
  return checkInRouter(
    ROUTER_ID,
    {
      protocolVersion: "2026-04-v1",
      routerId: ROUTER_ID,
      inventory: inventory(inventoryOverrides),
      health: {},
    },
    { devicePublicKey: DEVICE_KEY, credentialId: CREDENTIAL_ID, clientIp },
  );
}

const newEvents = () =>
  fake.inserts(eventLog).filter((event) => NEW_EVENT_TYPE.test(String(event.type)));

function setEnv(flags: Record<string, string> = {}) {
  envMock.env = {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: SECRETS_KEY,
    VECTRA_POLLING_INTERVAL_SECONDS: "45",
    VECTRA_DEFAULT_CONTROL_DOMAIN: "https://router.vectra-pro.net",
    ...flags,
  };
}

let info: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  setEnv();
  fake.reset();
  resetRouterAuthPolicyStateForTest();
  resetCredentialBindingStateForTest();
  info = vi.spyOn(console, "info").mockImplementation(() => undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("every switch at its default", () => {
  it("answers register and check-in exactly as the base does", async () => {
    scriptNewRouter();
    const registered = await register();

    expect(Object.keys(registered).sort()).toEqual(BASE_REGISTER_KEYS);
    expect(JSON.parse(JSON.stringify(registered))).not.toHaveProperty("checkInNonce");
    expect(fake.inserts(eventLog).map((event) => event.type)).toEqual(["router.registered"]);
    // A1 off: the brand-new router without a token is only logged.
    expect(info).toHaveBeenCalledWith(
      `[router-enrollment] would require provisioning token deviceIdentifier=${DEVICE_ID} verdict=absent`,
    );

    scriptCheckIn();
    const checkedIn = await checkIn();

    expect(Object.keys(checkedIn).sort()).toEqual(BASE_CHECK_IN_KEYS);
    expect(JSON.parse(JSON.stringify(checkedIn))).not.toHaveProperty("checkInNonce");
    expect(newEvents()).toEqual([]);
  });

  it("answers the same with every switch spelled out as false", async () => {
    setEnv({
      VECTRA_ENROLLMENT_TOKEN_REQUIRED: "false",
      VECTRA_ISSUE_CHECKIN_NONCE: "false",
      VECTRA_REQUIRE_CHECKIN_SIGNATURE: "false",
      VECTRA_DUPLICATE_CREDENTIAL_ENFORCE: "false",
    });
    scriptNewRouter();
    expect(Object.keys(await register({ enrollmentToken: "garbage" })).sort()).toEqual(BASE_REGISTER_KEYS);
    scriptCheckIn(bindingRow());
    // A mismatching device is only recorded (A3 detection is log-only).
    expect(Object.keys(await checkIn({ deviceBinding: B2 })).sort()).toEqual(BASE_CHECK_IN_KEYS);
    expect(newEvents().map((event) => event.type)).toEqual(["router.credential.device_mismatch"]);
  });

  it("differs from nonce issuance by the checkInNonce key and nothing else", async () => {
    scriptCheckIn();
    const off = await checkIn();
    setEnv({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });
    scriptCheckIn();
    const { checkInNonce, ...rest } = await checkIn();

    expect(checkInNonce).toEqual(expect.any(String));
    expect(rest).toEqual(off);
  });
});

describe("A1 provisioning token, switched on", () => {
  beforeEach(() => setEnv({ VECTRA_ENROLLMENT_TOKEN_REQUIRED: "true" }));

  it("refuses a brand-new router without a valid token before creating anything", async () => {
    // A token the contract cannot read (not a string, over the cap) was still
    // sent: malformed, not absent.
    for (const [enrollmentToken, verdict] of [
      [undefined, "absent"],
      [null, "absent"],
      ["vpt1.not-a-token", "malformed"],
      [42, "malformed"],
      ["x".repeat(257), "malformed"],
    ] as const) {
      scriptNewRouter();
      await expect(register({ enrollmentToken })).rejects.toMatchObject({
        status: 403,
        message: "Router enrollment requires a valid provisioning token.",
      });
      expect(fake.inserts(routers)).toEqual([]);
      expect(fake.inserts(routerCredentials)).toEqual([]);
      expect(fake.inserts(eventLog)).toEqual([
        expect.objectContaining({
          type: "router.enrollment.token_rejected",
          metadata: { deviceIdentifier: DEVICE_ID, verdict },
        }),
      ]);
    }
  });

  it("refuses a token minted for another device", async () => {
    scriptNewRouter();
    const enrollmentToken = mintEnrollmentToken({
      secretsKey: SECRETS_KEY,
      deviceIdentifier: "vectra-ffffffffffff",
      expiresAtUnix: Math.floor(Date.now() / 1000) + 3600,
    });
    await expect(register({ enrollmentToken })).rejects.toMatchObject({ status: 403 });
    expect(fake.inserts(eventLog)[0]?.metadata).toEqual({ deviceIdentifier: DEVICE_ID, verdict: "mac_mismatch" });
  });

  it("registers a brand-new router that presents its token", async () => {
    scriptNewRouter();
    const enrollmentToken = mintEnrollmentToken({
      secretsKey: SECRETS_KEY,
      deviceIdentifier: DEVICE_ID,
      expiresAtUnix: Math.floor(Date.now() / 1000) + 3600,
    });

    const response = await register({ enrollmentToken });

    expect(response.routerId).toBe(ROUTER_ID);
    expect(fake.inserts(routers)).toHaveLength(1);
    expect(newEvents()).toEqual([]);
    expect(info).not.toHaveBeenCalled();
  });

  it("never asks a known router re-registering with its token", async () => {
    const router = routerRow({ status: "active", approvedAt: new Date(), importState: "approved" });
    fake.reset({ selects: [[routers, [[router]]]], updateReturns: [[routers, [[router]]]] });

    const response = await register({}, { authenticatedRouterId: ROUTER_ID });

    expect(response.routerId).toBe(ROUTER_ID);
    expect(newEvents()).toEqual([]);
  });

  it("never asks a pre-claimed router adopting its record", async () => {
    const device = generateKeyPairSync("ed25519");
    const devicePublicKey = device.publicKey.export({ format: "der", type: "spki" }).subarray(12).toString("base64");
    const record = routerRow({ lastSeenAt: null, lastCheckInAt: null, approvedAt: new Date(), importState: "approved", ownerRef: "acct-42", ownerLabel: "iv***" });
    fake.reset({
      selects: [[routers, [[record]]], [routerCredentials, [[{ type: "bootstrap", devicePublicKey, revokedAt: null }]]]],
      updateReturns: [[routers, [[{ ...record, status: "active" }]]]],
    });
    const timestamp = Math.floor(Date.now() / 1000);

    const response = await registerRouter({
      protocolVersion: "2026-04-v1",
      inventory: inventory({ devicePublicKey }),
      proof: { timestamp, signature: sign(null, registerProofMessage(DEVICE_ID, timestamp), device.privateKey).toString("base64") },
    });

    expect(response.routerId).toBe(ROUTER_ID);
    expect(newEvents()).toEqual([]);
  });
});

describe("A2 nonce issuance, switched on", () => {
  it("hands a nonce for this router out on register and on check-in", async () => {
    setEnv({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });
    const nowUnix = () => Math.floor(Date.now() / 1000);

    scriptNewRouter();
    const registered = await register();
    scriptCheckIn();
    const checkedIn = await checkIn();

    for (const nonce of [registered.checkInNonce, checkedIn.checkInNonce]) {
      expect(
        verifyCheckInNonce({ secretsKey: SECRETS_KEY, nonce: nonce ?? "", routerId: ROUTER_ID, nowUnix: nowUnix() }),
      ).toBe("valid");
    }
    expect(registered.checkInNonce).not.toBe(checkedIn.checkInNonce);
  });
});

describe("A3 duplicate credential", () => {
  it("binds the credential a registration issues to the reporting device", async () => {
    scriptNewRouter();
    await register();

    const [credential] = fake.inserts(routerCredentials);
    expect(fake.inserts(routerCredentialBindings)).toEqual([
      expect.objectContaining({ routerId: ROUTER_ID, boundDeviceBinding: B1 }),
    ]);
    expect(credential).toBeDefined();
  });

  it("detects by default and never refuses", async () => {
    scriptCheckIn(bindingRow({ lastSeenAt: new Date(Date.now() - 5_000) }));

    const response = await checkIn({ deviceBinding: B2 }, "198.51.100.7");

    expect(response.routerId).toBe(ROUTER_ID);
    expect(newEvents().map((event) => event.type)).toEqual([
      "router.credential.device_mismatch",
      "router.credential.concurrent_sessions",
      "router.credential.new_source_cluster",
    ]);
  });

  describe("enforcement switched on", () => {
    beforeEach(() => setEnv({ VECTRA_DUPLICATE_CREDENTIAL_ENFORCE: "true" }));

    it("refuses a check-in from another device with the exact 403 before processing it", async () => {
      scriptCheckIn(bindingRow());

      const refusal = await checkIn({ deviceBinding: B2 }).then(
        () => null,
        (error: unknown) => error as { status?: number; responseBody?: unknown },
      );

      expect(refusal?.status).toBe(403);
      expect(JSON.stringify(refusal?.responseBody)).toBe(
        JSON.stringify({
          error: "Router credential is bound to a different device.",
          code: "credential_device_mismatch",
        }),
      );
      expect(fake.updates(routers)).toEqual([]);
      expect(fake.updates(routerCredentialBindings)).toEqual([]);
    });

    it("still refuses the other device when the event log cannot be written", async () => {
      vi.spyOn(console, "warn").mockImplementation(() => undefined);
      scriptCheckIn(bindingRow());
      const insert = fake.db.insert.bind(fake.db);
      vi.spyOn(fake.db, "insert").mockImplementation((table: unknown) => {
        if (table === eventLog) {
          throw new Error("event log unavailable");
        }
        return insert(table);
      });

      await expect(checkIn({ deviceBinding: B2 })).rejects.toMatchObject({ status: 403 });
      expect(fake.updates(routers)).toEqual([]);
    });

    it("never refuses an omitted binding or a move to another network", async () => {
      scriptCheckIn(bindingRow());
      await expect(checkIn({})).resolves.toMatchObject({ routerId: ROUTER_ID });
      scriptCheckIn(bindingRow());
      await expect(checkIn({ deviceBinding: B1 }, "2001:db8:42::9")).resolves.toMatchObject({ routerId: ROUTER_ID });
      expect(newEvents().map((event) => event.type)).toEqual(["router.credential.new_source_cluster"]);
    });

    it("lets the check-in through when detection itself fails", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
      scriptCheckIn(bindingRow());
      const select = fake.db.select.bind(fake.db);
      vi.spyOn(fake.db, "select").mockImplementation(() => {
        const builder = select();
        return {
          from(table: unknown) {
            if (table === routerCredentialBindings) {
              throw new Error('relation "vectra_router_credential_binding" does not exist');
            }
            return builder.from(table);
          },
        };
      });

      await expect(checkIn({ deviceBinding: B2 })).resolves.toMatchObject({ routerId: ROUTER_ID });
      expect(warn).toHaveBeenCalledTimes(1);
    });
  });

  it("remembers a check-in only once it is accepted", async () => {
    const router = routerRow();
    fake.reset({
      selects: [[routers, [[router]]], [routerCredentialBindings, [[bindingRow()]]]],
      // The router row update comes back empty: the check-in fails.
      updateReturns: [[routers, [[]]]],
    });

    await expect(checkIn()).rejects.toThrow("Router check-in update failed.");
    expect(fake.updates(routerCredentialBindings)).toEqual([]);

    scriptCheckIn(bindingRow());
    await checkIn();
    expect(fake.updates(routerCredentialBindings)).toEqual([
      expect.objectContaining({ lastSourceCluster: "203.0.113.0/24", lastDeviceBinding: B1 }),
    ]);
  });

  it("is skipped for a check-in that does not name its credential", async () => {
    scriptCheckIn(bindingRow());
    await checkInRouter(ROUTER_ID, {
      protocolVersion: "2026-04-v1",
      routerId: ROUTER_ID,
      inventory: inventory({ deviceBinding: B2 }),
      health: {},
    });
    expect(fake.calls.some((call) => call.table === routerCredentialBindings)).toBe(false);
  });
});
