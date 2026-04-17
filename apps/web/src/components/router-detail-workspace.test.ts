import { describe, expect, it } from "vitest";

import { buildFallbackPasswallBundleMetadata } from "~/lib/passwall-artifacts";
import {
  formatPasswallAvailableVersion,
  formatPasswallManagedStackAvailableVersion,
  summarizePasswallAttempt,
} from "~/lib/passwall-update-summary";

describe("router detail app update helpers", () => {
  it("renders explicit managed-stack target semantics for PassWall2", () => {
    const bundleMetadata = buildFallbackPasswallBundleMetadata();

    expect(formatPasswallManagedStackAvailableVersion(bundleMetadata)).toBe(
      "stack 26.4.10-1 / app 26.4.10-r1 · Vectra mirror",
    );
  });

  it("renders explicit available version and source for mirrored components", () => {
    const bundleMetadata = buildFallbackPasswallBundleMetadata();

    expect(formatPasswallAvailableVersion(bundleMetadata, "xray-core")).toBe(
      "runtime: built-in PassWall updater / package: 26.3.27-r1 · Vectra mirror",
    );
  });

  it("falls back to bundle source label when package artifact is missing", () => {
    const bundleMetadata = {
      ...buildFallbackPasswallBundleMetadata(),
      packageArtifacts: [],
      source: "upstream" as const,
    };

    expect(formatPasswallAvailableVersion(bundleMetadata, "xray-core")).toBe(
      "runtime: built-in PassWall updater / package: через upstream",
    );
  });

  it("surfaces drift and fallback summary for the latest managed stack update", () => {
    expect(
      summarizePasswallAttempt({
        jobState: "succeeded",
        resultStatus: "success",
        strategy: "xray-built-in-first",
        targetVersion: "26.3.27-r1",
        packageTargetVersion: "26.3.27-r1",
        runtimeTargetVersion: "26.4.15",
        updateScope: "managed-stack",
        originSource: "vectra",
        deliveryBlocked: false,
        deliveryBlockedReason: null,
        summary:
          "xray-core: built-in updater довёл runtime до Xray 26.4.15; запись пакета осталась 25.10.15-r1",
        fallbackSummary:
          "xray-core: built-in updater довёл runtime до Xray 26.4.15; запись пакета осталась 25.10.15-r1",
        driftDetected: true,
        reportedAt: null,
        packageResults: [],
      }),
    ).toBe(
      "Последняя попытка обновить PassWall-стек: xray-core: built-in updater довёл runtime до Xray 26.4.15; запись пакета осталась 25.10.15-r1.",
    );
  });

  it("surfaces failed managed stack updates as operator-facing error text", () => {
    expect(
      summarizePasswallAttempt({
        jobState: "succeeded",
        resultStatus: "failure",
        strategy: "managed-stack-package-first",
        targetVersion: "26.3.27-r1",
        packageTargetVersion: "26.3.27-r1",
        runtimeTargetVersion: null,
        updateScope: "managed-stack",
        originSource: "vectra",
        deliveryBlocked: false,
        deliveryBlockedReason: null,
        summary: "luci-app-passwall2 package install failed",
        fallbackSummary: null,
        driftDetected: false,
        reportedAt: null,
        packageResults: [],
      }),
    ).toBe(
      "Последняя попытка обновить PassWall-стек завершилась ошибкой: luci-app-passwall2 package install failed.",
    );
  });
});
