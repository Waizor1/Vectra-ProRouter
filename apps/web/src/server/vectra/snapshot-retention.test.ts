import type { SQL } from "drizzle-orm";
import { PgDialect } from "drizzle-orm/pg-core";
import { describe, expect, it } from "vitest";

import { runSnapshotRetentionTick } from "./snapshot-retention";

const dialect = new PgDialect();

// The tick issues one raw DELETE, so the useful assertions are on the SQL it
// builds and on the guard rails around it. The property that matters most is
// the per-router floor: an age-only rule would erase every snapshot of a
// router that has been offline for weeks, which is exactly when its last known
// state is most valuable.
function createRetentionMockDb() {
  const executed: { text: string; params: unknown[] }[] = [];
  return {
    db: {
      execute: (query: unknown) => {
        // Compile the sql`` template exactly as the driver would, so the
        // assertions below run against the real statement and its bound
        // parameters rather than a guess at drizzle's internals.
        const { sql: text, params } = dialect.sqlToQuery(query as SQL);
        executed.push({ text, params });
        return Promise.resolve({ count: 7 });
      },
    },
    executed,
  };
}

describe("runSnapshotRetentionTick", () => {
  it("is a no-op when disabled", async () => {
    const { db, executed } = createRetentionMockDb();

    const result = await runSnapshotRetentionTick(
      db as unknown as Parameters<typeof runSnapshotRetentionTick>[0],
      { enabled: false },
    );

    expect(result).toEqual({ enabled: false, deleted: 0 });
    expect(executed).toHaveLength(0);
  });

  it("reports the number of pruned rows", async () => {
    const { db } = createRetentionMockDb();

    const result = await runSnapshotRetentionTick(
      db as unknown as Parameters<typeof runSnapshotRetentionTick>[0],
      { enabled: true, retentionHours: 72, keepPerRouter: 20 },
    );

    expect(result).toEqual({ enabled: true, deleted: 7 });
  });

  it("keeps the newest N per router and only then applies the age cutoff", async () => {
    const { db, executed } = createRetentionMockDb();

    await runSnapshotRetentionTick(
      db as unknown as Parameters<typeof runSnapshotRetentionTick>[0],
      { enabled: true, retentionHours: 72, keepPerRouter: 20, maxPerTick: 500 },
    );

    const [statement] = executed;
    expect(statement).toBeDefined();
    // Partitioning by router is what protects a long-offline router from
    // losing its entire history to an age-only rule.
    expect(statement!.text).toContain("partition by router_id");
    expect(statement!.text).toContain("order by created_at desc");
    // Both conditions must be present; either alone is unsafe.
    expect(statement!.text).toContain("rn >");
    expect(statement!.text).toContain("created_at <");
    expect(statement!.params).toContain(20);
    expect(statement!.params).toContain(72);
    expect(statement!.params).toContain(500);
  });

  it("caps how many rows a single sweep removes", async () => {
    const { db, executed } = createRetentionMockDb();

    await runSnapshotRetentionTick(
      db as unknown as Parameters<typeof runSnapshotRetentionTick>[0],
      { enabled: true },
    );

    // Default cap keeps a large backlog from becoming one long transaction.
    expect(executed[0]!.params).toContain(50_000);
    expect(executed[0]!.text).toContain("limit");
  });
});
