import {
  compareControllerVersions,
  normalizeControllerVersion,
} from "~/lib/controller-version";

export const minimumTerminalControllerVersion = "0.1.12-r9";

export function supportsTerminalFeature(
  currentVersion: string | null | undefined,
  minimumVersion = minimumTerminalControllerVersion,
) {
  if (!normalizeControllerVersion(currentVersion)) {
    return false;
  }

  return (compareControllerVersions(currentVersion, minimumVersion) ?? -1) >= 0;
}
