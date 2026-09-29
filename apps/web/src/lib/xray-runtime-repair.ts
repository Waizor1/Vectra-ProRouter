// First controller release whose ensure_passwall_runtime understands the
// "xray_binary" action; older agents reject it as unsupported.
export const minimumXrayRuntimeRepairControllerVersion = "0.1.13-r41";

// Safety event the controller raises when xray provably cannot start: the
// binary is missing, or the last PassWall start failed on it.
export const proxyRuntimeUnusableEventType = "proxy_runtime_unusable";

/**
 * Whether the xray repair may remove the current binary before writing the
 * new one. The installer only does that for an xray that no longer runs, or on
 * request, because on a full overlay a working binary would otherwise be traded
 * for a download that might not fit. A router that itself reports its runtime
 * unusable has nothing worth keeping — an xray that answers `version` but
 * refuses PassWall's config (26.9.9 on andrey-avito, 2026-09-28) is exactly the
 * case the repair exists for, and on a 45 MB overlay it never fits alongside.
 */
export function shouldReplaceXrayInPlace(
  snapshotPayload: unknown,
  requested: boolean,
): boolean {
  if (requested) {
    return true;
  }
  if (!snapshotPayload || typeof snapshotPayload !== "object") {
    return false;
  }
  const events = (snapshotPayload as { safetyEvents?: unknown }).safetyEvents;
  if (!Array.isArray(events)) {
    return false;
  }
  return events.some(
    (event) =>
      !!event &&
      typeof event === "object" &&
      (event as { type?: unknown }).type === proxyRuntimeUnusableEventType,
  );
}
