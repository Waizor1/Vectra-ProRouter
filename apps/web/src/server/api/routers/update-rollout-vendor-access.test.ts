import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

// The rollout executors have their own tests; here they only report which
// routers they queued, and the procedure's announcement is what is checked
// (with the real notice, so Vectra's own and unclaimed routers fall silent).
const { enqueueWebhook, globalRollout, groupRollout } = vi.hoisted(() => ({
  enqueueWebhook: vi.fn(async (..._args: unknown[]): Promise<string | null> => "w1"),
  globalRollout: vi.fn(),
  groupRollout: vi.fn(),
}));
vi.mock("~/server/vectra/partner-webhooks", () => ({
  enqueuePartnerWebhookWithDb: enqueueWebhook,
  schedulePartnerWebhookDelivery: vi.fn(),
}));
vi.mock("~/server/vectra/global-template", async (importOriginal) => ({
  ...(await importOriginal<typeof import("~/server/vectra/global-template")>()),
  executeGlobalTemplateRollout: globalRollout,
}));
vi.mock("~/server/vectra/rollout-control", async (importOriginal) => ({
  ...(await importOriginal<typeof import("~/server/vectra/rollout-control")>()),
  queueGroupProfileRollout: groupRollout,
}));

import { updateRouter } from "./update";

const VECTRA_OWN = "11111111-1111-4111-8111-111111111111";
const BLOOP_QUEUED = "22222222-2222-4222-8222-222222222222";
const BLOOP_BLOCKED = "33333333-3333-4333-8333-333333333333";
const UNCLAIMED = "44444444-4444-4444-8444-444444444444";
const GROUP_ID = "55555555-5555-4555-8555-555555555555";

const routerRows = [
  { id: VECTRA_OWN, ownerRef: "vc_1", partnerId: null },
  { id: BLOOP_QUEUED, ownerRef: "bc_1", partnerId: "bloopcat" },
  { id: BLOOP_BLOCKED, ownerRef: "bc_2", partnerId: "bloopcat" },
  { id: UNCLAIMED, ownerRef: null, partnerId: "bloopcat" },
];

const queueApplyResults = [
  { routerId: VECTRA_OWN, status: "queued" },
  { routerId: BLOOP_QUEUED, status: "queued" },
  { routerId: BLOOP_BLOCKED, status: "blocked" },
  { routerId: UNCLAIMED, status: "queued" },
];

// The database does not evaluate WHERE, so it returns every router it has: the
// announcement itself must keep to the routers the rollout queued.
function createDb() {
  const select = vi.fn(() => ({
    from: () => ({ where: () => Promise.resolve(routerRows) }),
  }));
  return { db: { select }, select };
}

function caller(db: unknown) {
  return createCallerFactory(updateRouter)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

const visits = () =>
  enqueueWebhook.mock.calls.map(([, input]) => {
    const { routerId, partnerId, detail } = input as {
      routerId: string;
      partnerId: string;
      detail: Record<string, unknown>;
    };
    return { routerId, partnerId, ...detail };
  });

describe("rollouts and the partner's vendor-access notice", () => {
  beforeEach(() => {
    enqueueWebhook.mockClear();
    globalRollout.mockReset();
    groupRollout.mockReset();
  });

  it("a global template rollout tells only the partner routers it queued", async () => {
    globalRollout.mockResolvedValue({ ok: true, results: queueApplyResults });
    const { db } = createDb();

    await caller(db).queueGlobalTemplateRollout({
      routerIds: [VECTRA_OWN, BLOOP_QUEUED, BLOOP_BLOCKED, UNCLAIMED],
      mode: "queue_apply",
    });

    expect(visits()).toEqual([
      {
        routerId: BLOOP_QUEUED,
        partnerId: "bloopcat",
        kind: "config_apply",
        by: "operator",
      },
    ]);
  });

  it("a group profile rollout tells only the partner routers it queued", async () => {
    groupRollout.mockResolvedValue({ ok: true, results: queueApplyResults });
    const { db } = createDb();

    await caller(db).queueGroupProfileRollout({ groupId: GROUP_ID, mode: "queue_apply" });

    expect(visits()).toEqual([
      {
        routerId: BLOOP_QUEUED,
        partnerId: "bloopcat",
        kind: "config_apply",
        by: "operator",
      },
    ]);
  });

  it("drafts prepared but not applied are no visit", async () => {
    globalRollout.mockResolvedValue({
      ok: true,
      results: queueApplyResults.map((result) => ({ ...result, status: "prepared" })),
    });
    const { db, select } = createDb();

    await caller(db).queueGlobalTemplateRollout({
      routerIds: [BLOOP_QUEUED],
      mode: "draft_only",
    });

    expect(select).not.toHaveBeenCalled();
    expect(visits()).toEqual([]);
  });

  it("returns the rollout's own answer untouched", async () => {
    const outcome = { ok: true, results: queueApplyResults, summary: { queuedCount: 3 } };
    globalRollout.mockResolvedValue(outcome);
    const { db } = createDb();

    await expect(
      caller(db).queueGlobalTemplateRollout({
        routerIds: [BLOOP_QUEUED],
        mode: "queue_apply",
      }),
    ).resolves.toBe(outcome);
  });
});
