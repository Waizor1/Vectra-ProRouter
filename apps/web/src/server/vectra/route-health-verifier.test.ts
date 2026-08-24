import { describe, expect, it } from "vitest";

import {
  routeVerificationToHealthSample,
  selectRoutersForRouteHealthCheck,
  type RouteHealthCandidate,
} from "./route-health-verifier";

const NOW = new Date("2026-08-24T18:00:00.000Z");

function candidate(
  overrides: Partial<RouteHealthCandidate> & { routerId: string },
): RouteHealthCandidate {
  return {
    status: "active",
    importState: "approved",
    lastSeenAt: new Date("2026-08-24T17:59:00.000Z"),
    lastVerifiedAt: null,
    queuedJobCount: 0,
    routePolicyExempt: false,
    ...overrides,
  };
}

describe("selectRoutersForRouteHealthCheck", () => {
  it("prefers the router that has gone longest without a check", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({
          routerId: "recent",
          lastVerifiedAt: new Date("2026-08-24T17:00:00.000Z"),
        }),
        candidate({
          routerId: "old",
          lastVerifiedAt: new Date("2026-08-20T00:00:00.000Z"),
        }),
        candidate({ routerId: "never", lastVerifiedAt: null }),
      ],
      NOW,
      { limit: 2, staleAfterMs: 6 * 60 * 60 * 1000 },
    );

    // Never-checked first, then the oldest. "recent" is still inside its TTL.
    expect(picked).toEqual(["never", "old"]);
  });

  it("respects the limit so a tick cannot flood the fleet with jobs", () => {
    const picked = selectRoutersForRouteHealthCheck(
      ["a", "b", "c", "d"].map((routerId) => candidate({ routerId })),
      NOW,
      { limit: 2, staleAfterMs: 1000 },
    );

    expect(picked).toHaveLength(2);
  });

  // Terminal jobs dedupe per router, so queuing on top of pending operator
  // work replaces it — the payload of the existing row is overwritten and the
  // operator's command silently never runs. Never queue over a busy router.
  it("skips a router that already has work queued", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [candidate({ routerId: "busy", queuedJobCount: 1 })],
      NOW,
      { limit: 5, staleAfterMs: 1000 },
    );

    expect(picked).toEqual([]);
  });

  it("skips routers that are not currently reachable", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({
          routerId: "offline",
          lastSeenAt: new Date("2026-08-24T10:00:00.000Z"),
        }),
        candidate({ routerId: "no-contact", lastSeenAt: null }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 1000 },
    );

    expect(picked).toEqual([]);
  });

  it("skips routers that are not approved or not in proxy mode", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({ routerId: "pending", importState: "awaiting_import" }),
        candidate({ routerId: "rescued", status: "direct" }),
        candidate({ routerId: "exempt", routePolicyExempt: true }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 1000 },
    );

    expect(picked).toEqual([]);
  });
});

describe("routeVerificationToHealthSample", () => {
  const nodes = [
    { id: "node-nl", address: "nl3.nfnpx.online" },
    { id: "node-pl", address: "pl2.nfnpx.online" },
  ];

  // The whole point of this lane: Special and Tiktok have no destination
  // probe, so a dead Netherlands or Belarus host was invisible fleet-wide.
  // url_test_node reports per NODE, which covers every slot uniformly.
  it("turns per-slot smoke results into per-host observations", () => {
    const sample = routeVerificationToHealthSample("r1", nodes, {
      slots: [
        { slotId: "Special", boundNodeId: "node-nl", smokeOk: false },
        { slotId: "WorldProxy", boundNodeId: "node-pl", smokeOk: true },
      ],
    });

    expect(sample).toEqual({
      routerId: "r1",
      observations: [
        { host: "nl3.nfnpx.online", outcome: "fail" },
        { host: "pl2.nfnpx.online", outcome: "ok" },
      ],
    });
  });

  it("ignores slots whose node is not in the config", () => {
    const sample = routeVerificationToHealthSample("r1", nodes, {
      slots: [{ slotId: "Special", boundNodeId: "rotated-away", smokeOk: false }],
    });

    expect(sample).toBeNull();
  });

  it("ignores a slot with no binding at all", () => {
    const sample = routeVerificationToHealthSample("r1", nodes, {
      slots: [{ slotId: "Special", smokeOk: false }],
    });

    expect(sample).toBeNull();
  });

  it("has nothing to say about an exempt router", () => {
    const sample = routeVerificationToHealthSample("r1", nodes, {
      exempt: true,
      slots: [{ slotId: "Special", boundNodeId: "node-nl", smokeOk: false }],
    });

    expect(sample).toBeNull();
  });
});
