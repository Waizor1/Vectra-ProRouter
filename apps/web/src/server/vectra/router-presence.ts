import { env } from "~/env";

type RouterStatus =
  | "pending"
  | "active"
  | "offline"
  | "direct"
  | "rescue"
  | "disabled";

const HEARTBEAT_GRACE_MULTIPLIER = 3;
const MINIMUM_OFFLINE_WINDOW_SECONDS = 180;

export function getRouterOfflineThresholdMs(
  pollingIntervalSeconds = Number(env.VECTRA_POLLING_INTERVAL_SECONDS),
) {
  const thresholdSeconds = Math.max(
    pollingIntervalSeconds * HEARTBEAT_GRACE_MULTIPLIER,
    MINIMUM_OFFLINE_WINDOW_SECONDS,
  );

  return thresholdSeconds * 1000;
}

export function isRouterReachable(
  lastSeenAt: Date | null | undefined,
  now = new Date(),
) {
  if (!lastSeenAt) {
    return false;
  }

  return now.getTime() - lastSeenAt.getTime() <= getRouterOfflineThresholdMs();
}

export function getEffectiveRouterStatus(
  status: RouterStatus,
  lastSeenAt: Date | null | undefined,
  now = new Date(),
): RouterStatus {
  if (!isRouterReachable(lastSeenAt, now)) {
    return "offline";
  }

  return status;
}

export function hasActiveDirectMode(
  status: RouterStatus,
  lastSeenAt: Date | null | undefined,
  now = new Date(),
) {
  return getEffectiveRouterStatus(status, lastSeenAt, now) === "direct";
}
