import { describe, expect, it } from "vitest";

import { checkPasswallUpgradePreflight } from "./passwall-upgrade-preflight";

const healthy = {
  subscriptionHealth: {
    hwidEnabled: true,
    scheduleEnabled: true,
    placeholderNodes: 0,
  },
  xrayPackageVersion: "26.4.25-r1",
  targetVersion: "26.8.10-r1",
};

describe("passwall upgrade preflight", () => {
  it("allows an upgrade on a router that satisfies both gates", () => {
    expect(checkPasswallUpgradePreflight(healthy).blocked).toBe(false);
  });

  // Upgrading past 26.7.16 turns on the HWID gate. On a router without the
  // option the next nightly subscription replaces every node with a placeholder
  // — the exact sequence that took nataliafilisiti and stewe down hours after an
  // upgrade that reported success.
  it("blocks when the HWID gate is off and the target enables it", () => {
    const result = checkPasswallUpgradePreflight({
      ...healthy,
      subscriptionHealth: { ...healthy.subscriptionHealth, hwidEnabled: false },
    });
    expect(result.blocked).toBe(true);
    expect(result.reasons.join(" ")).toContain("hwid");
  });

  // 26.8.10 emits a config xray refuses below 26.3.27, and the feed package is
  // older than that, so a router whose xray-core is too old ends up with
  // PassWall running in no-proxy mode: a live process and no traffic.
  it("blocks when the installed xray-core is older than the floor", () => {
    const result = checkPasswallUpgradePreflight({
      ...healthy,
      xrayPackageVersion: "25.10.15-r1",
    });
    expect(result.blocked).toBe(true);
    expect(result.reasons.join(" ")).toContain("xray");
  });

  // Routers that carry no xray-core package run a manually placed binary that
  // opkg cannot see. Blocking them on an absent package record would stop
  // upgrades that are actually safe, so absence is not a blocker.
  it("does not block a router with no xray-core package record", () => {
    expect(
      checkPasswallUpgradePreflight({ ...healthy, xrayPackageVersion: null })
        .blocked,
    ).toBe(false);
  });

  // Controllers that predate the subscriptionHealth field report nothing. That
  // is unknown, not proof of a missing gate: blocking every un-updated router
  // would freeze the fleet.
  it("does not block when the controller reports no subscription health", () => {
    expect(
      checkPasswallUpgradePreflight({ ...healthy, subscriptionHealth: null })
        .blocked,
    ).toBe(false);
  });

  // Staying below the gate version keeps the old behaviour, so the HWID option
  // is irrelevant there.
  it("ignores the HWID gate when the target predates it", () => {
    expect(
      checkPasswallUpgradePreflight({
        ...healthy,
        subscriptionHealth: { ...healthy.subscriptionHealth, hwidEnabled: false },
        targetVersion: "26.5.1-r1",
      }).blocked,
    ).toBe(false);
  });
});
