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
        return Promise.resolve({ count: 4 });
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
    expect(executed[0]!.text).toContain("not in");
  });

  it("only prunes auto-generated imports, never operator work", async () => {
    const { db, executed } = createRetentionMockDb();

    await runRevisionRetentionTick(
      db as unknown as Parameters<typeof runRevisionRetentionTick>[0],
      { enabled: true },
    );

    // 95% of the table is router_import churn; operator_draft rows are the
    // things a human actually authored and must survive regardless of age.
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
