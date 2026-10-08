import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

// normalizeRoutePolicy's own logic is covered where it lives (fleet-route-policy
// and the revision helpers); this pins only what the procedure does with a job
// it queued: tell the router's partner, and only for an apply.
const { notifyVendorAccessWithDb, queueApply } = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
  queueApply: vi.fn(async (..._args: unknown[]) => ({ id: "job-1" }) as { id: string } | null),
}));
vi.mock("~/server/vectra/vendor-access", () => ({ notifyVendorAccessWithDb }));

const ROUTER_ID = "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9";
const router = {
  id: ROUTER_ID,
  deviceIdentifier: "router-test-policy-1",
  displayName: "Bloop customer",
  hostname: "openwrt-host",
  boardName: "xiaomi,mi-router-ax3000t",
  target: "mediatek/filogic",
  architecture: "aarch64_cortex-a53",
  openwrtRelease: "24.10.6",
  ownerRef: "bc_1",
  partnerId: "bloopcat",
};

vi.mock("~/server/vectra/fleet-monitoring-data", async (importOriginal) => ({
  ...(await importOriginal<typeof import("~/server/vectra/fleet-monitoring-data")>()),
  loadLatestSnapshots: async () =>
    new Map([
      [
        ROUTER_ID,
        {
          payload: {
            boardName: "xiaomi,mi-router-ax3000t",
            layoutFamily: "ubootmod",
            target: "mediatek/filogic",
            architecture: "aarch64_cortex-a53",
            openwrtRelease: "24.10.6",
            hostname: "openwrt-host",
          },
        },
      ],
    ]),
  loadLatestFleetPolicyConfigRows: async () =>
    new Map([[ROUTER_ID, { id: "rev-live", config: {} }]]),
}));
vi.mock("~/server/vectra/fleet-node-health-cache", () => ({
  fleetRoutePolicyOptions: async () => ({}),
}));
vi.mock("~/server/vectra/fleet-route-policy", async (importOriginal) => ({
  ...(await importOriginal<typeof import("~/server/vectra/fleet-route-policy")>()),
  buildFleetRoutePolicyIdentity: () => ({}),
  normalizeFleetRoutePolicy: () => ({
    policyVersion: "test",
    changed: true,
    changes: [],
    config: {},
    before: { status: "drifted" },
    after: { status: "compliant" },
  }),
}));
vi.mock("~/server/vectra/router-control", async (importOriginal) => ({
  ...(await importOriginal<typeof import("~/server/vectra/router-control")>()),
  getFullConfigForRevisionWithDb: async () => ({}),
  createOperatorDraftRevisionWithDb: async () => ({ id: "rev-draft", config: {} }),
  queueDesiredRevisionApplyJobWithDb: queueApply,
  sanitizeRevisionForClient: (revision: unknown) => revision,
}));

import { fleetRouter } from "./fleet";

function createDb() {
  const inserted: unknown[] = [];
  return {
    db: {
      select: () => ({ from: () => ({ where: () => Promise.resolve([router]) }) }),
      insert: () => ({
        values: (value: unknown) => {
          inserted.push(value);
          return Promise.resolve([]);
        },
      }),
    },
    inserted,
  };
}

function caller(db: unknown) {
  return createCallerFactory(fleetRouter)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

describe("fleet.normalizeRoutePolicy and the partner's vendor-access notice", () => {
  beforeEach(() => {
    notifyVendorAccessWithDb.mockClear();
    queueApply.mockClear();
  });

  it("tells the router's partner when the apply is queued", async () => {
    const { db } = createDb();

    const { results } = await caller(db).normalizeRoutePolicy({
      routerIds: [ROUTER_ID],
      mode: "queue_apply",
    });

    expect(results[0]).toMatchObject({ status: "queued_apply" });
    expect(notifyVendorAccessWithDb).toHaveBeenCalledTimes(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledWith(db, router, {
      kind: "config_apply",
      by: "operator",
    });
  });

  it.each(["dry_run", "draft"] as const)("says nothing for %s, which queues no apply", async (mode) => {
    const { db } = createDb();

    await caller(db).normalizeRoutePolicy({ routerIds: [ROUTER_ID], mode });

    expect(queueApply).not.toHaveBeenCalled();
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });
});
