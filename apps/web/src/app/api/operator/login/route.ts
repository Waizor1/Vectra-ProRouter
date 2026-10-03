import { createHash, timingSafeEqual } from "node:crypto";

import { cookies } from "next/headers";

import { env } from "~/env";
import {
  createOperatorSession,
  getOperatorCookieName,
  getOperatorSessionMaxAgeSeconds,
} from "~/server/operator-session";
import { relativeRedirect } from "~/server/redirect";
import {
  MemoryWindowRateLimiter,
  rateLimitKeyForIp,
  readRequestIp,
} from "~/server/vectra/public-install-rate-limit";

// Brute-force brake, in process (one web container). Only FAILED attempts
// are counted, never successes. An address with 5 failures in a minute is
// refused before its password is checked. Across all addresses, 30 failures a
// minute closes the door too — but only to addresses that have failed
// recently, so a flood from elsewhere never locks out an operator signing in
// from a clean address. The address is the Caddy-set client-ip header.
const LOGIN_FAILURES_PER_ADDRESS = 5;
const LOGIN_FAILURES_OVERALL = 30;
const loginFailuresPerAddress = new MemoryWindowRateLimiter(
  LOGIN_FAILURES_PER_ADDRESS,
  60 * 1000,
);
const loginFailuresOverall = new MemoryWindowRateLimiter(
  LOGIN_FAILURES_OVERALL,
  60 * 1000,
);

function loginRefusedForAddress(clientKey: string) {
  const failures = loginFailuresPerAddress.peek(clientKey);
  return (
    failures >= LOGIN_FAILURES_PER_ADDRESS ||
    (failures > 0 && loginFailuresOverall.peek("all") >= LOGIN_FAILURES_OVERALL)
  );
}

// A warning, never a refusal to start: production must keep running on the
// password it has. Checked on the first attempt, not at import, so a build
// without the env does not trip over it.
let shortPasswordWarned = false;
function warnIfPasswordShort() {
  if (shortPasswordWarned || env.NODE_ENV !== "production") {
    return;
  }
  shortPasswordWarned = true;
  if (env.VECTRA_OPERATOR_PASSWORD.length < 16) {
    console.warn(
      "[operator-login] VECTRA_OPERATOR_PASSWORD is shorter than 16 characters; use a longer one.",
    );
  }
}

function sha256(value: string) {
  return createHash("sha256").update(value).digest();
}

// Compares digests, which are equal-length whatever the inputs: neither the
// comparison nor a length check can leak how long the secret is.
function constantTimeStringEquals(actual: string, expected: string) {
  return timingSafeEqual(sha256(actual), sha256(expected));
}

export async function POST(request: Request) {
  warnIfPasswordShort();
  const clientKey = rateLimitKeyForIp(readRequestIp(request));
  if (loginRefusedForAddress(clientKey)) {
    return relativeRedirect("/login?error=rate");
  }

  const formData = await request.formData();
  const usernameEntry = formData.get("username");
  const passwordEntry = formData.get("password");
  const username =
    typeof usernameEntry === "string" ? usernameEntry.trim() : "";
  const password = typeof passwordEntry === "string" ? passwordEntry : "";

  const usernameMatches = constantTimeStringEquals(
    username,
    env.VECTRA_OPERATOR_USER,
  );
  const passwordMatches = constantTimeStringEquals(
    password,
    env.VECTRA_OPERATOR_PASSWORD,
  );
  if (!usernameMatches || !passwordMatches) {
    loginFailuresPerAddress.consume(clientKey);
    loginFailuresOverall.consume("all");
    return relativeRedirect("/login?error=1");
  }

  const cookieStore = await cookies();
  cookieStore.set({
    name: getOperatorCookieName(),
    value: await createOperatorSession(username),
    httpOnly: true,
    secure: env.NODE_ENV === "production",
    sameSite: "lax",
    maxAge: getOperatorSessionMaxAgeSeconds(),
    path: "/",
  });

  return relativeRedirect("/fleet");
}
