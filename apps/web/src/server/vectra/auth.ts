import { routerCredentials, routers } from "@vectra/db";
import { and, eq, isNull } from "drizzle-orm";
import { createHash, randomBytes } from "node:crypto";

import { db } from "~/server/db";

export function hashToken(token: string) {
  return createHash("sha256").update(token).digest("hex");
}

export async function issueRouterCredential(
  routerId: string,
  devicePublicKey: string,
  type: "bootstrap" | "agent_token" = "agent_token"
) {
  const token = randomBytes(24).toString("base64url");
  const tokenHash = hashToken(token);
  const tokenPreview = `${token.slice(0, 6)}…${token.slice(-4)}`;

  await db
    .update(routerCredentials)
    .set({ revokedAt: new Date() })
    .where(and(eq(routerCredentials.routerId, routerId), isNull(routerCredentials.revokedAt)));

  const [credential] = await db
    .insert(routerCredentials)
    .values({
      routerId,
      type,
      tokenHash,
      tokenPreview,
      devicePublicKey,
    })
    .returning();

  return {
    token,
    credential,
  };
}

export async function authenticateRouter(headers: Headers) {
  const routerId = headers.get("x-vectra-router-id");
  const token = headers.get("x-vectra-router-token");

  if (!routerId || !token) {
    return null;
  }

  const [credential] = await db
    .select()
    .from(routerCredentials)
    .where(
      and(
        eq(routerCredentials.routerId, routerId),
        eq(routerCredentials.tokenHash, hashToken(token)),
        isNull(routerCredentials.revokedAt)
      )
    )
    .limit(1);

  if (!credential) {
    return null;
  }

  const [router] = await db.select().from(routers).where(eq(routers.id, routerId)).limit(1);
  if (!router) {
    return null;
  }

  await db
    .update(routerCredentials)
    .set({ lastUsedAt: new Date() })
    .where(eq(routerCredentials.id, credential.id));

  return { router, credential };
}
