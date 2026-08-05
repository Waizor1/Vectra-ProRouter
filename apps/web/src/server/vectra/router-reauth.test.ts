import { generateKeyPairSync, sign as cryptoSign } from "node:crypto";
import { describe, expect, it } from "vitest";

import {
  ROUTER_REAUTH_MESSAGE_PREFIX,
  ROUTER_REAUTH_SKEW_SECONDS,
  buildRouterReauthMessage,
  canIssueRegistrationToken,
  verifyRouterReauthProof,
} from "./router-control";

const DEVICE_IDENTIFIER = "router-reauth-device-1";
const NOW = new Date("2026-06-29T12:00:00.000Z");

/**
 * Mirrors the contract the Go agent lane implements: an ed25519 keypair whose
 * RAW 32-byte public key is stored base64-std in routerCredentials.devicePublicKey.
 */
function generateDeviceKeypair() {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const rawPublicKey = publicKey.export({ type: "spki", format: "der" }).subarray(-32);
  return {
    privateKey,
    devicePublicKeyBase64: rawPublicKey.toString("base64"),
  };
}

function signCanonicalMessage(
  privateKey: ReturnType<typeof generateKeyPairSync>["privateKey"],
  deviceIdentifier: string,
  signedAt: string,
): string {
  const message = Buffer.from(
    buildRouterReauthMessage(deviceIdentifier, signedAt),
    "utf8",
  );
  return cryptoSign(null, message, privateKey).toString("base64");
}

function expectRejected(
  result: ReturnType<typeof verifyRouterReauthProof>,
  reason: string,
) {
  expect(result.ok).toBe(false);
  if (result.ok) {
    throw new Error("expected verification to fail");
  }
  expect(result.reason).toBe(reason);
}

describe("buildRouterReauthMessage", () => {
  it("produces the exact canonical bytes the agent signer must match", () => {
    const signedAt = "2026-06-29T11:59:30Z";
    expect(buildRouterReauthMessage(DEVICE_IDENTIFIER, signedAt)).toBe(
      `${ROUTER_REAUTH_MESSAGE_PREFIX}${DEVICE_IDENTIFIER}\n${signedAt}`,
    );
    expect(ROUTER_REAUTH_MESSAGE_PREFIX).toBe("vectra-router-reauth:v1\n");
    expect(ROUTER_REAUTH_SKEW_SECONDS).toBe(300);
  });
});

describe("verifyRouterReauthProof", () => {
  it("accepts a valid signature from the stored device key within the skew window", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signCanonicalMessage(privateKey, DEVICE_IDENTIFIER, signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expect(result.ok).toBe(true);
  });

  it("rejects a garbage / invalid signature", () => {
    const { devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();

    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt,
        signature: Buffer.from("not-a-real-signature".repeat(4)).toString("base64"),
      },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a proof whose signedAt is older than the 300s window", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = new Date(NOW.getTime() - 301_000).toISOString();
    const signature = signCanonicalMessage(privateKey, DEVICE_IDENTIFIER, signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_outside_window");
  });

  it("rejects a proof whose signedAt is in the future beyond the skew", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = new Date(NOW.getTime() + 301_000).toISOString();
    const signature = signCanonicalMessage(privateKey, DEVICE_IDENTIFIER, signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_outside_window");
  });

  it("rejects a valid signature made with a different device's key", () => {
    const signer = generateDeviceKeypair();
    const stored = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signCanonicalMessage(signer.privateKey, DEVICE_IDENTIFIER, signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      // stored credential belongs to a DIFFERENT keypair than the signer
      devicePublicKey: stored.devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a signature bound to a different deviceIdentifier", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    // sign over the WRONG device identifier
    const signature = signCanonicalMessage(privateKey, "some-other-device", signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a malformed signedAt timestamp", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signature = signCanonicalMessage(privateKey, DEVICE_IDENTIFIER, "not-a-date");

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt: "not-a-date", signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_invalid");
  });

  it("rejects when the stored device public key is malformed base64 / wrong length", () => {
    const { privateKey } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signCanonicalMessage(privateKey, DEVICE_IDENTIFIER, signedAt);

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      deviceIdentifier: DEVICE_IDENTIFIER,
      devicePublicKey: "not-valid-key",
      now: NOW,
    });

    expectRejected(result, "public_key_invalid");
  });
});

describe("registerRouter recovery gating", () => {
  // The recovery proof is only consulted when the existing router is NOT
  // token-authenticated. The normal token check-in path keeps its existing
  // behavior because canIssueRegistrationToken short-circuits to true and the
  // recovery branch is never entered (test scenario 5).
  it("token-authenticated existing router never enters the recovery branch", () => {
    expect(
      canIssueRegistrationToken({
        existingRouterId: "router-1",
        authenticatedRouterId: "router-1",
      }),
    ).toBe(true);
  });

  it("non-authenticated existing router requires the recovery proof to pass", () => {
    expect(
      canIssueRegistrationToken({
        existingRouterId: "router-1",
        authenticatedRouterId: null,
      }),
    ).toBe(false);
  });
});
