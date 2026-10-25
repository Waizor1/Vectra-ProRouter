import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The stored secrets are encrypted with the shared key (secrets.ts reads it
// from ~/env); the cookie code reads process.env at every sign and check.
const SHARED_KEY = "shared-secrets-key-0123456789abcdef0123";
vi.mock("~/env", () => ({ env: { VECTRA_SECRETS_KEY: SHARED_KEY } }));

const { createOperatorSession, operatorSessionsAvailable, verifyOperatorSession } =
  await import("./operator-session");
const { decryptJson, encryptJson } = await import("./vectra/secrets");

const KEY_A = "operator-session-secret-A-0123456789abcdef";
const KEY_B = "operator-session-secret-B-0123456789abcdef";
const T0 = new Date("2026-10-24T10:00:00.000Z");

function b64url(bytes: Uint8Array | string) {
  const raw = typeof bytes === "string" ? bytes : String.fromCharCode(...bytes);
  return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

// A cookie in the format before this change — {user, exp}, HMAC-SHA256 over
// the base64url payload — signed with `secret`: with the shared key it is what
// an operator may still hold from the old code.
async function oldFormatCookie(secret: string, payload: Record<string, unknown>) {
  const part = b64url(JSON.stringify(payload));
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(part)));
  return `${part}.${b64url(sig)}`;
}

function legacyCookie() {
  return oldFormatCookie(SHARED_KEY, { user: "operator", exp: Date.now() + 6 * 24 * 3600 * 1000 });
}

function set(name: "VECTRA_OPERATOR_SESSION_SECRET" | "VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", value: string | undefined) {
  if (value === undefined) delete process.env[name];
  else process.env[name] = value;
}

const seconds = (date: Date) => String(Math.floor(date.getTime() / 1000));

describe("operator session: its own key, a not-before cutoff, fail closed", () => {
  const saved = { ...process.env };

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(T0);
    vi.stubEnv("NODE_ENV", "production");
    process.env.VECTRA_SECRETS_KEY = SHARED_KEY;
    set("VECTRA_OPERATOR_SESSION_SECRET", undefined);
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
    process.env = { ...saved };
  });

  it("with its own key a new cookie is accepted and carries its issue time", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const cookie = await createOperatorSession("operator");
    expect(await verifyOperatorSession(cookie)).toMatchObject({ user: "operator", iat: T0.getTime() });
  });

  it("a cookie of the old code (shared key) stays refused: own key A, then removed, then empty", async () => {
    const legacy = await legacyCookie();
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    expect(await verifyOperatorSession(legacy)).toBeNull();
    set("VECTRA_OPERATOR_SESSION_SECRET", undefined);
    expect(await verifyOperatorSession(legacy)).toBeNull();
    set("VECTRA_OPERATOR_SESSION_SECRET", "");
    expect(await verifyOperatorSession(legacy)).toBeNull();
  });

  // Independent of the iat rule: a shared-key cookie that does carry an issue
  // time (no such cookie was ever issued, but a fallback would accept one).
  it("never falls back to the shared key: a shared-key cookie with iat is refused with own key A, without it and with it empty", async () => {
    const sharedWithIat = await oldFormatCookie(SHARED_KEY, { user: "operator", iat: Date.now(), exp: Date.now() + 3_600_000 });
    for (const key of [KEY_A, undefined, ""]) {
      set("VECTRA_OPERATOR_SESSION_SECRET", key);
      expect(await verifyOperatorSession(sharedWithIat)).toBeNull();
    }
    vi.stubEnv("NODE_ENV", "development");
    set("VECTRA_OPERATOR_SESSION_SECRET", undefined);
    expect(await verifyOperatorSession(sharedWithIat)).toBeNull();
  });

  it("without its own key production issues no cookie and accepts none", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const underA = await createOperatorSession("operator");
    for (const missing of [undefined, ""]) {
      set("VECTRA_OPERATOR_SESSION_SECRET", missing);
      expect(operatorSessionsAvailable()).toBe(false);
      await expect(createOperatorSession("operator")).rejects.toThrow(/sessions are off/);
      expect(await verifyOperatorSession(underA)).toBeNull();
    }
  });

  it("a short, padded, development, placeholder or shared-equal key is no key", async () => {
    const legacy = await legacyCookie();
    const padded = ` ${KEY_A} `;
    const spaces = " ".repeat(40);
    for (const bad of ["too-short", SHARED_KEY, padded, spaces, "vectra-dev-session-secret", "replace-with-a-long-random-32-byte-plus-session-secret"]) {
      set("VECTRA_OPERATOR_SESSION_SECRET", bad);
      expect(operatorSessionsAvailable()).toBe(false);
      await expect(createOperatorSession("operator")).rejects.toThrow(/sessions are off/);
      expect(await verifyOperatorSession(legacy)).toBeNull();
    }
  });

  it("rotating the key ends every cookie of the previous one", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const underA = await createOperatorSession("operator");
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_B);
    expect(await verifyOperatorSession(underA)).toBeNull();
    expect(await verifyOperatorSession(await createOperatorSession("operator"))).not.toBeNull();
  });

  it("the cutoff refuses cookies issued before it and accepts those issued after", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const before = await createOperatorSession("operator");
    vi.setSystemTime(new Date(T0.getTime() + 60_000));
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", seconds(new Date(T0.getTime() + 30_000)));
    expect(await verifyOperatorSession(before)).toBeNull();
    const after = await createOperatorSession("operator");
    expect(await verifyOperatorSession(after)).toMatchObject({ user: "operator" });
  });

  // The cutoff is whole seconds. Set to the current second (`date +%s`), it
  // still lets through a cookie issued earlier in that same second; the next
  // second does not, and no cookie is issued until that second has come. Hence
  // the runbook's `$(( $(date +%s) + 1 ))`, written once nothing issues cookies.
  it("a cutoff at the current second keeps a cookie issued earlier in that second; the next second ends it", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    vi.setSystemTime(new Date(T0.getTime() + 700));
    const sameSecond = await createOperatorSession("operator");
    vi.setSystemTime(new Date(T0.getTime() + 900));
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", seconds(new Date(T0.getTime() + 900)));
    expect(await verifyOperatorSession(sameSecond)).not.toBeNull();
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", String(Number(seconds(new Date(T0.getTime() + 900))) + 1));
    expect(await verifyOperatorSession(sameSecond)).toBeNull();
    expect(operatorSessionsAvailable()).toBe(false);
    await expect(createOperatorSession("operator")).rejects.toThrow(/sessions are off/);
    vi.setSystemTime(new Date(T0.getTime() + 1_000));
    expect(await verifyOperatorSession(await createOperatorSession("operator"))).not.toBeNull();
  });

  it("the cutoff keeps a key that is put back from reviving its old cookies", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const oldUnderA = await createOperatorSession("operator");
    vi.setSystemTime(new Date(T0.getTime() + 3_600_000));
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_B);
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", seconds(new Date(T0.getTime() + 3_600_000)));
    // Back to A: without the cutoff its old cookie would be good again.
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    expect(await verifyOperatorSession(oldUnderA)).toBeNull();
    vi.setSystemTime(new Date(T0.getTime() + 3_601_000));
    expect(await verifyOperatorSession(await createOperatorSession("operator"))).not.toBeNull();
  });

  it("a cutoff still ahead issues no cookie; an unreadable one turns sessions off", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const cookie = await createOperatorSession("operator");
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", seconds(new Date(T0.getTime() + 600_000)));
    expect(operatorSessionsAvailable()).toBe(false);
    await expect(createOperatorSession("operator")).rejects.toThrow(/sessions are off/);
    for (const unreadable of ["yesterday", "1.5", "-1", "2026-10-24T10:00:00Z"]) {
      set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", unreadable);
      expect(operatorSessionsAvailable()).toBe(false);
      expect(await verifyOperatorSession(cookie)).toBeNull();
    }
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", "");
    expect(await verifyOperatorSession(cookie)).not.toBeNull();
  });

  it("a cookie without an issue time is refused, even under the right key", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const noIat = await oldFormatCookie(KEY_A, { user: "operator", exp: Date.now() + 3_600_000 });
    expect(await verifyOperatorSession(noIat)).toBeNull();
    // Positive control: the same format with an issue time goes through.
    const withIat = await oldFormatCookie(KEY_A, { user: "operator", iat: Date.now(), exp: Date.now() + 3_600_000 });
    expect(await verifyOperatorSession(withIat)).not.toBeNull();
  });

  it("a user name outside Latin-1 signs in and round-trips (was a 500)", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const cookie = await createOperatorSession("оператор-生");
    expect(await verifyOperatorSession(cookie)).toMatchObject({ user: "оператор-生" });
  });

  it("an exp that is not a number, or a lifetime past 7 days, is refused even when signed", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const now = Date.now();
    for (const payload of [
      { user: "operator", iat: now, exp: String(now + 3_600_000) },
      { user: "operator", iat: now, exp: now + 8 * 24 * 3600 * 1000 },
    ]) {
      expect(await verifyOperatorSession(await oldFormatCookie(KEY_A, payload))).toBeNull();
    }
    // Positive control: a 7-day lifetime exactly is fine.
    expect(await verifyOperatorSession(await oldFormatCookie(KEY_A, { user: "operator", iat: now, exp: now + 7 * 24 * 3600 * 1000 }))).not.toBeNull();
  });

  it("an issue time more than 5 minutes ahead is refused even when signed, and it would outlive any cutoff", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const now = Date.now();
    const ahead = await oldFormatCookie(KEY_A, { user: "operator", iat: now + 10 * 60_000, exp: now + 10 * 60_000 + 3_600_000 });
    expect(await verifyOperatorSession(ahead)).toBeNull();
    // A cutoff set now would not end it if it were accepted: its iat is past it.
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", seconds(T0));
    expect(await verifyOperatorSession(ahead)).toBeNull();
    // Positive control: a clock step's worth ahead is still a cookie the panel wrote.
    set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", undefined);
    expect(await verifyOperatorSession(await oldFormatCookie(KEY_A, { user: "operator", iat: now + 60_000, exp: now + 3_600_000 }))).not.toBeNull();
  });

  it("an edited payload is refused", async () => {
    set("VECTRA_OPERATOR_SESSION_SECRET", KEY_A);
    const [, signature] = (await createOperatorSession("operator")).split(".");
    const forged = `${b64url(JSON.stringify({ user: "operator", iat: Date.now(), exp: Date.now() + 1e12 }))}.${signature}`;
    expect(await verifyOperatorSession(forged)).toBeNull();
  });

  // A guard, not a proof: the session code never reads or writes the shared
  // key's ciphertexts, so this pins that stored secrets keep opening across
  // every session-key and cutoff change.
  it("stored secrets keep decrypting across every session-key and cutoff change", async () => {
    const payload = { subscriptionUrl: "https://sub.example.invalid/x", password: "not-a-real-secret" };
    const envelope = encryptJson(payload, { aad: "router:r-1|revision:v-1" });
    for (const [key, cutoff] of [[KEY_A, undefined], [KEY_B, seconds(T0)], [undefined, undefined], ["", ""]] as const) {
      set("VECTRA_OPERATOR_SESSION_SECRET", key);
      set("VECTRA_OPERATOR_SESSIONS_NOT_BEFORE", cutoff);
      if (operatorSessionsAvailable()) await createOperatorSession("operator");
    }
    expect(decryptJson(envelope, { aad: "router:r-1|revision:v-1" })).toEqual(payload);
  });

  it("outside production a missing key is the fixed development key, never the shared one", async () => {
    vi.stubEnv("NODE_ENV", "development");
    const legacy = await legacyCookie();
    expect(await verifyOperatorSession(await createOperatorSession("operator"))).not.toBeNull();
    expect(await verifyOperatorSession(legacy)).toBeNull();
  });

  it("outside production a key that is set but no key stays no key: never the development key", async () => {
    vi.stubEnv("NODE_ENV", "development");
    const devCookie = await createOperatorSession("operator");
    for (const bad of ["short", " " + KEY_A, "replace-with-a-long-random-session-key-0123456789", SHARED_KEY]) {
      set("VECTRA_OPERATOR_SESSION_SECRET", bad);
      expect(operatorSessionsAvailable()).toBe(false);
      expect(await verifyOperatorSession(devCookie)).toBeNull();
    }
  });
});
