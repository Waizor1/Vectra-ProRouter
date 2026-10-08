import { createHash } from "node:crypto";

import {
  type PartnerRouterClaimRequest,
  partnerRouterClaimRequestSchema,
  partnerRouterUnbindRequestSchema,
} from "@vectra/contracts";
import { partnerIdempotencyKeys, partnerRequestNonces } from "@vectra/db";
import { and, eq, isNull, lt, or } from "drizzle-orm";
import type { ZodError } from "zod";

import { env } from "~/env";
import { db } from "~/server/db";
import {
  DEFAULT_PARTNER_ID,
  PARTNER_ID_PATTERN,
  findPartner,
  scopeIdempotencyKey,
  type PartnerConfig,
} from "./partner-registry";
import {
  PARTNER_ID_HEADER,
  verifyPartnerRequest,
} from "./partner-request-signature";
import { keyedDigest } from "./secrets";
import {
  claimRouterWithDb,
  type RouterClaimOutcome,
  type RouterUnbindOutcome,
  unbindRouterClaimWithDb,
} from "~/server/vectra/router-claims";

/**
 * HTTP layer of the partner API (ADR-0006): signature, Idempotency-Key, body
 * validation, and the mapping of claim outcomes to status codes. The route
 * files only delegate here, so this is what the tests drive.
 */

export const IDEMPOTENCY_KEY_HEADER = "Idempotency-Key";
export const IDEMPOTENT_REPLAY_HEADER = "Idempotent-Replayed";
export const PARTNER_CLAIMS_PATH = "/api/partner/router-claims";

export const MAX_BODY_BYTES = 16 * 1024;
const IDEMPOTENCY_RETENTION_MS = 7 * 24 * 60 * 60 * 1000;
// A first attempt holds its key this long; a crashed attempt's key frees itself.
const IDEMPOTENCY_LEASE_MS = 60_000;
// status_code of a key that is reserved but has no final answer (yet).
const IDEMPOTENCY_PENDING = 0;
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const LEGACY_REQUEST_HASH = /^[0-9a-f]{64}$/;

type IdempotencyDatabase = Pick<
  typeof db,
  "select" | "insert" | "update" | "delete"
>;

export type IdempotencyRecord = {
  requestHash: string;
  statusCode: number;
  response: Record<string, unknown>;
};

/**
 * What a request is bound to under its Idempotency-Key. `requestHash` is a
 * keyed HMAC (the body may carry a Wi-Fi password); `legacyHash` is the bare
 * sha256 rows written before that carry. It is only ever COMPARED, never
 * stored, so a legacy row keeps replaying until its retention expires.
 */
export type PartnerRequestHashes = { requestHash: string; legacyHash: string };

export function partnerHashMatches(
  stored: unknown,
  hashes: PartnerRequestHashes,
) {
  return (
    typeof stored === "string" &&
    (stored === hashes.requestHash ||
      (LEGACY_REQUEST_HASH.test(stored) && stored === hashes.legacyHash))
  );
}

export type IdempotencyReservation =
  | { kind: "run" }
  | { kind: "replay"; record: IdempotencyRecord }
  | { kind: "mismatch" }
  | { kind: "busy" };

export type PartnerIdentity = { id: string; brand: string; label: string };

export type PartnerApiDeps = {
  secret: string | null | undefined;
  /** Registered partners by id (the registry in production). */
  partners?: (id: string) => PartnerConfig | null;
  reserveNonce: (
    requestId: string,
    fingerprint: string,
    expiresAt: Date,
  ) => Promise<boolean>;
  now: () => Date;
  claim: (
    request: PartnerRouterClaimRequest,
    partnerId: string,
  ) => Promise<RouterClaimOutcome>;
  unbind: (input: {
    routerId: string;
    ownerRef: string;
    partnerId: string;
  }) => Promise<RouterUnbindOutcome>;
  /**
   * Bind the key to this request BEFORE running it: the first attempt runs,
   * a concurrent twin is told to retry (busy), the same key with another body
   * is a mismatch for as long as the key is kept, a final answer is replayed.
   */
  reserveIdempotent: (
    key: string,
    hashes: PartnerRequestHashes,
  ) => Promise<IdempotencyReservation>;
  /**
   * Store the final (2xx) answer, or with null release the key for a retry:
   * a 404/410 is re-evaluated, but only for the same body.
   */
  finishIdempotent: (
    key: string,
    requestHash: string,
    record: IdempotencyRecord | null,
  ) => Promise<void>;
};

export async function reserveIdempotencyKeyWithDb(
  client: IdempotencyDatabase,
  key: string,
  hashes: PartnerRequestHashes,
  now = new Date(),
): Promise<IdempotencyReservation> {
  await client
    .delete(partnerIdempotencyKeys)
    .where(
      lt(
        partnerIdempotencyKeys.createdAt,
        new Date(now.getTime() - IDEMPOTENCY_RETENTION_MS),
      ),
    );

  const lockedUntil = new Date(now.getTime() + IDEMPOTENCY_LEASE_MS);
  const [inserted] = await client
    .insert(partnerIdempotencyKeys)
    .values({
      key,
      requestHash: hashes.requestHash,
      statusCode: IDEMPOTENCY_PENDING,
      response: {},
      lockedUntil,
    })
    .onConflictDoNothing({ target: partnerIdempotencyKeys.key })
    .returning();
  if (inserted) {
    return { kind: "run" };
  }

  const [row] = await client
    .select()
    .from(partnerIdempotencyKeys)
    .where(eq(partnerIdempotencyKeys.key, key))
    .limit(1);
  if (!row) {
    // Expired between the insert and the read: the caller retries.
    return { kind: "busy" };
  }
  if (!partnerHashMatches(row.requestHash, hashes)) {
    return { kind: "mismatch" };
  }
  if (row.statusCode !== IDEMPOTENCY_PENDING) {
    return {
      kind: "replay",
      record: {
        requestHash: row.requestHash,
        statusCode: row.statusCode,
        response: row.response,
      },
    };
  }

  // A released key (an earlier non-2xx) or an expired lease is taken over by
  // compare-and-swap: of concurrent retries exactly one runs.
  const [taken] = await client
    .update(partnerIdempotencyKeys)
    .set({ lockedUntil })
    .where(
      and(
        eq(partnerIdempotencyKeys.key, key),
        eq(partnerIdempotencyKeys.statusCode, IDEMPOTENCY_PENDING),
        eq(partnerIdempotencyKeys.requestHash, row.requestHash),
        or(
          isNull(partnerIdempotencyKeys.lockedUntil),
          lt(partnerIdempotencyKeys.lockedUntil, now),
        ),
      ),
    )
    .returning();
  return taken ? { kind: "run" } : { kind: "busy" };
}

export async function finishIdempotencyKeyWithDb(
  client: IdempotencyDatabase,
  key: string,
  requestHash: string,
  record: IdempotencyRecord | null,
) {
  await client
    .update(partnerIdempotencyKeys)
    .set(
      record
        ? {
            statusCode: record.statusCode,
            response: record.response,
            lockedUntil: null,
          }
        : { lockedUntil: null },
    )
    .where(
      and(
        eq(partnerIdempotencyKeys.key, key),
        eq(partnerIdempotencyKeys.requestHash, requestHash),
        eq(partnerIdempotencyKeys.statusCode, IDEMPOTENCY_PENDING),
      ),
    );
}

export async function reservePartnerNonceWithDb(
  client: IdempotencyDatabase,
  requestId: string,
  fingerprint: string,
  expiresAt: Date,
  now = new Date(),
) {
  await client
    .delete(partnerRequestNonces)
    .where(lt(partnerRequestNonces.expiresAt, now));
  const rows = await client
    .insert(partnerRequestNonces)
    .values({ requestId, fingerprint, expiresAt })
    .onConflictDoNothing({ target: partnerRequestNonces.requestId })
    .returning();
  return rows.length === 1;
}

export function defaultDeps(): PartnerApiDeps {
  return {
    secret: env.VECTRA_PARTNER_SECRET,
    partners: findPartner,
    reserveNonce: (requestId, fingerprint, expiresAt) =>
      reservePartnerNonceWithDb(db, requestId, fingerprint, expiresAt),
    now: () => new Date(),
    claim: (request, partnerId) =>
      claimRouterWithDb(db, request, { partnerId }),
    unbind: (input) => unbindRouterClaimWithDb(db, input),
    reserveIdempotent: (key, hashes) =>
      reserveIdempotencyKeyWithDb(db, key, hashes),
    finishIdempotent: (key, requestHash, record) =>
      finishIdempotencyKeyWithDb(db, key, requestHash, record),
  };
}

export function partnerJson(
  body: Record<string, unknown>,
  status: number,
  headers: Record<string, string> = {},
) {
  return Response.json(body, {
    status,
    headers: { "cache-control": "no-store", ...headers },
  });
}

function invalid(detail: string) {
  return partnerJson({ error: "invalid", detail }, 400);
}

/** The request identity an Idempotency-Key is bound to (see PartnerRequestHashes). */
export function hashPartnerRequest(
  method: string,
  path: string,
  rawBody: Uint8Array,
): PartnerRequestHashes {
  return {
    requestHash: keyedDigest(
      "partner-idempotency-v1",
      `${method} ${path}\n`,
      rawBody,
    ),
    legacyHash: createHash("sha256")
      .update(`${method} ${path}\n`)
      .update(rawBody)
      .digest("hex"),
  };
}

/** null = no key sent; false = a malformed key. */
export function parseIdempotencyKey(raw: string | null) {
  if (raw === null) {
    return null;
  }
  const key = raw;
  return /^[\x21-\x7e]{1,200}$/.test(key) ? key : false;
}

/**
 * The raw body, or null past MAX_BODY_BYTES. Counted while it is read and cut
 * off at the cap — a chunked request carries no Content-Length, and
 * arrayBuffer() would buffer every byte before anything could refuse it.
 */
export async function readRawBody(request: Request) {
  const declared = Number(request.headers.get("content-length") ?? "0");
  if (Number.isFinite(declared) && declared > MAX_BODY_BYTES) {
    return null;
  }
  if (!request.body) {
    return new Uint8Array(0);
  }

  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    total += value.byteLength;
    if (total > MAX_BODY_BYTES) {
      await reader.cancel().catch(() => undefined);
      return null;
    }
    chunks.push(value);
  }

  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return body;
}

function parseJsonBody(rawBody: Uint8Array): unknown {
  try {
    return JSON.parse(new TextDecoder().decode(rawBody)) as unknown;
  } catch {
    return undefined;
  }
}

function describeZodError(error: ZodError) {
  return error.issues
    .map((issue) => `${issue.path.join(".") || "body"}: ${issue.message}`)
    .join("; ")
    .slice(0, 500);
}

/** Who is calling and the secrets that may have signed the call. */
function resolvePartnerCaller(
  request: Request,
  deps: PartnerApiDeps,
): { identity: PartnerIdentity; secrets: string[] } | Response {
  const named = request.headers.get(PARTNER_ID_HEADER);
  if (named === null || named === DEFAULT_PARTNER_ID) {
    const configured = deps.partners?.(DEFAULT_PARTNER_ID) ?? null;
    const secrets =
      configured?.secrets.length
        ? configured.secrets
        : deps.secret
          ? [deps.secret]
          : [];
    if (secrets.length === 0) {
      return partnerJson({ error: "partner_api_disabled" }, 503);
    }
    return {
      identity: {
        id: DEFAULT_PARTNER_ID,
        brand: configured?.brand ?? "vectra",
        label: configured?.label ?? "Vectra",
      },
      secrets,
    };
  }
  const partner = PARTNER_ID_PATTERN.test(named)
    ? (deps.partners?.(named) ?? null)
    : null;
  if (!partner || partner.secrets.length === 0) {
    return partnerJson({ error: "unknown_partner" }, 401);
  }
  return {
    identity: { id: partner.id, brand: partner.brand, label: partner.label },
    secrets: partner.secrets,
  };
}

export async function authenticatePartnerRequest(
  request: Request,
  rawBody: Uint8Array,
  deps: PartnerApiDeps,
  method: string,
  path: string,
): Promise<Response | { partner: PartnerIdentity }> {
  const caller = resolvePartnerCaller(request, deps);
  if (caller instanceof Response) return caller;
  const check = (secret: string) =>
    verifyPartnerRequest({
      request,
      rawBody,
      secret,
      nowMs: deps.now().getTime(),
      method,
      path,
    });
  // During a rotation the old secret is still accepted; any other failure
  // (stale timestamp, missing headers) is the same for both.
  let auth = check(caller.secrets[0]!);
  for (const secret of caller.secrets.slice(1)) {
    if (auth.ok) break;
    const next = check(secret);
    if (next.ok) auth = next;
  }
  if (!auth.ok) return partnerJson({ error: auth.error }, auth.status);
  try {
    if (
      !(await deps.reserveNonce(
        scopeIdempotencyKey(caller.identity.id, auth.requestId),
        auth.fingerprint,
        auth.expiresAt,
      ))
    )
      return partnerJson({ error: "request_replayed" }, 409);
  } catch {
    return partnerJson({ error: "partner_replay_unavailable" }, 503);
  }
  return { partner: caller.identity };
}

type PartnerExecution = {
  request: Request;
  deps: PartnerApiDeps;
  method: "POST" | "DELETE";
  path: string;
  /** Validate the body and run the operation (only after auth and replay). */
  run: (
    body: unknown,
    partner: PartnerIdentity,
  ) => Promise<
    Response | { ok: boolean; status: number; body: Record<string, unknown> }
  >;
};

// Signature first, then the body, then the Idempotency-Key reservation, then
// the operation. The key is bound to the body before anything runs: the same
// key with another body is refused even while the first attempt is still
// running or ended in a failure. Only a 2xx is stored as the final answer: a
// 404/410 releases the key, so a backend that retries while the router has not
// yet checked in with its new code can still succeed with the same key.
export async function executePartnerRequest(args: PartnerExecution) {
  const rawBody = await readRawBody(args.request);
  if (!rawBody) {
    return invalid(`body must be at most ${MAX_BODY_BYTES} bytes`);
  }

  const authentication = await authenticatePartnerRequest(
    args.request,
    rawBody,
    args.deps,
    args.method,
    args.path,
  );
  if (authentication instanceof Response) return authentication;
  const { partner } = authentication;

  const key = parseIdempotencyKey(
    args.request.headers.get(IDEMPOTENCY_KEY_HEADER),
  );
  if (key === false) {
    return invalid(
      `${IDEMPOTENCY_KEY_HEADER} must be 1-200 visible ASCII characters`,
    );
  }

  const body = parseJsonBody(rawBody);
  if (body === undefined) {
    return invalid("body must be JSON");
  }

  const hashes = hashPartnerRequest(args.method, args.path, rawBody);
  // Partners share the table: every partner's key is stored under its own scope.
  const storedKey = key ? scopeIdempotencyKey(partner.id, key) : key;
  if (storedKey) {
    let reservation: IdempotencyReservation;
    try {
      reservation = await args.deps.reserveIdempotent(storedKey, hashes);
    } catch {
      return partnerJson({ error: "partner_idempotency_unavailable" }, 503);
    }
    if (reservation.kind === "mismatch") {
      return partnerJson({ error: "idempotency_key_mismatch" }, 422);
    }
    if (reservation.kind === "busy") {
      return partnerJson({ error: "idempotency_in_progress" }, 503, {
        "retry-after": "2",
      });
    }
    if (reservation.kind === "replay") {
      return partnerJson(
        reservation.record.response,
        reservation.record.statusCode,
        { [IDEMPOTENT_REPLAY_HEADER]: "true" },
      );
    }
  }

  let outcome: Awaited<ReturnType<PartnerExecution["run"]>>;
  try {
    outcome = await args.run(body, partner);
  } catch (error) {
    if (storedKey) {
      await args.deps
        .finishIdempotent(storedKey, hashes.requestHash, null)
        .catch(() => undefined);
    }
    throw error;
  }

  if (storedKey) {
    // The operation already ran: a failed bookkeeping write must not turn its
    // answer into a 500. The key's lease then expires and a retry re-runs,
    // which every partner operation absorbs (claims and actions dedupe).
    await args.deps
      .finishIdempotent(
        storedKey,
        hashes.requestHash,
        !(outcome instanceof Response) && outcome.ok
          ? {
              requestHash: hashes.requestHash,
              statusCode: outcome.status,
              response: outcome.body,
            }
          : null,
      )
      .catch((error: unknown) =>
        console.error("[partner-api] idempotency key not finished", error),
      );
  }

  return outcome instanceof Response
    ? outcome
    : partnerJson(outcome.body, outcome.status);
}

/** POST /api/partner/router-claims */
export async function handleRouterClaimRequest(
  request: Request,
  deps: PartnerApiDeps = defaultDeps(),
) {
  return executePartnerRequest({
    request,
    deps,
    method: "POST",
    path: PARTNER_CLAIMS_PATH,
    run: async (body, partner) => {
      const parsed = partnerRouterClaimRequestSchema.safeParse(body);
      if (!parsed.success) {
        return invalid(describeZodError(parsed.error));
      }
      return deps.claim(parsed.data, partner.id);
    },
  });
}

/**
 * DELETE /api/partner/router-claims/:routerId
 *
 * The body must repeat the router id ({"routerId": "..."}): the signature
 * covers the body, not the path, so without it a captured DELETE could be
 * replayed against another router inside the timestamp window.
 */
export async function handleRouterUnbindRequest(
  request: Request,
  routerId: string,
  deps: PartnerApiDeps = defaultDeps(),
) {
  return executePartnerRequest({
    request,
    deps,
    method: "DELETE",
    path: `${PARTNER_CLAIMS_PATH}/${routerId}`,
    run: async (body, partner) => {
      if (!UUID_PATTERN.test(routerId)) {
        return invalid("routerId must be a UUID");
      }
      const parsed = partnerRouterUnbindRequestSchema.safeParse(body);
      if (!parsed.success) {
        return invalid(describeZodError(parsed.error));
      }
      if (parsed.data.routerId.toLowerCase() !== routerId.toLowerCase()) {
        return invalid("routerId in the body must match the path");
      }
      return deps.unbind({
        routerId: parsed.data.routerId,
        ownerRef: parsed.data.ownerRef,
        partnerId: partner.id,
      });
    },
  });
}
