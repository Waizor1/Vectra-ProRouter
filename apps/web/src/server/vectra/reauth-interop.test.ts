import { describe, it, expect } from "vitest";

import { verifyRouterReauthProof } from "./router-control";

// Golden cross-language vector: produced by the Go controller agent's
// controlplane.SignReauth (deterministic ed25519 seed = bytes 1..32), signing
// the canonical v2 message
//   "vectra-router-reauth:v2\n" + routerId + "\n" + deviceId + "\n"
//     + newDevicePublicKey + "\n" + signedAt
// This guards against the agent and panel ever drifting on the reauth contract —
// if either side changes the message format, this test fails. Regenerated for
// v2 (the proof now binds routerId AND the submitted device key); see
// scratchpad gen.go in the hardening change.
const GO_VECTOR = {
  routerId: "router-interop-1",
  devicePublicKey: "ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=",
  deviceIdentifier: "vectra-interop",
  signedAt: "2026-06-29T16:00:00Z",
  signature:
    "xqq7xP1yDipUbIzdtQJ5vSgpvERzC5y2bxKN4CUYGi5tEkbPWCcXAiafa94NBbAcmSyTWNm0soMdPSyr8p7cAw==",
};

describe("reauth interop (Go agent signs -> TS panel verifies)", () => {
  it("verifies a v2 signature produced by the Go controller", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt: GO_VECTOR.signedAt,
        signature: GO_VECTOR.signature,
      },
      routerId: GO_VECTOR.routerId,
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      submittedDevicePublicKey: GO_VECTOR.devicePublicKey,
      storedDevicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(true);
  });

  it("rejects the Go signature when the device identifier is tampered", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt: GO_VECTOR.signedAt,
        signature: GO_VECTOR.signature,
      },
      routerId: GO_VECTOR.routerId,
      deviceIdentifier: "vectra-attacker",
      submittedDevicePublicKey: GO_VECTOR.devicePublicKey,
      storedDevicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(false);
  });

  it("rejects the Go signature when the router id is tampered", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt: GO_VECTOR.signedAt,
        signature: GO_VECTOR.signature,
      },
      routerId: "router-other",
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      submittedDevicePublicKey: GO_VECTOR.devicePublicKey,
      storedDevicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(false);
  });

  it("rejects the Go signature when an attacker swaps in a different submitted key", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt: GO_VECTOR.signedAt,
        signature: GO_VECTOR.signature,
      },
      routerId: GO_VECTOR.routerId,
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      // Attacker claims their own key; the stored (legit) key signed the real
      // key, so reconstructing with the attacker's key no longer verifies.
      submittedDevicePublicKey:
        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
      storedDevicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(false);
  });

  it("rejects the Go signature once outside the freshness window", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt: GO_VECTOR.signedAt,
        signature: GO_VECTOR.signature,
      },
      routerId: GO_VECTOR.routerId,
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      submittedDevicePublicKey: GO_VECTOR.devicePublicKey,
      storedDevicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:30:00Z"),
    });
    expect(result.ok).toBe(false);
  });
});
