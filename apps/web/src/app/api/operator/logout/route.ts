import { cookies } from "next/headers";

import { getOperatorCookieName } from "~/server/operator-session";
import { relativeRedirect } from "~/server/redirect";

export async function POST() {
  const cookieStore = await cookies();
  cookieStore.set({
    name: getOperatorCookieName(),
    value: "",
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    maxAge: 0,
    path: "/",
  });

  return relativeRedirect("/login");
}
