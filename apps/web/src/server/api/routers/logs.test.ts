import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

const { notifyVendorAccessWithDb } = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
}));
vi.mock("~/server/vectra/vendor-access", () => ({ notifyVendorAccessWithDb }));

import { logsRouter } from "./logs";

const ROUTER_ID = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf";
const partnerRouter = { id: ROUTER_ID, ownerRef: "bc_1", partnerId: "bloopcat" };

// A database answering selects in order: the router, then the active-job check.
function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;
  const inserted: unknown[] = [];
  const chain = () => ({
    from() {
      return this;
    },
    where() {
      return this;
    },
    orderBy() {
      return this;
    },
    limit() {
      return Promise.resolve(selectResponses[selectIndex++] ?? []);
    },
  });
  return {
    db: {
      select: chain,
      insert() {
        return {
          values(value: Record<string, unknown>) {
            inserted.push(value);
            return { returning: () => Promise.resolve([{ id: "job-1", ...value }]) };
          },
        };
      },
    },
    inserted,
  };
}

function caller(db: unknown) {
  return createCallerFactory(logsRouter)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

describe("logs.queueSnapshot and the partner's vendor-access notice", () => {
  beforeEach(() => {
    notifyVendorAccessWithDb.mockClear();
  });

  it("tells the router's partner when a log collection is queued", async () => {
    const mock = createMockDb([[partnerRouter], []]);

    await caller(mock.db).queueSnapshot({ routerId: ROUTER_ID });

    expect(mock.inserted).toHaveLength(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledTimes(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledWith(mock.db, partnerRouter, {
      kind: "diagnostics",
      by: "operator",
    });
  });

  it("says nothing when a collection is already running", async () => {
    const mock = createMockDb([[partnerRouter], [{ id: "job-0", state: "running" }]]);

    await caller(mock.db).queueSnapshot({ routerId: ROUTER_ID });

    expect(mock.inserted).toHaveLength(0);
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });

  it("says nothing for a router that does not exist", async () => {
    const mock = createMockDb([[]]);

    await expect(caller(mock.db).queueSnapshot({ routerId: ROUTER_ID })).rejects.toMatchObject({
      code: "NOT_FOUND",
    });
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });
});
