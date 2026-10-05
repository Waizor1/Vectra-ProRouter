import type { SQL } from "drizzle-orm";
import { PgDialect } from "drizzle-orm/pg-core";
import { describe, expect, it } from "vitest";

import { runRevisionRetentionTick } from "./revision-retention";

const dialect = new PgDialect();

// Like the snapshot tick, this issues one raw DELETE, so the assertions run
// against the compiled statement. What matters here is narrower than the
// snapshot case: this table holds the operator's audit trail, and deleting the
// wrong row destroys the record of what a router was actually told to run.
function createRetentionMockDb() {
  const executed: { text: string; params: unknown[] }[] = [];
  return {
    db: {
      execute: (query: unknown) => {
        const { sql: text, params } = dialect.sqlToQuery(query as SQL);
        executed.push({ text, params });
        // postgres.js: the rows, with the affected-row count on the array.
        return Promise.resolve(Object.assign([{ count: 4 }], { count: 4 }));
      },
    },
    executed,
  };
}

describe("runRevisionRetentionTick", () => {
  it("is a no-op when disabled", async () => {
    const { db, executed } = createRetentionMockDb();

    const result = await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: false },
    );

    expect(result).toEqual({ enabled: false, deleted: 0 });
    expect(executed).toHaveLength(0);
  });

  it("reports the number of pruned revisions", async () => {
    const { db } = createRetentionMockDb();

    const result = await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true },
    );

    expect(result).toEqual({ enabled: true, deleted: 4 });
  });

  it("never touches a revision that was actually applied to a router", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true },
    );

    // An applied revision is the record of what a router was running. Losing it
    // breaks rollback and leaves an applied-revision row pointing at nothing.
    expect(executed[0]!.text).toContain("vectra_passwall_applied_revision");
    expect(executed[0]!.text).toContain("not exists");
  });

  it("never touches a revision a router, a job or an onboarding run points at", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, retentionDays: null },
    );

    const text = executed[0]!.text;
    for (const reference of [
      "active_revision_id from vectra_router",
      "last_applied_revision_id from vectra_router",
      "pending_import_revision_id from vectra_router",
      "desired_revision_id from vectra_job",
      "payload->>'restoreRevisionId' from vectra_job",
      "active_revision_id from vectra_router_onboarding_run",
      // the route-policy fallback config and the check-in's diff base
      "distinct on (router_id) id",
      "prev.revision_number < cur.revision_number",
    ]) {
      expect(text).toContain(reference);
    }
  });

  it("locks what it deletes and skips a revision someone else holds", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, retentionDays: null },
    );

    expect(executed[0]!.text).toContain("for update of target skip locked");
  });

  it("keeps at least three revisions per router whatever is configured", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, keepPerRouter: 0, retentionDays: null },
    );

    expect(executed[0]!.params).toContain(3);
    expect(executed[0]!.params).not.toContain(0);
  });

  it("only counts what the 30-day rule would delete while dry-run is on", async () => {
    const { db, executed } = createRetentionMockDb();

    const result = await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, retentionDays: 30, dryRun: true },
    );

    expect(executed).toHaveLength(2);
    expect(executed[0]!.text.trimStart()).toMatch(/^delete/);
    expect(executed[1]!.text.trimStart()).toMatch(/^select count/);
    expect(executed[1]!.params).toEqual(expect.arrayContaining(["draft", "queued", 30]));
    expect(result.wouldDelete).toBeDefined();
  });

  it("deletes with the 30-day rule once dry-run is off, never an open draft or a queued apply", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, retentionDays: 30, dryRun: false },
    );

    const history = executed.find((statement) => statement.params.includes(30));
    expect(history?.text.trimStart()).toMatch(/^delete/);
    expect(history?.text).toContain("status not in");
    expect(history?.params).toEqual(expect.arrayContaining(["draft", "queued"]));
  });

  it("only prunes auto-generated imports, never operator work", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true },
    );

    // 95% of the table is router_import churn, and this rule removes only that
    // after a week. Anything else waits for the 30-day rule, and an applied
    // operator revision is protected by its applied record forever.
    expect(executed[0]!.params).toContain("router_import");
    expect(executed[0]!.params).toContain("approved");
  });

  it("keeps the newest N per router and only then applies the age cutoff", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true, retentionHours: 168, keepPerRouter: 20, maxPerTick: 500 },
    );

    const [statement] = executed;
    expect(statement!.text).toContain("partition by router_id");
    expect(statement!.text).toContain("order by created_at desc");
    expect(statement!.text).toContain("rn >");
    expect(statement!.text).toContain("created_at <");
    expect(statement!.params).toContain(20);
    expect(statement!.params).toContain(168);
    expect(statement!.params).toContain(500);
  });

  it("caps how many revisions a single sweep removes", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true },
    );

    expect(executed[0]!.text).toContain("limit");
  });
});
