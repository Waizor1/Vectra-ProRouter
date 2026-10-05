/**
 * A stand-in for the Drizzle query surface that answers every read from a
 * function of the query, so the same flow can run any number of times (a
 * benchmark loop, repeated check-ins) without re-scripting a queue the way
 * fake-db.ts needs.
 *
 * `params` holds the values bound in the WHERE clause (drizzle Params), which
 * is enough to pick rows by id; `fields` is the select projection, if any.
 * Writes are no-ops; an update of `routers` returns the router row from
 * `rowsFor` merged with what was set, as `.returning()` would.
 */
export type StaticDbQuery = {
  table: unknown;
  params: Set<unknown>;
  ordered: boolean;
  fields: Record<string, unknown> | undefined;
};

function paramValues(node: unknown, out: Set<unknown>, seen: WeakSet<object>) {
  if (!node || typeof node !== "object" || seen.has(node)) return out;
  seen.add(node);
  if (Array.isArray(node)) {
    for (const item of node) paramValues(item, out, seen);
    return out;
  }
  const record = node as Record<string, unknown>;
  if (record.constructor?.name === "Param") out.add(record.value);
  if (Array.isArray(record.queryChunks)) {
    for (const chunk of record.queryChunks) paramValues(chunk, out, seen);
  }
  return out;
}

export function createStaticDb(
  rowsFor: (query: StaticDbQuery) => unknown[],
  options: { routersTable?: unknown } = {},
) {
  const queries: StaticDbQuery[] = [];
  /** Every insert, in order (writes are otherwise no-ops). */
  const inserts: Array<{ table: unknown; values: unknown }> = [];
  // A `then` that makes a query builder awaitable, as Drizzle's are.
  const thenOf =
    (rows: () => unknown[]) =>
    (ok: (value: unknown[]) => unknown, err?: (error: unknown) => unknown) =>
      Promise.resolve().then(rows).then(ok, err);

  const db = {
    select(fields?: Record<string, unknown>) {
      return {
        from(table: unknown) {
          const query: StaticDbQuery = { table, params: new Set(), ordered: false, fields };
          const rows = () => {
            queries.push(query);
            return rowsFor(query);
          };
          const chain = {
            where(condition: unknown) {
              query.params = paramValues(condition, new Set(), new WeakSet());
              return chain;
            },
            orderBy() {
              query.ordered = true;
              return chain;
            },
            for: () => chain,
            limit: () => Promise.resolve().then(rows),
            then: thenOf(rows),
          };
          return chain;
        },
      };
    },
    insert(table?: unknown) {
      return {
        values(values: Record<string, unknown>) {
          inserts.push({ table, values });
          const statement = {
            onConflictDoNothing: () => statement,
            returning: () => Promise.resolve([values]),
            then: thenOf(() => []),
          };
          return statement;
        },
      };
    },
    update(table: unknown) {
      return {
        set(set: Record<string, unknown>) {
          return {
            where: (condition: unknown) => ({
              returning: () =>
                Promise.resolve(
                  table === options.routersTable
                    ? [
                        {
                          ...(rowsFor({
                            table,
                            params: paramValues(condition, new Set(), new WeakSet()),
                            ordered: false,
                            fields: undefined,
                          })[0] as object),
                          ...set,
                        },
                      ]
                    : [set],
                ),
              then: thenOf(() => []),
            }),
          };
        },
      };
    },
    delete: () => ({ where: () => Promise.resolve([]) }),
    transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
  };

  return { db, queries, inserts };
}
