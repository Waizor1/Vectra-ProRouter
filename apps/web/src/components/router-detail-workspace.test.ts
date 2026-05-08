import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { buildFallbackPasswallBundleMetadata } from "~/lib/passwall-artifacts";
import {
  formatPasswallAvailableVersion,
  formatPasswallManagedStackAvailableVersion,
  runtimeMeetsOrExceedsTargetVersion,
  summarizePasswallAttempt,
} from "~/lib/passwall-update-summary";

describe("router detail app update helpers", () => {
  it("keeps inline router hostname controls near the router summary", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("api.fleet.renameRouter.useMutation");
    expect(source).toContain('name="router-hostname"');
    expect(source).toContain("[A-Za-z0-9\\\\-]");
    expect(source).toContain("Hostname роутера");
    expect(source).toContain("Меняется именно `system.@system[0].hostname` на");
  });

  it("keeps the app update summary copy on a wrapping full-width row", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain(
      'className="w-full min-w-0 space-y-2 lg:basis-full"',
    );
    expect(source).toContain(
      'className="text-sm leading-6 break-words text-slate-300"',
    );
    expect(source).toContain(
      'className="text-sm leading-6 break-words text-slate-400"',
    );
  });

  it("keeps a single-router reboot action inside App Update", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("api.update.queueRouterReboot.useMutation");
    expect(source).toContain("Перезагрузить роутер");
    expect(source).toContain("Поставить перезагрузку в очередь?");
    expect(source).toContain("Последняя перезагрузка от панели");
  });

  it("keeps managed subscription nodes inspectable without making them editable", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("Открыть read-only детали ноды");
    expect(source).toContain("REALITY public key");
    expect(source).toContain("Extra keys без значений секретов");
    expect(source).toContain("node.details.realityPublicKeyPresent");
  });

  it("keeps fresh PassWall parameters visible behind version gates", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("PASSWALL_FEATURE_MIN_VERSIONS");
    expect(source).toContain("Domain DNS Resolve");
    expect(source).toContain("domain_resolver_dns_https");
    expect(source).toContain("mKCP MTU");
    expect(source).toContain("TLS pinSHA256");
    expect(source).toContain('{ value: "quic", label: "quic" }');
  });

  it("keeps the live editor surface refreshing while sync or drift can clear", () => {
    const source = readFileSync(
      new URL("./router-detail-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("getRouterDetailSurfaceRefetchInterval");
    expect(source).toContain("surfaceData.configTrust.requiresReimport");
    expect(source).toContain(
      'surfaceData.unconfirmedChanges.panel.status !== "none"',
    );
    expect(source).toContain('refetchOnMount: "always"');
    expect(source).toContain('refetchOnWindowFocus: "always"');
  });

  it("renders explicit managed-stack target semantics for PassWall2", () => {
    const bundleMetadata = buildFallbackPasswallBundleMetadata();

    expect(formatPasswallManagedStackAvailableVersion(bundleMetadata)).toBe(
      "stack 26.4.10-1 / app 26.4.10-r1 · Vectra mirror",
    );
  });

  it("renders explicit available version and source for mirrored components", () => {
    const bundleMetadata = {
      ...buildFallbackPasswallBundleMetadata(),
      runtimeTargets: {
        "xray-core": {
          componentName: "xray",
          remoteVersion: "26.4.17",
          releaseUrl: "https://github.com/XTLS/Xray-core/releases/tag/v26.4.17",
          assetName: "Xray-linux-arm64-v8a.zip",
          assetUrl:
            "https://github.com/XTLS/Xray-core/releases/download/v26.4.17/Xray-linux-arm64-v8a.zip",
          assetSizeBytes: 12345678,
        },
      },
    };

    expect(formatPasswallAvailableVersion(bundleMetadata, "xray-core")).toBe(
      "runtime: 26.4.17 via built-in PassWall updater / package: 26.3.27-r1 · Vectra mirror",
    );
  });

  it("renders runtime target metadata for non-xray runtime-only components", () => {
    const bundleMetadata = {
      ...buildFallbackPasswallBundleMetadata(),
      runtimeTargets: {
        "sing-box": {
          componentName: "sing-box",
          remoteVersion: "1.13.9",
          releaseUrl:
            "https://github.com/SagerNet/sing-box/releases/tag/v1.13.9",
          assetName: "sing-box-1.13.9-linux-arm64-musl.tar.gz",
          assetUrl:
            "https://github.com/SagerNet/sing-box/releases/download/v1.13.9/sing-box-1.13.9-linux-arm64-musl.tar.gz",
          assetSizeBytes: 9876543,
        },
      },
      packageArtifacts:
        buildFallbackPasswallBundleMetadata().packageArtifacts.map(
          (artifact) =>
            artifact.name === "sing-box"
              ? { ...artifact, artifactVersion: "1.13.6-r1" }
              : artifact,
        ),
    };

    expect(formatPasswallAvailableVersion(bundleMetadata, "sing-box")).toBe(
      "runtime: 1.13.9 via built-in PassWall updater / package: 1.13.6-r1 · Vectra mirror",
    );
  });

  it("falls back to bundle source label when package artifact is missing", () => {
    const bundleMetadata = {
      ...buildFallbackPasswallBundleMetadata(),
      packageArtifacts: [],
      source: "upstream" as const,
      runtimeTargets: {
        "xray-core": {
          componentName: "xray",
          remoteVersion: "26.4.17",
          releaseUrl: "https://github.com/XTLS/Xray-core/releases/tag/v26.4.17",
          assetName: "Xray-linux-arm64-v8a.zip",
          assetUrl:
            "https://github.com/XTLS/Xray-core/releases/download/v26.4.17/Xray-linux-arm64-v8a.zip",
          assetSizeBytes: 12345678,
        },
      },
    };

    expect(formatPasswallAvailableVersion(bundleMetadata, "xray-core")).toBe(
      "runtime: 26.4.17 via built-in PassWall updater / package: через upstream",
    );
  });

  it("treats Xray runtime at the upstream runtime target as current", () => {
    expect(
      runtimeMeetsOrExceedsTargetVersion(
        "Xray 26.4.17 (Xray, Penetrates Everything.) c5edc12 (go1.26.2 linux/arm64)",
        "26.4.17",
      ),
    ).toBe(true);
  });

  it("does not treat older Xray runtime as current when upstream moved ahead", () => {
    expect(
      runtimeMeetsOrExceedsTargetVersion(
        "Xray 26.4.15 (Xray, Penetrates Everything.) abc123 (go1.26.1 linux/arm64)",
        "26.4.17",
      ),
    ).toBe(false);
  });

  it("treats newer sing-box runtime strings the same way as Xray runtime strings", () => {
    expect(
      runtimeMeetsOrExceedsTargetVersion("sing-box version 1.13.9", "1.13.9"),
    ).toBe(true);
    expect(
      runtimeMeetsOrExceedsTargetVersion("sing-box version 1.13.6", "1.13.9"),
    ).toBe(false);
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
