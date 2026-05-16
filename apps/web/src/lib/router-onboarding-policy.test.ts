import { describe, expect, it } from "vitest";

import {
  buildOnboardingSaveProfileInput,
  getOnboardingDoneBannerCopy,
  normalizeOnboardingVerifyPolicyForBaseline,
  shouldEnableOnboardingAdvance,
  shouldEnableOnboardingRetry,
} from "./router-onboarding-policy";

describe("router onboarding policy helpers", () => {
  it("preserves disabled profiles by omitting enabled from save input", () => {
    const input = buildOnboardingSaveProfileInput({
      routerId: "router-1",
      targetHostname: "  yuranrod-msk  ",
      displayName: "  YuranRod-msk  ",
      subscriptionUrl: " https://sub.example.invalid/api/sub/token ",
      subscriptionRemark: " StarMY ",
      baseline: "hh-exempt",
      runtimePolicy: "auto-minimal-passwall-xray",
      verifyPolicy: "route-smoke",
      notes: "  pilot onboarding  ",
    });

    expect(input).not.toHaveProperty("enabled");
    expect(input).toMatchObject({
      routerId: "router-1",
      targetHostname: "yuranrod-msk",
      displayName: "YuranRod-msk",
      subscriptionUrl: "https://sub.example.invalid/api/sub/token",
      subscriptionRemark: "StarMY",
      baseline: "hh-exempt",
      runtimePolicy: "auto-minimal-passwall-xray",
      verifyPolicy: "services-only",
      notes: "pilot onboarding",
    });
  });

  it("disables route-smoke for non-standard baselines", () => {
    expect(
      normalizeOnboardingVerifyPolicyForBaseline(
        "subscription-only",
        "route-smoke",
      ),
    ).toBe("services-only");
    expect(
      normalizeOnboardingVerifyPolicyForBaseline(
        "standard-non-hh",
        "route-smoke",
      ),
    ).toBe("route-smoke");
  });

  it("keeps advance disabled for blocked and failed runs while retry remains available", () => {
    expect(
      shouldEnableOnboardingAdvance({
        profileEnabled: true,
        canRunJobs: true,
        busy: false,
        runStatus: "blocked",
      }),
    ).toBe(false);
    expect(
      shouldEnableOnboardingAdvance({
        profileEnabled: true,
        canRunJobs: true,
        busy: false,
        runStatus: "failed",
      }),
    ).toBe(false);
    expect(
      shouldEnableOnboardingRetry({
        profilePresent: true,
        canRunJobs: true,
        busy: false,
        runStatus: "blocked",
      }),
    ).toBe(true);
    expect(
      shouldEnableOnboardingRetry({
        profilePresent: true,
        canRunJobs: true,
        busy: false,
        runStatus: "failed",
      }),
    ).toBe(true);
  });

  it("uses the new done banner copy without promising a missing start-new-run path", () => {
    expect(getOnboardingDoneBannerCopy()).toContain(
      "Run завершён и больше не перезапускается автоматически",
    );
    expect(getOnboardingDoneBannerCopy()).not.toContain("новый run");
  });
});
