import { describe, it, expect } from "vitest";

import { verifyRouterReauthProof } from "./router-control";

// Golden cross-language vector: produced by the Go controller agent's
// controlplane.SignReauth (deterministic ed25519 seed = bytes 1..32), signing
// the canonical message "vectra-router-reauth:v1\n" + deviceId + "\n" + signedAt.
// This guards against the agent and panel ever drifting on the reauth contract —
// if either side changes the message format, this test fails.
const GO_VECTOR = {
  devicePublicKey: "ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=",
  deviceIdentifier: "vectra-interop",
  signedAt: "2026-06-29T16:00:00Z",
  signature:
    "RdjgYmlD4Xua4kYucbqmzkiN6Rv67kBZShCueeyIkmKwnIJI2zneCOzVy8Lh8dyLzqPAeDbYz3NOZdKjWjhHAw==",
};

describe("reauth interop (Go agent signs -> TS panel verifies)", () => {
  it("verifies a signature produced by the Go controller", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt: GO_VECTOR.signedAt, signature: GO_VECTOR.signature },
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      devicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(true);
  });

  it("rejects the Go signature when the device identifier is tampered", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt: GO_VECTOR.signedAt, signature: GO_VECTOR.signature },
      deviceIdentifier: "vectra-attacker",
      devicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:01:00Z"),
    });
    expect(result.ok).toBe(false);
  });

  it("rejects the Go signature once outside the freshness window", () => {
    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt: GO_VECTOR.signedAt, signature: GO_VECTOR.signature },
      deviceIdentifier: GO_VECTOR.deviceIdentifier,
      devicePublicKey: GO_VECTOR.devicePublicKey,
      now: new Date("2026-06-29T16:30:00Z"),
    });
    expect(result.ok).toBe(false);
  });
});
