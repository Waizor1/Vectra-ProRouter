import { describe, expect, it } from "vitest";

import {
  PASSWALL_FEATURE_MIN_VERSIONS,
  getPasswallFeatureGate,
} from "~/lib/passwall-feature-gates";

describe("passwall feature gates", () => {
  it("keeps new subscription DNS resolver controls inactive before 26.4.20", () => {
    const gate = getPasswallFeatureGate(
      "26.4.10-r1",
      PASSWALL_FEATURE_MIN_VERSIONS.subscriptionDomainResolver,
    );

    expect(gate.supported).toBe(false);
    expect(gate.reason).toContain("PassWall2 26.4.20+");
    expect(gate.reason).toContain("26.4.10-r1");
  });

  it("enables 26.4.20 subscription params while keeping 26.5.1 shunt params gated", () => {
    expect(
      getPasswallFeatureGate(
        "26.4.20-r1",
        PASSWALL_FEATURE_MIN_VERSIONS.subscriptionDomainResolver,
      ).supported,
    ).toBe(true);
    expect(
      getPasswallFeatureGate(
        "26.4.20-r1",
        PASSWALL_FEATURE_MIN_VERSIONS.shuntQuicProtocol,
      ).supported,
    ).toBe(false);
  });

  it("enables latest 26.5.1-only PassWall fields and blocks unknown versions", () => {
    expect(
      getPasswallFeatureGate(
        "26.5.1-r1",
        PASSWALL_FEATURE_MIN_VERSIONS.xrayMkcpMtu,
      ).supported,
    ).toBe(true);
    expect(
      getPasswallFeatureGate(null, PASSWALL_FEATURE_MIN_VERSIONS.xrayMkcpMtu),
    ).toMatchObject({
      supported: false,
      currentVersion: null,
    });
  });
});
