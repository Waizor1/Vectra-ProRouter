import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  createDefaultRescuePolicy,
  createDefaultUpdatePolicy,
  ROUTER_ENROLLMENT_TOKEN_MAX_CHARS,
  routerCheckInRequestSchema,
  routerCheckInResponseSchema,
  routerRegisterRequestSchema,
  routerRegisterResponseSchema,
} from "@vectra/contracts";

/**
 * The panel half of the router auth integrity contract
 * (ai_docs/develop/features/router-auth-integrity-and-native-runtime.md):
 * every new field is optional, a malformed one is dropped instead of failing
 * the request, and a response carries checkInNonce only when it is issued.
 */
const vectors = JSON.parse(
  readFileSync(
    new URL("../../../../packages/contracts/fixtures/router-auth-vectors.json", import.meta.url),
    "utf8",
  ),
) as { deviceBinding: { binding: string }; enrollmentToken: { token: string } };

const ROUTER_ID = "3f1c9a52-6a8e-4c1e-9d2b-7b5f0e4a1c33";

function inventory(extra: Record<string, unknown> = {}) {
  return {
    protocolVersion: "2026-04-v1",
    deviceIdentifier: "vectra-0a1b2c3d4e5f",
    devicePublicKey: Buffer.alloc(32, 9).toString("base64"),
    controllerVersion: "0.1.13-r48",
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
    ...extra,
  };
}

const checkIn = (extra: Record<string, unknown> = {}) =>
  routerCheckInRequestSchema.safeParse({
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    inventory: inventory(extra),
    health: {},
  });

describe("inventory.deviceBinding (A4)", () => {
  it("keeps a v1 binding", () => {
    const parsed = checkIn({ deviceBinding: vectors.deviceBinding.binding });
    expect(parsed.success && parsed.data.inventory.deviceBinding).toBe(vectors.deviceBinding.binding);
  });

  it("drops a malformed one and never fails the check-in over it", () => {
    for (const deviceBinding of [
      vectors.deviceBinding.binding.toUpperCase(),
      `v2:${"a".repeat(64)}`,
      `v1:${"a".repeat(63)}`,
      "",
      42,
      null,
      { binding: vectors.deviceBinding.binding },
    ]) {
      const parsed = checkIn({ deviceBinding });
      expect(parsed.success).toBe(true);
      expect(parsed.success && parsed.data.inventory.deviceBinding).toBeUndefined();
    }
  });

  it("is absent from a check-in that does not send it", () => {
    const parsed = checkIn();
    expect(parsed.success && "deviceBinding" in parsed.data.inventory).toBe(false);
  });
});

describe("register enrollmentToken (A1)", () => {
  const register = (extra: Record<string, unknown>) =>
    routerRegisterRequestSchema.safeParse({ protocolVersion: "2026-04-v1", inventory: inventory(), ...extra });

  it("keeps a bounded string for the panel to judge", () => {
    for (const enrollmentToken of [vectors.enrollmentToken.token, "garbage"]) {
      const parsed = register({ enrollmentToken });
      expect(parsed.success && parsed.data.enrollmentToken).toBe(enrollmentToken);
    }
  });

  it("drops anything else and never fails the registration over it", () => {
    for (const enrollmentToken of ["", "x".repeat(ROUTER_ENROLLMENT_TOKEN_MAX_CHARS + 1), 42, null, ["vpt1"]]) {
      const parsed = register({ enrollmentToken });
      expect(parsed.success).toBe(true);
      expect(parsed.success && parsed.data.enrollmentToken).toBeUndefined();
    }
  });
});

describe("checkInNonce in answers (A2)", () => {
  const registerAnswer = {
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    status: "pending",
    issuedToken: "fake-issued-token",
    pollingIntervalSeconds: 45,
    pendingApproval: true,
    configSyncState: { importState: "awaiting_import" },
    rescuePolicy: createDefaultRescuePolicy(),
    updatePolicy: createDefaultUpdatePolicy(),
    operatorMessage: null,
    owner: null,
  };
  const checkInAnswer = {
    protocolVersion: "2026-04-v1",
    routerId: ROUTER_ID,
    status: "pending",
    pollingIntervalSeconds: 45,
    configSyncState: { importState: "awaiting_import" },
    rescuePolicy: createDefaultRescuePolicy(),
    updatePolicy: createDefaultUpdatePolicy(),
    desiredRevision: null,
    jobs: [],
    operatorMessage: null,
    routePolicy: null,
    owner: null,
  };

  it("has no key at all unless the panel issued one", () => {
    expect("checkInNonce" in routerRegisterResponseSchema.parse(registerAnswer)).toBe(false);
    expect("checkInNonce" in routerCheckInResponseSchema.parse(checkInAnswer)).toBe(false);
  });

  it("carries an issued nonce", () => {
    const checkInNonce = "vcn1.1791460800.AAECAwQFBgcICQoLDA0ODw.qQdypn9DTzJ19kumha_itbJN5aX4KwAwrEQ5QpG65gU";
    expect(routerRegisterResponseSchema.parse({ ...registerAnswer, checkInNonce }).checkInNonce).toBe(checkInNonce);
    expect(routerCheckInResponseSchema.parse({ ...checkInAnswer, checkInNonce }).checkInNonce).toBe(checkInNonce);
  });
});
