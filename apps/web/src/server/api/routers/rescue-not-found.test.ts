import { TRPCError } from "@trpc/server";
import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { rescueRouter } from "./rescue";

/** A database in which no rescue case exists any more (retention deleted it). */
function emptyDb() {
  const chain = {
    from: () => chain,
    where: () => chain,
    orderBy: () => chain,
    for: () => chain,
    limit: () => Promise.resolve([]),
    then: (ok: (rows: unknown[]) => unknown) => Promise.resolve([]).then(ok),
  };
  return {
    select: () => chain,
    update: () => ({
      set: () => ({
        where: () => Object.assign(Promise.resolve([]), { returning: () => Promise.resolve([]) }),
      }),
    }),
    insert: () => ({ values: () => ({ returning: () => Promise.resolve([]) }) }),
  };
}

const caller = createCallerFactory(rescueRouter)({
  db: emptyDb() as never,
  operatorSession: { subject: "operator" } as never,
  headers: new Headers(),
});

const DELETED_CASE = "5f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5";

async function codeOf(call: Promise<unknown>) {
  try {
    await call;
  } catch (error) {
    return error instanceof TRPCError ? error.code : `untyped: ${String(error)}`;
  }
  return "resolved";
}

describe("rescue procedures on a deleted case", () => {
  it("answer NOT_FOUND, never an internal error", async () => {
    expect(await codeOf(caller.caseById({ caseId: DELETED_CASE }))).toBe("NOT_FOUND");
    expect(await codeOf(caller.runCaseSafeRepair({ caseId: DELETED_CASE }))).toBe("NOT_FOUND");
    expect(await codeOf(caller.reconnectCase({ caseId: DELETED_CASE }))).toBe("NOT_FOUND");
    expect(await codeOf(caller.collectCaseLogs({ caseId: DELETED_CASE }))).toBe("NOT_FOUND");
    expect(await codeOf(caller.silenceCase({ caseId: DELETED_CASE }))).toBe("NOT_FOUND");
  });
});
