import { sql } from "drizzle-orm";

import { db } from "~/server/db";
import { startBrowserPushMonitor } from "~/server/vectra/browser-push-monitor";

export const dynamic = "force-dynamic";

export async function GET() {
  try {
    startBrowserPushMonitor();
    await db.execute(sql`select 1`);
    return Response.json(
      {
        ok: true,
        service: "vectra-web",
        checkedAt: new Date().toISOString(),
      },
      { status: 200 }
    );
  } catch (error) {
    console.error("[health]", error);
    return Response.json(
      {
        ok: false,
        service: "vectra-web",
        checkedAt: new Date().toISOString(),
      },
      { status: 503 }
    );
  }
}
