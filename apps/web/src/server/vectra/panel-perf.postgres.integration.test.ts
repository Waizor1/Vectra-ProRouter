import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
vi.mock("~/server/db", () => ({ db: {} }));
import { drizzle } from "drizzle-orm/postgres-js";
import { sql } from "drizzle-orm";
import postgres from "postgres";

import { passwallDesiredConfigSchema, type PasswallDesiredConfig } from "@vectra/contracts";
import * as schema from "@vectra/db";

import {
  loadLatestFleetPolicyConfigRows,
  loadLatestFleetPolicyConfigSummaries,
  loadLatestSnapshots,
  resetFleetPolicyConfigSummaryCacheForTest,
} from "./fleet-monitoring-data";
import { runHistoryRetentionTick } from "./history-retention";
import { runRevisionRetentionTick } from "./revision-retention";
import {
  resetRevisionSummaryCacheForTest,
  resolveDesiredRevisionWithDb,
} from "./router-control";
import { createSecretPayload, sanitizePasswallConfig } from "./secrets";

/**
 * The raw SQL of the check-in revision cache, the monitors' compact config
 * loader and both retention sweeps, against a real PostgreSQL with every
 * migration applied. Runs only against a throwaway local cluster named by
 * PANEL_PERF_PG_PORT on 127.0.0.1 — never DATABASE_URL or .env:
 *
 *   initdb -D <dir> -U panel_test --auth=trust --no-locale -E UTF8
 *   pg_ctl -D <dir> -o "-k '' -p 55441 -c listen_addresses=127.0.0.1" start
 *   for f in packages/db/drizzle/*.sql; do psql -h 127.0.0.1 -p 55441 -U panel_test -d postgres -f $f; done
 *   PANEL_PERF_PG_PORT=55441 npx vitest run src/server/vectra/panel-perf.postgres.integration.test.ts
 */
const port = process.env.PANEL_PERF_PG_PORT;

const DAY = 24 * 60 * 60 * 1000;
const now = Date.now();
const daysAgo = (days: number) => new Date(now - days * DAY);

function config(label: string, password: string): PasswallDesiredConfig {
  return passwallDesiredConfigSchema.parse({
    basicSettings: { main: { mainSwitch: true, selectedNodeId: "myshunt" }, dns: {}, log: {}, maintenance: {} },
    nodes: [{ id: "node-a", label, protocol: "vless", address: "a.example", port: 443, password }],
    subscriptions: { items: [] },
    appUpdate: {},
    ruleManage: { geoipUrl: "https://example.test/geoip.dat", geositeUrl: "https://example.test/geosite.dat" },
  });
}

describe.skipIf(!port)("panel performance SQL on a real PostgreSQL", () => {
  const client = postgres({ host: "127.0.0.1", port: Number(port), username: "panel_test", database: "postgres", max: 4 });
  const db = drizzle(client, { schema });

  async function newRouter(label: string) {
    const [router] = await db
      .insert(schema.routers)
      .values({ deviceIdentifier: `perf-${label}-${crypto.randomUUID()}`, status: "active", importState: "approved", approvedAt: new Date() })
      .returning();
    return router!;
  }

  async function newRevision(
    routerId: string,
    revisionNumber: number,
    values: Partial<typeof schema.passwallDesiredRevisions.$inferInsert> & { secret?: PasswallDesiredConfig } = {},
  ) {
    const cfg = values.secret ?? config(`rev ${revisionNumber}`, "pw");
    const [revision] = await db
      .insert(schema.passwallDesiredRevisions)
      .values({
        routerId,
        revisionNumber,
        status: "approved",
        origin: "router_import",
        configDigest: `d${revisionNumber}`,
        config: sanitizePasswallConfig(cfg),
        rawImportedSnapshot: { big: "x".repeat(100) },
        ...values,
      })
      .returning();
    await db.insert(schema.passwallSecretBlobs).values({
      routerId,
      desiredRevisionId: revision!.id,
      scope: "router_import",
      ciphertext: createSecretPayload(cfg),
    });
    return revision!;
  }

  beforeAll(async () => {
    resetRevisionSummaryCacheForTest();
    resetFleetPolicyConfigSummaryCacheForTest();
    // A throwaway cluster: start every run from empty tables, since the
    // retention sweeps count fleet-wide.
    await db.execute(sql`truncate vectra_router cascade`);
  });
  afterAll(async () => {
    await client.end();
  });

  it("check-in revision cache: correlates each revision with its own newest secret blob", async () => {
    const router = await newRouter("cache");
    await newRevision(router.id, 1, { secret: config("one", "pw-1") });
    const active = await newRevision(router.id, 2, { secret: config("two", "pw-2"), origin: "operator_draft" });
    const routerRow = { ...router, activeRevisionId: active.id };

    const first = await resolveDesiredRevisionWithDb(db, routerRow, []);
    expect((first?.config as PasswallDesiredConfig).nodes[0]?.password).toBe("pw-2");
    expect(first?.impact.changedSections.length).toBeGreaterThan(0);

    // A re-imported secret is delete + insert of the blob: a new id, a new key.
    await db.delete(schema.passwallSecretBlobs).where(sql`desired_revision_id = ${active.id}`);
    await db.insert(schema.passwallSecretBlobs).values({
      routerId: router.id,
      desiredRevisionId: active.id,
      scope: "desired_revision",
      ciphertext: createSecretPayload(config("two", "pw-rotated")),
    });
    const second = await resolveDesiredRevisionWithDb(db, routerRow, []);
    expect((second?.config as PasswallDesiredConfig).nodes[0]?.password).toBe("pw-rotated");
  });

  it("monitor loaders: compact summaries match the full loader, snapshots drop telemetry in SQL", async () => {
    const router = await newRouter("monitor");
    await newRevision(router.id, 1, { createdAt: daysAgo(2) });
    const latest = await newRevision(router.id, 2, { createdAt: daysAgo(1), secret: config("latest", "pw") });
    await newRevision(router.id, 3, { origin: "operator_draft", createdAt: new Date() });

    const [summaries, full] = await Promise.all([
      loadLatestFleetPolicyConfigSummaries(db, [router.id]),
      loadLatestFleetPolicyConfigRows(db, [router.id]),
    ]);
    expect(summaries.get(router.id)?.id).toBe(latest.id);
    expect(full.get(router.id)?.id).toBe(latest.id);
    expect(summaries.get(router.id)?.config.nodes).toEqual(full.get(router.id)?.config.nodes);

    await db.insert(schema.routerInventorySnapshots).values({
      routerId: router.id,
      payload: { hostname: "h", connect: { entries: [] }, rawSnapshot: { a: 1 } } as never,
    });
    const slim = await loadLatestSnapshots(db, [router.id], { monitoringPayload: true });
    expect(slim.get(router.id)?.payload).toEqual({ hostname: "h" });
  });

  it("revision retention: deletes only unreferenced, old revisions beyond the newest N", async () => {
    const router = await newRouter("retention");
    const other = await newRouter("retention-other");
    const old = daysAgo(40);
    const revs: Record<string, string> = {};
    for (let n = 1; n <= 20; n += 1) {
      revs[n] = (await newRevision(router.id, n, { status: "import_review", createdAt: new Date(old.getTime() + n * 1000) })).id;
    }
    // Statuses that are never pruned.
    revs.draft = (await newRevision(router.id, 21, { origin: "operator_draft", status: "draft", createdAt: old })).id;
    revs.queued = (await newRevision(router.id, 22, { origin: "operator_draft", status: "queued", createdAt: old })).id;
    // Newest three (keep-N), recent.
    for (const n of [23, 24, 25]) {
      revs[n] = (await newRevision(router.id, n, { origin: "operator_draft", status: "failed", createdAt: daysAgo(1) })).id;
    }

    // References: active and last applied (each with its diff base: 4 and
    // 6), pending import, a finished job, an applied record, an onboarding
    // run, and an unfinished job's restore target.
    await db.update(schema.routers).set({ activeRevisionId: revs[5], lastAppliedRevisionId: revs[7], pendingImportRevisionId: revs[9] }).where(sql`id = ${router.id}`);
    await db.insert(schema.jobs).values({ routerId: router.id, type: "apply_passwall_config", state: "succeeded", desiredRevisionId: revs[11], createdAt: old });
    await db.insert(schema.jobs).values({ routerId: router.id, type: "refresh_subscriptions", state: "queued", payload: { restoreRevisionId: revs[13] } });
    await db.insert(schema.passwallAppliedRevisions).values({ routerId: router.id, desiredRevisionId: revs[15] });
    const [profile] = await db.insert(schema.routerOnboardingProfiles).values({ routerId: router.id }).returning();
    await db.insert(schema.routerOnboardingRuns).values({ routerId: router.id, profileId: profile!.id, activeRevisionId: revs[17] });
    // Another router's revision is not this router's keep-N.
    const otherOld = (await newRevision(other.id, 1, { status: "import_review", createdAt: old })).id;

    const dry = await runRevisionRetentionTick(db, { enabled: true, retentionHours: 168, keepPerRouter: 3, retentionDays: 30, dryRun: true });
    const kept = new Set([
      revs[4], revs[5], revs[6], revs[7], revs[9], revs[11], revs[13], revs[15], revs[17],
      // the latest router_import of the router: revision 20
      revs[20],
      revs.draft, revs.queued, revs[23], revs[24], revs[25],
      // the other router's only revision is its own newest
      otherOld,
    ]);
    const all = Object.values(revs).concat(otherOld);
    const expectedDeleted = all.filter((id) => !kept.has(id));
    expect(dry.deleted).toBe(0);
    expect(dry.wouldDelete).toBe(expectedDeleted.length);

    const result = await runRevisionRetentionTick(db, { enabled: true, retentionHours: 168, keepPerRouter: 3, retentionDays: 30, dryRun: false });
    expect(result.deleted).toBe(expectedDeleted.length);
    const remaining = new Set(
      (await db.select({ id: schema.passwallDesiredRevisions.id }).from(schema.passwallDesiredRevisions).where(sql`router_id in (${router.id}, ${other.id})`)).map((row) => row.id),
    );
    expect([...remaining].sort()).toEqual([...kept].sort());
    // Secret blobs went with their revisions.
    const orphanBlobs = await db.execute(sql`select count(*)::int as count from vectra_passwall_secret_blob where router_id = ${router.id} and desired_revision_id is not null and desired_revision_id not in (select id from vectra_passwall_desired_revision)`);
    expect((orphanBlobs as unknown as Array<{ count: number }>)[0]?.count).toBe(0);
    const blobCount = await db.execute(sql`select count(*)::int as count from vectra_passwall_secret_blob where router_id = ${router.id}`);
    expect((blobCount as unknown as Array<{ count: number }>)[0]?.count).toBe(kept.size - 1);
  });

  it("revision retention: skips a revision a job is being queued for right now", async () => {
    const router = await newRouter("retention-race");
    const old = daysAgo(40);
    const revisions: string[] = [];
    for (let n = 1; n <= 6; n += 1) {
      revisions.push((await newRevision(router.id, n, { status: "failed", origin: "operator_draft", createdAt: new Date(old.getTime() + n * 1000) })).id);
    }
    // Revisions 1-3 are past keep-3 and unreferenced. A second connection is
    // queueing an apply for revision 1 and has not committed yet.
    const other = postgres({ host: "127.0.0.1", port: Number(port), username: "panel_test", database: "postgres", max: 1 });
    try {
      const reserved = await other.reserve();
      await reserved`begin`;
      await reserved`insert into vectra_job (id, router_id, type, state, desired_revision_id) values (${crypto.randomUUID()}, ${router.id}, 'apply_passwall_config', 'queued', ${revisions[0]!})`;

      const result = await runRevisionRetentionTick(db, { enabled: true, retentionHours: 168, keepPerRouter: 3, retentionDays: 30, dryRun: false });
      await reserved`commit`;
      reserved.release();

      expect(result.deleted).toBe(2);
      const remaining = new Set(
        (await db.select({ id: schema.passwallDesiredRevisions.id }).from(schema.passwallDesiredRevisions).where(sql`router_id = ${router.id}`)).map((row) => row.id),
      );
      expect(remaining.has(revisions[0]!)).toBe(true);
      expect(remaining.has(revisions[1]!) || remaining.has(revisions[2]!)).toBe(false);
      const [queued] = (await db.execute(sql`select desired_revision_id from vectra_job where router_id = ${router.id}`)) as unknown as Array<{ desired_revision_id: string | null }>;
      expect(queued?.desired_revision_id).toBe(revisions[0]);
    } finally {
      await other.end();
    }
  });

  it("history retention: batches old journal, finished jobs (with results), resolved incidents and alerts", async () => {
    const router = await newRouter("history");
    const old = daysAgo(31);
    const recent = daysAgo(29);
    await db.insert(schema.eventLog).values([
      { routerId: router.id, type: "t", message: "old-1", createdAt: old },
      { routerId: router.id, type: "t", message: "old-2", createdAt: old },
      { routerId: router.id, type: "t", message: "old-3", createdAt: old },
      { routerId: router.id, type: "t", message: "recent", createdAt: recent },
    ]);
    const job = async (values: Partial<typeof schema.jobs.$inferInsert>, routerId = router.id) =>
      (await db.insert(schema.jobs).values({ routerId, type: "refresh_subscriptions", createdAt: old, ...values }).returning())[0]!;
    const at = (days: number, seconds = 0) => new Date(daysAgo(days).getTime() + seconds * 1000);
    const oldDone = await job({ state: "succeeded", completedAt: old, createdAt: at(40, 1) });
    const oldFailed = await job({ state: "failed", completedAt: old, createdAt: at(40, 2) });
    const oldKeyed = await job({ state: "succeeded", completedAt: old, createdAt: at(40, 3), dedupeKey: `partner-action:${crypto.randomUUID()}` });
    const oldRunning = await job({ state: "running", createdAt: at(40, 4) });
    const finishedRecently = await job({ state: "succeeded", completedAt: recent, createdAt: at(40, 5) });
    // The newest job of its type on this router stays, however old.
    const newestOfType = await job({ type: "verify_passwall_routes", state: "succeeded", completedAt: old, createdAt: at(45) });
    const olderOfThatType = await job({ type: "verify_passwall_routes", state: "succeeded", completedAt: old, createdAt: at(50) });
    // Still referenced by an onboarding run (cancelled: its dedupe key was cleared).
    const onboardingJob = await job({ type: "apply_passwall_config", state: "cancelled", completedAt: at(60), createdAt: at(60) });
    await job({ type: "apply_passwall_config", state: "succeeded", completedAt: at(1), createdAt: at(1) });
    const [profile] = await db.insert(schema.routerOnboardingProfiles).values({ routerId: router.id }).returning();
    await db.insert(schema.routerOnboardingRuns).values({ routerId: router.id, profileId: profile!.id, lastJobId: onboardingJob.id });

    // A router with an incident open for 40 days and an active rescue case
    // started 40 days ago: the reconnects and repairs the caps count stay.
    const capped = await newRouter("history-capped");
    await db.insert(schema.healthIncidents).values({ routerId: capped.id, type: "proxy_outage", state: "open", reason: "r", openedAt: at(40) });
    const beforeIncident = await job({ type: "reconnect", state: "succeeded", completedAt: at(41), createdAt: at(41) }, capped.id);
    const counted = await job({ type: "reconnect", state: "succeeded", completedAt: at(39), createdAt: at(39) }, capped.id);
    await job({ type: "reconnect", state: "failed", completedAt: at(35), createdAt: at(35) }, capped.id);
    const cased = await newRouter("history-case");
    await db.insert(schema.rescueCases).values({ routerId: cased.id, trigger: "direct_mode", state: "escalated", startedAt: at(38) });
    const repairBeforeCase = await job({ type: "run_rescue_repair", state: "failed", completedAt: at(39), createdAt: at(39) }, cased.id);
    const repairCounted = await job({ type: "run_rescue_repair", state: "failed", completedAt: at(37), createdAt: at(37) }, cased.id);
    await job({ type: "run_rescue_repair", state: "failed", completedAt: at(36), createdAt: at(36) }, cased.id);
    for (const parent of [oldDone, oldFailed, oldKeyed, oldRunning, finishedRecently]) {
      await db.insert(schema.jobResults).values([
        { jobId: parent.id, routerId: router.id, status: "accepted", reportedAt: old },
        { jobId: parent.id, routerId: router.id, status: "success", reportedAt: old },
      ]);
    }
    await db.insert(schema.healthIncidents).values([
      { routerId: router.id, type: "proxy_outage", state: "resolved", reason: "r", openedAt: old, resolvedAt: old },
      { routerId: router.id, type: "proxy_outage", state: "open", reason: "r", openedAt: old },
      { routerId: router.id, type: "proxy_outage", state: "resolved", reason: "r", openedAt: old, resolvedAt: recent },
    ]);
    await db.insert(schema.operatorPushAlerts).values([
      { routerId: router.id, kind: "offline", dedupeKey: `a-${crypto.randomUUID()}`, title: "t", body: "b", href: "/", createdAt: old, resolvedAt: old },
      { routerId: router.id, kind: "offline", dedupeKey: `b-${crypto.randomUUID()}`, title: "t", body: "b", href: "/", createdAt: old },
    ]);

    const options = { enabled: true, retentionDays: 30, batchSize: 2, pauseMs: 0, now: new Date(now) } as const;
    const dry = await runHistoryRetentionTick(db, { ...options, dryRun: true });
    expect(dry.counts.job).toBe(5);
    const before = await db.execute(sql`select count(*)::int as count from vectra_event_log where router_id = ${router.id}`);
    expect((before as unknown as Array<{ count: number }>)[0]?.count).toBe(4);

    const result = await runHistoryRetentionTick(db, { ...options, dryRun: false });
    expect(result.counts.event_log).toBeGreaterThanOrEqual(3);

    const rows = async (query: ReturnType<typeof sql>) => (await db.execute(query)) as unknown as Array<Record<string, unknown>>;
    expect((await rows(sql`select message from vectra_event_log where router_id = ${router.id}`)).map((row) => row.message)).toEqual(["recent"]);
    const deletedJobs = new Set(
      [oldDone, oldFailed, olderOfThatType, beforeIncident, repairBeforeCase].map((row) => row.id),
    );
    const remainingJobs = new Set(
      (await rows(sql`select id from vectra_job where router_id in (${router.id}, ${capped.id}, ${cased.id})`)).map((row) => row.id),
    );
    for (const kept of [oldKeyed, oldRunning, finishedRecently, newestOfType, onboardingJob, counted, repairCounted]) {
      expect(remainingJobs.has(kept.id)).toBe(true);
    }
    for (const id of deletedJobs) {
      expect(remainingJobs.has(id)).toBe(false);
    }
    expect(result.counts.job).toBe(deletedJobs.size);
    const [run] = await rows(sql`select last_job_id from vectra_router_onboarding_run where router_id = ${router.id}`);
    expect(run?.last_job_id).toBe(onboardingJob.id);
    const resultJobIds = new Set((await rows(sql`select job_id from vectra_job_result where router_id = ${router.id}`)).map((row) => row.job_id));
    expect([...resultJobIds].sort()).toEqual([oldKeyed.id, oldRunning.id, finishedRecently.id].sort());
    expect((await rows(sql`select state, resolved_at from vectra_health_incident where router_id = ${router.id}`)).length).toBe(2);
    expect((await rows(sql`select id from vectra_operator_push_alert where router_id = ${router.id} and resolved_at is null`)).length).toBe(1);
    expect((await rows(sql`select id from vectra_operator_push_alert where router_id = ${router.id}`)).length).toBe(1);
  });
});
