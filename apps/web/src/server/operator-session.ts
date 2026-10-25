const SESSION_COOKIE_NAME = "vectra_operator_session";

function timingSafeStringEqual(a: string, b: string) {
  if (a.length !== b.length) {
    return false;
  }
  let mismatch = 0;
  for (let index = 0; index < a.length; index += 1) {
    mismatch |= a.charCodeAt(index) ^ b.charCodeAt(index);
  }
  return mismatch === 0;
}
const SESSION_TTL_MS = 1000 * 60 * 60 * 24 * 7;

type SessionPayload = {
  user: string;
  // When the cookie was issued (ms); VECTRA_OPERATOR_SESSIONS_NOT_BEFORE is
  // checked against it. A cookie without it is refused.
  iat: number;
  exp: number;
};

// The panel issues and checks every cookie on its own clock, so an issue time
// further ahead than a clock step is not one it wrote: refused, or a cookie
// signed with a leaked key could carry an iat past any cutoff set before it.
const MAX_ISSUED_AHEAD_MS = 1000 * 60 * 5;

const MIN_SESSION_SECRET_LENGTH = 32;
const DEV_SESSION_SECRET = "vectra-dev-session-secret";

// The cookie's own key, VECTRA_OPERATOR_SESSION_SECRET, and nothing else:
// never VECTRA_SECRETS_KEY, which encrypts the stored secrets and is never
// rotated to log operators out. Rotating this key ends every operator session
// at once and touches nothing else. In production a key that is missing,
// empty, shorter than 32 characters, padded with whitespace, the development
// key, a "replace-with…" placeholder or equal to VECTRA_SECRETS_KEY is no key:
// no cookie is issued and none is accepted. There is no fallback, so removing
// the key never brings back a cookie an earlier key ended. Outside production
// a fixed development key stands in for a missing one.
function getSessionSecret(): string | null {
  const dedicated = process.env.VECTRA_OPERATOR_SESSION_SECRET ?? "";
  if (
    dedicated.length >= MIN_SESSION_SECRET_LENGTH &&
    dedicated.trim() === dedicated &&
    dedicated !== DEV_SESSION_SECRET &&
    !dedicated.startsWith("replace-with") &&
    dedicated !== process.env.VECTRA_SECRETS_KEY
  ) {
    return dedicated;
  }
  if (dedicated === "" && process.env.NODE_ENV !== "production") {
    return DEV_SESSION_SECRET;
  }
  return null;
}

// VECTRA_OPERATOR_SESSIONS_NOT_BEFORE, unix seconds: a cookie issued before it
// is refused whatever key signed it, so it ends every session without a new
// key and keeps a key that is put back from reviving its old cookies. Unset or
// empty: no cutoff. Any other value than an integer number of seconds turns
// sessions off, so that a typo never drops the cutoff unnoticed.
function getNotBeforeMs(): number | null {
  const raw = (process.env.VECTRA_OPERATOR_SESSIONS_NOT_BEFORE ?? "").trim();
  if (raw === "") {
    return 0;
  }
  if (!/^\d{1,12}$/.test(raw)) {
    return null;
  }
  return Number(raw) * 1000;
}

/** Whether a cookie can be issued now: a key, a readable cutoff that has passed. */
export function operatorSessionsAvailable() {
  const notBefore = getNotBeforeMs();
  return getSessionSecret() !== null && notBefore !== null && notBefore <= Date.now();
}

function encodeBase64Url(value: string) {
  return btoa(value)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

function decodeBase64Url(value: string) {
  const padded = value
    .replace(/-/g, "+")
    .replace(/_/g, "/")
    .padEnd(Math.ceil(value.length / 4) * 4, "=");
  return atob(padded);
}

// The payload as UTF-8 bytes in base64url: btoa takes Latin-1 only, and a
// user name outside it made the login throw. ASCII encodes as before.
function encodePayload(payload: SessionPayload) {
  const bytes = new TextEncoder().encode(JSON.stringify(payload));
  return encodeBase64Url(String.fromCharCode(...bytes));
}

function decodePayload(value: string) {
  const binary = decodeBase64Url(value);
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)) as SessionPayload;
}

async function importSigningKey(secret: string) {
  return crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign", "verify"],
  );
}

async function signValue(value: string, secret: string) {
  const key = await importSigningKey(secret);
  const signature = await crypto.subtle.sign(
    "HMAC",
    key,
    new TextEncoder().encode(value),
  );

  const bytes = Array.from(new Uint8Array(signature))
    .map((entry) => String.fromCharCode(entry))
    .join("");
  return encodeBase64Url(bytes);
}

export async function createOperatorSession(user: string) {
  const secret = getSessionSecret();
  if (!secret || !operatorSessionsAvailable()) {
    throw new Error(
      "Operator sessions are off: VECTRA_OPERATOR_SESSION_SECRET must be set (32+ characters, not VECTRA_SECRETS_KEY) and VECTRA_OPERATOR_SESSIONS_NOT_BEFORE, if set, a past unix time in seconds.",
    );
  }
  const now = Date.now();
  const payload: SessionPayload = {
    user,
    iat: now,
    exp: now + SESSION_TTL_MS,
  };
  const encodedPayload = encodePayload(payload);
  const signature = await signValue(encodedPayload, secret);
  return `${encodedPayload}.${signature}`;
}

export async function verifyOperatorSession(token: string | undefined | null) {
  if (!token) {
    return null;
  }

  const [payloadPart, signaturePart] = token.split(".");
  if (!payloadPart || !signaturePart) {
    return null;
  }

  const secret = getSessionSecret();
  const notBefore = getNotBeforeMs();
  if (!secret || notBefore === null) {
    return null;
  }

  const expectedSignature = await signValue(payloadPart, secret);
  if (!timingSafeStringEqual(expectedSignature, signaturePart)) {
    return null;
  }

  try {
    const payload = decodePayload(payloadPart);
    if (
      !payload.user ||
      typeof payload.iat !== "number" ||
      !Number.isFinite(payload.iat) ||
      typeof payload.exp !== "number" ||
      payload.iat < notBefore ||
      payload.iat > Date.now() + MAX_ISSUED_AHEAD_MS ||
      payload.exp - payload.iat > SESSION_TTL_MS ||
      payload.exp <= Date.now()
    ) {
      return null;
    }

    return payload;
  } catch {
    return null;
  }
}

export function getOperatorCookieName() {
  return SESSION_COOKIE_NAME;
}

export function getOperatorSessionMaxAgeSeconds() {
  return Math.floor(SESSION_TTL_MS / 1000);
}
