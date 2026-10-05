/**
 * One minute of the background monitors, before and after round 2, against a
 * real PostgreSQL seeded at production volume (2026-10-05: 38 routers, 7,616
 * verify_passwall_routes results of ~14 MB).
 *
 *   PANEL_PERF_PG_PORT=55441 SKIP_ENV_VALIDATION=1 \
 *   DATABASE_URL=postgres://synthetic@localhost/none \
 *   VECTRA_SECRETS_KEY=$(head -c 32 /dev/zero | base64) \
 *   npx vitest bench --run src/server/vectra/monitor-tick.postgres.bench.ts
 *
 * Throwaway cluster only (see panel-perf.postgres.integration.test.ts): the
 * seed TRUNCATEs vectra_router.
 *
 *   before: auto-rescue and the push monitor each load the fleet snapshot, in
 *           parallel, with the original route-verification loader (every
 *           result of the fleet); auto-rescue's blocked-reachability scan
 *           reads three full snapshots per router, one router at a time.
 *   after:  one shared fleet snapshot with the per-router LATERAL loader, and
 *           one batched blocked-reachability read.
 */
import { drizzle } from "drizzle-orm/postgres-js";
import { sql } from "drizzle-orm";
import postgres from "postgres";
import { bench, describe, vi } from "vitest";

import * as schema from "@vectra/db";

vi.mock("~/server/db", () => ({ db: {} }));
vi.mock("server-only", () => ({}));

const mode = vi.hoisted(() => ({ original: false }));
vi.mock("./route-health-verifier", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./route-health-verifier")>();
  return {
    ...actual,
    loadLatestRouteVerifications: (
      ...args: Parameters<typeof actual.loadLatestRouteVerifications>
    ) =>
      mode.original
        ? actual.loadLatestRouteVerificationsBySelect(...args)
        : actual.loadLatestRouteVerifications(...args),
  };
});

const { detectBlockedReachabilityTriggers } = await import("./auto-rescue");
const {
  loadFleetMonitoringSnapshot,
  loadSharedFleetMonitoringSnapshot,
  resetSharedFleetMonitoringSnapshotForTest,
} = await import("./fleet-monitoring-data");
const { loadLatestRouteVerifications, loadLatestRouteVerificationsBySelect } =
  await import("./route-health-verifier");

const port = process.env.PANEL_PERF_PG_PORT;
const ROUTERS = 38;
const VERIFY_JOBS_PER_ROUTER = 100; // x2 results (receipt + verdict) = 7,600
const SNAPSHOTS_PER_ROUTER = 20;

const client = port
  ? postgres({ host: "127.0.0.1", port: Number(port), username: "panel_test", database: "postgres", max: 8 })
  : null;
const db = client ? drizzle(client, { schema }) : null;
const selectOnly = db ? ({ select: db.select.bind(db) } as never) : null;

/** Incompressible text, so TOAST stores what production stores. */
function noise(length: number) {
  let out = "";
  while (out.length < length) out += crypto.randomUUID();
  return out.slice(0, length);
}

async function seed() {
  if (!db) return [] as string[];
  await db.execute(sql`truncate vectra_router cascade`);
  const ids: string[] = [];
  const now = Date.now();
  for (let r = 0; r < ROUTERS; r += 1) {
    const [router] = await db
      .insert(schema.routers)
      .values({
        deviceIdentifier: `bench-${r}-${crypto.randomUUID()}`,
        status: "active",
        importState: "approved",
        approvedAt: new Date(now - 30 * 86_400_000),
        lastSeenAt: new Date(now - (r % 5) * 60_000),
      })
      .returning();
    ids.push(router!.id);
    const jobRows = Array.from({ length: VERIFY_JOBS_PER_ROUTER }, (_, j) => ({
      id: crypto.randomUUID(),
      routerId: router!.id,
      type: "verify_passwall_routes" as const,
      state: "succeeded" as const,
      createdAt: new Date(now - (VERIFY_JOBS_PER_ROUTER - j) * 3_600_000),
    }));
    await db.insert(schema.jobs).values(jobRows);
    const results = jobRows.flatMap((job, j) => [
      { jobId: job.id, routerId: router!.id, status: "accepted" as const, payload: { accepted: true }, reportedAt: new Date(job.createdAt.getTime() + 1_000) },
      {
        jobId: job.id,
        routerId: router!.id,
        status: "success" as const,
        // ~3.6 KB, the production verdict size (14 MB / 7,616 rows, half receipts).
        payload: {
          routeVerification: {
            slots: ["WorldProxy", "YouTube", "Special", "Tiktok", "DiscordVoiceUdp"].map((slot, s) => ({
              slot,
              boundNodeId: `node-${s}`,
              smokeOk: (j + s) % 7 !== 0,
              statusCode: (j + s) % 7 !== 0 ? 204 : 0,
              detail: noise(600),
            })),
          },
          log: noise(400),
        },
        reportedAt: new Date(job.createdAt.getTime() + 30_000),
      },
    ]);
    await db.insert(schema.jobResults).values(results);
    await db.insert(schema.routerInventorySnapshots).values(
      Array.from({ length: SNAPSHOTS_PER_ROUTER }, (_, n) => ({
        routerId: router!.id,
        payload: {
          hostname: `bench-${r}`,
          packageVersions: { "luci-app-passwall2": "26.8.10" },
          binaryVersions: { xray: "26.3.27" },
          foreignReachability: { status: r % 9 === 0 ? "blocked" : "healthy", reachable: r % 9 !== 0, checkedAt: `c${n}` },
          telegramReachability: { status: "healthy", reachable: true, checkedAt: `t${n}` },
          resources: { memAvailableMb: 80, overlayFreeMb: 20 },
          rawSnapshot: { blob: "z".repeat(6_000) },
        } as never,
        createdAt: new Date(now - (SNAPSHOTS_PER_ROUTER - n) * 60_000),
      })),
    );
  }
  // The rest of job_result (151 MB in production): other job types' results,
  // so the planner sees a table of production size.
  await db.execute(sql`
    insert into vectra_job (id, router_id, type, state, created_at)
    select 'other-' || g, (array[${sql.join(ids.map((id) => sql`${id}`), sql`, `)}])[1 + g % ${ids.length}],
      'refresh_subscriptions', 'succeeded', now() - (g || ' minutes')::interval
    from generate_series(1, 60000) g
  `);
  await db.execute(sql`
    insert into vectra_job_result (id, job_id, router_id, status, payload, reported_at)
    select j.id || '-' || k, j.id, j.router_id, 'success',
      jsonb_build_object('log', (select string_agg(md5(j.id || k || n), '') from generate_series(1, 16) n)),
      j.created_at
    from vectra_job j cross join generate_series(1, 4) k
    where j.type = 'refresh_subscriptions'
  `);
  await db.execute(sql`analyze`);
  return ids;
}

const routerIds = await seed();
const options = { iterations: 20, warmupIterations: 3, time: 0 };

describe.skipIf(!db)("loadLatestRouteVerifications (38 routers, 7,600 results)", () => {
  bench("original: every result into Node", async () => {
    await loadLatestRouteVerificationsBySelect(db!, routerIds);
  }, options);
  bench("round 2: one LATERAL row per router", async () => {
    await loadLatestRouteVerifications(db!, routerIds);
  }, options);
});

describe.skipIf(!db)("one monitor minute (auto-rescue + push monitor)", () => {
  bench("original: two parallel fleet snapshots + per-router blocked scan", async () => {
    mode.original = true;
    const now = new Date();
    await Promise.all([
      Promise.all([
        loadFleetMonitoringSnapshot(db!, now),
        detectBlockedReachabilityTriggers(selectOnly!, now),
      ]),
      loadFleetMonitoringSnapshot(db!, now),
    ]);
  }, options);
  bench("round 2: one shared snapshot + batched blocked scan", async () => {
    mode.original = false;
    resetSharedFleetMonitoringSnapshotForTest();
    const now = new Date();
    await Promise.all([
      loadSharedFleetMonitoringSnapshot(db!, now),
      detectBlockedReachabilityTriggers(db! as never, now),
    ]);
    // The push monitor, 15 s later, reuses it.
    await loadSharedFleetMonitoringSnapshot(db!, new Date(now.getTime() + 15_000));
  }, options);
});
