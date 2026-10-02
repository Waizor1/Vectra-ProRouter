import { randomUUID } from "node:crypto";

/**
 * A scripted stand-in for the Drizzle query surface, for tests that drive a
 * multi-step server flow without Postgres.
 *
 * Reads are answered per TABLE from a queue (the n-th select on a table gets
 * the n-th scripted result, then []), so a flow's cross-table ordering does not
 * matter. Predicates are not evaluated — script what the query would return.
 * Writes are recorded; `returning()` echoes the written values (with an id) or
 * the next scripted result for that table.
 */

type Row = Record<string, unknown>;

export type FakeDbCall =
  | { kind: "select"; table: unknown }
  | { kind: "insert"; table: unknown; values: Row; conflict: boolean }
  | { kind: "update"; table: unknown; set: Row }
  | { kind: "delete"; table: unknown };

export type FakeDbScript = {
  selects?: Array<[unknown, Row[][]]>;
  insertReturns?: Array<[unknown, Row[][]]>;
  updateReturns?: Array<[unknown, Row[][]]>;
  /** Tables whose `onConflictDoNothing()` inserts hit a conflict. */
  insertConflicts?: unknown[];
};

// A `then` that makes a query builder awaitable, as Drizzle's are.
function thenOf<T>(value: () => T) {
  return function then<TResult1 = T, TResult2 = never>(
    onfulfilled?: ((result: T) => TResult1 | PromiseLike<TResult1>) | null,
    onrejected?: ((reason: unknown) => TResult2 | PromiseLike<TResult2>) | null,
  ) {
    return Promise.resolve()
      .then(value)
      .then(onfulfilled, onrejected);
  };
}

export function createFakeDb(script: FakeDbScript = {}) {
  const calls: FakeDbCall[] = [];
  let selects = new Map(script.selects ?? []);
  let insertReturns = new Map(script.insertReturns ?? []);
  let updateReturns = new Map(script.updateReturns ?? []);
  let insertConflicts = new Set(script.insertConflicts ?? []);

  const take = (queues: Map<unknown, Row[][]>, table: unknown) =>
    queues.get(table)?.shift();

  const db = {
    select() {
      return {
        from(table: unknown) {
          calls.push({ kind: "select", table });
          const rows = take(selects, table) ?? [];
          const chain = {
            where: () => chain,
            orderBy: () => chain,
            for: () => chain,
            limit: () => Promise.resolve(rows),
            then: thenOf(() => rows),
          };
          return chain;
        },
      };
    },
    insert(table: unknown) {
      return {
        values(values: Row) {
          let conflict = false;
          const record = () => {
            calls.push({ kind: "insert", table, values, conflict });
            if (conflict) {
              return [];
            }
            return (
              take(insertReturns, table) ?? [
                { id: randomUUID(), createdAt: new Date(), ...values },
              ]
            );
          };
          const statement = {
            onConflictDoNothing() {
              conflict = insertConflicts.has(table);
              return statement;
            },
            returning: () => Promise.resolve().then(record),
            then: thenOf(record),
          };
          return statement;
        },
      };
    },
    update(table: unknown) {
      return {
        set(set: Row) {
          calls.push({ kind: "update", table, set });
          const filtered = {
            returning: () =>
              Promise.resolve(take(updateReturns, table) ?? [set]),
            then: thenOf(() => []),
          };
          return { where: () => filtered };
        },
      };
    },
    delete(table: unknown) {
      calls.push({ kind: "delete", table });
      return { where: () => ({ then: thenOf(() => []) }) };
    },
    async transaction<T>(run: (tx: unknown) => Promise<T>) {
      return run(db);
    },
  };

  return {
    db,
    calls,
    /** Values of every insert into `table`, in order. */
    inserts: (table: unknown) =>
      calls.flatMap((call) =>
        call.kind === "insert" && call.table === table ? [call.values] : [],
      ),
    /** `set` of every update of `table`, in order. */
    updates: (table: unknown) =>
      calls.flatMap((call) =>
        call.kind === "update" && call.table === table ? [call.set] : [],
      ),
    deletes: (table: unknown) =>
      calls.filter((call) => call.kind === "delete" && call.table === table)
        .length,
    reset(next: FakeDbScript = {}) {
      calls.length = 0;
      selects = new Map(next.selects ?? []);
      insertReturns = new Map(next.insertReturns ?? []);
      updateReturns = new Map(next.updateReturns ?? []);
      insertConflicts = new Set(next.insertConflicts ?? []);
    },
  };
}

export type FakeDb = ReturnType<typeof createFakeDb>;
