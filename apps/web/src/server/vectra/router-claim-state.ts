import { createHash, createHmac, createPublicKey, verify } from "node:crypto";

import type {
  RouterClaim,
  RouterClaimKey,
  RouterRegisterProof,
} from "@vectra/contracts";
import { routerCredentials, type routers } from "@vectra/db";
import { eq } from "drizzle-orm";

import { env } from "~/env";
import type { db } from "~/server/db";

/**
 * ADR-0006 claim state the router control plane (register / check-in) needs.
 * A leaf module: the claim service (router-claims.ts) builds on it, and
 * router-control.ts reads it, so neither has to import the other.
 */

type DatabaseClient = Pick<typeof db, "select">;
type RouterRow = typeof routers.$inferSelect;

export const CLAIM_CODE_PREFIX = "vectra-claim-code/v1:";
export const CLAIM_CODE_HASH_PREFIX = "vectra-claim-codehash/v1:";
export const CLAIM_CODE_LENGTH = 8;
export const CLAIM_NONCE_BYTES = 16;

// Crockford base32: no I, L, O or U.
const CROCKFORD_ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const CROCKFORD_CODE = /^[0-9A-HJKMNP-TV-Z]+$/;

// The deliberately PUBLIC test key of router/vectra-controller-pro/ui/contract/
// claim-vector.json. Anyone can open a QR sealed to it, so it must never reach
// a production router (env.js refuses it too; this guards a skipped validation).
export const ROUTER_CLAIM_PUBLIC_TEST_KEY =
  "dsTt0kkqBdr7emCqoixBS+iz/r9Y86jyAxEzzKARNBs=";

/**
 * The canonical form of a code a person typed: upper case, spaces and dashes
 * dropped, Crockford's look-alikes folded (O -> 0, I and L -> 1). The folds can
 * never change a valid code — Crockford's alphabet has no I, L or O — they only
 * rescue a mistyped one. Null when the input cannot be a code at all.
 */
export function normalizeClaimCode(raw: string | null | undefined) {
  if (typeof raw !== "string") {
    return null;
  }

  const code = raw
    .toUpperCase()
    .replace(/[\s-]+/g, "")
    .replace(/O/g, "0")
    .replace(/[IL]/g, "1");

  return code.length === CLAIM_CODE_LENGTH && CROCKFORD_CODE.test(code)
    ? code
    : null;
}

/**
 * The code a claim nonce `n` stands for, as vctl derives it (internal/claim):
 * Crockford base32 of SHA-256("vectra-claim-code/v1:" || n)[:5] — 40 bits,
 * 8 characters, no padding.
 */
export function claimCodeFromNonce(nonce: Uint8Array) {
  const digest = createHash("sha256")
    .update(CLAIM_CODE_PREFIX)
    .update(nonce)
    .digest();

  let code = "";
  let buffered = 0;
  let bits = 0;
  for (const byte of digest.subarray(0, 5)) {
    buffered = (buffered << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      code += CROCKFORD_ALPHABET[(buffered >>> bits) & 31];
    }
    buffered &= (1 << bits) - 1;
  }
  return code;
}

/** The QR's 16-byte nonce from its base64, or null when it is not one. */
export function decodeClaimNonce(value: string) {
  const bytes = decodeBase64Loose(value);
  return bytes?.length === CLAIM_NONCE_BYTES ? bytes : null;
}

/** The digest a router reports on check-in for the code it shows. */
export function hashClaimCode(code: string) {
  return createHash("sha256")
    .update(`${CLAIM_CODE_HASH_PREFIX}${code}`)
    .digest("hex");
}

/**
 * The at-rest form of a reported code digest. An 8-character code carries only
 * 40 bits, so the router's plain sha256 is reversible offline in minutes; keyed
 * with the panel secret, a leaked column (dump, log, operator API) is useless
 * without the key. Lookups seal the candidate the same way and compare.
 */
export function sealClaimCodeHash(codeHash: string) {
  return createHmac("sha256", `vectra-claim-code-seal/v1:${env.VECTRA_SECRETS_KEY}`)
    .update(codeHash.toLowerCase())
    .digest("hex");
}

type ClaimCodeSlots = Pick<
  RouterRow,
  | "claimCodeHash"
  | "claimExpiresAt"
  | "previousClaimCodeHash"
  | "previousClaimExpiresAt"
>;

function isLive(expiresAt: Date | null, now: Date) {
  return expiresAt !== null && expiresAt.getTime() > now.getTime();
}

/**
 * The router columns a check-in `claim` maps to.
 *
 * The router replaces its code every 10 minutes and promises the replaced one
 * stays valid until its own expiry (2 minutes past the replacement). So when a
 * check-in brings a NEW code, the current one moves to the previous slot with
 * its expiry instead of being dropped. An absent/null claim clears both: the
 * router stops reporting a code once it is linked, and no code may stay
 * claimable after that.
 */
export function resolveCheckInClaimColumns(
  existing: ClaimCodeSlots,
  claim: RouterClaim | null | undefined,
  now = new Date(),
) {
  if (!claim) {
    return {
      claimCodeHash: null,
      claimExpiresAt: null,
      previousClaimCodeHash: null,
      previousClaimExpiresAt: null,
    };
  }

  const sealed = sealClaimCodeHash(claim.codeHash);
  const expiresAt = new Date(claim.expiresAt);
  // Same code as before: the previous slot stays as it is (unless it lapsed).
  // A new code: the one it replaces is kept while it is still valid.
  const [keptHash, keptExpiresAt] =
    existing.claimCodeHash === sealed
      ? [existing.previousClaimCodeHash, existing.previousClaimExpiresAt]
      : [existing.claimCodeHash, existing.claimExpiresAt];
  const keep = keptHash !== null && isLive(keptExpiresAt, now);

  return {
    claimCodeHash: sealed,
    claimExpiresAt: expiresAt,
    previousClaimCodeHash: keep ? keptHash : null,
    previousClaimExpiresAt: keep ? keptExpiresAt : null,
  };
}

/**
 * The sealed codes a router is showing now: its current one and, until its
 * own expiry, the one it just replaced.
 */
export function liveClaimCodeHashes(router: ClaimCodeSlots, now: Date) {
  const live: string[] = [];
  if (router.claimCodeHash && isLive(router.claimExpiresAt, now)) {
    live.push(router.claimCodeHash);
  }
  if (router.previousClaimCodeHash && isLive(router.previousClaimExpiresAt, now)) {
    live.push(router.previousClaimCodeHash);
  }
  return live;
}

function decodeBase64Loose(value: string) {
  const trimmed = value.trim();
  if (!/^[A-Za-z0-9+/_-]+={0,2}$/.test(trimmed)) {
    return null;
  }

  const bytes = Buffer.from(
    trimmed.replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, ""),
    "base64",
  );
  return bytes.length > 0 ? bytes : null;
}

/**
 * Whether two renderings of a device's ed25519 public key are the same key.
 * The router sends standard padded base64; a partner may re-encode it
 * (url-safe, unpadded), so bytes are compared when both sides decode.
 */
export function devicePublicKeysMatch(
  stored: string | null | undefined,
  presented: string | null | undefined,
) {
  if (!stored || !presented) {
    return false;
  }

  const storedBytes = decodeBase64Loose(stored);
  const presentedBytes = decodeBase64Loose(presented);
  if (storedBytes && presentedBytes) {
    return storedBytes.equals(presentedBytes);
  }

  return stored.trim() === presented.trim();
}

/** The key the router seals its QR to, when configured. */
export function resolveRouterClaimKey(): RouterClaimKey | undefined {
  const publicKey =
    typeof env.VECTRA_ROUTER_CLAIM_PUBKEY === "string"
      ? env.VECTRA_ROUTER_CLAIM_PUBKEY.trim()
      : "";
  // Parsed to a number by env validation; a raw string when it is skipped.
  const rawKid = env.VECTRA_ROUTER_CLAIM_KID as unknown;
  const kid =
    typeof rawKid === "number"
      ? rawKid
      : typeof rawKid === "string" && rawKid.trim().length > 0
        ? Number(rawKid)
        : Number.NaN;

  // vctl (claim.ParseKey) takes a key id that fits a byte.
  if (!publicKey || !Number.isInteger(kid) || kid < 0 || kid > 255) {
    return undefined;
  }
  // Never hand production routers the public test key (see its definition).
  if (
    env.NODE_ENV === "production" &&
    publicKey === ROUTER_CLAIM_PUBLIC_TEST_KEY
  ) {
    return undefined;
  }

  return { kid, publicKey };
}

export function resolveBotUsername() {
  const username =
    typeof env.VECTRA_CONNECT_BOT_USERNAME === "string"
      ? env.VECTRA_CONNECT_BOT_USERNAME.trim().replace(/^@/, "")
      : "";
  return username.length > 0 ? username : undefined;
}

/**
 * A router its Vectra account has unlinked (partner unbind) and nobody has
 * claimed since. It is out of service while it waits for a new owner: it is
 * told to drop the previous owner's config, and it raises no fleet or rescue
 * alerts.
 */
export function isReleasedAwaitingOwner(
  router: Pick<RouterRow, "releasedAt" | "ownerRef">,
) {
  return router.releasedAt !== null && !router.ownerRef;
}

/**
 * The ADR-0006 fields every register and check-in response carries.
 *
 * claimKey / botUsername are omitted while unconfigured (the feature is off).
 * `owner` is ALWAYS sent: vctl reads an absent owner as "no news" and keeps
 * what it last knew, so an unlinked router must get an explicit null — without
 * it a router whose claim was unbound would go on showing itself as linked.
 * `released: true` is sent only for a released router; otherwise the field is
 * absent, because `owner: null` alone is what every fleet router receives and
 * must never be read as "wipe yourself".
 */
export function buildRouterClaimResponseFields(
  router: Pick<RouterRow, "ownerRef" | "ownerLabel" | "releasedAt">,
) {
  const claimKey = resolveRouterClaimKey();
  const botUsername = resolveBotUsername();
  const ownerLabel = router.ownerLabel?.trim() ?? "";

  return {
    ...(claimKey ? { claimKey } : {}),
    ...(botUsername ? { botUsername } : {}),
    owner: router.ownerRef
      ? { ownerRef: router.ownerRef, label: ownerLabel.length > 0 ? ownerLabel : "Vectra" }
      : null,
    ...(isReleasedAwaitingOwner(router) ? { released: true as const } : {}),
  };
}

export const REGISTER_PROOF_WINDOW_SECONDS = 300;

// DER prefix of an ed25519 SubjectPublicKeyInfo; the raw 32-byte key follows.
const ED25519_SPKI_PREFIX = Buffer.from("302a300506032b6570032100", "hex");

/**
 * What a register proof signs, exactly as vctl builds it
 * (claim.RegisterMessage): the UTF-8 bytes
 * "vectra-register/v1\n<deviceIdentifier>\n<timestamp>", unix seconds, base 10.
 */
export function registerProofMessage(
  deviceIdentifier: string,
  timestamp: number,
) {
  return Buffer.from(
    `vectra-register/v1\n${deviceIdentifier}\n${timestamp}`,
    "utf8",
  );
}

export type RegisterProofCheck =
  | "valid"
  | "missing"
  | "stale"
  | "bad_key"
  | "bad_signature";

/**
 * Whether `proof` shows its sender holds the ed25519 key `publicKey` (base64
 * of 32 raw bytes) for `deviceIdentifier`, signed within +-300 s of now.
 */
export function checkRegisterProof(args: {
  deviceIdentifier: string;
  publicKey: string;
  proof: RouterRegisterProof | null | undefined;
  nowMs?: number;
}): RegisterProofCheck {
  if (!args.proof) {
    return "missing";
  }

  const nowSeconds = Math.floor((args.nowMs ?? Date.now()) / 1000);
  if (
    Math.abs(nowSeconds - args.proof.timestamp) > REGISTER_PROOF_WINDOW_SECONDS
  ) {
    return "stale";
  }

  const rawKey = decodeBase64Loose(args.publicKey);
  if (rawKey?.length !== 32) {
    return "bad_key";
  }
  const signature = decodeBase64Loose(args.proof.signature);
  if (signature?.length !== 64) {
    return "bad_signature";
  }

  try {
    const key = createPublicKey({
      key: Buffer.concat([ED25519_SPKI_PREFIX, rawKey]),
      format: "der",
      type: "spki",
    });
    return verify(
      null,
      registerProofMessage(args.deviceIdentifier, args.proof.timestamp),
      key,
      signature,
    )
      ? "valid"
      : "bad_signature";
  } catch {
    return "bad_key";
  }
}

export type PreclaimAdoption =
  | { adopt: true }
  | {
      adopt: false;
      reason:
        | "not_preclaimed"
        | "key_mismatch"
        | "proof_missing"
        | "proof_stale"
        | "proof_invalid";
    };

/**
 * Whether an unauthenticated registration may take over an existing record:
 * only a record the partner API created ahead of the router's first contact
 * (claim by device, ADR-0006) — never seen, never issued an agent token, the
 * backend-verified device key held in a reserved "bootstrap" credential — and
 * only for a router that PROVES it holds that key: a register proof signed by
 * it within +-300 s. Presenting the key is not enough; it is public (it is in
 * the QR and in every inventory). Every other existing record still requires
 * the router's token.
 */
export async function evaluatePreclaimAdoptionWithDb(
  client: DatabaseClient,
  router: Pick<RouterRow, "id" | "lastSeenAt" | "deviceIdentifier">,
  presented: {
    devicePublicKey: string;
    proof: RouterRegisterProof | null | undefined;
  },
  nowMs = Date.now(),
): Promise<PreclaimAdoption> {
  if (router.lastSeenAt) {
    return { adopt: false, reason: "not_preclaimed" };
  }

  const credentials = await client
    .select()
    .from(routerCredentials)
    .where(eq(routerCredentials.routerId, router.id));

  const bootstrap = credentials.find(
    (credential) => credential.type === "bootstrap" && !credential.revokedAt,
  );
  if (
    !bootstrap ||
    credentials.some((credential) => credential.type === "agent_token")
  ) {
    return { adopt: false, reason: "not_preclaimed" };
  }

  if (!devicePublicKeysMatch(bootstrap.devicePublicKey, presented.devicePublicKey)) {
    return { adopt: false, reason: "key_mismatch" };
  }

  const proof = checkRegisterProof({
    deviceIdentifier: router.deviceIdentifier,
    publicKey: bootstrap.devicePublicKey,
    proof: presented.proof,
    nowMs,
  });
  if (proof === "valid") {
    return { adopt: true };
  }
  return {
    adopt: false,
    reason:
      proof === "missing"
        ? "proof_missing"
        : proof === "stale"
          ? "proof_stale"
          : "proof_invalid",
  };
}
