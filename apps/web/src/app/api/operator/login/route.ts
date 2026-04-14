import { cookies } from "next/headers";

import { env } from "~/env";
import {
  createOperatorSession,
  getOperatorCookieName,
  getOperatorSessionMaxAgeSeconds,
} from "~/server/operator-session";
import { relativeRedirect } from "~/server/redirect";

export async function POST(request: Request) {
  const formData = await request.formData();
  const usernameEntry = formData.get("username");
  const passwordEntry = formData.get("password");
  const username =
    typeof usernameEntry === "string" ? usernameEntry.trim() : "";
  const password =
    typeof passwordEntry === "string" ? passwordEntry : "";

  if (
    username !== env.VECTRA_OPERATOR_USER ||
    password !== env.VECTRA_OPERATOR_PASSWORD
  ) {
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
