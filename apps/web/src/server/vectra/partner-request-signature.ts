import {
  createHash,
  createHmac,
  randomUUID,
  timingSafeEqual,
} from "node:crypto";
import {
  PARTNER_SIGNATURE_HEADER,
  PARTNER_TIMESTAMP_HEADER,
} from "./partner-signature";
export const PARTNER_VERSION_HEADER = "X-Vectra-Partner-Version";
export const PARTNER_REQUEST_ID_HEADER = "X-Vectra-Partner-Request-Id";
/**
 * Which partner is calling (default: Vectra Connect). Not part of the signed
 * message — the secret already is the partner's own, so a request carrying
 * another partner's id is checked against that partner's secret and fails.
 */
export const PARTNER_ID_HEADER = "X-Vectra-Partner-Id";
const uuid =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const encode = (value: string) =>
  encodeURIComponent(value).replace(
    /[!'()*]/g,
    (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`,
  );
export function canonicalPartnerQuery(url: URL) {
  const pairs = [...url.searchParams.entries()];
  if (new Set(pairs.map(([key]) => key)).size !== pairs.length)
    throw new Error("Duplicate query key");
  return pairs
    .sort(([a, av], [b, bv]) =>
      a < b ? -1 : a > b ? 1 : av < bv ? -1 : av > bv ? 1 : 0,
    )
    .map(([key, value]) => `${encode(key)}=${encode(value)}`)
    .join("&");
}
export function canonicalPartnerMessage(args: {
  timestamp: number;
  requestId: string;
  method: string;
  pathname: string;
  canonicalQuery: string;
  rawBody: Uint8Array | string;
  idempotencyKey: string;
}) {
  return JSON.stringify([
    "vectra-partner-v2",
    args.timestamp,
    args.requestId,
    args.method,
    args.pathname,
    args.canonicalQuery,
    createHash("sha256").update(args.rawBody).digest("hex"),
    args.idempotencyKey,
  ]);
}
export function signPartnerRequest(
  secret: string,
  args: Parameters<typeof canonicalPartnerMessage>[0],
) {
  return createHmac("sha256", secret)
    .update(canonicalPartnerMessage(args))
    .digest("hex");
}
export function buildPartnerRequestHeaders(
  secret: string,
  method: string,
  url: string,
  rawBody: Uint8Array | string,
  key = "",
  nowMs = Date.now(),
  requestId: string = randomUUID(),
  partnerId?: string,
) {
  const target = new URL(url);
  const timestamp = Math.floor(nowMs / 1000);
  return {
    [PARTNER_VERSION_HEADER]: "2",
    [PARTNER_REQUEST_ID_HEADER]: requestId,
    [PARTNER_TIMESTAMP_HEADER]: String(timestamp),
    [PARTNER_SIGNATURE_HEADER]: signPartnerRequest(secret, {
      timestamp,
      requestId,
      method,
      pathname: target.pathname,
      canonicalQuery: canonicalPartnerQuery(target),
      rawBody,
      idempotencyKey: key,
    }),
    ...(key ? { "Idempotency-Key": key } : {}),
    ...(partnerId ? { [PARTNER_ID_HEADER]: partnerId } : {}),
  };
}
export function verifyPartnerRequest(args: {
  request: Request;
  secret: string | null | undefined;
  rawBody: Uint8Array;
  nowMs: number;
  method: string;
  path: string;
}) {
  const fail = (error: string, status = 401) => ({
    ok: false as const,
    error,
    status,
  });
  if (!args.secret) return fail("partner_api_disabled", 503);
  const { request } = args;
  const timestamp = request.headers.get(PARTNER_TIMESTAMP_HEADER) ?? "";
  const requestId = request.headers.get(PARTNER_REQUEST_ID_HEADER) ?? "";
  const signature = request.headers.get(PARTNER_SIGNATURE_HEADER) ?? "";
  if (
    request.headers.get(PARTNER_VERSION_HEADER) !== "2" ||
    !uuid.test(requestId) ||
    !signature
  )
    return fail("missing_signature");
  if (
    !/^(0|[1-9][0-9]{0,11})$/.test(timestamp) ||
    Math.abs(args.nowMs / 1000 - Number(timestamp)) > 300
  )
    return fail("stale_timestamp");
  const url = new URL(request.url);
  const key = request.headers.get("Idempotency-Key") ?? "";
  if (
    request.method !== args.method ||
    url.pathname !== args.path ||
    url.pathname.includes("%")
  )
    return fail("bad_signature");
  const allowed = request.method === "GET" ? ["ownerRef"] : [];
  if (
    [...url.searchParams.keys()].some((name) => !allowed.includes(name)) ||
    (request.method === "GET" ? key !== "" : !/^[\x21-\x7e]{1,200}$/.test(key))
  )
    return fail("bad_signature");
  let canonicalQuery: string;
  try {
    canonicalQuery = canonicalPartnerQuery(url);
  } catch {
    return fail("bad_signature");
  }
  if (!/^[0-9a-f]{64}$/.test(signature)) return fail("bad_signature");
  const message = {
    timestamp: Number(timestamp),
    requestId,
    method: request.method,
    pathname: url.pathname,
    canonicalQuery,
    rawBody: args.rawBody,
    idempotencyKey: key,
  };
  const expected = signPartnerRequest(args.secret, message);
  if (
    !timingSafeEqual(
      Buffer.from(signature, "hex"),
      Buffer.from(expected, "hex"),
    )
  )
    return fail("bad_signature");
  return {
    ok: true as const,
    requestId,
    fingerprint: createHash("sha256")
      .update(canonicalPartnerMessage(message))
      .digest("hex"),
    expiresAt: new Date((Number(timestamp) + 600) * 1000),
  };
}
