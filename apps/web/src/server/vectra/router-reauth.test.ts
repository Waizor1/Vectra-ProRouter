import { generateKeyPairSync, sign as cryptoSign } from "node:crypto";
import { beforeEach, describe, expect, it, vi } from "vitest";

// consumeReauthProof reads the module `db` only as a default fallback; every
// ledger test passes its own stub, so a real client is never built or reached.
import {
  ROUTER_REAUTH_MESSAGE_PREFIX,
  ROUTER_REAUTH_SKEW_SECONDS,
  buildRouterReauthMessage,
  canIssueRegistrationToken,
  consumeReauthProof,
  resetReauthReplayStateForTest,
  verifyRouterReauthProof,
} from "./router-control";

const ROUTER_ID = "11111111-1111-4111-8111-111111111111";
const OTHER_ROUTER = "22222222-2222-4222-8222-222222222222";
const DEVICE_IDENTIFIER = "router-reauth-device-1";
const NOW = new Date("2026-06-29T12:00:00.000Z");

/**
 * Mirrors the contract the Go agent lane implements: an ed25519 keypair whose
 * RAW 32-byte public key is stored base64-std in routerCredentials.devicePublicKey.
 */
function generateDeviceKeypair() {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const rawPublicKey = publicKey
    .export({ type: "spki", format: "der" })
    .subarray(-32);
  return {
    privateKey,
    devicePublicKeyBase64: rawPublicKey.toString("base64"),
  };
}

function signV2(
  privateKey: ReturnType<typeof generateKeyPairSync>["privateKey"],
  routerId: string,
  deviceIdentifier: string,
  newDevicePublicKey: string,
  signedAt: string,
): string {
  const message = Buffer.from(
    buildRouterReauthMessage(
      routerId,
      deviceIdentifier,
      newDevicePublicKey,
      signedAt,
    ),
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
  it("produces the exact canonical v2 bytes the agent signer must match", () => {
    const signedAt = "2026-06-29T11:59:30Z";
    expect(
      buildRouterReauthMessage(
        ROUTER_ID,
        DEVICE_IDENTIFIER,
        "KEY_BASE64",
        signedAt,
      ),
    ).toBe(
      `${ROUTER_REAUTH_MESSAGE_PREFIX}${ROUTER_ID}\n${DEVICE_IDENTIFIER}\nKEY_BASE64\n${signedAt}`,
    );
    expect(ROUTER_REAUTH_MESSAGE_PREFIX).toBe("vectra-router-reauth:v2\n");
    expect(ROUTER_REAUTH_SKEW_SECONDS).toBe(300);
  });
});

describe("verifyRouterReauthProof (v2 binding)", () => {
  it("accepts a valid same-key proof within the skew window", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expect(result.ok).toBe(true);
  });

  it("rejects a proof replayed against a DIFFERENT router id", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    // Signed for ROUTER_ID; an attacker presents it for OTHER_ROUTER.
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: OTHER_ROUTER,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects an attacker-chosen NEW key: the stored key signed the real key, not the attacker's", () => {
    const stored = generateDeviceKeypair();
    const attacker = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    // The captured proof was signed by the legit (stored) key over the legit
    // key. The attacker replays it but submits their own key.
    const signature = signV2(
      stored.privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      stored.devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: attacker.devicePublicKeyBase64,
      storedDevicePublicKey: stored.devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a v1-format proof (binds neither routerId nor key) against v2 bytes", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    // Sign the OLD v1 message shape; it must not verify under v2.
    const v1Message = Buffer.from(
      `vectra-router-reauth:v1\n${DEVICE_IDENTIFIER}\n${signedAt}`,
      "utf8",
    );
    const signature = cryptoSign(null, v1Message, privateKey).toString("base64");

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a garbage / invalid signature", () => {
    const { devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();

    const result = verifyRouterReauthProof({
      recoveryProof: {
        signedAt,
        signature: Buffer.from("not-a-real-signature".repeat(4)).toString(
          "base64",
        ),
      },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a non-canonical re-encoding of an otherwise valid signature (FIX B)", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const canonical = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );
    // Same 64 signature bytes, re-encoded as unpadded base64url: a DIFFERENT
    // string that is not canonical std base64. It must be refused before verify.
    const nonCanonical = Buffer.from(canonical, "base64").toString("base64url");
    expect(nonCanonical).not.toBe(canonical);

    expectRejected(
      verifyRouterReauthProof({
        recoveryProof: { signedAt, signature: nonCanonical },
        routerId: ROUTER_ID,
        deviceIdentifier: DEVICE_IDENTIFIER,
        submittedDevicePublicKey: devicePublicKeyBase64,
        storedDevicePublicKey: devicePublicKeyBase64,
        now: NOW,
      }),
      "signature_invalid",
    );

    // The canonical form of the SAME signature still verifies.
    expect(
      verifyRouterReauthProof({
        recoveryProof: { signedAt, signature: canonical },
        routerId: ROUTER_ID,
        deviceIdentifier: DEVICE_IDENTIFIER,
        submittedDevicePublicKey: devicePublicKeyBase64,
        storedDevicePublicKey: devicePublicKeyBase64,
        now: NOW,
      }).ok,
    ).toBe(true);
  });

  it("rejects a proof whose signedAt is older than the 300s window", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = new Date(NOW.getTime() - 301_000).toISOString();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_outside_window");
  });

  it("rejects a proof whose signedAt is in the future beyond the skew", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = new Date(NOW.getTime() + 301_000).toISOString();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_outside_window");
  });

  it("rejects a valid signature made with a different device's key", () => {
    const signer = generateDeviceKeypair();
    const stored = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signV2(
      signer.privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      stored.devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: stored.devicePublicKeyBase64,
      // stored credential belongs to a DIFFERENT keypair than the signer
      storedDevicePublicKey: stored.devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a signature bound to a different deviceIdentifier", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      "some-other-device",
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "signature_invalid");
  });

  it("rejects a malformed signedAt timestamp", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      "not-a-date",
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt: "not-a-date", signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: devicePublicKeyBase64,
      now: NOW,
    });

    expectRejected(result, "timestamp_invalid");
  });

  it("rejects when the stored device public key is malformed base64 / wrong length", () => {
    const { privateKey, devicePublicKeyBase64 } = generateDeviceKeypair();
    const signedAt = NOW.toISOString();
    const signature = signV2(
      privateKey,
      ROUTER_ID,
      DEVICE_IDENTIFIER,
      devicePublicKeyBase64,
      signedAt,
    );

    const result = verifyRouterReauthProof({
      recoveryProof: { signedAt, signature },
      routerId: ROUTER_ID,
      deviceIdentifier: DEVICE_IDENTIFIER,
      submittedDevicePublicKey: devicePublicKeyBase64,
      storedDevicePublicKey: "not-valid-key",
      now: NOW,
    });

    expectRejected(result, "public_key_invalid");
  });
});

// A stateful stand-in for the single-use ledger: insert(...).onConflictDoNothing()
// deduplicates by (routerId, proofSha256) exactly as the unique index does.
function createReauthLedgerStub() {
  const consumed = new Set<string>();
  let failInsert = false;
  let lastValues: Record<string, unknown> | null = null;
  let deleteCalls = 0;

  const db = {
    delete() {
      deleteCalls += 1;
      return { where: () => Promise.resolve([]) };
    },
    insert() {
      return {
        values(values: Record<string, unknown>) {
          lastValues = values;
          const statement = {
            onConflictDoNothing() {
              return statement;
            },
            returning: async () => {
              if (failInsert) {
                throw new Error("relation does not exist");
              }
              const key = `${values.routerId as string}|${values.proofSha256 as string}`;
              if (consumed.has(key)) {
                return [];
              }
              consumed.add(key);
              return [{ proofSha256: values.proofSha256 }];
            },
          };
          return statement;
        },
      };
    },
  };

  return {
    db: db as unknown as Parameters<typeof consumeReauthProof>[0]["database"],
    consumed,
    get deleteCalls() {
      return deleteCalls;
    },
    get lastValues() {
      return lastValues;
    },
    failInsert(value: boolean) {
      failInsert = value;
    },
  };
}

beforeEach(() => {
  resetReauthReplayStateForTest();
});

describe("consumeReauthProof (single-use)", () => {
  const SIGNED_AT = NOW.toISOString();

  it("accepts a fresh proof (replay:false) and records it", async () => {
    const ledger = createReauthLedgerStub();
    const result = await consumeReauthProof({
      routerId: ROUTER_ID,
      signature: "sig-fresh",
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    });
    expect(result.replay).toBe(false);
    expect(ledger.consumed.size).toBe(1);
    expect(ledger.deleteCalls).toBe(1);
  });

  it("flags the same (router, proof) consumed a second time as a replay", async () => {
    const ledger = createReauthLedgerStub();
    const call = () =>
      consumeReauthProof({
        routerId: ROUTER_ID,
        signature: "sig-reused",
        signedAt: SIGNED_AT,
        now: NOW,
        database: ledger.db,
      });
    expect((await call()).replay).toBe(false);
    expect((await call()).replay).toBe(true);
    expect(ledger.consumed.size).toBe(1);
  });

  it("keeps the same proof distinct per router (keyed by routerId)", async () => {
    const ledger = createReauthLedgerStub();
    const sharedArgs = {
      signature: "sig-shared",
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    };
    expect(
      (await consumeReauthProof({ routerId: ROUTER_ID, ...sharedArgs })).replay,
    ).toBe(false);
    expect(
      (await consumeReauthProof({ routerId: OTHER_ROUTER, ...sharedArgs }))
        .replay,
    ).toBe(false);
    expect(ledger.consumed.size).toBe(2);
  });

  it("fails open (replay:false) and warns once on a database error, never throwing", async () => {
    const ledger = createReauthLedgerStub();
    ledger.failInsert(true);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const result = await consumeReauthProof({
      routerId: ROUTER_ID,
      signature: "sig-err",
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    });

    expect(result.replay).toBe(false);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it("maps a non-canonical re-encoding to the SAME consumed key so a replay is caught (FIX B)", async () => {
    const ledger = createReauthLedgerStub();
    const { privateKey } = generateDeviceKeypair();
    // One real 64-byte ed25519 signature, two encodings of the SAME bytes.
    const canonical = signV2(privateKey, ROUTER_ID, DEVICE_IDENTIFIER, "k", SIGNED_AT);
    const nonCanonical = Buffer.from(canonical, "base64").toString("base64url");
    expect(nonCanonical).not.toBe(canonical);

    const first = await consumeReauthProof({
      routerId: ROUTER_ID,
      signature: canonical,
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    });
    const second = await consumeReauthProof({
      routerId: ROUTER_ID,
      signature: nonCanonical,
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    });

    expect(first.replay).toBe(false);
    expect(second.replay).toBe(true);
    // Both encodings collapsed to ONE ledger key (sha256 of the decoded bytes).
    expect(ledger.consumed.size).toBe(1);
  });

  it("gives the ledger row a TTL of signedAt + skew", async () => {
    const ledger = createReauthLedgerStub();
    await consumeReauthProof({
      routerId: ROUTER_ID,
      signature: "sig-ttl",
      signedAt: SIGNED_AT,
      now: NOW,
      database: ledger.db,
    });
    const expiresAt = ledger.lastValues?.expiresAt as Date;
    expect(expiresAt.getTime()).toBe(
      NOW.getTime() + ROUTER_REAUTH_SKEW_SECONDS * 1000,
    );
  });
});

describe("registerRouter recovery gating", () => {
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
