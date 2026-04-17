import type { RouterOutputs } from "~/trpc/react";

import {
  formatPasswallArtifactSourceLabel,
  type PasswallBundleMetadata,
  type PasswallPackageArtifactDescriptor,
} from "~/lib/passwall-artifacts";

type EditorSurface = RouterOutputs["draft"]["editorSurface"];

function findPasswallPackageArtifact(
  bundleMetadata: PasswallBundleMetadata,
  packageName: string,
): PasswallPackageArtifactDescriptor | null {
  return (
    bundleMetadata.packageArtifacts.find((entry) => entry.name === packageName) ??
    null
  );
}

export function formatPasswallManagedStackAvailableVersion(
  bundleMetadata: PasswallBundleMetadata,
) {
  const passwallAppArtifact = findPasswallPackageArtifact(
    bundleMetadata,
    "luci-app-passwall2",
  );
  const sourceLabel = formatPasswallArtifactSourceLabel(bundleMetadata.source);

  if (!passwallAppArtifact) {
    return `stack ${bundleMetadata.releaseTag} · ${sourceLabel}`;
  }

  return `stack ${bundleMetadata.releaseTag} / app ${passwallAppArtifact.artifactVersion} · ${sourceLabel}`;
}

export function formatPasswallAvailableVersion(
  bundleMetadata: PasswallBundleMetadata,
  packageName: string,
) {
  const packageArtifact = findPasswallPackageArtifact(bundleMetadata, packageName);
  const sourceLabel = formatPasswallArtifactSourceLabel(bundleMetadata.source);
  if (!packageArtifact) {
    return packageName === "xray-core"
      ? `runtime: built-in PassWall updater / package: через ${sourceLabel}`
      : `package: через ${sourceLabel}`;
  }

  if (packageName === "xray-core") {
    return `runtime: built-in PassWall updater / package: ${packageArtifact.artifactVersion} · ${formatPasswallArtifactSourceLabel(
      packageArtifact.source,
    )}`;
  }

  return `package: ${packageArtifact.artifactVersion} · ${formatPasswallArtifactSourceLabel(
    packageArtifact.source,
  )}`;
}

export function summarizePasswallAttempt(
  attempt: NonNullable<EditorSurface["lastPasswallUpdateAttempt"]> | null,
) {
  if (!attempt) {
    return null;
  }

  if (
    ["queued", "delivered", "running"].includes(attempt.jobState) ||
    attempt.resultStatus === "accepted"
  ) {
    return "Обновление PassWall-стека ещё выполняется.";
  }

  if (attempt.resultStatus === "failure") {
    return `Последняя попытка обновить PassWall-стек завершилась ошибкой: ${attempt.summary}.`;
  }

  if (attempt.summary.trim().length > 0) {
    return `Последняя попытка обновить PassWall-стек: ${attempt.summary}.`;
  }

  return null;
}
