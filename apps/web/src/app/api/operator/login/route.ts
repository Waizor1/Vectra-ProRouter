import { timingSafeEqual } from "node:crypto";

import { cookies } from "next/headers";

import { env } from "~/env";
import {
  createOperatorSession,
  getOperatorCookieName,
  getOperatorSessionMaxAgeSeconds,
} from "~/server/operator-session";
import { relativeRedirect } from "~/server/redirect";

function constantTimeStringEquals(actual: string, expected: string) {
  const actualBuffer = Buffer.from(actual);
  const expectedBuffer = Buffer.from(expected);
  if (actualBuffer.length !== expectedBuffer.length) {
    return false;
  }
  return timingSafeEqual(actualBuffer, expectedBuffer);
}

export async function POST(request: Request) {
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
