import { eventLog } from "@vectra/db";
import { createPrivateKey, sign } from "node:crypto";
import { readFileSync } from "node:fs";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";
import {
  buildCheckInSignatureMessage,
  mintCheckInNonce,
  sha256Hex,
  verifyCheckInNonce,
} from "~/server/vectra/router-auth-integrity";
import {
  resetRouterAuthPolicyStateForTest,
  routerErrorWithBody,
} from "~/server/vectra/router-auth-policy";
import type { FakeDb } from "~/server/vectra/testing/fake-db";

// Fake, public test vectors shared with the Go agent.
const vectors = JSON.parse(
  readFileSync(
    new URL("../../../../../../../packages/contracts/fixtures/router-auth-vectors.json", import.meta.url),
    "utf8",
  ),
) as {
  secretsKey: string;
  checkInSignature: { devicePrivateKeyBase64: string; devicePublicKeyBase64: string; routerId: string };
};
const ROUTER_ID = vectors.checkInSignature.routerId;
const CREDENTIAL_ID = "c0ffee00-0000-4000-8000-000000000001";

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("~/server/vectra/testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});
const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

const checkInRouter = vi.fn(async (..._args: unknown[]) => ({ ok: true }));
vi.mock("~/server/vectra/router-control", () => ({ checkInRouter }));
vi.mock("~/server/vectra/router-auto-onboarding", () => ({
  safelyMaybeAdvanceRouterOnboarding: vi.fn(async () => undefined),
}));
vi.mock("~/server/vectra/auth", () => ({
  authenticateRouter: vi.fn(async (headers: Headers) =>
    headers.get("x-vectra-router-id")
      ? {
          router: { id: headers.get("x-vectra-router-id") },
          credential: { id: CREDENTIAL_ID, devicePublicKey: vectors.checkInSignature.devicePublicKeyBase64 },
        }
      : null,
  ),
}));

const { POST } = await import("./route");

function devicePrivateKey() {
  const seed = Buffer.from(vectors.checkInSignature.devicePrivateKeyBase64, "base64").subarray(0, 32);
  return createPrivateKey({
    key: Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), seed]),
    format: "der",
    type: "pkcs8",
  });
}

const BODY = '{"protocolVersion":"2026-04-v1",  "routerId":"' + ROUTER_ID + '"}';

function signatureHeaders(signedBody: string) {
  const nonce = mintCheckInNonce({
    secretsKey: vectors.secretsKey,
    routerId: ROUTER_ID,
    issuedAtUnix: Math.floor(Date.now() / 1000),
  });
  const message = buildCheckInSignatureMessage(nonce, ROUTER_ID, sha256Hex(Buffer.from(signedBody, "utf8")));
  return {
    "x-vectra-checkin-nonce": nonce,
    "x-vectra-checkin-signature": sign(null, Buffer.from(message, "utf8"), devicePrivateKey()).toString("base64"),
  };
}

function checkInRequest(headers: Record<string, string> = {}, body = BODY) {
  return new Request("https://example.test/api/router/check-in", {
    method: "POST",
    headers: { "x-vectra-router-id": ROUTER_ID, "x-vectra-client-ip": "203.0.113.77", ...headers },
    body,
  });
}

function setFlags(flags: Record<string, string> = {}) {
  envMock.env = { NODE_ENV: "test", VECTRA_SECRETS_KEY: vectors.secretsKey, ...flags };
}

beforeEach(() => {
  setFlags();
  fake.reset();
  checkInRouter.mockClear();
  resetRouterAuthPolicyStateForTest();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("POST /api/router/check-in — check-in signatures (A2)", () => {
  it("off: reads no signature header, writes nothing and hands over the A3 context", async () => {
    const request = checkInRequest({
      "x-vectra-checkin-nonce": "vcn1.garbage",
      "x-vectra-checkin-signature": "garbage",
    });
    const get = vi.spyOn(request.headers, "get");

    const response = await POST(request);

    expect(response.status).toBe(200);
    expect(get.mock.calls.map(([name]) => name.toLowerCase())).not.toContain("x-vectra-checkin-nonce");
    expect(get.mock.calls.map(([name]) => name.toLowerCase())).not.toContain("x-vectra-checkin-signature");
    expect(fake.calls).toEqual([]);
    expect(checkInRouter).toHaveBeenCalledWith(ROUTER_ID, JSON.parse(BODY), {
      devicePublicKey: vectors.checkInSignature.devicePublicKeyBase64,
      credentialId: CREDENTIAL_ID,
      clientIp: "203.0.113.77",
      router: { id: ROUTER_ID },
    });
  });

  it("observing: accepts an unsigned check-in and records the verdict", async () => {
    setFlags({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });

    const response = await POST(checkInRequest());

    expect(response.status).toBe(200);
    expect(checkInRouter).toHaveBeenCalledTimes(1);
    expect(fake.inserts(eventLog)).toEqual([
      expect.objectContaining({ type: "router.checkin_signature.observed", metadata: { verdict: "absent", previousVerdict: null } }),
    ]);
  });

  it("requiring: refuses an unsigned check-in with the exact 401 and does not process it", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });

    const response = await POST(checkInRequest());

    expect(response.status).toBe(401);
    const text = await response.text();
    const { checkInNonce } = JSON.parse(text) as { checkInNonce: string };
    expect(text).toBe(
      JSON.stringify({ error: "Check-in signature required.", code: "checkin_signature_required", checkInNonce }),
    );
    expect(
      verifyCheckInNonce({
        secretsKey: vectors.secretsKey,
        nonce: checkInNonce,
        routerId: ROUTER_ID,
        nowUnix: Math.floor(Date.now() / 1000),
      }),
    ).toBe("valid");
    expect(checkInRouter).not.toHaveBeenCalled();
    expect(response.headers.get("server-timing")).toMatch(/^app;dur=/);
  });

  it("requiring: accepts a signature over the exact bytes sent, and only those", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });

    expect((await POST(checkInRequest(signatureHeaders(BODY)))).status).toBe(200);
    expect(checkInRouter).toHaveBeenCalledTimes(1);

    // Same JSON, other bytes: what the router signed is not what arrived.
    const reserialized = JSON.stringify(JSON.parse(BODY));
    expect((await POST(checkInRequest(signatureHeaders(reserialized)))).status).toBe(401);
    expect(checkInRouter).toHaveBeenCalledTimes(1);
  });
});

describe("POST /api/router/check-in — duplicate credential (A3)", () => {
  it("answers an enforced device mismatch with the exact 403 body", async () => {
    checkInRouter.mockRejectedValueOnce(
      routerErrorWithBody(403, {
        error: "Router credential is bound to a different device.",
        code: "credential_device_mismatch",
      }),
    );

    const response = await POST(checkInRequest());

    expect(response.status).toBe(403);
    expect(await response.text()).toBe(
      JSON.stringify({ error: "Router credential is bound to a different device.", code: "credential_device_mismatch" }),
    );
  });
});
