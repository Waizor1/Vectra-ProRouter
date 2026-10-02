import { createHmac, timingSafeEqual } from "node:crypto";

/**
 * Legacy outbound webhook body signing between the panel and the Vectra backend
 * (ADR-0006). Inbound partner requests require partner-request-signature v2:
 *
 *   X-Vectra-Partner-Timestamp: <unix seconds>
 *   X-Vectra-Partner-Signature: hex(HMAC-SHA256(secret, `${timestamp}\n${rawBody}`))
 *
 * The panel signs outbound webhooks with VECTRA_CONNECT_WEBHOOK_SECRET.
 * This verifier is not used by any inbound partner endpoint.
 * A timestamp outside +-300 s is refused, which bounds how long a captured
 * request stays replayable.
 */
export const PARTNER_TIMESTAMP_HEADER = "X-Vectra-Partner-Timestamp";
export const PARTNER_SIGNATURE_HEADER = "X-Vectra-Partner-Signature";
export const PARTNER_SIGNATURE_WINDOW_SECONDS = 300;

export function signPartnerPayload(
  secret: string,
  timestamp: string,
  rawBody: Uint8Array | string,
) {
  return createHmac("sha256", secret)
    .update(`${timestamp}\n`)
    .update(rawBody)
    .digest("hex");
}

export type PartnerAuthFailure =
  | "partner_api_disabled"
  | "missing_signature"
  | "stale_timestamp"
  | "bad_signature";

export type PartnerAuthResult =
  | { ok: true }
  | { ok: false; status: 401 | 503; error: PartnerAuthFailure };

export function verifyPartnerSignature(args: {
  secret: string | null | undefined;
  timestamp: string | null | undefined;
  signature: string | null | undefined;
  rawBody: Uint8Array | string;
  nowMs?: number;
}): PartnerAuthResult {
  // Fail closed: with no secret configured an HMAC is computable by anyone.
  if (!args.secret) {
    return { ok: false, status: 503, error: "partner_api_disabled" };
  }

  const timestamp = args.timestamp?.trim() ?? "";
  const signature = args.signature?.trim().toLowerCase() ?? "";
  if (!timestamp || !signature) {
    return { ok: false, status: 401, error: "missing_signature" };
  }

  if (!/^\d{1,12}$/.test(timestamp)) {
    return { ok: false, status: 401, error: "stale_timestamp" };
  }
  const nowSeconds = Math.floor((args.nowMs ?? Date.now()) / 1000);
  if (
    Math.abs(nowSeconds - Number(timestamp)) > PARTNER_SIGNATURE_WINDOW_SECONDS
  ) {
    return { ok: false, status: 401, error: "stale_timestamp" };
  }

  if (!/^[0-9a-f]{64}$/.test(signature)) {
    return { ok: false, status: 401, error: "bad_signature" };
  }
  const expected = Buffer.from(
    signPartnerPayload(args.secret, timestamp, args.rawBody),
    "hex",
  );
  const presented = Buffer.from(signature, "hex");
  if (!timingSafeEqual(expected, presented)) {
    return { ok: false, status: 401, error: "bad_signature" };
  }

  return { ok: true };
}

/** Headers for a signed request the panel sends (webhooks). */
export function buildPartnerSignatureHeaders(
  secret: string,
  rawBody: string,
  nowMs = Date.now(),
) {
  const timestamp = String(Math.floor(nowMs / 1000));
  return {
    [PARTNER_TIMESTAMP_HEADER]: timestamp,
    [PARTNER_SIGNATURE_HEADER]: signPartnerPayload(secret, timestamp, rawBody),
  };
}
