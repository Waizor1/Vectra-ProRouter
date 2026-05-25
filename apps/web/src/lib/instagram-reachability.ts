import type { RouterInstagramReachability } from "@vectra/contracts";

type InstagramReachabilityCheckLike = {
  label?: string | null;
  reachable?: boolean | null;
  checkedAt?: string | null;
  targetUrl?: string | null;
  statusCode?: number | null;
  error?: string | null;
};

type InstagramReachabilityLike =
  | (Partial<RouterInstagramReachability> & InstagramReachabilityCheckLike)
  | null
  | undefined;

type InstagramReachabilityStatus =
  | "unknown"
  | "reachable"
  | "partial"
  | "blocked";

export type InstagramReachabilityCheckSummary = {
  label: string;
  reachable: boolean;
  checkedAt?: string | null;
  detail: string;
};

function formatTarget(
  targetUrl: string | null | undefined,
  fallbackLabel?: string | null,
) {
  if (fallbackLabel?.trim()) {
    return fallbackLabel.trim();
  }

  if (!targetUrl) {
    return "instagram.com";
  }

  try {
    return new URL(targetUrl).hostname.replace(/^www\./, "");
  } catch {
    return targetUrl;
  }
}

function shorten(value: string, maxLength: number) {
  if (value.length <= maxLength) {
    return value;
  }

  return `${value.slice(0, maxLength - 3)}...`;
}

function getChecks(probe: InstagramReachabilityLike) {
  if (!probe) {
    return [] as InstagramReachabilityCheckLike[];
  }

  if (Array.isArray(probe.checks) && probe.checks.length > 0) {
    return probe.checks;
  }

  if (probe.targetUrl || probe.label) {
    return [probe];
  }

  return [] as InstagramReachabilityCheckLike[];
}

export function getInstagramReachabilityStatus(
  probe: InstagramReachabilityLike,
): InstagramReachabilityStatus {
  if (!probe) {
    return "unknown";
  }

  if (probe.status === "reachable") {
    return "reachable";
  }
  if (probe.status === "partial") {
    return "partial";
  }
  if (probe.status === "blocked") {
    return "blocked";
  }

  const checks = getChecks(probe);
  if (checks.length > 1 || typeof probe.totalCount === "number") {
    const totalCount = probe.totalCount ?? checks.length;
    const reachableCount =
      probe.reachableCount ??
      checks.filter((entry) => entry.reachable === true).length;

    if (totalCount <= 0) {
      return "unknown";
    }
    if (reachableCount === totalCount) {
      return "reachable";
    }
    if (reachableCount === 0) {
      return "blocked";
    }
    return "partial";
  }

  return probe.reachable ? "reachable" : "blocked";
}

export function formatInstagramReachabilityLabel(
  probe: InstagramReachabilityLike,
) {
  switch (getInstagramReachabilityStatus(probe)) {
    case "reachable":
      return "доступна";
    case "partial":
      return "частично доступна";
    case "blocked":
      return "недоступна";
    default:
      return "нет данных";
  }
}

function describeSingleCheck(check: InstagramReachabilityCheckLike) {
  const label = formatTarget(check.targetUrl, check.label);

  if (check.reachable) {
    return `${label} отвечает`;
  }

  if (typeof check.statusCode === "number" && check.statusCode > 0) {
    return `${label} вернул HTTP ${check.statusCode}`;
  }

  if (check.error) {
    return `${label} недоступен: ${shorten(check.error, 96)}`;
  }

  return `${label} недоступен`;
}

export function describeInstagramReachability(probe: InstagramReachabilityLike) {
  if (!probe) {
    return "Агент не прислал проверку Instagram: service-probes выполняются редко и пропускаются при low-memory или неработающем PassWall.";
  }

  const checks = getChecks(probe);
  if (checks.length <= 1) {
    return checks[0]
      ? describeSingleCheck(checks[0])
      : "Нет телеметрии Instagram.";
  }

  const status = getInstagramReachabilityStatus(probe);
  const totalCount = probe.totalCount ?? checks.length;
  const reachableCount =
    probe.reachableCount ??
    checks.filter((entry) => entry.reachable === true).length;
  const failingLabels = checks
    .filter((entry) => !entry.reachable)
    .map((entry) => formatTarget(entry.targetUrl, entry.label));

  if (status === "reachable") {
    return `Отвечают все ${totalCount} цели Instagram.`;
  }

  if (status === "partial") {
    const suffix =
      failingLabels.length > 0
        ? ` Не отвечают: ${shorten(failingLabels.join(", "), 80)}.`
        : "";
    return `Отвечают ${reachableCount} из ${totalCount} целей Instagram.${suffix}`;
  }

  return `Не отвечает ни одна из ${totalCount} целей Instagram.`;
}

export function getInstagramReachabilityChecks(
  probe: InstagramReachabilityLike,
): InstagramReachabilityCheckSummary[] {
  return getChecks(probe).map((entry) => ({
    label: formatTarget(entry.targetUrl, entry.label),
    reachable: entry.reachable === true,
    checkedAt: entry.checkedAt,
    detail: describeSingleCheck(entry),
  }));
}

export function hasInstagramReachabilityProblem(
  probe: InstagramReachabilityLike,
) {
  const status = getInstagramReachabilityStatus(probe);
  return status === "partial" || status === "blocked";
}
