import { describe, expect, it } from "vitest";

import { repairActionsForTrigger } from "./auto-rescue";

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
