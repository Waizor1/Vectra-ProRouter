import { timingSafeEqual } from "node:crypto";

const SESSION_COOKIE_NAME = "vectra_operator_session";
const SESSION_TTL_MS = 1000 * 60 * 60 * 24 * 7;

type SessionPayload = {
  user: string;
  exp: number;
};

function getSessionSecret() {
  if (process.env.VECTRA_SECRETS_KEY) {
    return process.env.VECTRA_SECRETS_KEY;
  }

  if (process.env.NODE_ENV !== "production") {
    return "vectra-dev-session-secret";
  }

  throw new Error("VECTRA_SECRETS_KEY is required in production.");
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

async function importSigningKey(secret: string) {
  return crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign", "verify"],
  );
}

async function signValue(value: string) {
  const key = await importSigningKey(getSessionSecret());
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
  const payload: SessionPayload = {
    user,
    exp: Date.now() + SESSION_TTL_MS,
  };
  const encodedPayload = encodeBase64Url(JSON.stringify(payload));
  const signature = await signValue(encodedPayload);
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

  const expectedSignature = await signValue(payloadPart);
  const expectedBuffer = Buffer.from(expectedSignature);
  const actualBuffer = Buffer.from(signaturePart);
  if (
    expectedBuffer.length !== actualBuffer.length ||
    !timingSafeEqual(expectedBuffer, actualBuffer)
  ) {
    return null;
  }

  try {
    const payload = JSON.parse(decodeBase64Url(payloadPart)) as SessionPayload;
    if (!payload.user || payload.exp <= Date.now()) {
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
