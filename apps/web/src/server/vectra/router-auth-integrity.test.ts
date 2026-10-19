import { createPrivateKey, generateKeyPairSync, sign } from "node:crypto";
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  buildCheckInSignatureMessage,
  CHECKIN_NONCE_MAX_AGE_SECONDS,
  CHECKIN_NONCE_SUBKEY_LABEL,
  computeDeviceBinding,
  decodeRawEd25519PublicKey,
  deriveRouterAuthSubkey,
  ENROLLMENT_TOKEN_MAX_TTL_SECONDS,
  ENROLLMENT_TOKEN_SUBKEY_LABEL,
  mintCheckInNonce,
  mintEnrollmentToken,
  readReportedDeviceBinding,
  ROUTER_AUTH_CLOCK_SKEW_SECONDS,
  sha256Hex,
  sourceClusterForIp,
  verifyCheckInNonce,
  verifyCheckInSignature,
  verifyEnrollmentToken,
} from "./router-auth-integrity";

// Shared with the Go agent; every key and secret in it is fake and public.
const vectors = JSON.parse(
  readFileSync(
    new URL(
      "../../../../../packages/contracts/fixtures/router-auth-vectors.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as {
  secretsKey: string;
  enrollmentToken: { deviceIdentifier: string; expiresAtUnix: number; token: string };
  checkInNonce: {
    issuedAtUnix: number;
    nonce: string;
    randomBase64Url: string;
    routerId: string;
  };
  checkInSignature: {
    body: string;
    bodySha256Hex: string;
    devicePrivateKeyBase64: string;
    devicePublicKeyBase64: string;
    message: string;
    nonce: string;
    routerId: string;
    signatureBase64: string;
  };
  deviceBinding: { binding: string; boardName: string; mac: string };
};

const KEY = vectors.secretsKey;
const TOKEN = vectors.enrollmentToken;
const NONCE = vectors.checkInNonce;
const SIG = vectors.checkInSignature;

/** The vector's Go-format private key (seed || public) as a Node key. */
function vectorPrivateKey() {
  const seed = Buffer.from(SIG.devicePrivateKeyBase64, "base64").subarray(0, 32);
  return createPrivateKey({
    key: Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), seed]),
    format: "der",
    type: "pkcs8",
  });
}

describe("key material", () => {
  it("derives each subkey as HMAC-SHA256 keyed by the secrets key over its label", () => {
    const enroll = deriveRouterAuthSubkey(KEY, ENROLLMENT_TOKEN_SUBKEY_LABEL);
    const nonce = deriveRouterAuthSubkey(KEY, CHECKIN_NONCE_SUBKEY_LABEL);
    expect(enroll).toHaveLength(32);
    expect(nonce).toHaveLength(32);
    expect(enroll.equals(nonce)).toBe(false);
    expect(ENROLLMENT_TOKEN_SUBKEY_LABEL).toBe("vectra-router-auth/enrollment-token/v1");
    expect(CHECKIN_NONCE_SUBKEY_LABEL).toBe("vectra-router-auth/checkin-nonce/v1");
  });
});

describe("enrollment token (A1)", () => {
  const validNow = TOKEN.expiresAtUnix - 3600;

  it("mints the golden token byte for byte", () => {
    expect(
      mintEnrollmentToken({
        secretsKey: KEY,
        deviceIdentifier: TOKEN.deviceIdentifier,
        expiresAtUnix: TOKEN.expiresAtUnix,
      }),
    ).toBe(TOKEN.token);
  });

  it("accepts the golden token for its device before expiry", () => {
    expect(
      verifyEnrollmentToken({
        secretsKey: KEY,
        token: TOKEN.token,
        deviceIdentifier: TOKEN.deviceIdentifier,
        nowUnix: validNow,
      }),
    ).toBe("valid");
  });

  it("names every other verdict", () => {
    const verdict = (token: string | null | undefined, overrides: { device?: string; now?: number; key?: string } = {}) =>
      verifyEnrollmentToken({
        secretsKey: overrides.key ?? KEY,
        token,
        deviceIdentifier: overrides.device ?? TOKEN.deviceIdentifier,
        nowUnix: overrides.now ?? validNow,
      });

    expect(verdict(undefined)).toBe("absent");
    expect(verdict(null)).toBe("absent");
    expect(verdict("")).toBe("absent");
    expect(verdict("not-a-token")).toBe("malformed");
    expect(verdict(TOKEN.token.replace("vpt1.", "vpt2."))).toBe("malformed");
    expect(verdict(`vpt1.0${TOKEN.expiresAtUnix}.${TOKEN.token.split(".")[2]}`)).toBe("malformed");
    expect(verdict(`${TOKEN.token}A`)).toBe("malformed");
    // Another device, a forged expiry, another secrets key: same MAC fails.
    expect(verdict(TOKEN.token, { device: "vectra-ffffffffffff" })).toBe("mac_mismatch");
    expect(
      verdict(`vpt1.${TOKEN.expiresAtUnix + 60}.${TOKEN.token.split(".")[2]}`),
    ).toBe("mac_mismatch");
    expect(verdict(TOKEN.token, { key: "another-fake-secrets-key-0123456789" })).toBe("mac_mismatch");
    expect(verdict(TOKEN.token, { now: TOKEN.expiresAtUnix })).toBe("expired");
    expect(verdict(TOKEN.token, { now: TOKEN.expiresAtUnix + 1 })).toBe("expired");
  });

  it("refuses a token valid for longer than a week plus the skew", () => {
    const now = 1_790_000_000;
    const at = (ttl: number) =>
      verifyEnrollmentToken({
        secretsKey: KEY,
        token: mintEnrollmentToken({ secretsKey: KEY, deviceIdentifier: "d", expiresAtUnix: now + ttl }),
        deviceIdentifier: "d",
        nowUnix: now,
      });
    expect(at(ENROLLMENT_TOKEN_MAX_TTL_SECONDS + ROUTER_AUTH_CLOCK_SKEW_SECONDS)).toBe("valid");
    expect(at(ENROLLMENT_TOKEN_MAX_TTL_SECONDS + ROUTER_AUTH_CLOCK_SKEW_SECONDS + 1)).toBe("ttl_too_long");
  });
});

describe("check-in nonce (A2)", () => {
  const random = Buffer.from(NONCE.randomBase64Url, "base64url");

  it("mints the golden nonce byte for byte", () => {
    expect(random).toHaveLength(16);
    expect(
      mintCheckInNonce({
        secretsKey: KEY,
        routerId: NONCE.routerId,
        issuedAtUnix: NONCE.issuedAtUnix,
        random,
      }),
    ).toBe(NONCE.nonce);
  });

  it("is valid for 24 h after issue and up to the skew before it", () => {
    const at = (nowUnix: number, routerId = NONCE.routerId, nonce = NONCE.nonce) =>
      verifyCheckInNonce({ secretsKey: KEY, nonce, routerId, nowUnix });

    expect(at(NONCE.issuedAtUnix)).toBe("valid");
    expect(at(NONCE.issuedAtUnix + CHECKIN_NONCE_MAX_AGE_SECONDS)).toBe("valid");
    expect(at(NONCE.issuedAtUnix + CHECKIN_NONCE_MAX_AGE_SECONDS + 1)).toBe("nonce_expired");
    expect(at(NONCE.issuedAtUnix - ROUTER_AUTH_CLOCK_SKEW_SECONDS)).toBe("valid");
    expect(at(NONCE.issuedAtUnix - ROUTER_AUTH_CLOCK_SKEW_SECONDS - 1)).toBe("nonce_invalid");
    // Another router's nonce, a tampered one, garbage.
    expect(at(NONCE.issuedAtUnix, "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31")).toBe("nonce_invalid");
    expect(at(NONCE.issuedAtUnix, NONCE.routerId, NONCE.nonce.replace("vcn1.1791460800", "vcn1.1791460801"))).toBe("nonce_invalid");
    expect(at(NONCE.issuedAtUnix, NONCE.routerId, "vcn1.garbage")).toBe("nonce_invalid");
  });

  it("draws fresh randomness when none is pinned", () => {
    const mint = () => mintCheckInNonce({ secretsKey: KEY, routerId: NONCE.routerId, issuedAtUnix: NONCE.issuedAtUnix });
    const first = mint();
    expect(first).not.toBe(mint());
    expect(first).toMatch(/^vcn1\.1791460800\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}$/);
  });
});

describe("check-in signature (A2)", () => {
  const nowUnix = NONCE.issuedAtUnix + 60;
  const verify = (overrides: Partial<Parameters<typeof verifyCheckInSignature>[0]> = {}) =>
    verifyCheckInSignature({
      secretsKey: KEY,
      routerId: SIG.routerId,
      nonce: SIG.nonce,
      signature: SIG.signatureBase64,
      bodySha256Hex: SIG.bodySha256Hex,
      devicePublicKey: SIG.devicePublicKeyBase64,
      nowUnix,
      ...overrides,
    });

  it("reproduces the golden digest, message and signature", () => {
    expect(sha256Hex(Buffer.from(SIG.body, "utf8"))).toBe(SIG.bodySha256Hex);
    expect(buildCheckInSignatureMessage(SIG.nonce, SIG.routerId, SIG.bodySha256Hex)).toBe(SIG.message);
    // ed25519 is deterministic: signing the message again gives the vector.
    expect(
      sign(null, Buffer.from(SIG.message, "utf8"), vectorPrivateKey()).toString("base64"),
    ).toBe(SIG.signatureBase64);
    expect(decodeRawEd25519PublicKey(SIG.devicePublicKeyBase64)).not.toBeNull();
  });

  it("accepts the golden signature", () => {
    expect(verify()).toBe("valid");
  });

  it("names every other verdict", () => {
    expect(verify({ nonce: null })).toBe("absent");
    expect(verify({ signature: undefined })).toBe("absent");
    expect(verify({ signature: "" })).toBe("absent");
    expect(verify({ nonce: "vcn1.nope" })).toBe("nonce_invalid");
    expect(verify({ nowUnix: NONCE.issuedAtUnix + CHECKIN_NONCE_MAX_AGE_SECONDS + 1 })).toBe("nonce_expired");
    expect(verify({ devicePublicKey: Buffer.alloc(31, 1).toString("base64") })).toBe("public_key_invalid");
    expect(verify({ devicePublicKey: "" })).toBe("public_key_invalid");
    // The body a router signed is not the body that arrived.
    expect(verify({ bodySha256Hex: sha256Hex("{}") })).toBe("signature_invalid");
    expect(verify({ signature: "not base64 at all" })).toBe("signature_invalid");
    expect(verify({ signature: Buffer.alloc(64, 7).toString("base64") })).toBe("signature_invalid");
    // Signed by another device's key.
    const other = generateKeyPairSync("ed25519");
    expect(
      verify({
        signature: sign(null, Buffer.from(SIG.message, "utf8"), other.privateKey).toString("base64"),
      }),
    ).toBe("signature_invalid");
  });
});

describe("device binding (A4) and source cluster (A3)", () => {
  it("reproduces the golden binding and reads only well-formed ones", () => {
    const binding = vectors.deviceBinding;
    expect(computeDeviceBinding(binding.boardName, binding.mac)).toBe(binding.binding);
    expect(readReportedDeviceBinding(binding.binding)).toBe(binding.binding);
    expect(readReportedDeviceBinding(binding.binding.toUpperCase())).toBeNull();
    expect(readReportedDeviceBinding(`${binding.binding}0`)).toBeNull();
    expect(readReportedDeviceBinding("v2:" + "a".repeat(64))).toBeNull();
    expect(readReportedDeviceBinding(42)).toBeNull();
    expect(readReportedDeviceBinding(undefined)).toBeNull();
  });

  it("clusters IPv4 by /24, IPv6 by /48, IPv4-mapped IPv6 as its IPv4", () => {
    expect(sourceClusterForIp("203.0.113.77")).toBe("203.0.113.0/24");
    expect(sourceClusterForIp(" 198.51.100.9 ")).toBe("198.51.100.0/24");
    expect(sourceClusterForIp("2001:db8:1234:5678::1")).toBe("2001:db8:1234::/48");
    expect(sourceClusterForIp("2001:0DB8:0001:ffff:0:0:0:1")).toBe("2001:db8:1::/48");
    expect(sourceClusterForIp("2001:db8::1")).toBe("2001:db8:0::/48");
    expect(sourceClusterForIp("fe80::1%eth0")).toBe("fe80:0:0::/48");
    expect(sourceClusterForIp("::ffff:203.0.113.77")).toBe("203.0.113.0/24");
    expect(sourceClusterForIp("::ffff:cb00:714d")).toBe("203.0.113.0/24");
    expect(sourceClusterForIp("64:ff9b::203.0.113.77")).toBe("64:ff9b:0::/48");
  });

  it("is unknown for anything that is not an address", () => {
    for (const value of ["unknown", "", null, undefined, "999.1.1.1", "example.com", "1.2.3", "2001:db8::1::2"]) {
      expect(sourceClusterForIp(value)).toBe("unknown");
    }
  });
});
