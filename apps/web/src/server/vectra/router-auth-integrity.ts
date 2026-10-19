// Router auth integrity: the pure, byte-exact half shared with the Go agent.
// No env, no database: every function here takes its key and clock as
// arguments, so the golden vectors in
// packages/contracts/fixtures/router-auth-vectors.json pin them exactly.
// Contract: ai_docs/develop/features/router-auth-integrity-and-native-runtime.md.

import {
  createHash,
  createHmac,
  createPublicKey,
  randomBytes,
  timingSafeEqual,
  verify as cryptoVerify,
  type KeyObject,
} from "node:crypto";
import { isIP } from "node:net";

import { ROUTER_DEVICE_BINDING_PATTERN } from "@vectra/contracts";

// ---------------------------------------------------------------------------
// Key material. No new secret: every MAC key is a subkey of
// VECTRA_SECRETS_KEY, HMAC-SHA256 keyed by its UTF-8 bytes over a label.
// ---------------------------------------------------------------------------

export const ENROLLMENT_TOKEN_SUBKEY_LABEL =
  "vectra-router-auth/enrollment-token/v1";
export const CHECKIN_NONCE_SUBKEY_LABEL = "vectra-router-auth/checkin-nonce/v1";

export function deriveRouterAuthSubkey(secretsKey: string, label: string) {
  return createHmac("sha256", Buffer.from(secretsKey, "utf8"))
    .update(label, "utf8")
    .digest();
}

/** Allowed clock skew, in seconds, between the router or token and the panel. */
export const ROUTER_AUTH_CLOCK_SKEW_SECONDS = 300;

// Canonical base 10 unix seconds: no sign, no leading zeros. The MAC covers
// the decimal string, so only the one spelling the panel mints is accepted.
const UNIX_SECONDS_PATTERN = "(0|[1-9][0-9]{0,14})";
// base64url without padding of a 32-byte HMAC-SHA256.
const MAC_B64URL_PATTERN = "([A-Za-z0-9_-]{43})";
// base64url without padding of 16 random bytes.
const RAND_B64URL_PATTERN = "([A-Za-z0-9_-]{22})";

function macBase64Url(key: Buffer, message: string) {
  return createHmac("sha256", key).update(message, "utf8").digest("base64url");
}

/** Constant-time equality of two MACs in their base64url spelling. */
function macMatches(expected: string, presented: string) {
  const left = Buffer.from(expected, "utf8");
  const right = Buffer.from(presented, "utf8");
  return left.length === right.length && timingSafeEqual(left, right);
}

// ---------------------------------------------------------------------------
// A1. Provisioning token for a router's first registration.
//   token = "vpt1." + exp + "." + base64url_nopad(HMAC-SHA256(K_enroll,
//           "vectra-enrollment-token/v1\n" + deviceIdentifier + "\n" + exp))
// ---------------------------------------------------------------------------

export const ENROLLMENT_TOKEN_PREFIX = "vpt1";
/** A token can never be minted (or accepted) longer-lived than a week. */
export const ENROLLMENT_TOKEN_MAX_TTL_SECONDS = 7 * 24 * 60 * 60;

const ENROLLMENT_TOKEN_PATTERN = new RegExp(
  `^${ENROLLMENT_TOKEN_PREFIX}\\.${UNIX_SECONDS_PATTERN}\\.${MAC_B64URL_PATTERN}$`,
);

export type EnrollmentTokenVerdict =
  | "valid"
  | "absent"
  | "malformed"
  | "expired"
  | "ttl_too_long"
  | "mac_mismatch";

function enrollmentTokenMac(
  secretsKey: string,
  deviceIdentifier: string,
  expiresAtUnix: string,
) {
  return macBase64Url(
    deriveRouterAuthSubkey(secretsKey, ENROLLMENT_TOKEN_SUBKEY_LABEL),
    `vectra-enrollment-token/v1\n${deviceIdentifier}\n${expiresAtUnix}`,
  );
}

export function mintEnrollmentToken(args: {
  secretsKey: string;
  deviceIdentifier: string;
  expiresAtUnix: number;
}) {
  if (!Number.isSafeInteger(args.expiresAtUnix) || args.expiresAtUnix < 0) {
    throw new Error("Enrollment token expiry must be non-negative unix seconds.");
  }
  const exp = String(args.expiresAtUnix);
  return `${ENROLLMENT_TOKEN_PREFIX}.${exp}.${enrollmentTokenMac(args.secretsKey, args.deviceIdentifier, exp)}`;
}

/**
 * The MAC is checked before the expiry, so the expiry of a token the panel
 * never minted is not reported as if it meant anything.
 */
export function verifyEnrollmentToken(args: {
  secretsKey: string;
  token: string | null | undefined;
  deviceIdentifier: string;
  nowUnix: number;
}): EnrollmentTokenVerdict {
  if (args.token === undefined || args.token === null || args.token === "") {
    return "absent";
  }
  const match = ENROLLMENT_TOKEN_PATTERN.exec(args.token);
  const exp = match?.[1];
  const presentedMac = match?.[2];
  if (!exp || !presentedMac) {
    return "malformed";
  }
  if (
    !macMatches(
      enrollmentTokenMac(args.secretsKey, args.deviceIdentifier, exp),
      presentedMac,
    )
  ) {
    return "mac_mismatch";
  }
  const expiresAtUnix = Number(exp);
  if (args.nowUnix >= expiresAtUnix) {
    return "expired";
  }
  if (
    expiresAtUnix - args.nowUnix >
    ENROLLMENT_TOKEN_MAX_TTL_SECONDS + ROUTER_AUTH_CLOCK_SKEW_SECONDS
  ) {
    return "ttl_too_long";
  }
  return "valid";
}

// ---------------------------------------------------------------------------
// A2. Check-in nonce and signature.
//   nonce   = "vcn1." + iat + "." + rand + "." + base64url_nopad(HMAC-SHA256(
//             K_nonce, "vectra-checkin-nonce/v1\n" + routerId + "\n" + iat + "\n" + rand))
//   message = "vectra-router-checkin:v1\n" + nonce + "\n" + routerId + "\n"
//             + hex_lower(sha256(raw request body bytes))
// ---------------------------------------------------------------------------

export const CHECKIN_NONCE_PREFIX = "vcn1";
export const CHECKIN_NONCE_MAX_AGE_SECONDS = 24 * 60 * 60;
export const CHECKIN_SIGNATURE_MESSAGE_PREFIX = "vectra-router-checkin:v1\n";
export const CHECKIN_NONCE_HEADER = "x-vectra-checkin-nonce";
export const CHECKIN_SIGNATURE_HEADER = "x-vectra-checkin-signature";

const CHECKIN_NONCE_PATTERN = new RegExp(
  `^${CHECKIN_NONCE_PREFIX}\\.${UNIX_SECONDS_PATTERN}\\.${RAND_B64URL_PATTERN}\\.${MAC_B64URL_PATTERN}$`,
);

export type CheckInNonceVerdict = "valid" | "nonce_invalid" | "nonce_expired";

export type CheckInSignatureVerdict =
  | "valid"
  | "absent"
  | "nonce_invalid"
  | "nonce_expired"
  | "signature_invalid"
  | "public_key_invalid";

function checkInNonceMac(
  secretsKey: string,
  routerId: string,
  issuedAtUnix: string,
  random: string,
) {
  return macBase64Url(
    deriveRouterAuthSubkey(secretsKey, CHECKIN_NONCE_SUBKEY_LABEL),
    `vectra-checkin-nonce/v1\n${routerId}\n${issuedAtUnix}\n${random}`,
  );
}

export function mintCheckInNonce(args: {
  secretsKey: string;
  routerId: string;
  issuedAtUnix: number;
  /** 16 bytes; fresh random ones unless a test pins them. */
  random?: Buffer;
}) {
  if (!Number.isSafeInteger(args.issuedAtUnix) || args.issuedAtUnix < 0) {
    throw new Error("Check-in nonce issue time must be non-negative unix seconds.");
  }
  const random = args.random ?? randomBytes(16);
  if (random.length !== 16) {
    throw new Error("Check-in nonce randomness must be 16 bytes.");
  }
  const iat = String(args.issuedAtUnix);
  const rand = random.toString("base64url");
  return `${CHECKIN_NONCE_PREFIX}.${iat}.${rand}.${checkInNonceMac(args.secretsKey, args.routerId, iat, rand)}`;
}

/**
 * Valid: the MAC matches this router, it was issued no more than the skew in
 * the future and no more than 24 h ago. A nonce dated beyond the skew in the
 * future cannot be the panel's own and is `nonce_invalid`.
 */
export function verifyCheckInNonce(args: {
  secretsKey: string;
  nonce: string;
  routerId: string;
  nowUnix: number;
}): CheckInNonceVerdict {
  const match = CHECKIN_NONCE_PATTERN.exec(args.nonce);
  const iat = match?.[1];
  const random = match?.[2];
  const presentedMac = match?.[3];
  if (!iat || !random || !presentedMac) {
    return "nonce_invalid";
  }
  if (
    !macMatches(
      checkInNonceMac(args.secretsKey, args.routerId, iat, random),
      presentedMac,
    )
  ) {
    return "nonce_invalid";
  }
  const issuedAtUnix = Number(iat);
  if (issuedAtUnix > args.nowUnix + ROUTER_AUTH_CLOCK_SKEW_SECONDS) {
    return "nonce_invalid";
  }
  if (args.nowUnix - issuedAtUnix > CHECKIN_NONCE_MAX_AGE_SECONDS) {
    return "nonce_expired";
  }
  return "valid";
}

export function sha256Hex(bytes: Uint8Array | string) {
  return createHash("sha256").update(bytes).digest("hex");
}

/**
 * The issued-at unix seconds of a syntactically valid check-in nonce, or null.
 * The policy layer uses it to bound the replay ledger's TTL without re-deriving
 * the MAC — `verifyCheckInNonce` has already proved the nonce by the time this
 * is called on a valid verdict.
 */
export function parseCheckInNonceIat(nonce: string): number | null {
  const match = CHECKIN_NONCE_PATTERN.exec(nonce);
  const iat = match?.[1];
  if (!iat) {
    return null;
  }
  const value = Number(iat);
  return Number.isSafeInteger(value) ? value : null;
}

export function buildCheckInSignatureMessage(
  nonce: string,
  routerId: string,
  bodySha256Hex: string,
) {
  return `${CHECKIN_SIGNATURE_MESSAGE_PREFIX}${nonce}\n${routerId}\n${bodySha256Hex}`;
}

/** DER prefix for an ed25519 SubjectPublicKeyInfo wrapping the raw 32-byte key. */
const ED25519_SPKI_DER_PREFIX = Buffer.from("302a300506032b6570032100", "hex");

/**
 * The device public key as routerCredentials.devicePublicKey stores it (std
 * base64 of the raw 32 bytes), as a Node key object; null when malformed.
 */
export function decodeRawEd25519PublicKey(devicePublicKey: string): KeyObject | null {
  let raw: Buffer;
  try {
    raw = Buffer.from(devicePublicKey, "base64");
  } catch {
    return null;
  }

  // base64 of a 32-byte ed25519 public key; anything else is malformed.
  if (raw.length !== 32) {
    return null;
  }

  try {
    return createPublicKey({
      key: Buffer.concat([ED25519_SPKI_DER_PREFIX, raw]),
      format: "der",
      type: "spki",
    });
  } catch {
    return null;
  }
}

// Standard, padded base64 of a 64-byte ed25519 signature.
const ED25519_SIGNATURE_B64_PATTERN = /^[A-Za-z0-9+/]{86}==$/;

/**
 * Strictly decodes a canonical standard-base64 ed25519 signature to its raw 64
 * bytes, or returns null when the input is not canonical. "Canonical" means ALL
 * of:
 *   - it matches the padded 86-char + "==" ed25519 signature pattern,
 *   - it decodes to exactly 64 bytes, and
 *   - re-encoding those bytes reproduces the exact input (so no alternate
 *     padding / base64url-alphabet / whitespace variant of the same signature
 *     slips through as a different string).
 *
 * Callers hash the returned BYTES (never the string) for the single-use ledger
 * key: that way a non-canonical re-encoding of the same signature maps to the
 * same consumed key instead of bypassing single-use (CWE-294). ed25519 in
 * OpenSSL is non-malleable (S < L), so the canonical decoded bytes are a sound
 * single-use identity for a verified proof.
 */
export function decodeCanonicalEd25519Signature(
  signature: string,
): Buffer | null {
  if (!ED25519_SIGNATURE_B64_PATTERN.test(signature)) {
    return null;
  }
  const decoded = Buffer.from(signature, "base64");
  if (decoded.length !== 64) {
    return null;
  }
  if (decoded.toString("base64") !== signature) {
    return null;
  }
  return decoded;
}

/**
 * Verifies a check-in signature against the device key of the credential the
 * bearer token authenticated. Either header missing is `absent`.
 */
export function verifyCheckInSignature(args: {
  secretsKey: string;
  routerId: string;
  nonce: string | null | undefined;
  signature: string | null | undefined;
  bodySha256Hex: string;
  devicePublicKey: string;
  nowUnix: number;
}): CheckInSignatureVerdict {
  if (!args.nonce || !args.signature) {
    return "absent";
  }
  const nonceVerdict = verifyCheckInNonce({
    secretsKey: args.secretsKey,
    nonce: args.nonce,
    routerId: args.routerId,
    nowUnix: args.nowUnix,
  });
  if (nonceVerdict !== "valid") {
    return nonceVerdict;
  }
  const publicKey = decodeRawEd25519PublicKey(args.devicePublicKey);
  if (!publicKey) {
    return "public_key_invalid";
  }
  if (!ED25519_SIGNATURE_B64_PATTERN.test(args.signature)) {
    return "signature_invalid";
  }
  const message = Buffer.from(
    buildCheckInSignatureMessage(args.nonce, args.routerId, args.bodySha256Hex),
    "utf8",
  );
  try {
    return cryptoVerify(
      null,
      message,
      publicKey,
      Buffer.from(args.signature, "base64"),
    )
      ? "valid"
      : "signature_invalid";
  } catch {
    return "signature_invalid";
  }
}

// ---------------------------------------------------------------------------
// A3/A4. Device binding and source cluster.
// ---------------------------------------------------------------------------

/**
 * The agent's binding formula, kept here as the panel-side reference the
 * shared vectors pin. The panel itself never sees the inputs: it only stores
 * and compares what the agent reports.
 */
export function computeDeviceBinding(boardName: string, mac: string) {
  return `v1:${sha256Hex(`vectra-device-binding/v1\n${boardName}\n${mac}`)}`;
}

/** A reported device binding, or null when absent or not a v1 binding. */
export function readReportedDeviceBinding(value: unknown): string | null {
  return typeof value === "string" && ROUTER_DEVICE_BINDING_PATTERN.test(value)
    ? value
    : null;
}

export const UNKNOWN_SOURCE_CLUSTER = "unknown";

/** The eight 16-bit groups of an IPv6 address, or null. */
function expandIpv6(address: string): number[] | null {
  if (isIP(address) !== 6) {
    return null;
  }
  const halves = address.split("::");
  if (halves.length > 2) {
    return null;
  }
  const groupsOf = (part: string | undefined) => {
    if (!part) {
      return [];
    }
    return part.split(":").flatMap((group) => {
      if (group.includes(".")) {
        // An embedded IPv4 tail fills the last two groups.
        const octets = group.split(".").map(Number);
        return [
          ((octets[0] ?? 0) << 8) | (octets[1] ?? 0),
          ((octets[2] ?? 0) << 8) | (octets[3] ?? 0),
        ];
      }
      return [Number.parseInt(group, 16)];
    });
  };
  const head = groupsOf(halves[0]);
  const tail = groupsOf(halves[1]);
  const groups =
    halves.length === 2
      ? [...head, ...Array<number>(8 - head.length - tail.length).fill(0), ...tail]
      : head;
  return groups.length === 8 && groups.every((group) => Number.isInteger(group))
    ? groups
    : null;
}

/**
 * The network a request came from, coarse enough that one router's dynamic
 * address stays inside it: IPv4 /24 ("a.b.c.0/24"), IPv6 /48
 * ("2001:db8:1::/48"), "unknown" for anything else (including the "unknown"
 * readRequestIp returns without the trusted proxy header).
 *
 * An IPv4-mapped IPv6 address (::ffff:a.b.c.d, either spelling) is the same
 * IPv4 client seen through a dual-stack socket, so it clusters as its IPv4
 * /24 rather than as one huge IPv6 /48 shared by every IPv4 client.
 */
export function sourceClusterForIp(ip: string | null | undefined) {
  const address = (ip ?? "").trim().replace(/%.*$/, "");
  if (isIP(address) === 4) {
    const [a, b, c] = address.split(".");
    return `${Number(a)}.${Number(b)}.${Number(c)}.0/24`;
  }
  const groups = expandIpv6(address);
  if (!groups) {
    return UNKNOWN_SOURCE_CLUSTER;
  }
  const mapped =
    groups.slice(0, 5).every((group) => group === 0) && groups[5] === 0xffff;
  if (mapped) {
    const high = groups[6] ?? 0;
    const low = groups[7] ?? 0;
    return `${high >> 8}.${high & 0xff}.${low >> 8}.0/24`;
  }
  return `${groups
    .slice(0, 3)
    .map((group) => group.toString(16))
    .join(":")}::/48`;
}
