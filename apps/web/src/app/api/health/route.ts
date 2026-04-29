import { sql } from "drizzle-orm";

import { db } from "~/server/db";
import { startAutoRescueMonitor } from "~/server/vectra/auto-rescue";
import { startBrowserPushMonitor } from "~/server/vectra/browser-push-monitor";

export const dynamic = "force-dynamic";

export async function GET() {
  const checkedAt = new Date().toISOString();
  const checks = {
    browserPushMonitor: false,
    autoRescueMonitor: false,
    dbRead: false,
    dbWriteProbe: false,
  };

  try {
    startBrowserPushMonitor();
    checks.browserPushMonitor = true;
    startAutoRescueMonitor();
    checks.autoRescueMonitor = true;
    await db.execute(sql`select 1`);
    checks.dbRead = true;
    const probeId = crypto.randomUUID();
    await db.execute(sql`
      with inserted as (
        insert into vectra_event_log (id, type, severity, message)
        values (${probeId}, 'health.db_write_probe', 'info', 'health route db write probe')
        returning id
      )
      delete from vectra_event_log
      where id in (select id from inserted)
    `);
    checks.dbWriteProbe = true;

    return Response.json(
      {
        ok: true,
        service: "vectra-web",
        checkedAt,
        checks,
      },
      { status: 200 },
    );
  } catch (error) {
    console.error("[health]", error);
    return Response.json(
      {
        ok: false,
        service: "vectra-web",
        checkedAt,
        checks,
        error: error instanceof Error ? error.message : "health check failed",
      },
      { status: 503 },
    );
  }
}
