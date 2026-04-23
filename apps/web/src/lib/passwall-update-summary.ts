import type { RouterOutputs } from "~/trpc/react";

import {
  findPasswallRuntimeTarget,
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
    bundleMetadata.packageArtifacts.find(
      (entry) => entry.name === packageName,
    ) ?? null
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
  const packageArtifact = findPasswallPackageArtifact(
    bundleMetadata,
    packageName,
  );
  const runtimeTarget = findPasswallRuntimeTarget(bundleMetadata, packageName);
  const sourceLabel = formatPasswallArtifactSourceLabel(bundleMetadata.source);
  if (!packageArtifact) {
    if (runtimeTarget) {
      return `runtime: ${runtimeTarget.remoteVersion} via built-in PassWall updater / package: через ${sourceLabel}`;
    }

    return packageName === "xray-core"
      ? `runtime: built-in PassWall updater / package: через ${sourceLabel}`
      : `package: через ${sourceLabel}`;
  }

  if (runtimeTarget) {
    return `runtime: ${runtimeTarget.remoteVersion} via built-in PassWall updater / package: ${packageArtifact.artifactVersion} · ${formatPasswallArtifactSourceLabel(
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

  if (attempt.deliveryBlocked) {
    return `Последняя попытка обновить PassWall-стек упёрлась в delivery path: ${attempt.summary}.`;
  }

  if (attempt.summary.trim().length > 0) {
    return `Последняя попытка обновить PassWall-стек: ${attempt.summary}.`;
  }

  return null;
}

function extractLooseSemverParts(value: string | null | undefined) {
  if (typeof value !== "string") {
    return null;
  }

  const semverPattern = /(\d+)\.(\d+)\.(\d+)/;
  const match = semverPattern.exec(value);
  if (!match) {
    return null;
  }

  return [Number(match[1]), Number(match[2]), Number(match[3])] as const;
}

export function compareLooseSemverVersions(
  left: string | null | undefined,
  right: string | null | undefined,
) {
  const leftParts = extractLooseSemverParts(left);
  const rightParts = extractLooseSemverParts(right);

  if (!leftParts || !rightParts) {
    return null;
  }

  const [leftMajor, leftMinor, leftPatch] = leftParts;
  const [rightMajor, rightMinor, rightPatch] = rightParts;
  const diffs = [
    leftMajor - rightMajor,
    leftMinor - rightMinor,
    leftPatch - rightPatch,
  ];

  for (const diff of diffs) {
    if (diff !== 0) {
      return diff > 0 ? 1 : -1;
    }
  }

  return 0;
}

export function runtimeMeetsOrExceedsTargetVersion(
  runtimeVersion: string | null | undefined,
  targetVersion: string | null | undefined,
) {
  const comparison = compareLooseSemverVersions(runtimeVersion, targetVersion);
  return comparison !== null && comparison >= 0;
}
