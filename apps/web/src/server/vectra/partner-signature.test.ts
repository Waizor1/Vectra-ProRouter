import { createHmac } from "node:crypto";

import { describe, expect, it } from "vitest";

import {
  buildPartnerSignatureHeaders,
  PARTNER_SIGNATURE_HEADER,
  PARTNER_TIMESTAMP_HEADER,
  signPartnerPayload,
  verifyPartnerSignature,
} from "./partner-signature";

const SECRET = "partner-signature-test-secret-0123456789";
const NOW_MS = Date.UTC(2026, 8, 28, 10, 0, 0);
const NOW_S = String(NOW_MS / 1000);
const BODY = '{"code":"7KQ4M9XD","owner":{"ref":"acct-42","label":"x"}}';

function verify(overrides: Partial<Parameters<typeof verifyPartnerSignature>[0]>) {
  return verifyPartnerSignature({
    secret: SECRET,
    timestamp: NOW_S,
    signature: signPartnerPayload(SECRET, NOW_S, BODY),
    rawBody: BODY,
    nowMs: NOW_MS,
    ...overrides,
  });
}

describe("partner request signature", () => {
  it("is hex HMAC-SHA256 over `${timestamp}\\n${rawBody}`", () => {
    const expected = createHmac("sha256", SECRET)
      .update(`${NOW_S}\n${BODY}`)
      .digest("hex");
    expect(signPartnerPayload(SECRET, NOW_S, BODY)).toBe(expected);
    // Bytes and the same string sign identically.
    expect(signPartnerPayload(SECRET, NOW_S, new TextEncoder().encode(BODY))).toBe(
      expected,
    );
  });

  it("accepts a good signature, in either hex case", () => {
    expect(verify({})).toEqual({ ok: true });
    expect(
      verify({ signature: signPartnerPayload(SECRET, NOW_S, BODY).toUpperCase() }),
    ).toEqual({ ok: true });
  });

  it("refuses a signature made with another secret or over another body", () => {
    expect(
      verify({ signature: signPartnerPayload("another-secret-0123456789abcdef", NOW_S, BODY) }),
    ).toEqual({ ok: false, status: 401, error: "bad_signature" });
    expect(verify({ rawBody: `${BODY} ` })).toEqual({
      ok: false,
      status: 401,
      error: "bad_signature",
    });
    expect(verify({ signature: "not-hex" })).toMatchObject({
      status: 401,
      error: "bad_signature",
    });
  });

  it("refuses a timestamp outside +-300 s, in both directions", () => {
    const at = (offsetSeconds: number) => {
      const timestamp = String(NOW_MS / 1000 + offsetSeconds);
      return verify({
        timestamp,
        signature: signPartnerPayload(SECRET, timestamp, BODY),
      });
    };
    expect(at(-300)).toEqual({ ok: true });
    expect(at(300)).toEqual({ ok: true });
    expect(at(-301)).toEqual({ ok: false, status: 401, error: "stale_timestamp" });
    expect(at(301)).toEqual({ ok: false, status: 401, error: "stale_timestamp" });
    expect(verify({ timestamp: "1e9" })).toMatchObject({ error: "stale_timestamp" });
  });

  it("refuses a request without the headers", () => {
    expect(verify({ timestamp: null })).toEqual({
      ok: false,
      status: 401,
      error: "missing_signature",
    });
    expect(verify({ signature: "" })).toMatchObject({ error: "missing_signature" });
  });

  it("fails closed when no secret is configured", () => {
    // An HMAC with an empty key is something anyone can compute.
    expect(
      verify({ secret: undefined, signature: signPartnerPayload("", NOW_S, BODY) }),
    ).toEqual({ ok: false, status: 503, error: "partner_api_disabled" });
  });

  it("builds headers the verifier accepts", () => {
    const headers = buildPartnerSignatureHeaders(SECRET, BODY, NOW_MS);
    expect(headers[PARTNER_TIMESTAMP_HEADER]).toBe(NOW_S);
    expect(
      verify({
        timestamp: headers[PARTNER_TIMESTAMP_HEADER],
        signature: headers[PARTNER_SIGNATURE_HEADER],
      }),
    ).toEqual({ ok: true });
  });
});
