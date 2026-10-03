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

// Brute-force brake, in process (one web container). Per client address —
// Caddy overwrites the trusted client-ip header — and fleet-wide, so many
// addresses cannot share the guessing either. An address already over its own
// budget does not spend the fleet-wide one.
const loginAttemptsPerAddress = new MemoryWindowRateLimiter(5, 60 * 1000);
const loginAttemptsOverall = new MemoryWindowRateLimiter(30, 60 * 1000);

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
  if (
    !loginAttemptsPerAddress.consume(clientKey).allowed ||
    !loginAttemptsOverall.consume("all").allowed
  ) {
    return relativeRedirect("/login?error=rate");
  }

  const formData = await request.formData();
  const usernameEntry = formData.get("username");
  const passwordEntry = formData.get("password");
  const username =
    typeof usernameEntry === "string" ? usernameEntry.trim() : "";
  const password =
    typeof passwordEntry === "string" ? passwordEntry : "";

  const usernameMatches = constantTimeStringEquals(
    username,
    env.VECTRA_OPERATOR_USER,
  );
  const passwordMatches = constantTimeStringEquals(
    password,
    env.VECTRA_OPERATOR_PASSWORD,
  );
  if (!usernameMatches || !passwordMatches) {
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
