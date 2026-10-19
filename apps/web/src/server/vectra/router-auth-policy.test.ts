import { eventLog, routerCheckInNonces } from "@vectra/db";
import { createPrivateKey, sign } from "node:crypto";
import { readFileSync } from "node:fs";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";

import {
  buildCheckInSignatureMessage,
  mintEnrollmentToken,
  sha256Hex,
  verifyCheckInNonce,
  verifyEnrollmentToken,
} from "./router-auth-integrity";
import {
  checkInNonceField,
  checkInSignatureMode,
  enforceEnrollmentTokenForNewRouter,
  enrollmentTokenPresented,
  gateCheckInSignature,
  mintEnrollmentTokenForDevice,
  resetRouterAuthPolicyStateForTest,
  routerAuthFlags,
} from "./router-auth-policy";
import { resetCheckInReplayStateForTest } from "./router-checkin-replay";
import type { FakeDb } from "./testing/fake-db";

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});
const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

// Fake, public test vectors shared with the Go agent.
const vectors = JSON.parse(
  readFileSync(
    new URL("../../../../../packages/contracts/fixtures/router-auth-vectors.json", import.meta.url),
    "utf8",
  ),
) as {
  secretsKey: string;
  enrollmentToken: { deviceIdentifier: string; expiresAtUnix: number; token: string };
  checkInSignature: { devicePrivateKeyBase64: string; devicePublicKeyBase64: string; routerId: string };
};
const SECRETS_KEY = vectors.secretsKey;
const DEVICE = vectors.enrollmentToken.deviceIdentifier;
const ROUTER_ID = vectors.checkInSignature.routerId;
const DEVICE_PUBLIC_KEY = vectors.checkInSignature.devicePublicKeyBase64;
const NOW = new Date("2026-10-08T12:00:00.000Z");
const NOW_UNIX = Math.floor(NOW.getTime() / 1000);

function devicePrivateKey() {
  const seed = Buffer.from(vectors.checkInSignature.devicePrivateKeyBase64, "base64").subarray(0, 32);
  return createPrivateKey({
    key: Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), seed]),
    format: "der",
    type: "pkcs8",
  });
}

function setFlags(flags: Record<string, string> = {}) {
  envMock.env = {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: SECRETS_KEY,
    ...flags,
  };
}

/** Headers of a check-in signed by the vector device key over `body`. */
function signedHeaders(nonce: string, body: string, routerId = ROUTER_ID) {
  const message = buildCheckInSignatureMessage(nonce, routerId, sha256Hex(Buffer.from(body, "utf8")));
  return new Headers({
    "x-vectra-checkin-nonce": nonce,
    "x-vectra-checkin-signature": sign(null, Buffer.from(message, "utf8"), devicePrivateKey()).toString("base64"),
  });
}

beforeEach(() => {
  setFlags();
  fake.reset();
  resetRouterAuthPolicyStateForTest();
  resetCheckInReplayStateForTest();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("switches", () => {
  it("are all off by default and read a raw string the way the schema would", () => {
    expect(routerAuthFlags()).toEqual({
      enrollmentTokenRequired: false,
      issueCheckInNonce: false,
      requireCheckInSignature: false,
      duplicateCredentialEnforce: false,
    });
    setFlags({
      VECTRA_ENROLLMENT_TOKEN_REQUIRED: "false",
      VECTRA_ISSUE_CHECKIN_NONCE: "false",
      VECTRA_REQUIRE_CHECKIN_SIGNATURE: "false",
      VECTRA_DUPLICATE_CREDENTIAL_ENFORCE: "false",
    });
    expect(Object.values(routerAuthFlags()).some(Boolean)).toBe(false);
    expect(checkInSignatureMode()).toBe("off");
  });

  it("makes requiring signatures imply issuing nonces", () => {
    setFlags({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });
    expect(checkInSignatureMode()).toBe("observe");
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });
    expect(routerAuthFlags().issueCheckInNonce).toBe(true);
    expect(checkInSignatureMode()).toBe("require");
  });
});

describe("A1 provisioning token", () => {
  const validToken = () =>
    mintEnrollmentToken({ secretsKey: SECRETS_KEY, deviceIdentifier: DEVICE, expiresAtUnix: NOW_UNIX + 3600 });

  it("off: never refuses, says once what it would have refused", async () => {
    const info = vi.spyOn(console, "info").mockImplementation(() => undefined);

    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: undefined, now: NOW }),
    ).resolves.toBe("absent");
    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: "vpt1.1.bad", now: NOW }),
    ).resolves.toBe("malformed");
    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: validToken(), now: NOW }),
    ).resolves.toBe("valid");

    expect(info.mock.calls).toEqual([
      [`[router-enrollment] would require provisioning token deviceIdentifier=${DEVICE} verdict=absent`],
      [`[router-enrollment] would require provisioning token deviceIdentifier=${DEVICE} verdict=malformed`],
    ]);
    expect(fake.calls).toEqual([]);
  });

  it("off: keeps the log line one line whatever identifier an anonymous caller sends", async () => {
    const info = vi.spyOn(console, "info").mockImplementation(() => undefined);
    await enforceEnrollmentTokenForNewRouter({ deviceIdentifier: "evil\n[forged] line", enrollmentToken: undefined, now: NOW });
    expect(String(info.mock.calls[0]?.[0])).not.toContain("\n");
  });

  it("on: refuses every verdict but valid with 403 and records the verdict, never the token", async () => {
    setFlags({ VECTRA_ENROLLMENT_TOKEN_REQUIRED: "true" });
    const expired = mintEnrollmentToken({ secretsKey: SECRETS_KEY, deviceIdentifier: DEVICE, expiresAtUnix: NOW_UNIX - 1 });
    const otherDevice = mintEnrollmentToken({ secretsKey: SECRETS_KEY, deviceIdentifier: "vectra-ffffffffffff", expiresAtUnix: NOW_UNIX + 60 });

    for (const [token, verdict] of [
      [undefined, "absent"],
      ["garbage", "malformed"],
      [expired, "expired"],
      [otherDevice, "mac_mismatch"],
    ] as const) {
      await expect(
        enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: token, now: NOW }),
      ).rejects.toMatchObject({ status: 403, message: "Router enrollment requires a valid provisioning token." });
      const event = fake.inserts(eventLog).at(-1);
      expect(Object.keys(event ?? {}).sort()).toEqual(["message", "metadata", "severity", "type"]);
      expect(event).toMatchObject({
        type: "router.enrollment.token_rejected",
        severity: "warning",
        metadata: { deviceIdentifier: DEVICE, verdict },
      });
      expect(event?.metadata).toEqual({ deviceIdentifier: DEVICE, verdict });
    }
    expect(JSON.stringify(fake.calls.map(({ table: _table, ...call }) => call))).not.toContain(expired);

    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: validToken(), now: NOW }),
    ).resolves.toBe("valid");
    expect(fake.inserts(eventLog)).toHaveLength(4);
  });

  it("tells a token the contract dropped apart from none", async () => {
    const info = vi.spyOn(console, "info").mockImplementation(() => undefined);
    expect(enrollmentTokenPresented({ enrollmentToken: 42 })).toBe(true);
    expect(enrollmentTokenPresented({ enrollmentToken: "x".repeat(300) })).toBe(true);
    expect(enrollmentTokenPresented({ enrollmentToken: "" })).toBe(false);
    expect(enrollmentTokenPresented({ enrollmentToken: null })).toBe(false);
    expect(enrollmentTokenPresented({})).toBe(false);
    expect(enrollmentTokenPresented(null)).toBe(false);
    expect(enrollmentTokenPresented([])).toBe(false);

    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: undefined, tokenPresented: true, now: NOW }),
    ).resolves.toBe("malformed");
    // A readable token is judged on its own, whatever the flag says.
    await expect(
      enforceEnrollmentTokenForNewRouter({ deviceIdentifier: DEVICE, enrollmentToken: validToken(), tokenPresented: true, now: NOW }),
    ).resolves.toBe("valid");
    expect(info).toHaveBeenCalledTimes(1);
  });

  it("mints a token the verifier accepts, for 1..168 hours", () => {
    const minted = mintEnrollmentTokenForDevice({ deviceIdentifier: DEVICE, ttlHours: 24, now: NOW });
    expect(minted.expiresAt.toISOString()).toBe("2026-10-09T12:00:00.000Z");
    expect(
      verifyEnrollmentToken({ secretsKey: SECRETS_KEY, token: minted.token, deviceIdentifier: DEVICE, nowUnix: NOW_UNIX }),
    ).toBe("valid");
    expect(
      verifyEnrollmentToken({
        secretsKey: SECRETS_KEY,
        token: mintEnrollmentTokenForDevice({ deviceIdentifier: DEVICE, ttlHours: 168, now: NOW }).token,
        deviceIdentifier: DEVICE,
        nowUnix: NOW_UNIX,
      }),
    ).toBe("valid");
    for (const ttlHours of [0, 169, 1.5]) {
      expect(() => mintEnrollmentTokenForDevice({ deviceIdentifier: DEVICE, ttlHours, now: NOW })).toThrow();
    }
  });
});

describe("A2 check-in nonce issuance", () => {
  it("adds no key at all while both switches are off", () => {
    const field = checkInNonceField(ROUTER_ID, NOW);
    expect(Object.keys(field)).toEqual([]);
  });

  it("issues a nonce for this router when observing or requiring", () => {
    for (const flag of ["VECTRA_ISSUE_CHECKIN_NONCE", "VECTRA_REQUIRE_CHECKIN_SIGNATURE"]) {
      setFlags({ [flag]: "true" });
      const { checkInNonce } = checkInNonceField(ROUTER_ID, NOW);
      expect(
        verifyCheckInNonce({ secretsKey: SECRETS_KEY, nonce: checkInNonce ?? "", routerId: ROUTER_ID, nowUnix: NOW_UNIX }),
      ).toBe("valid");
    }
  });
});

describe("A2 check-in signature gate", () => {
  const body = '{"protocolVersion":"2026-04-v1"}';
  const bodySha256Hex = sha256Hex(Buffer.from(body, "utf8"));
  const gate = (mode: "observe" | "require", headers: Headers, routerId = ROUTER_ID) =>
    gateCheckInSignature({ mode, routerId, headers, bodySha256Hex, devicePublicKey: DEVICE_PUBLIC_KEY, now: NOW });
  const freshNonce = () => checkInNonceField(ROUTER_ID, NOW).checkInNonce ?? "";

  it("observing: accepts regardless and records only verdict changes", async () => {
    setFlags({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });

    expect(await gate("observe", new Headers())).toBeNull();
    expect(await gate("observe", new Headers())).toBeNull();
    expect(await gate("observe", signedHeaders(freshNonce(), body))).toBeNull();
    expect(await gate("observe", signedHeaders(freshNonce(), '{"tampered":true}'))).toBeNull();

    expect(fake.inserts(eventLog)).toEqual([
      expect.objectContaining({
        routerId: ROUTER_ID,
        type: "router.checkin_signature.observed",
        severity: "warning",
        metadata: { verdict: "absent", previousVerdict: null },
      }),
      expect.objectContaining({
        type: "router.checkin_signature.observed",
        severity: "info",
        metadata: { verdict: "valid", previousVerdict: "absent" },
      }),
      expect.objectContaining({
        type: "router.checkin_signature.observed",
        severity: "warning",
        metadata: { verdict: "signature_invalid", previousVerdict: "valid" },
      }),
    ]);
  });

  it("observing: a failing event write never fails the check-in", async () => {
    setFlags({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    vi.spyOn(fake.db, "insert").mockImplementation(() => {
      throw new Error("relation does not exist");
    });

    expect(await gate("observe", new Headers())).toBeNull();
    expect(warn).toHaveBeenCalledTimes(1);
  });

  it("requiring: refuses with the exact 401 body and a fresh nonce", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });

    const refusal = await gate("require", new Headers());

    expect(refusal?.status).toBe(401);
    const text = await refusal?.text();
    const parsed = JSON.parse(text ?? "{}") as { checkInNonce: string };
    expect(text).toBe(
      JSON.stringify({
        error: "Check-in signature required.",
        code: "checkin_signature_required",
        checkInNonce: parsed.checkInNonce,
      }),
    );
    expect(
      verifyCheckInNonce({ secretsKey: SECRETS_KEY, nonce: parsed.checkInNonce, routerId: ROUTER_ID, nowUnix: NOW_UNIX }),
    ).toBe("valid");
  });

  it("requiring: refuses a bad or foreign signature and lets a valid one through", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });

    expect((await gate("require", signedHeaders(freshNonce(), "{}")))?.status).toBe(401);
    expect((await gate("require", signedHeaders("vcn1.0.AAAAAAAAAAAAAAAAAAAAAA.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", body)))?.status).toBe(401);
    // A nonce issued to another router does not sign this one's check-in.
    const otherRouter = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
    const foreign = checkInNonceField(otherRouter, NOW).checkInNonce ?? "";
    expect((await gate("require", signedHeaders(foreign, body)))?.status).toBe(401);
    expect(await gate("require", signedHeaders(freshNonce(), body))).toBeNull();
  });

  it("requiring: records a refusal at most once an hour per router and verdict", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });

    await gate("require", new Headers());
    await gate("require", new Headers());
    await gate("require", signedHeaders(freshNonce(), "{}"));
    await gate("require", new Headers(), "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31");

    expect(fake.inserts(eventLog).map((event) => [event.routerId, event.type, event.metadata])).toEqual([
      [ROUTER_ID, "router.checkin_signature.rejected", { verdict: "absent" }],
      [ROUTER_ID, "router.checkin_signature.rejected", { verdict: "signature_invalid" }],
      ["6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31", "router.checkin_signature.rejected", { verdict: "absent" }],
    ]);
  });
});

describe("A2 check-in nonce replay (F2)", () => {
  const body = '{"protocolVersion":"2026-04-v1"}';
  const bodySha256Hex = sha256Hex(Buffer.from(body, "utf8"));
  const freshNonce = () => checkInNonceField(ROUTER_ID, NOW).checkInNonce ?? "";

  type Row = Record<string, unknown>;

  // A stateful ledger: nonce inserts dedupe by (routerId, nonceHash) exactly as
  // the composite primary key does; event inserts are recorded. It is injected
  // as `database` so the shared fake db's non-replay assertions stay untouched.
  function createReplayLedgerDb() {
    const consumed = new Set<string>();
    const events: Row[] = [];
    const settle =
      <T,>(run: () => T) =>
      (onF?: (value: T) => unknown, onR?: (reason: unknown) => unknown) =>
        Promise.resolve()
          .then(run)
          .then(onF as never, onR as never);
    const db = {
      delete() {
        return { where: () => ({ then: settle(() => []) }) };
      },
      insert(table: unknown) {
        return {
          values(values: Row) {
            const run = () => {
              if (table === routerCheckInNonces) {
                const key = `${values.routerId as string}|${values.nonceHash as string}`;
                if (consumed.has(key)) {
                  return [] as Row[];
                }
                consumed.add(key);
                return [{ nonceHash: values.nonceHash }];
              }
              events.push(values);
              return [{ id: "evt", ...values }];
            };
            const statement = {
              onConflictDoNothing() {
                return statement;
              },
              returning: () => Promise.resolve().then(run),
              then: settle(run),
            };
            return statement;
          },
        };
      },
    };
    return { db, events };
  }

  const gate = (
    mode: "observe" | "require",
    headers: Headers,
    database: ReturnType<typeof createReplayLedgerDb>["db"],
  ) =>
    gateCheckInSignature({
      mode,
      routerId: ROUTER_ID,
      headers,
      bodySha256Hex,
      devicePublicKey: DEVICE_PUBLIC_KEY,
      now: NOW,
      // The stub implements only the insert/delete the gate uses here.
      database: database as never,
    });

  it("requiring: refuses a replayed valid tuple with 401 + fresh nonce and logs it", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });
    const ledger = createReplayLedgerDb();
    const nonce = freshNonce();

    // ed25519 is deterministic, so re-signing the same nonce+body yields the
    // identical tuple — a true capture-and-replay.
    expect(await gate("require", signedHeaders(nonce, body), ledger.db)).toBeNull();
    const replay = await gate("require", signedHeaders(nonce, body), ledger.db);

    expect(replay?.status).toBe(401);
    const parsed = JSON.parse((await replay?.text()) ?? "{}") as {
      code: string;
      checkInNonce: string;
    };
    expect(parsed.code).toBe("checkin_signature_required");
    expect(
      verifyCheckInNonce({ secretsKey: SECRETS_KEY, nonce: parsed.checkInNonce, routerId: ROUTER_ID, nowUnix: NOW_UNIX }),
    ).toBe("valid");
    expect(ledger.events.map((event) => event.type)).toContain(
      "router.checkin_signature.replayed",
    );
  });

  it("requiring: two sequential check-ins with distinct fresh nonces are both accepted", async () => {
    setFlags({ VECTRA_REQUIRE_CHECKIN_SIGNATURE: "true" });
    const ledger = createReplayLedgerDb();

    expect(await gate("require", signedHeaders(freshNonce(), body), ledger.db)).toBeNull();
    expect(await gate("require", signedHeaders(freshNonce(), body), ledger.db)).toBeNull();
    expect(ledger.events).toEqual([]);
  });

  it("observing: a replayed valid tuple is accepted but logged", async () => {
    setFlags({ VECTRA_ISSUE_CHECKIN_NONCE: "true" });
    const ledger = createReplayLedgerDb();
    const nonce = freshNonce();

    expect(await gate("observe", signedHeaders(nonce, body), ledger.db)).toBeNull();
    expect(await gate("observe", signedHeaders(nonce, body), ledger.db)).toBeNull();

    const types = ledger.events.map((event) => event.type);
    expect(types).toContain("router.checkin_signature.observed");
    expect(types).toContain("router.checkin_signature.replayed");
  });
});
