const invalidControllerVersionSentinels = new Set([
  "unknown",
  "неизвестно",
]);

export const unknownControllerVersionLabel = "Не удалось определить";

export function normalizeControllerVersion(
  value: string | null | undefined,
): string | null {
  if (typeof value !== "string") {
    return null;
  }

  const trimmed = value.trim();
  if (trimmed.length === 0) {
    return null;
  }

  if (invalidControllerVersionSentinels.has(trimmed.toLowerCase())) {
    return null;
  }

  return trimmed;
}

export function formatControllerVersion(
  value: string | null | undefined,
  fallback = unknownControllerVersionLabel,
): string {
  return normalizeControllerVersion(value) ?? fallback;
}

export function compareControllerVersions(
  left: string | null | undefined,
  right: string | null | undefined,
): number | null {
  const normalizedLeft = normalizeControllerVersion(left);
  const normalizedRight = normalizeControllerVersion(right);

  if (!normalizedLeft || !normalizedRight) {
    return null;
  }

  const leftParts = parseControllerVersion(normalizedLeft);
  const rightParts = parseControllerVersion(normalizedRight);

  if (!leftParts || !rightParts) {
    return normalizedLeft.localeCompare(normalizedRight);
  }

  const majorDiff = leftParts.version[0] - rightParts.version[0];
  if (majorDiff !== 0) {
    return majorDiff;
  }

  const minorDiff = leftParts.version[1] - rightParts.version[1];
  if (minorDiff !== 0) {
    return minorDiff;
  }

  const patchDiff = leftParts.version[2] - rightParts.version[2];
  if (patchDiff !== 0) {
    return patchDiff;
  }

  return leftParts.release - rightParts.release;
}

function parseControllerVersion(value: string) {
  const match = /^(\d+)\.(\d+)\.(\d+)-r(\d+)$/i.exec(value);
  if (!match) {
    return null;
  }

  return {
    version: [Number(match[1]), Number(match[2]), Number(match[3])] as [
      number,
      number,
      number,
    ],
    release: Number(match[4]),
  };
}
