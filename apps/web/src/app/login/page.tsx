import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import {
  getOperatorCookieName,
  verifyOperatorSession,
} from "~/server/operator-session";
import { LoginV2 } from "~/features/auth/login-v2";

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string }>;
}) {
  const [cookieStore, params] = await Promise.all([cookies(), searchParams]);
  const session = await verifyOperatorSession(
    cookieStore.get(getOperatorCookieName())?.value,
  );

  if (session) {
    redirect("/fleet");
  }

  return <LoginV2 hasError={params.error === "1"} />;
}
