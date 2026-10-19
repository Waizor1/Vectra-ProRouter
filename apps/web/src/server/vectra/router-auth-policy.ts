// Router auth integrity, the switch-dependent half: what the panel does with
// the verdicts of router-auth-integrity.ts while each switch is off or on.
// With every switch at its default nothing here changes an answer: no
// rejection, no new response key, no header read.
// Contract: ai_docs/develop/features/router-auth-integrity-and-native-runtime.md.

import { eventLog } from "@vectra/db";

import { env } from "~/env";
import { db } from "~/server/db";
import { MemoryWindowRateLimiter } from "~/server/vectra/public-install-rate-limit";
import {
  CHECKIN_NONCE_HEADER,
  CHECKIN_SIGNATURE_HEADER,
  ENROLLMENT_TOKEN_MAX_TTL_SECONDS,
  type CheckInSignatureVerdict,
  type EnrollmentTokenVerdict,
  mintCheckInNonce,
  mintEnrollmentToken,
  parseCheckInNonceIat,
  verifyCheckInSignature,
  verifyEnrollmentToken,
} from "~/server/vectra/router-auth-integrity";
import { consumeCheckInNonce } from "~/server/vectra/router-checkin-replay";

type EventWriter = Pick<typeof db, "insert">;
// The check-in signature gate both logs events and consumes nonces, so its
// database must offer the delete the replay ledger prunes with.
type CheckInGateDatabase = Pick<typeof db, "insert" | "delete">;

// A string "true" counts too: with SKIP_ENV_VALIDATION the raw value arrives
// untransformed, and "false" must then not read as on.
function flagOn(value: unknown) {
  return value === true || value === "true";
}

export function routerAuthFlags() {
  const requireCheckInSignature = flagOn(env.VECTRA_REQUIRE_CHECKIN_SIGNATURE);
  return {
    enrollmentTokenRequired: flagOn(env.VECTRA_ENROLLMENT_TOKEN_REQUIRED),
    // Requiring signatures implies issuing the nonces they are made over.
    issueCheckInNonce:
      requireCheckInSignature || flagOn(env.VECTRA_ISSUE_CHECKIN_NONCE),
    requireCheckInSignature,
    duplicateCredentialEnforce: flagOn(env.VECTRA_DUPLICATE_CREDENTIAL_ENFORCE),
    // F: require the v2 re-auth recovery proof (reject the legacy v1 form).
    // Off (default) accepts both, so a legacy router's v1 proof still works.
    requireReauthV2: flagOn(env.VECTRA_REQUIRE_REAUTH_V2),
  };
}

/**
 * A status error whose JSON answer is exactly `body` (toRouteErrorResponse
 * reads `responseBody`), for refusals the agent tells apart by `code`.
 */
export function routerErrorWithBody(
  status: number,
  body: { error: string; code: string } & Record<string, unknown>,
) {
  return Object.assign(new Error(body.error), { status, responseBody: body });
}

function unixSeconds(now: Date) {
  return Math.floor(now.getTime() / 1000);
}

// Event metadata and log lines carry the identifier an anonymous register
// sent; real ones are ~20 characters.
function boundedIdentifier(deviceIdentifier: string) {
  return deviceIdentifier.slice(0, 256);
}

// One log line whatever the identifier holds: control characters, quotes and
// backslashes are escaped; a real identifier ("vectra-<hex>") is unchanged.
function loggableIdentifier(deviceIdentifier: string) {
  return JSON.stringify(deviceIdentifier).slice(1, -1);
}

async function writeEventSafely(
  database: EventWriter,
  values: typeof eventLog.$inferInsert,
) {
  try {
    await database.insert(eventLog).values(values);
  } catch (error) {
    console.warn("[router-auth] event could not be recorded", {
      type: values.type,
      error: error instanceof Error ? error.name : typeof error,
    });
  }
}

// ---------------------------------------------------------------------------
// A1. Provisioning token.
// ---------------------------------------------------------------------------

export const ENROLLMENT_TOKEN_DEFAULT_TTL_HOURS = 24;
export const ENROLLMENT_TOKEN_MAX_TTL_HOURS =
  ENROLLMENT_TOKEN_MAX_TTL_SECONDS / 3600;

export const ENROLLMENT_TOKEN_REQUIRED_MESSAGE =
  "Router enrollment requires a valid provisioning token.";

export function mintEnrollmentTokenForDevice(args: {
  deviceIdentifier: string;
  ttlHours: number;
  now?: Date;
}) {
  if (
    !Number.isInteger(args.ttlHours) ||
    args.ttlHours < 1 ||
    args.ttlHours > ENROLLMENT_TOKEN_MAX_TTL_HOURS
  ) {
    throw new Error(
      `Enrollment token lifetime must be 1..${ENROLLMENT_TOKEN_MAX_TTL_HOURS} hours.`,
    );
  }
  const expiresAtUnix =
    unixSeconds(args.now ?? new Date()) + args.ttlHours * 3600;
  return {
    token: mintEnrollmentToken({
      secretsKey: env.VECTRA_SECRETS_KEY,
      deviceIdentifier: args.deviceIdentifier,
      expiresAtUnix,
    }),
    expiresAt: new Date(expiresAtUnix * 1000),
  };
}

/**
 * Whether a register body carried an enrollmentToken at all. The contract
 * drops one that is not a bounded string, so without this an unreadable
 * token would be reported as no token.
 */
export function enrollmentTokenPresented(body: unknown) {
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return false;
  }
  const raw = (body as { enrollmentToken?: unknown }).enrollmentToken;
  return raw !== undefined && raw !== null && raw !== "";
}

/**
 * Called only where a registration would create a NEW router record. Off:
 * never refuses, says what it would have refused. On: refuses (403) any
 * verdict but `valid`.
 */
export async function enforceEnrollmentTokenForNewRouter(args: {
  deviceIdentifier: string;
  enrollmentToken: string | null | undefined;
  /** The body carried a token the contract could not read (see above). */
  tokenPresented?: boolean;
  now: Date;
  database?: EventWriter;
}): Promise<EnrollmentTokenVerdict> {
  const verdict: EnrollmentTokenVerdict =
    !args.enrollmentToken && args.tokenPresented
      ? "malformed"
      : verifyEnrollmentToken({
          secretsKey: env.VECTRA_SECRETS_KEY,
          token: args.enrollmentToken,
          deviceIdentifier: args.deviceIdentifier,
          nowUnix: unixSeconds(args.now),
        });
  if (verdict === "valid") {
    return verdict;
  }

  const deviceIdentifier = boundedIdentifier(args.deviceIdentifier);
  if (!routerAuthFlags().enrollmentTokenRequired) {
    console.info(
      `[router-enrollment] would require provisioning token deviceIdentifier=${loggableIdentifier(deviceIdentifier)} verdict=${verdict}`,
    );
    return verdict;
  }

  // The token itself is never recorded.
  await (args.database ?? db).insert(eventLog).values({
    type: "router.enrollment.token_rejected",
    severity: "warning",
    message:
      "A new router's registration was refused: no valid provisioning token.",
    metadata: { deviceIdentifier, verdict },
  });
  throw Object.assign(new Error(ENROLLMENT_TOKEN_REQUIRED_MESSAGE), {
    status: 403,
  });
}

// ---------------------------------------------------------------------------
// A2. Check-in nonce and signature.
// ---------------------------------------------------------------------------

/** `{ checkInNonce }` to spread into a register/check-in answer, or nothing. */
export function checkInNonceField(
  routerId: string,
  now = new Date(),
): { checkInNonce?: string } {
  if (!routerAuthFlags().issueCheckInNonce) {
    return {};
  }
  return {
    checkInNonce: mintCheckInNonce({
      secretsKey: env.VECTRA_SECRETS_KEY,
      routerId,
      issuedAtUnix: unixSeconds(now),
    }),
  };
}

export type CheckInSignatureMode = "off" | "observe" | "require";

export function checkInSignatureMode(): CheckInSignatureMode {
  const flags = routerAuthFlags();
  if (flags.requireCheckInSignature) {
    return "require";
  }
  return flags.issueCheckInNonce ? "observe" : "off";
}

export const CHECKIN_SIGNATURE_REQUIRED_CODE = "checkin_signature_required";

// The last verdict seen per router while observing, so a change (and the
// first sight after a start) is recorded once rather than on every check-in.
// One entry per authenticated router; the cap only guards against a leak.
const OBSERVED_VERDICTS_MAX = 10_000;
const observedVerdicts = new Map<string, CheckInSignatureVerdict>();
const REJECTION_EVENT_INTERVAL_MS = 60 * 60 * 1000;
let rejectionEvents = new MemoryWindowRateLimiter(1, REJECTION_EVENT_INTERVAL_MS);

function rememberObservedVerdict(
  routerId: string,
  verdict: CheckInSignatureVerdict,
) {
  observedVerdicts.delete(routerId);
  if (observedVerdicts.size >= OBSERVED_VERDICTS_MAX) {
    const oldest = observedVerdicts.keys().next().value;
    if (oldest !== undefined) {
      observedVerdicts.delete(oldest);
    }
  }
  observedVerdicts.set(routerId, verdict);
}

export function resetRouterAuthPolicyStateForTest() {
  observedVerdicts.clear();
  rejectionEvents = new MemoryWindowRateLimiter(1, REJECTION_EVENT_INTERVAL_MS);
}

/**
 * Checks the signature headers of a check-in against the credential's device
 * key. Observing: always lets the check-in through and records verdict
 * changes. Requiring: returns the 401 answer for any verdict but `valid`
 * (nothing else of the check-in is processed). Must not be called with the
 * mode "off": then not even the headers are read.
 */
export async function gateCheckInSignature(args: {
  mode: Exclude<CheckInSignatureMode, "off">;
  routerId: string;
  headers: Headers;
  bodySha256Hex: string;
  devicePublicKey: string;
  now?: Date;
  database?: CheckInGateDatabase;
}): Promise<Response | null> {
  const now = args.now ?? new Date();
  const database = args.database ?? db;
  const verdict = verifyCheckInSignature({
    secretsKey: env.VECTRA_SECRETS_KEY,
    routerId: args.routerId,
    nonce: args.headers.get(CHECKIN_NONCE_HEADER),
    signature: args.headers.get(CHECKIN_SIGNATURE_HEADER),
    bodySha256Hex: args.bodySha256Hex,
    devicePublicKey: args.devicePublicKey,
    nowUnix: unixSeconds(now),
  });

  // A valid signature proves possession of the device key, but the nonce must
  // also be single-use so a captured (nonce, signature, body) tuple cannot be
  // replayed within its 24h window. Only a valid verdict reaches the ledger
  // (so attacker garbage with a bad signature never populates it), and the
  // consume fails open, so a database hiccup never rejects a legitimate
  // check-in. A replay is a post-valid sub-state, not a new verdict value.
  const replayed =
    verdict === "valid"
      ? await consumeValidCheckInNonce(args.routerId, args.headers, now, database)
      : false;

  if (args.mode === "observe") {
    const previousVerdict = observedVerdicts.get(args.routerId) ?? null;
    rememberObservedVerdict(args.routerId, verdict);
    if (previousVerdict !== verdict) {
      await writeEventSafely(database, {
        routerId: args.routerId,
        type: "router.checkin_signature.observed",
        severity: verdict === "valid" ? "info" : "warning",
        message:
          verdict === "valid"
            ? "Router check-ins are signed by its device key."
            : `Router check-in signature is ${verdict}; accepted (signatures are observed, not required).`,
        metadata: { verdict, previousVerdict },
      });
    }
    if (replayed && rejectionEvents.consume(`${args.routerId}|replayed`).allowed) {
      await writeEventSafely(database, {
        routerId: args.routerId,
        type: "router.checkin_signature.replayed",
        severity: "warning",
        message:
          "Router check-in replayed a used nonce; accepted (signatures are observed, not required).",
        metadata: { verdict: "replayed" },
      });
    }
    return null;
  }

  if (verdict === "valid" && !replayed) {
    return null;
  }
  // Requiring: a fresh invalid verdict and a replay are both refused with the
  // same 401 + fresh nonce, which the agent re-signs and retries once.
  const rejectionReason = verdict === "valid" ? "replayed" : verdict;
  const rejectionType =
    rejectionReason === "replayed"
      ? "router.checkin_signature.replayed"
      : "router.checkin_signature.rejected";
  if (rejectionEvents.consume(`${args.routerId}|${rejectionReason}`).allowed) {
    await writeEventSafely(database, {
      routerId: args.routerId,
      type: rejectionType,
      severity: "warning",
      message:
        rejectionReason === "replayed"
          ? "Router check-in refused: its signed nonce was already used."
          : `Router check-in refused: signature ${verdict}.`,
      metadata: { verdict: rejectionReason },
    });
  }
  return Response.json(
    {
      error: "Check-in signature required.",
      code: CHECKIN_SIGNATURE_REQUIRED_CODE,
      checkInNonce: mintCheckInNonce({
        secretsKey: env.VECTRA_SECRETS_KEY,
        routerId: args.routerId,
        issuedAtUnix: unixSeconds(now),
      }),
    },
    { status: 401 },
  );
}

/**
 * Consumes the nonce of a check-in whose signature already verified, returning
 * whether it was already used (a replay). The nonce is guaranteed to parse on
 * a valid verdict; the null guard is belt-and-braces and treats an unparseable
 * nonce as not-a-replay (fail-open).
 */
async function consumeValidCheckInNonce(
  routerId: string,
  headers: Headers,
  now: Date,
  database: CheckInGateDatabase,
): Promise<boolean> {
  const nonce = headers.get(CHECKIN_NONCE_HEADER) ?? "";
  const nonceIatUnix = parseCheckInNonceIat(nonce);
  if (nonceIatUnix === null) {
    return false;
  }
  const { replay } = await consumeCheckInNonce({
    routerId,
    nonce,
    nonceIatUnix,
    now,
    database,
  });
  return replay;
}
