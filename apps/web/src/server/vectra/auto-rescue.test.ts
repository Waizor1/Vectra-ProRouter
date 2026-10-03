import {
  jobs,
  rescueCases,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { describe, expect, it } from "vitest";

import {
  planAutoRepairRetry,
  autoRepairActionsForTrigger,
  detectBlockedReachabilityTriggers,
  hasDistinctBlockedReachabilityEvidence,
  isStaleControlPlaneRecoveryPark,
  noAutoRepairEscalationReason,
  planRepairActionsForRouterSafety,
  queueRescueCaseLogCollection,
  repairActionsForTrigger,
  resourceGuardReasonsForLogCollection,
} from "./auto-rescue";
import { createFakeDb } from "./testing/fake-db";

describe("repairActionsForTrigger", () => {
  it("maps critical proxy/direct triggers to safe repair only", () => {
    expect(repairActionsForTrigger("direct_mode")).toEqual([
      "restart_passwall",
      "restart_dnsmasq",
      "refresh_rules",
      "refresh_subscriptions",
      "reconnect_proxy",
    ]);
    expect(repairActionsForTrigger("proxy_outage")).toEqual([
      "restart_passwall",
      "restart_dnsmasq",
      "refresh_rules",
      "refresh_subscriptions",
      "reconnect_proxy",
    ]);
  });

  it("does not assign remote repair to stale offline routers", () => {
    expect(repairActionsForTrigger("stale_check_in")).toEqual([]);
  });

  it("never includes reboot, package update, node switch, or raw shell actions", () => {
    const allActions = [
      ...repairActionsForTrigger("direct_mode"),
      ...repairActionsForTrigger("server_unreachable"),
      ...repairActionsForTrigger("telegram_blocked"),
      ...repairActionsForTrigger("foreign_reachability_blocked"),
    ];

    expect(allActions).not.toContain("reboot");
    expect(allActions).not.toContain("update_passwall_packages");
    expect(allActions).not.toContain("switch_node");
    expect(allActions).not.toContain("run_terminal_command");
  });
});

describe("autoRepairActionsForTrigger", () => {
  it("narrows unattended direct/proxy recovery to reconnect-only", () => {
    // Heavy re-import/refresh steps are reserved for operator-initiated repairs;
    // the unattended monitor only resumes the proxy on the current node.
    expect(autoRepairActionsForTrigger("direct_mode")).toEqual([
      "reconnect_proxy",
    ]);
    expect(autoRepairActionsForTrigger("proxy_outage")).toEqual([
      "reconnect_proxy",
    ]);
  });

  it("never runs subscription/rule refresh unattended", () => {
    const actions = [
      ...autoRepairActionsForTrigger("direct_mode"),
      ...autoRepairActionsForTrigger("proxy_outage"),
    ];
    expect(actions).not.toContain("refresh_subscriptions");
    expect(actions).not.toContain("refresh_rules");
    expect(actions).not.toContain("restart_passwall");
  });

  it("delegates non-direct triggers to the operator repair mapping", () => {
    expect(autoRepairActionsForTrigger("server_unreachable")).toEqual(
      repairActionsForTrigger("server_unreachable"),
    );
    expect(autoRepairActionsForTrigger("stale_check_in")).toEqual([]);
  });

  it("escalates reachability triggers instead of repairing them", () => {
    // These fire fleet-wide when a shared exit or the service itself breaks.
    // Running any repair then would restart xray on every router at once and
    // turn a single-service blip into a total VPN outage, so the unattended
    // monitor hands them to the operator untouched.
    expect(autoRepairActionsForTrigger("telegram_blocked")).toEqual([]);
    expect(autoRepairActionsForTrigger("foreign_reachability_blocked")).toEqual(
      [],
    );
  });

  it("keeps the heavy sequence available for operator-initiated repair", () => {
    expect(repairActionsForTrigger("telegram_blocked")).toContain(
      "refresh_subscriptions",
    );
    expect(repairActionsForTrigger("foreign_reachability_blocked")).toContain(
      "refresh_subscriptions",
    );
  });

  it("never runs subscription/rule refresh unattended for any trigger", () => {
    const triggers = [
      "direct_mode",
      "proxy_outage",
      "server_unreachable",
      "stale_check_in",
      "telegram_blocked",
      "foreign_reachability_blocked",
    ] as const;

    for (const trigger of triggers) {
      const actions = autoRepairActionsForTrigger(trigger);
      expect(actions).not.toContain("refresh_subscriptions");
      expect(actions).not.toContain("refresh_rules");
    }
  });

  it("explains an escalation without claiming the router is offline", () => {
    expect(noAutoRepairEscalationReason("telegram_blocked")).toContain(
      "proxy itself is up",
    );
    expect(noAutoRepairEscalationReason("stale_check_in")).toContain(
      "offline/stale",
    );
  });
});

describe("hasDistinctBlockedReachabilityEvidence", () => {
  it("does not treat repeated cached service probe snapshots as new evidence", () => {
    const snapshots = [1, 2, 3].map(() => ({
      payload: {
        telegramReachability: {
          reachable: false,
          status: "blocked",
          checkedAt: "2026-05-12T00:00:00.000Z",
        },
      },
    }));

    expect(
      hasDistinctBlockedReachabilityEvidence(snapshots, "telegramReachability"),
    ).toBe(false);
  });

  it("requires separate blocked probe executions before auto-rescue triggers", () => {
    const snapshots = [0, 1, 2].map((index) => ({
      payload: {
        telegramReachability: {
          reachable: false,
          status: "blocked",
          checkedAt: `2026-05-12T00:0${index}:00.000Z`,
        },
      },
    }));

    expect(
      hasDistinctBlockedReachabilityEvidence(snapshots, "telegramReachability"),
    ).toBe(true);
  });
});

describe("planRepairActionsForRouterSafety", () => {
  it("keeps full repair sequence when router resources are safe", () => {
    const actions = repairActionsForTrigger("proxy_outage");

    expect(
      planRepairActionsForRouterSafety(actions, {
        resources: {
          memoryAvailableMb: 96,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        safetyEvents: [],
      }),
    ).toEqual({
      actions: [
        "restart_passwall",
        "restart_dnsmasq",
        "refresh_rules",
        "refresh_subscriptions",
        "reconnect_proxy",
      ],
      droppedActions: [],
      reasons: [],
    });
  });

  it("does not restart PassWall for service-specific repair while memory is low", () => {
    const planned = planRepairActionsForRouterSafety(
      repairActionsForTrigger("telegram_blocked"),
      {
        resources: {
          memoryAvailableMb: 45,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
      },
      "telegram_blocked",
    );

    expect(planned.actions).toEqual(["restart_dnsmasq"]);
    expect(planned.droppedActions).toEqual([
      "restart_passwall",
      "refresh_rules",
      "refresh_subscriptions",
    ]);
    expect(planned.reasons.join("; ")).toContain("available RAM 45 MB");
  });

  it("blocks log collection under the diagnostic resource floor", () => {
    expect(
      resourceGuardReasonsForLogCollection({
        resources: {
          memoryAvailableMb: 40,
          overlayFreeMb: 64,
          tmpFreeMb: 64,
        },
      }).join("; "),
    ).toContain("available RAM 40 MB");
  });
});

describe("isStaleControlPlaneRecoveryPark", () => {
  // Shape copied from a real prod incident: the metadata is frozen at the
  // moment the router parked itself, so its foreignStatus stays "blocked"
  // forever even after the proxy path comes back.
  const parkedIncident = {
    type: "proxy_outage",
    metadata: {
      origin: "control-plane-recovery",
      ruStatus: "reachable",
      panelStatus: "reachable",
      foreignStatus: "blocked",
      recoveryPhase: "operator_attention",
      awaitingOperator: true,
    },
  };
  const healthySnapshot = {
    foreignReachability: { status: "healthy" },
    serviceHealth: { passwall: "running" },
  };

  it("judges the park by the live check-in, not the frozen incident metadata", () => {
    // The incident still claims foreignStatus "blocked". The router's own
    // latest snapshot is the authority, and it says the outage is over.
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: parkedIncident,
        passwallEnabled: true,
        snapshotPayload: healthySnapshot,
      }),
    ).toBe(true);
  });

  it("leaves a genuine outage alone", () => {
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: parkedIncident,
        passwallEnabled: false,
        snapshotPayload: { foreignReachability: { status: "blocked" } },
      }),
    ).toBe(false);
  });

  it("requires the proxy to be back up, not just foreign reachable", () => {
    // Direct mode reaches foreign hosts for anything the censor allows, so
    // reachability alone must never be read as "the proxy recovered".
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: parkedIncident,
        passwallEnabled: false,
        snapshotPayload: healthySnapshot,
      }),
    ).toBe(false);
  });

  it("ignores incidents the control plane recovery machine did not open", () => {
    // Operator- or monitor-opened incidents are not parked behind the agent
    // phase machine, so unparking them would be meaningless.
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: {
          type: "proxy_outage",
          metadata: { awaitingOperator: true },
        },
        passwallEnabled: true,
        snapshotPayload: healthySnapshot,
      }),
    ).toBe(false);
  });

  it("only unparks incidents that are actually awaiting an operator", () => {
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: {
          type: "proxy_outage",
          metadata: {
            origin: "control-plane-recovery",
            recoveryPhase: "direct_settle",
            awaitingOperator: false,
          },
        },
        passwallEnabled: true,
        snapshotPayload: healthySnapshot,
      }),
    ).toBe(false);
  });

  it("does not trust the UCI switch when the stack itself is down", () => {
    // passwallEnabled reads passwall2.@global[0].enabled -- a config flag, not
    // a running process. A box whose xray died with the switch still on keeps
    // reporting true and still reaches foreign hosts, because its traffic
    // falls through to direct. Treating that as recovered would close a live
    // outage.
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: parkedIncident,
        passwallEnabled: true,
        snapshotPayload: {
          foreignReachability: { status: "healthy" },
          serviceHealth: { passwall: "stopped" },
        },
      }),
    ).toBe(false);
  });

  it("ignores a transient retry-wait phase that also awaits an operator", () => {
    expect(
      isStaleControlPlaneRecoveryPark({
        incident: {
          type: "proxy_outage",
          metadata: {
            origin: "control-plane-recovery",
            recoveryPhase: "passwall_retry_wait",
            awaitingOperator: true,
          },
        },
        passwallEnabled: true,
        snapshotPayload: healthySnapshot,
      }),
    ).toBe(false);
  });

  it("rejects every non-healthy foreign status, including 'reachable'", () => {
    // "reachable" is a legal status in the contract but a weaker claim than
    // "healthy"; only the latter means the whole probe group passed.
    for (const status of ["reachable", "partial", "blocked", "unknown"]) {
      expect(
        isStaleControlPlaneRecoveryPark({
          incident: parkedIncident,
          passwallEnabled: true,
          snapshotPayload: {
            foreignReachability: { status },
            serviceHealth: { passwall: "running" },
          },
        }),
      ).toBe(false);
    }
  });

  it("tolerates a missing or malformed snapshot payload", () => {
    for (const snapshotPayload of [null, undefined, {}, "nonsense", 42]) {
      expect(
        isStaleControlPlaneRecoveryPark({
          incident: parkedIncident,
          passwallEnabled: true,
          snapshotPayload,
        }),
      ).toBe(false);
    }
  });
});

describe("planAutoRepairRetry", () => {
  const NOW = new Date("2026-08-28T02:00:00.000Z");

  it("repairs immediately the first time a case is seen", () => {
    expect(
      planAutoRepairRetry({ attempts: 0, lastAttemptAt: null, now: NOW }),
    ).toEqual({ attempt: true, exhausted: false });
  });

  // The hole this closes: a reconnect has reported `succeeded` while PassWall
  // stayed off, and the old one-shot guard meant nothing ever tried again.
  it("retries after the backoff has elapsed", () => {
    expect(
      planAutoRepairRetry({
        attempts: 1,
        lastAttemptAt: new Date("2026-08-28T01:45:00.000Z"),
        now: NOW,
      }),
    ).toEqual({ attempt: true, exhausted: false });
  });

  it("holds off while the backoff is still running", () => {
    expect(
      planAutoRepairRetry({
        attempts: 1,
        lastAttemptAt: new Date("2026-08-28T01:55:00.000Z"),
        now: NOW,
      }),
    ).toEqual({ attempt: false, exhausted: false });
  });

  it("widens the gap for the later attempt", () => {
    // 20 minutes after attempt two: past the first gap, short of the second.
    const args = {
      attempts: 2,
      lastAttemptAt: new Date("2026-08-28T01:40:00.000Z"),
      now: NOW,
    };
    expect(planAutoRepairRetry(args)).toEqual({
      attempt: false,
      exhausted: false,
    });
    expect(
      planAutoRepairRetry({
        ...args,
        lastAttemptAt: new Date("2026-08-28T01:25:00.000Z"),
      }),
    ).toEqual({ attempt: true, exhausted: false });
  });

  // Distinct from "not due yet": the case stops being the monitor's problem
  // and escalates to a human instead of retrying forever.
  it("gives up after the third attempt instead of looping", () => {
    expect(
      planAutoRepairRetry({
        attempts: 3,
        lastAttemptAt: new Date("2026-08-27T00:00:00.000Z"),
        now: NOW,
      }),
    ).toEqual({ attempt: false, exhausted: true });
  });
});

describe("detectBlockedReachabilityTriggers and released routers (ADR-0006)", () => {
  const NOW = new Date("2026-09-28T10:00:00.000Z");
  // Three snapshots with distinct probe times, all blocked: real evidence.
  const blockedSnapshots = [0, 1, 2].map((index) => ({
    id: `snapshot-${index}`,
    createdAt: new Date(NOW.getTime() - index * 60_000),
    payload: {
      telegramReachability: {
        reachable: false,
        status: "blocked",
        checkedAt: new Date(NOW.getTime() - index * 60_000).toISOString(),
      },
    },
  }));

  it("opens no case for a released router, only for an ordinary one", async () => {
    const fake = createFakeDb({
      selects: [
        [
          routers,
          [
            [
              {
                id: "released",
                releasedAt: new Date("2026-09-28T09:00:00.000Z"),
                ownerRef: null,
              },
              {
                id: "in-service",
                releasedAt: null,
                ownerRef: "acct-42",
                approvedAt: new Date("2026-09-01T00:00:00.000Z"),
              },
            ],
          ],
        ],
        // Only one router's snapshots are ever read: the released one is
        // skipped before its evidence is loaded.
        [routerInventorySnapshots, [blockedSnapshots]],
      ],
    });

    const triggers = await detectBlockedReachabilityTriggers(
      fake.db as never,
      NOW,
    );

    expect(
      triggers.map((trigger) => `${trigger.trigger}:${trigger.routerId}`),
    ).toEqual(["telegram_blocked:in-service"]);
  });
});

describe("detectBlockedReachabilityTriggers and unapproved routers", () => {
  const NOW = new Date("2026-09-28T10:00:00.000Z");
  const blockedSnapshots = [0, 1, 2].map((index) => ({
    id: `snapshot-${index}`,
    createdAt: new Date(NOW.getTime() - index * 60_000),
    payload: {
      foreignReachability: {
        reachable: false,
        status: "blocked",
        checkedAt: new Date(NOW.getTime() - index * 60_000).toISOString(),
      },
    },
  }));

  // Anyone can register a router; nobody approved this one, so its probes
  // are not read and it can open no case.
  it("never scans a router nobody approved", async () => {
    const fake = createFakeDb({
      selects: [
        [
          routers,
          [
            [
              { id: "anonymous", releasedAt: null, approvedAt: null },
              {
                id: "approved",
                releasedAt: null,
                approvedAt: new Date("2026-09-01T00:00:00.000Z"),
              },
            ],
          ],
        ],
        [routerInventorySnapshots, [blockedSnapshots]],
      ],
    });

    const triggers = await detectBlockedReachabilityTriggers(
      fake.db as never,
      NOW,
    );

    expect(
      triggers.map((trigger) => `${trigger.trigger}:${trigger.routerId}`),
    ).toEqual(["foreign_reachability_blocked:approved"]);
    expect(
      fake.calls.filter(
        (call) =>
          call.kind === "select" && call.table === routerInventorySnapshots,
      ),
    ).toHaveLength(1);
  });
});

// collect_router_logs is a PassWall agent job; vctl never runs it, so one
// queued for an xray-direct router only waits to be failed at check-in.
describe("queueRescueCaseLogCollection on a vctl router", () => {
  const CASE_ID = "5b4a3c2d-1e0f-4a9b-8c7d-6e5f4a3b2c1d";

  function scripted(engineMode: string) {
    return createFakeDb({
      selects: [
        [rescueCases, [[{ id: CASE_ID, routerId: "router-1" }]]],
        [jobs, [[]]],
        [routers, [[{ engineMode }]]],
      ],
    });
  }

  it("tells an operator why it refuses", async () => {
    const fake = scripted("xray-direct");

    await expect(
      queueRescueCaseLogCollection(CASE_ID, fake.db as never),
    ).rejects.toThrow("vctl routers: logs come from vctl");
    expect(fake.inserts(jobs)).toEqual([]);
  });

  it("skips quietly when unattended", async () => {
    const fake = scripted("xray-direct");

    await expect(
      queueRescueCaseLogCollection(CASE_ID, fake.db as never, {
        unattended: true,
      }),
    ).resolves.toBeNull();
    expect(fake.inserts(jobs)).toEqual([]);
  });

  it("still goes on for a PassWall router", async () => {
    const fake = scripted("passwall");

    // Past the engine gate it meets the resource guard (no snapshot here).
    await expect(
      queueRescueCaseLogCollection(CASE_ID, fake.db as never),
    ).rejects.toThrow(/resource guard/);
  });
});
