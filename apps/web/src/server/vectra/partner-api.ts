import { createHash } from "node:crypto";

import {
  type PartnerRouterClaimRequest,
  partnerRouterClaimRequestSchema,
  partnerRouterUnbindRequestSchema,
} from "@vectra/contracts";
import { partnerIdempotencyKeys, partnerRequestNonces } from "@vectra/db";
import { eq, lt } from "drizzle-orm";
import type { ZodError } from "zod";

import { env } from "~/env";
import { db } from "~/server/db";
import { verifyPartnerRequest } from "./partner-request-signature";
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
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

type IdempotencyDatabase = Pick<typeof db, "select" | "insert" | "delete">;

export type IdempotencyRecord = {
  requestHash: string;
  statusCode: number;
  response: Record<string, unknown>;
};

export type PartnerApiDeps = {
  secret: string | null | undefined;
  reserveNonce: (
    requestId: string,
    fingerprint: string,
    expiresAt: Date,
  ) => Promise<boolean>;
  now: () => Date;
  claim: (request: PartnerRouterClaimRequest) => Promise<RouterClaimOutcome>;
  unbind: (input: {
    routerId: string;
    ownerRef?: string | null;
  }) => Promise<RouterUnbindOutcome>;
  findIdempotent: (key: string) => Promise<IdempotencyRecord | null>;
  /** Store a first answer; returns what is stored (a concurrent twin's wins). */
  storeIdempotent: (
    key: string,
    record: IdempotencyRecord,
  ) => Promise<IdempotencyRecord>;
};

export async function findIdempotencyRecordWithDb(
  client: IdempotencyDatabase,
  key: string,
): Promise<IdempotencyRecord | null> {
  const [row] = await client
    .select()
    .from(partnerIdempotencyKeys)
    .where(eq(partnerIdempotencyKeys.key, key))
    .limit(1);

  return row
    ? {
        requestHash: row.requestHash,
        statusCode: row.statusCode,
        response: row.response,
      }
    : null;
}

export async function storeIdempotencyRecordWithDb(
  client: IdempotencyDatabase,
  key: string,
  record: IdempotencyRecord,
  now = new Date(),
): Promise<IdempotencyRecord> {
  await client
    .delete(partnerIdempotencyKeys)
    .where(
      lt(
        partnerIdempotencyKeys.createdAt,
        new Date(now.getTime() - IDEMPOTENCY_RETENTION_MS),
      ),
    );

  const [inserted] = await client
    .insert(partnerIdempotencyKeys)
    .values({ key, ...record })
    .onConflictDoNothing({ target: partnerIdempotencyKeys.key })
    .returning();
  if (inserted) {
    return record;
  }

  return (await findIdempotencyRecordWithDb(client, key)) ?? record;
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
    reserveNonce: (requestId, fingerprint, expiresAt) =>
      reservePartnerNonceWithDb(db, requestId, fingerprint, expiresAt),
    now: () => new Date(),
    claim: (request) => claimRouterWithDb(db, request),
    unbind: (input) => unbindRouterClaimWithDb(db, input),
    findIdempotent: (key) => findIdempotencyRecordWithDb(db, key),
    storeIdempotent: (key, record) =>
      storeIdempotencyRecordWithDb(db, key, record),
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

/** The request identity an Idempotency-Key is bound to. */
export function hashPartnerRequest(
  method: string,
  path: string,
  rawBody: Uint8Array,
) {
  return createHash("sha256")
    .update(`${method} ${path}\n`)
    .update(rawBody)
    .digest("hex");
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

export async function authenticatePartnerRequest(
  request: Request,
  rawBody: Uint8Array,
  deps: PartnerApiDeps,
  method: string,
  path: string,
) {
  const auth = verifyPartnerRequest({
    request,
    rawBody,
    secret: deps.secret,
    nowMs: deps.now().getTime(),
    method,
    path,
  });
  if (!auth.ok) return partnerJson({ error: auth.error }, auth.status);
  try {
    if (
      !(await deps.reserveNonce(
        auth.requestId,
        auth.fingerprint,
        auth.expiresAt,
      ))
    )
      return partnerJson({ error: "request_replayed" }, 409);
  } catch {
    return partnerJson({ error: "partner_replay_unavailable" }, 503);
  }
  return null;
}

type PartnerExecution = {
  request: Request;
  deps: PartnerApiDeps;
  method: "POST" | "DELETE";
  path: string;
  /** Validate the body and run the operation (only after auth and replay). */
  run: (
    body: unknown,
  ) => Promise<
    Response | { ok: boolean; status: number; body: Record<string, unknown> }
  >;
};

// Signature first, then the Idempotency-Key replay, then the operation. Only
// successful answers are stored: a 404/410 is re-evaluated on retry, so a
// backend that retries while the router has not yet checked in with its new
// code can still succeed with the same key.
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
  if (authentication) return authentication;

  const key = parseIdempotencyKey(
    args.request.headers.get(IDEMPOTENCY_KEY_HEADER),
  );
  if (key === false) {
    return invalid(
      `${IDEMPOTENCY_KEY_HEADER} must be 1-200 visible ASCII characters`,
    );
  }

  const requestHash = hashPartnerRequest(args.method, args.path, rawBody);
  if (key) {
    const stored = await args.deps.findIdempotent(key);
    if (stored) {
      return stored.requestHash === requestHash
        ? partnerJson(stored.response, stored.statusCode, {
            [IDEMPOTENT_REPLAY_HEADER]: "true",
          })
        : partnerJson({ error: "idempotency_key_mismatch" }, 422);
    }
  }

  const body = parseJsonBody(rawBody);
  if (body === undefined) {
    return invalid("body must be JSON");
  }

  const outcome = await args.run(body);
  if (outcome instanceof Response) {
    return outcome;
  }

  if (outcome.ok && key) {
    const stored = await args.deps.storeIdempotent(key, {
      requestHash,
      statusCode: outcome.status,
      response: outcome.body,
    });
    if (stored.requestHash !== requestHash) {
      return partnerJson({ error: "idempotency_key_mismatch" }, 422);
    }
    return partnerJson(stored.response, stored.statusCode);
  }

  return partnerJson(outcome.body, outcome.status);
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
    run: async (body) => {
      const parsed = partnerRouterClaimRequestSchema.safeParse(body);
      if (!parsed.success) {
        return invalid(describeZodError(parsed.error));
      }
      return deps.claim(parsed.data);
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
    run: async (body) => {
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
        ownerRef: parsed.data.ownerRef ?? null,
      });
    },
  });
}
