import { describe, expect, it } from "vitest";

import {
  routeVerificationToHealthSample,
  selectRoutersForRouteHealthCheck,
  selectRoutersForSubscriptionRescue,
  subscriptionHasHardwareId,
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

  it("skips routers that are not approved, exempt, or in an unprobeable state", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({ routerId: "pending", importState: "awaiting_import" }),
        candidate({ routerId: "disabled", status: "disabled" }),
        candidate({ routerId: "rescuing", status: "rescue" }),
        candidate({ routerId: "exempt", routePolicyExempt: true }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 1000 },
    );

    expect(picked).toEqual([]);
  });

  // A router parked in direct is the one that most needs a verdict: the
  // auto-rescue unpark will not touch it without recent proof that its nodes
  // still answer, and nothing else in the system produces that proof. Skipping
  // it here is what left DmitryGubenko in direct for 33 hours with four live
  // nodes.
  it("still probes a router parked in direct mode", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [candidate({ routerId: "parked", status: "direct" })],
      NOW,
      { limit: 5, staleAfterMs: 1000 },
    );

    expect(picked).toEqual(["parked"]);
  });

  it("holds a parked router to the same guards as an active one", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({
          routerId: "parked-offline",
          status: "direct",
          lastSeenAt: null,
        }),
        candidate({
          routerId: "parked-busy",
          status: "direct",
          queuedJobCount: 1,
        }),
        candidate({
          routerId: "parked-disturbed",
          status: "direct",
          lastDisruptionAt: new Date("2026-08-24T17:55:00.000Z"),
        }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 6 * 60 * 60 * 1000 },
    );

    expect(picked).toEqual([]);
  });

  // The fleet TTL is tuned for routine telemetry: two routers a tick means each
  // one is judged about every four hours. A parked router waiting that long is
  // a customer offline that long, because the verdict is what unparks it.
  it("re-probes a parked router without waiting out the fleet TTL", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({
          routerId: "parked",
          status: "direct",
          lastVerifiedAt: new Date("2026-08-24T17:30:00.000Z"),
        }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 6 * 60 * 60 * 1000 },
    );

    expect(picked).toEqual(["parked"]);
  });

  it("does not re-probe a parked router it just probed", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({
          routerId: "parked",
          status: "direct",
          lastVerifiedAt: new Date("2026-08-24T17:58:00.000Z"),
        }),
      ],
      NOW,
      { limit: 5, staleAfterMs: 6 * 60 * 60 * 1000 },
    );

    expect(picked).toEqual([]);
  });

  it("spends the tick budget on parked routers before routine telemetry", () => {
    const picked = selectRoutersForRouteHealthCheck(
      [
        candidate({ routerId: "never-checked", lastVerifiedAt: null }),
        candidate({
          routerId: "parked",
          status: "direct",
          lastVerifiedAt: new Date("2026-08-24T17:30:00.000Z"),
        }),
      ],
      NOW,
      { limit: 1, staleAfterMs: 6 * 60 * 60 * 1000 },
    );

    expect(picked).toEqual(["parked"]);
  });
});

describe("routeVerificationToHealthSample", () => {
  const nodes = [
    { id: "node-nl", address: "nl3.nfnpx.online", port: 443 },
    { id: "node-pl", address: "pl2.nfnpx.online", port: 443 },
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
        { host: "nl3.nfnpx.online:443", outcome: "fail" },
        { host: "pl2.nfnpx.online:443", outcome: "ok" },
      ],
    });
  });

  it("ignores slots whose node is not in the config", () => {
    const sample = routeVerificationToHealthSample("r1", nodes, {
      slots: [
        { slotId: "Special", boundNodeId: "rotated-away", smokeOk: false },
      ],
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

describe("selectRoutersForSubscriptionRescue", () => {
  const NOW2 = new Date("2026-08-24T18:00:00.000Z");
  function rescue(
    overrides: Partial<
      import("./route-health-verifier").SubscriptionRescueCandidate
    > & { routerId: string },
  ) {
    return {
      strandedSlots: ["Special"],
      hwidPresent: true,
      lastRefreshAt: null,
      queuedJobCount: 0,
      ...overrides,
    };
  }

  it("refreshes when a slot has no live node left to move to", () => {
    expect(
      selectRoutersForSubscriptionRescue([rescue({ routerId: "a" })], NOW2, {
        limit: 5,
        cooldownMs: 1000,
      }),
    ).toEqual(["a"]);
  });

  it("leaves alone a router that still has somewhere live to go", () => {
    expect(
      selectRoutersForSubscriptionRescue(
        [rescue({ routerId: "a", strandedSlots: [] })],
        NOW2,
        { limit: 5, cooldownMs: 1000 },
      ),
    ).toEqual([]);
  });

  // Without the hardware id the provider returns a stub and PassWall wipes the
  // node list. Refreshing there is not a repair, it is the outage.
  it("never refreshes a subscription that carries no hardware id", () => {
    expect(
      selectRoutersForSubscriptionRescue(
        [rescue({ routerId: "a", hwidPresent: false })],
        NOW2,
        { limit: 5, cooldownMs: 1000 },
      ),
    ).toEqual([]);
  });

  it("holds off while a previous refresh is still within its cooldown", () => {
    expect(
      selectRoutersForSubscriptionRescue(
        [
          rescue({
            routerId: "a",
            lastRefreshAt: new Date("2026-08-24T17:30:00.000Z"),
          }),
        ],
        NOW2,
        { limit: 5, cooldownMs: 6 * 60 * 60 * 1000 },
      ),
    ).toEqual([]);
  });

  it("does not step on a router that already has work queued", () => {
    expect(
      selectRoutersForSubscriptionRescue(
        [rescue({ routerId: "a", queuedJobCount: 1 })],
        NOW2,
        { limit: 5, cooldownMs: 1000 },
      ),
    ).toEqual([]);
  });
});

describe("subscriptionHasHardwareId", () => {
  it("recognises the gate being set", () => {
    expect(
      subscriptionHasHardwareId({
        subscriptions: { items: [{ extras: { hwid: "1" } }] },
      }),
    ).toBe(true);
  });

  it("treats a missing or unset gate as absent", () => {
    expect(subscriptionHasHardwareId({ subscriptions: { items: [{}] } })).toBe(
      false,
    );
    expect(subscriptionHasHardwareId(null)).toBe(false);
  });
});
