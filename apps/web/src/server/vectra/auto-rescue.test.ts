import { describe, expect, it } from "vitest";

import {
  hasDistinctBlockedReachabilityEvidence,
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
