import type { SQL } from "drizzle-orm";
import { PgDialect } from "drizzle-orm/pg-core";
import { describe, expect, it } from "vitest";

import { runHistoryRetentionTick } from "./history-retention";

const dialect = new PgDialect();

// The statements are checked compiled; panel-perf.postgres.integration.test.ts
// runs them against a real PostgreSQL.
function createMockDb(affected: number) {
  const executed: { text: string; params: unknown[] }[] = [];
  return {
    db: {
      execute: (query: unknown) => {
        const { sql: text, params } = dialect.sqlToQuery(query as SQL);
        executed.push({ text, params });
        return Promise.resolve(
          Object.assign([{ count: 7 }], { count: text.trimStart().startsWith("delete") ? affected : 1 }),
        );
      },
    },
    executed,
  };
}

const NOW = new Date("2026-10-05T12:00:00.000Z");

describe("runHistoryRetentionTick", () => {
  it("is a no-op when disabled", async () => {
    const { db, executed } = createMockDb(0);
    const result = await runHistoryRetentionTick(db as never, { enabled: false, dryRun: false });
    expect(result.enabled).toBe(false);
    expect(executed).toHaveLength(0);
  });

  it("only counts in dry-run, with a cutoff of exactly the retention window", async () => {
    const { db, executed } = createMockDb(0);
    const result = await runHistoryRetentionTick(db as never, {
      enabled: true,
      dryRun: true,
      retentionDays: 30,
      now: NOW,
    });
    expect(executed.every((statement) => statement.text.trimStart().startsWith("select count"))).toBe(true);
    expect(executed[0]!.params).toContain("2026-09-05T12:00:00.000Z");
    expect(result).toMatchObject({ dryRun: true, counts: { event_log: 7, job: 7, health_incident: 7, operator_push_alert: 7 }, jobResults: 7 });
  });

  it("deletes only finished, unkeyed jobs, resolved incidents and resolved alerts", async () => {
    const { db, executed } = createMockDb(0);
    await runHistoryRetentionTick(db as never, { enabled: true, dryRun: false, now: NOW, retentionDays: 30, batchSize: 100, pauseMs: 0 });
    const deletes = executed.filter((statement) => statement.text.trimStart().startsWith("delete"));
    expect(deletes.map((statement) => /delete from (\w+)/.exec(statement.text)?.[1])).toEqual([
      "vectra_event_log",
      "vectra_job",
      "vectra_health_incident",
      "vectra_operator_push_alert",
    ]);
    expect(deletes[1]!.text).toContain("state in ('succeeded', 'failed', 'cancelled')");
    expect(deletes[1]!.text).toContain("dedupe_key is null");
    // Rows an attempt cap still counts, an onboarding run's last job, and the
    // newest job of each type per router are kept.
    expect(deletes[1]!.text).toContain("i.state = 'open'");
    expect(deletes[1]!.text).toContain("j.created_at >= i.opened_at");
    expect(deletes[1]!.text).toContain("j.created_at >= c.started_at");
    expect(deletes[1]!.text).toContain("r.last_job_id = j.id");
    expect(deletes[1]!.text).toContain("newer.created_at > j.created_at");
    expect(deletes[2]!.text).toContain("state = 'resolved'");
    expect(deletes[3]!.text).toContain("resolved_at is not null");
  });

  it("works in batches and stops at the per-tick cap", async () => {
    const { db, executed } = createMockDb(50);
    const result = await runHistoryRetentionTick(db as never, {
      enabled: true,
      dryRun: false,
      now: NOW,
      retentionDays: 30,
      batchSize: 50,
      maxBatchesPerTable: 3,
      pauseMs: 0,
    });
    const eventLogDeletes = executed.filter((statement) => statement.text.includes("delete from vectra_event_log"));
    expect(eventLogDeletes).toHaveLength(3);
    expect(eventLogDeletes[0]!.params).toContain(50);
    expect(result.counts.event_log).toBe(150);
  });

  it("never asks for a batch above 10k rows", async () => {
    const { db, executed } = createMockDb(0);
    await runHistoryRetentionTick(db as never, { enabled: true, dryRun: false, now: NOW, retentionDays: 30, batchSize: 50_000, pauseMs: 0 });
    const first = executed.find((statement) => statement.text.includes("delete from vectra_event_log"));
    expect(first?.params).toContain(10_000);
  });

  it("counts but keeps rescue cases until their own switch is off too", async () => {
    const kept = createMockDb(0);
    const keptResult = await runHistoryRetentionTick(kept.db as never, { enabled: true, dryRun: false, now: NOW, retentionDays: 30, pauseMs: 0 });
    expect(keptResult.rescueCaseDryRun).toBe(true);
    expect(kept.executed.some((statement) => statement.text.includes("delete from vectra_rescue_case"))).toBe(false);
    expect(kept.executed.some((statement) => statement.text.includes("from vectra_rescue_case"))).toBe(true);
    expect(keptResult.counts.rescue_case).toBe(7);

    // The global dry-run wins over the table's own switch.
    const dry = await runHistoryRetentionTick(createMockDb(0).db as never, { enabled: true, dryRun: true, rescueCaseDryRun: false, now: NOW, retentionDays: 30 });
    expect(dry.rescueCaseDryRun).toBe(true);

    const pruned = createMockDb(0);
    await runHistoryRetentionTick(pruned.db as never, { enabled: true, dryRun: false, rescueCaseDryRun: false, now: NOW, retentionDays: 30, pauseMs: 0 });
    const deletes = pruned.executed.filter((statement) => statement.text.trimStart().startsWith("delete"));
    const caseDelete = deletes.find((statement) => statement.text.includes("delete from vectra_rescue_case"));
    expect(caseDelete?.text).toContain("c.state = 'resolved'");
    expect(caseDelete?.text).toContain("partition by router_id order by started_at desc");
    expect(caseDelete?.params).toContain(50);
    expect(caseDelete?.text).toContain("j.payload ->> 'caseId' = c.id");
    expect(caseDelete?.text).toContain("'auto_rescue_repair:' || c.id");
  });
});
