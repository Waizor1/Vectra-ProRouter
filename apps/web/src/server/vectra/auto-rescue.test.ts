import { describe, expect, it } from "vitest";

import {
  autoRepairActionsForTrigger,
  hasDistinctBlockedReachabilityEvidence,
  noAutoRepairEscalationReason,
  planRepairActionsForRouterSafety,
  repairActionsForTrigger,
  resourceGuardReasonsForLogCollection,
} from "./auto-rescue";

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
      hasDistinctBlockedReachabilityEvidence(
        snapshots,
        "telegramReachability",
      ),
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
      hasDistinctBlockedReachabilityEvidence(
        snapshots,
        "telegramReachability",
      ),
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
