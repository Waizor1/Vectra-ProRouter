import type {
  RouterInventory,
  RouterSafetyEvent,
  RouterTelegramReachability,
  RouterYoutubeReachability,
  SupportState,
} from "@vectra/contracts";

import {
  describeRouterMemory,
  type RouterMemoryLevel,
  type RouterMemoryStatus,
} from "~/lib/router-memory";
import {
  formatTelegramReachabilityLabel,
  getTelegramReachabilityStatus,
} from "~/lib/telegram-reachability";
import {
  formatYoutubeReachabilityLabel,
  getYoutubeReachabilityStatus,
} from "~/lib/youtube-reachability";

import type { ConfigSourceMode } from "./config-trust";
import type {
  FleetRoutePolicyCompliance,
  FleetRoutePolicyStatus,
} from "./fleet-route-policy";
import {
  getEffectiveRouterStatus,
  getRouterOfflineThresholdMs,
} from "./router-presence";

type MonitoringOperationalState =
  | "stable"
  | "recovery"
  | "offline"
  | "review"
  | "blocked";

type MonitoringFreshnessState = "fresh" | "watch" | "offline" | "never";
type MonitoringAlertSeverity = "critical" | "warning" | "info";
type MonitoringAlertKind =
  | "direct_mode"
  | "incident"
  | "offline"
  | "telegram_degraded"
  | "youtube_degraded"
  | "import_review"
  | "out_of_sync"
  | "reimport_needed"
  | "fleet_policy_violation"
  | "awaiting_import"
  | "low_memory"
  | "router_safety"
  | "blocked_support";

type FleetMonitoringConfigTrust = {
  liveConfigAvailable: boolean;
  requiresReimport: boolean;
  digestMismatch: boolean;
  configSourceMode: ConfigSourceMode;
  lastLiveImportAt: string | null;
  lastCheckInAt: string | null;
};

type MonitoringChartTone = "good" | "warning" | "critical" | "default";
type MonitoringServiceFilterValue =
  | "telegram_degraded"
  | "youtube_degraded"
  | "service_unknown";

export type FleetMonitoringRouterInput = {
  id: string;
  name: string;
  status: "pending" | "active" | "offline" | "direct" | "rescue" | "disabled";
  importState: "awaiting_import" | "import_review" | "approved" | "out_of_sync";
  supportState: SupportState;
  lastSeenAt: Date | null;
  selectedNode: string;
  passwallEnabled: boolean;
  nodeCount: number;
  subscriptionCount: number;
  controllerVersion: string;
  passwallVersion: string;
  components: Record<string, string>;
  telegramReachability?: RouterTelegramReachability | null;
  youtubeReachability?: RouterYoutubeReachability | null;
  resources?: Pick<
    RouterInventory["resources"],
    "memoryTotalMb" | "memoryAvailableMb"
  > | null;
  safetyEvents?: RouterSafetyEvent[] | null;
  queuedJobCount: number;
  lastRescueReason: string | null;
  configTrust?: Partial<FleetMonitoringConfigTrust> | null;
  fleetPolicyCompliance?: FleetRoutePolicyCompliance | null;
  openIncident: {
    type:
      | "proxy_outage"
      | "server_unreachable"
      | "subscription_degraded"
      | "entered_direct_mode"
      | "recovered";
    reason: string;
    openedAt: Date | null;
  } | null;
};

type FleetMonitoringChartFilter = {
  kind: "operational" | "freshness" | "memory" | "service" | "policy";
  value: string;
};

type FleetMonitoringChartSlice = {
  key: string;
  label: string;
  count: number;
  percent: number;
  tone: MonitoringChartTone;
  description: string;
  filter: FleetMonitoringChartFilter;
};

type FleetMonitoringChart = {
  id: "operational" | "freshness" | "memory" | "service" | "policy";
  title: string;
  description: string;
  slices: FleetMonitoringChartSlice[];
};

type FleetMonitoringStat = {
  label: string;
  value: string;
  tone: "default" | "good" | "warning" | "critical";
  hint: string;
};

type FleetMonitoringAlert = {
  id: string;
  kind: MonitoringAlertKind;
  severity: MonitoringAlertSeverity;
  routerId: string;
  routerName: string;
  href: string;
  title: string;
  description: string;
  openedAt: string | null;
  filters: {
    operational: MonitoringOperationalState;
    freshness: MonitoringFreshnessState;
    memory: RouterMemoryLevel;
    service?: MonitoringServiceFilterValue | null;
    policy?: FleetRoutePolicyStatus | null;
  };
};

type FleetMonitoringRouter = {
  id: string;
  name: string;
  selectedNode: string;
  passwallEnabled: boolean;
  directMode: boolean;
  offline: boolean;
  reachable: boolean;
  statusLabel: string;
  nodeCount: number;
  subscriptionCount: number;
  controllerVersion: string;
  passwallVersion: string;
  components: Record<string, string>;
  telegramReachability?: RouterTelegramReachability | null;
  youtubeReachability?: RouterYoutubeReachability | null;
  safetyEvents: RouterSafetyEvent[];
  memory: RouterMemoryStatus;
  lastSeen: string;
  pendingChanges: number;
  lastRescue: string;
  importState: string;
  needsImportReview: boolean;
  configTrust: FleetMonitoringConfigTrust;
  fleetPolicyCompliance: FleetRoutePolicyCompliance;
  lastSeenAt: string | null;
  operationalState: MonitoringOperationalState;
  freshnessState: MonitoringFreshnessState;
  supportState: SupportState;
  alertKinds: MonitoringAlertKind[];
};

export type FleetMonitoringSnapshot = {
  generatedAt: string;
  notificationNote: string;
  stats: FleetMonitoringStat[];
  charts: FleetMonitoringChart[];
  alerts: FleetMonitoringAlert[];
  routers: FleetMonitoringRouter[];
  totalAlerts: number;
};

function normalizeConfigTrust(
  trust: FleetMonitoringRouterInput["configTrust"],
): FleetMonitoringConfigTrust {
  return {
    liveConfigAvailable: Boolean(trust?.liveConfigAvailable),
    requiresReimport: Boolean(trust?.requiresReimport),
    digestMismatch: Boolean(trust?.digestMismatch),
    configSourceMode:
      trust?.configSourceMode === "live-import" ||
      trust?.configSourceMode === "authoritative" ||
      trust?.configSourceMode === "stale-authoritative" ||
      trust?.configSourceMode === "inventory-only"
        ? trust.configSourceMode
        : "inventory-only",
    lastLiveImportAt: trust?.lastLiveImportAt ?? null,
    lastCheckInAt: trust?.lastCheckInAt ?? null,
  };
}

function normalizeFleetPolicyCompliance(
  compliance: FleetMonitoringRouterInput["fleetPolicyCompliance"],
): FleetRoutePolicyCompliance {
  return (
    compliance ?? {
      policyVersion: "2026-05-12-v1",
      status: "unknown",
      checked: false,
      exempt: false,
      exceptionReason: null,
      canNormalize: false,
      matchedSlots: [],
      mismatches: [],
      summary: "No full live PassWall import is available for fleet policy matching.",
    }
  );
}

function formatRelativeTime(value: Date | null | undefined, now = new Date()) {
  if (!value) {
    return "никогда";
  }

  const diffMs = Math.max(0, now.getTime() - value.getTime());
  const diffMinutes = Math.round(diffMs / 60000);
  if (diffMinutes < 1) {
    return "только что";
  }
  if (diffMinutes < 60) {
    return `${diffMinutes} мин назад`;
  }

  const diffHours = Math.round(diffMinutes / 60);
  if (diffHours < 24) {
    return `${diffHours} ч назад`;
  }

  const diffDays = Math.round(diffHours / 24);
  return `${diffDays} дн назад`;
}

function formatIso(value: Date | null | undefined) {
  return value ? value.toISOString() : null;
}

function getFreshnessState(
  lastSeenAt: Date | null,
  now: Date,
  offlineThresholdMs: number,
): MonitoringFreshnessState {
  if (!lastSeenAt) {
    return "never";
  }

  const diffMs = Math.max(0, now.getTime() - lastSeenAt.getTime());
  if (diffMs > offlineThresholdMs) {
    return "offline";
  }

  if (diffMs > offlineThresholdMs / 2) {
    return "watch";
  }

  return "fresh";
}

function getOperationalState(
  input: FleetMonitoringRouterInput,
  reachable: boolean,
): MonitoringOperationalState {
  if (!reachable) {
    return "offline";
  }

  if (input.supportState === "blocked") {
    return "blocked";
  }

  if (
    input.status === "direct" ||
    input.status === "rescue" ||
    (input.openIncident !== null && input.openIncident.type !== "recovered")
  ) {
    return "recovery";
  }

  if (
    input.importState !== "approved" ||
    input.configTrust?.requiresReimport ||
    input.configTrust?.digestMismatch ||
    input.fleetPolicyCompliance?.status === "violation"
  ) {
    return "review";
  }

  return "stable";
}

function getStatusLabel(input: FleetMonitoringRouterInput, reachable: boolean) {
  if (!reachable) {
    return "Нет связи";
  }

  if (input.status === "rescue") {
    return "Rescue / восстановление";
  }

  if (input.status === "direct") {
    return "Прямой режим";
  }

  if (input.status === "disabled") {
    return "Отключён";
  }

  if (input.status === "pending") {
    return "Ожидает первичный импорт";
  }

  return input.passwallEnabled ? "Прокси-режим" : "PassWall2 выключен";
}

function getIncidentSeverity(
  type: FleetMonitoringRouterInput["openIncident"] extends infer T
    ? T extends { type: infer U }
      ? U
      : never
    : never,
): MonitoringAlertSeverity {
  switch (type) {
    case "subscription_degraded":
      return "warning";
    case "recovered":
      return "info";
    default:
      return "critical";
  }
}

function getIncidentTitle(
  type: FleetMonitoringRouterInput["openIncident"] extends infer T
    ? T extends { type: infer U }
      ? U
      : never
    : never,
) {
  switch (type) {
    case "proxy_outage":
      return "Прокси-маршрут деградировал";
    case "server_unreachable":
      return "Апстрим недоступен";
    case "subscription_degraded":
      return "Подписка деградировала";
    case "entered_direct_mode":
      return "Роутер ушёл в прямой режим";
    case "recovered":
      return "Роутер восстановился";
    default:
      return "Открыт rescue-инцидент";
  }
}

function buildAlerts(
  router: FleetMonitoringRouter,
  incident: FleetMonitoringRouterInput["openIncident"],
): FleetMonitoringAlert[] {
  const alerts: FleetMonitoringAlert[] = [];
  const href = `/routers/${router.id}`;
  const routerFilters = {
    operational: router.operationalState,
    freshness: router.freshnessState,
    memory: router.memory.level,
    service: null,
    policy: router.fleetPolicyCompliance.status,
  } as const;

  if (router.offline) {
    alerts.push({
      id: `offline:${router.id}`,
      kind: "offline",
      severity: "critical",
      routerId: router.id,
      routerName: router.name,
      href,
      title: "Нет свежей связи",
      description:
        router.lastSeenAt === null
          ? "Контроллер ещё не сделал первый check-in."
          : `Последний известный check-in: ${router.lastSeen}.`,
      openedAt: router.lastSeenAt,
      filters: routerFilters,
    });
  }

  if (router.directMode) {
    alerts.push({
      id: `direct:${router.id}`,
      kind: "direct_mode",
      severity: "critical",
      routerId: router.id,
      routerName: router.name,
      href,
      title:
        router.statusLabel === "Rescue / восстановление"
          ? "Роутер в rescue-сценарии"
          : "Роутер в прямом режиме",
      description:
        incident?.reason ??
        router.lastRescue.replace(/^Последнее известное rescue:\s*/i, ""),
      openedAt: incident?.openedAt
        ? formatIso(incident.openedAt)
        : router.lastSeenAt,
      filters: routerFilters,
    });
  } else if (
    incident &&
    incident.type !== "recovered" &&
    router.operationalState === "recovery"
  ) {
    alerts.push({
      id: `incident:${router.id}:${incident.type}`,
      kind: "incident",
      severity: getIncidentSeverity(incident.type),
      routerId: router.id,
      routerName: router.name,
      href,
      title: getIncidentTitle(incident.type),
      description: incident.reason,
      openedAt: formatIso(incident.openedAt),
      filters: routerFilters,
    });
  }

  if (router.reachable) {
    const telegramStatus = getTelegramReachabilityStatus(
      router.telegramReachability,
    );
    if (telegramStatus === "partial" || telegramStatus === "blocked") {
      alerts.push({
        id: `telegram:${router.id}:${telegramStatus}`,
        kind: "telegram_degraded",
        severity: telegramStatus === "blocked" ? "critical" : "warning",
        routerId: router.id,
        routerName: router.name,
        href,
        title:
          telegramStatus === "blocked"
            ? "Telegram не отвечает"
            : "Telegram частично деградировал",
        description: `Telegram ${formatTelegramReachabilityLabel(router.telegramReachability)}: сервисные probes уже не полностью зелёные.`,
        openedAt: router.telegramReachability?.checkedAt ?? router.lastSeenAt,
        filters: {
          ...routerFilters,
          service: "telegram_degraded",
        },
      });
    }

    const youtubeStatus = getYoutubeReachabilityStatus(
      router.youtubeReachability,
    );
    if (youtubeStatus === "partial" || youtubeStatus === "blocked") {
      alerts.push({
        id: `youtube:${router.id}:${youtubeStatus}`,
        kind: "youtube_degraded",
        severity: youtubeStatus === "blocked" ? "critical" : "warning",
        routerId: router.id,
        routerName: router.name,
        href,
        title:
          youtubeStatus === "blocked"
            ? "YouTube не отвечает"
            : "YouTube частично деградировал",
        description: `YouTube ${formatYoutubeReachabilityLabel(router.youtubeReachability)}: сервисные probes уже не полностью зелёные.`,
        openedAt: router.youtubeReachability?.checkedAt ?? router.lastSeenAt,
        filters: {
          ...routerFilters,
          service: "youtube_degraded",
        },
      });
    }
  }

  switch (router.importState) {
    case "import_review":
      alerts.push({
        id: `import_review:${router.id}`,
        kind: "import_review",
        severity: "warning",
        routerId: router.id,
        routerName: router.name,
        href,
        title: "Нужна проверка импорта",
        description:
          "Панель ждёт, что оператор примет импортированную конфигурацию как эталон.",
        openedAt: router.lastSeenAt,
        filters: routerFilters,
      });
      break;
    case "out_of_sync":
      alerts.push({
        id: `out_of_sync:${router.id}`,
        kind: "out_of_sync",
        severity: "warning",
        routerId: router.id,
        routerName: router.name,
        href,
        title: "Обнаружен дрейф конфигурации",
        description:
          "Нужно сравнить текущий импорт роутера с эталоном и решить, что делать дальше.",
        openedAt: router.lastSeenAt,
        filters: routerFilters,
      });
      break;
    case "awaiting_import":
      alerts.push({
        id: `awaiting_import:${router.id}`,
        kind: "awaiting_import",
        severity: "info",
        routerId: router.id,
        routerName: router.name,
        href,
        title: "Ожидается первый импорт",
        description:
          "Контроллер зарегистрирован, но authoritative-конфигурация ещё не подтверждена.",
        openedAt: router.lastSeenAt,
        filters: routerFilters,
      });
      break;
  }

  if (
    router.importState === "approved" &&
    router.configTrust.requiresReimport
  ) {
    alerts.push({
      id: `reimport_needed:${router.id}`,
      kind: "reimport_needed",
      severity: "warning",
      routerId: router.id,
      routerName: router.name,
      href,
      title: router.configTrust.digestMismatch
        ? "Нужен re-import: live config уже ушёл вперёд"
        : "Нужен re-import: deep config не подтверждён",
      description:
        "Свежий snapshot уже есть, но matching full import для глубокой PassWall-конфигурации пока отсутствует.",
      openedAt: router.configTrust.lastCheckInAt ?? router.lastSeenAt,
      filters: routerFilters,
    });
  }

  if (router.fleetPolicyCompliance.status === "violation") {
    alerts.push({
      id: `fleet_policy:${router.id}:${router.fleetPolicyCompliance.policyVersion}`,
      kind: "fleet_policy_violation",
      severity: "warning",
      routerId: router.id,
      routerName: router.name,
      href,
      title: "Маршруты не совпадают с fleet package",
      description: router.fleetPolicyCompliance.summary,
      openedAt: router.configTrust.lastCheckInAt ?? router.lastSeenAt,
      filters: routerFilters,
    });
  }

  if (router.supportState === "blocked" && !router.offline) {
    alerts.push({
      id: `blocked:${router.id}`,
      kind: "blocked_support",
      severity: "warning",
      routerId: router.id,
      routerName: router.name,
      href,
      title: "Операции заблокированы support-профилем",
      description:
        "Плата вне pilot/certified контура, поэтому destructive-действия из панели отключены.",
      openedAt: router.lastSeenAt,
      filters: routerFilters,
    });
  }

  if (
    router.reachable &&
    (router.memory.level === "critical" || router.memory.level === "warning")
  ) {
    alerts.push({
      id: `low_memory:${router.id}`,
      kind: "low_memory",
      severity: router.memory.level === "critical" ? "critical" : "warning",
      routerId: router.id,
      routerName: router.name,
      href,
      title:
        router.memory.level === "critical"
          ? "RAM критически низкая"
          : "RAM близко к low-memory зоне",
      description: `${router.memory.summary}. ${router.memory.detail}`,
      openedAt: router.lastSeenAt,
      filters: routerFilters,
    });
  }

  for (const event of router.safetyEvents) {
    if (event.severity !== "critical" && event.severity !== "warning") {
      continue;
    }
    alerts.push({
      id: `router_safety:${router.id}:${event.type}:${event.component ?? "router"}:${event.observedAt}`,
      kind: "router_safety",
      severity: event.severity,
      routerId: router.id,
      routerName: router.name,
      href,
      title:
        event.severity === "critical"
          ? "Критическое событие на роутере"
          : "Предупреждение от контроллера",
      description: [event.message, event.evidence]
        .filter((part) => part && part.trim().length > 0)
        .join(" — "),
      openedAt: event.observedAt ?? router.lastSeenAt,
      filters: routerFilters,
    });
  }

  return alerts;
}

function compareAlerts(
  left: FleetMonitoringAlert,
  right: FleetMonitoringAlert,
) {
  const severityScore: Record<MonitoringAlertSeverity, number> = {
    critical: 3,
    warning: 2,
    info: 1,
  };

  const severityDiff =
    severityScore[right.severity] - severityScore[left.severity];
  if (severityDiff !== 0) {
    return severityDiff;
  }

  const leftTime = left.openedAt ? Date.parse(left.openedAt) : 0;
  const rightTime = right.openedAt ? Date.parse(right.openedAt) : 0;
  return rightTime - leftTime;
}

function compareRouters(
  left: FleetMonitoringRouter,
  right: FleetMonitoringRouter,
) {
  const stateScore: Record<MonitoringOperationalState, number> = {
    recovery: 5,
    offline: 4,
    blocked: 3,
    review: 2,
    stable: 1,
  };

  const stateDiff =
    stateScore[right.operationalState] - stateScore[left.operationalState];
  if (stateDiff !== 0) {
    return stateDiff;
  }

  const memoryScore: Record<RouterMemoryLevel, number> = {
    critical: 4,
    warning: 3,
    unknown: 2,
    good: 1,
  };
  const memoryDiff =
    memoryScore[right.memory.level] - memoryScore[left.memory.level];
  if (memoryDiff !== 0) {
    return memoryDiff;
  }

  const queueDiff = right.pendingChanges - left.pendingChanges;
  if (queueDiff !== 0) {
    return queueDiff;
  }

  const leftTime = left.lastSeenAt ? Date.parse(left.lastSeenAt) : 0;
  const rightTime = right.lastSeenAt ? Date.parse(right.lastSeenAt) : 0;
  return rightTime - leftTime;
}

function buildSlices<T extends string>(options: {
  items: readonly {
    key: T;
    label: string;
    tone: MonitoringChartTone;
    description: string;
  }[];
  counts: Record<T, number>;
  total: number;
  filterKind: FleetMonitoringChartFilter["kind"];
}) {
  return options.items.map((item) => {
    const count = options.counts[item.key] ?? 0;
    const percent =
      options.total > 0 ? Math.round((count / options.total) * 100) : 0;

    return {
      key: item.key,
      label: item.label,
      count,
      percent,
      tone: item.tone,
      description: item.description,
      filter: {
        kind: options.filterKind,
        value: item.key,
      },
    };
  });
}

export function buildFleetMonitoringSnapshot(args: {
  routers: FleetMonitoringRouterInput[];
  openIncidentCount: number;
  queuedJobs: number;
  now?: Date;
  offlineThresholdMs?: number;
}): FleetMonitoringSnapshot {
  const now = args.now ?? new Date();
  const offlineThresholdMs =
    args.offlineThresholdMs ?? getRouterOfflineThresholdMs();

  const operationalCounts: Record<MonitoringOperationalState, number> = {
    stable: 0,
    recovery: 0,
    offline: 0,
    review: 0,
    blocked: 0,
  };
  const freshnessCounts: Record<MonitoringFreshnessState, number> = {
    fresh: 0,
    watch: 0,
    offline: 0,
    never: 0,
  };
  const memoryCounts: Record<RouterMemoryLevel, number> = {
    good: 0,
    warning: 0,
    critical: 0,
    unknown: 0,
  };
  const serviceCounts: Record<MonitoringServiceFilterValue, number> = {
    telegram_degraded: 0,
    youtube_degraded: 0,
    service_unknown: 0,
  };
  const policyCounts: Record<FleetRoutePolicyStatus, number> = {
    compliant: 0,
    violation: 0,
    exempt: 0,
    unknown: 0,
  };

  const routers = args.routers.map((input) => {
    const effectiveStatus = getEffectiveRouterStatus(
      input.status,
      input.lastSeenAt,
      now,
    );
    const reachable = effectiveStatus !== "offline";
    const freshnessState = getFreshnessState(
      input.lastSeenAt,
      now,
      offlineThresholdMs,
    );
    const operationalState = getOperationalState(input, reachable);
    const directMode =
      reachable && (input.status === "direct" || input.status === "rescue");
    const configTrust = normalizeConfigTrust(input.configTrust);
    const fleetPolicyCompliance = normalizeFleetPolicyCompliance(
      input.fleetPolicyCompliance,
    );
    const memory = describeRouterMemory(input.resources);
    const router: FleetMonitoringRouter = {
      id: input.id,
      name: input.name,
      selectedNode: input.selectedNode,
      passwallEnabled: reachable && input.passwallEnabled,
      directMode,
      offline: !reachable,
      reachable,
      statusLabel: getStatusLabel(input, reachable),
      nodeCount: input.nodeCount,
      subscriptionCount: input.subscriptionCount,
      controllerVersion: input.controllerVersion,
      passwallVersion: input.passwallVersion,
      components: input.components,
      telegramReachability: input.telegramReachability ?? null,
      youtubeReachability: input.youtubeReachability ?? null,
      safetyEvents: input.safetyEvents ?? [],
      memory,
      lastSeen: formatRelativeTime(input.lastSeenAt, now),
      pendingChanges: input.queuedJobCount,
      lastRescue:
        input.lastRescueReason && input.lastRescueReason.length > 0
          ? reachable
            ? input.lastRescueReason
            : `Последнее известное rescue: ${input.lastRescueReason}`
          : "Нет недавних rescue-событий",
      importState: input.importState,
      needsImportReview:
        input.importState !== "approved" ||
        configTrust.requiresReimport ||
        fleetPolicyCompliance.status === "violation",
      configTrust,
      fleetPolicyCompliance,
      lastSeenAt: formatIso(input.lastSeenAt),
      operationalState,
      freshnessState,
      supportState: input.supportState,
      alertKinds: [],
    };

    operationalCounts[operationalState] += 1;
    freshnessCounts[freshnessState] += 1;
    memoryCounts[memory.level] += 1;
    policyCounts[fleetPolicyCompliance.status] += 1;

    if (reachable) {
      const telegramStatus = getTelegramReachabilityStatus(
        router.telegramReachability,
      );
      const youtubeStatus = getYoutubeReachabilityStatus(
        router.youtubeReachability,
      );
      if (telegramStatus === "partial" || telegramStatus === "blocked") {
        serviceCounts.telegram_degraded += 1;
      }
      if (youtubeStatus === "partial" || youtubeStatus === "blocked") {
        serviceCounts.youtube_degraded += 1;
      }
      if (telegramStatus === "unknown" || youtubeStatus === "unknown") {
        serviceCounts.service_unknown += 1;
      }
    }

    return {
      router,
      incident: input.openIncident,
    };
  });

  const alerts = routers
    .flatMap(({ router, incident }) => {
      const nextAlerts = buildAlerts(router, incident);
      router.alertKinds = nextAlerts.map((alert) => alert.kind);
      return nextAlerts;
    })
    .sort(compareAlerts);

  const sortedRouters = routers
    .map(({ router }) => router)
    .sort(compareRouters);

  const problemRouters = sortedRouters.filter((router) =>
    ["recovery", "offline", "blocked"].includes(router.operationalState),
  ).length;
  const reviewRouters = sortedRouters.filter(
    (router) => router.operationalState === "review",
  ).length;
  const ramRiskRouters = memoryCounts.warning + memoryCounts.critical;
  const availableMemoryValues = sortedRouters.flatMap((router) =>
    router.memory.availableMb !== null ? [router.memory.availableMb] : [],
  );
  const minimumAvailableMemoryMb =
    availableMemoryValues.length > 0
      ? Math.min(...availableMemoryValues)
      : null;

  return {
    generatedAt: now.toISOString(),
    notificationNote:
      "Браузерные уведомления срабатывают, пока вкладка панели открыта. Это быстрый local alerting без отдельного push-сервиса.",
    stats: [
      {
        label: "Всего устройств",
        value: String(args.routers.length),
        tone: "default",
        hint: "Все роутеры, которые уже зарегистрированы в панели.",
      },
      {
        label: "В строю",
        value: String(operationalCounts.stable),
        tone: operationalCounts.stable > 0 ? "good" : "default",
        hint: "Связь свежая, approved-эталон подтверждён и matching import для deep config не вызывает сомнений.",
      },
      {
        label: "Проблемные",
        value: String(problemRouters),
        tone: problemRouters > 0 ? "warning" : "default",
        hint: "Офлайн, recovery/direct mode или blocked support-профиль.",
      },
      {
        label: "Импорт / drift",
        value: String(reviewRouters),
        tone: reviewRouters > 0 ? "warning" : "default",
        hint: "Нужно принять import, проверить configTrust drift, fleet policy drift или перечитать live-конфиг с роутера.",
      },
      {
        label: "Policy drift",
        value: String(policyCounts.violation),
        tone: policyCounts.violation > 0 ? "warning" : "good",
        hint: "Роутеры, где live ShuntRules не совпадают с общим fleet server package.",
      },
      {
        label: "Открытые инциденты",
        value: String(args.openIncidentCount),
        tone: args.openIncidentCount > 0 ? "warning" : "default",
        hint: "Текущие rescue/health incident записи по парку.",
      },
      {
        label: "RAM риск",
        value: String(ramRiskRouters),
        tone:
          memoryCounts.critical > 0
            ? "critical"
            : ramRiskRouters > 0
              ? "warning"
              : "default",
        hint: "Роутеры, где последний check-in показывает RAM ниже безопасного запаса.",
      },
      {
        label: "Мин. RAM",
        value:
          minimumAvailableMemoryMb !== null
            ? `${Math.round(minimumAvailableMemoryMb)} МБ`
            : "нет данных",
        tone:
          memoryCounts.critical > 0
            ? "critical"
            : ramRiskRouters > 0
              ? "warning"
              : minimumAvailableMemoryMb !== null
                ? "good"
                : "default",
        hint: "Минимальное числовое значение свободной RAM по свежим inventory snapshots.",
      },
      {
        label: "Задания в очереди",
        value: String(args.queuedJobs),
        tone: args.queuedJobs > 0 ? "warning" : "default",
        hint: "Jobs, которые ещё не завершились на роутерах.",
      },
    ],
    charts: [
      {
        id: "operational",
        title: "Операционный контур",
        description:
          "Клик по сегменту отфильтрует парк ниже и оставит только нужный operational lane.",
        slices: buildSlices({
          items: [
            {
              key: "stable",
              label: "В строю",
              tone: "good",
              description:
                "Свежая связь, approved-эталон и без активных проблем.",
            },
            {
              key: "recovery",
              label: "Recovery",
              tone: "critical",
              description:
                "Direct/rescue mode или активный incident, где нужно реагировать быстро.",
            },
            {
              key: "offline",
              label: "Нет связи",
              tone: "critical",
              description:
                "Контроллер давно не делал check-in, состояние роутера уже stale.",
            },
            {
              key: "review",
              label: "Проверка",
              tone: "warning",
              description:
                "Импорт, drift или состояние вне confirmed live-import контура.",
            },
            {
              key: "blocked",
              label: "Заблокированы",
              tone: "warning",
              description:
                "Устройства вне pilot/certified контура, где destructive-действия запрещены.",
            },
          ],
          counts: operationalCounts,
          total: args.routers.length,
          filterKind: "operational",
        }),
      },
      {
        id: "freshness",
        title: "Свежесть связи",
        description:
          "Показывает, насколько свежий последний check-in по текущему heartbeat window панели.",
        slices: buildSlices({
          items: [
            {
              key: "fresh",
              label: "Свежая",
              tone: "good",
              description:
                "Недавний check-in, роутер явно на связи прямо сейчас.",
            },
            {
              key: "watch",
              label: "На грани",
              tone: "warning",
              description:
                "Связь ещё считается живой, но уже близко к offline threshold.",
            },
            {
              key: "offline",
              label: "Офлайн",
              tone: "critical",
              description:
                "Heartbeat window уже пройден, текущие runtime-данные считаются stale.",
            },
            {
              key: "never",
              label: "Без check-in",
              tone: "default",
              description:
                "Устройство зарегистрировано, но ещё не прислало ни одного check-in.",
            },
          ],
          counts: freshnessCounts,
          total: args.routers.length,
          filterKind: "freshness",
        }),
      },
      {
        id: "memory",
        title: "RAM на роутерах",
        description:
          "Свободная RAM из последнего check-in. Warning начинается до зоны, где controller уже должен экономить probes.",
        slices: buildSlices({
          items: [
            {
              key: "good",
              label: "RAM OK",
              tone: "good",
              description:
                "Запаса RAM достаточно для штатных probes и PassWall runtime.",
            },
            {
              key: "warning",
              label: "Низкая",
              tone: "warning",
              description:
                "Меньше 64 МБ или 28% доступно: роутер близко к low-memory зоне.",
            },
            {
              key: "critical",
              label: "Критично",
              tone: "critical",
              description:
                "Меньше 48 МБ или 20% доступно: риск OOM и обрыва Xray/PassWall.",
            },
            {
              key: "unknown",
              label: "Нет данных",
              tone: "default",
              description:
                "Последний snapshot не содержит usable RAM telemetry.",
            },
          ],
          counts: memoryCounts,
          total: args.routers.length,
          filterKind: "memory",
        }),
      },
      {
        id: "policy",
        title: "Fleet server package",
        description:
          "Отдельная проверка live ShuntRules против общего пакета серверов; это не configTrust/revision sync.",
        slices: buildSlices({
          items: [
            {
              key: "compliant",
              label: "Policy OK",
              tone: "good",
              description:
                "Live bindings совпадают с canonical fleet route policy.",
            },
            {
              key: "violation",
              label: "Policy drift",
              tone: policyCounts.violation > 0 ? "warning" : "good",
              description:
                "Роутер синхронен с revision, но выбранные серверы не из fleet package.",
            },
            {
              key: "exempt",
              label: "Исключения",
              tone: "default",
              description:
                "Явно исключены из общего пакета серверов, например hh.",
            },
            {
              key: "unknown",
              label: "Нет deep import",
              tone: policyCounts.unknown > 0 ? "warning" : "default",
              description:
                "Нет полного live import, поэтому semantic policy нельзя проверить.",
            },
          ],
          counts: policyCounts,
          total: args.routers.length,
          filterKind: "policy",
        }),
      },
      {
        id: "service",
        title: "Telegram / YouTube",
        description:
          "Быстрые срезы по сервисным probes. Срезы не взаимоисключающие: один роутер может попасть сразу в несколько.",
        slices: buildSlices({
          items: [
            {
              key: "telegram_degraded",
              label: "Telegram сбои",
              tone:
                serviceCounts.telegram_degraded > 0 ? "warning" : "good",
              description:
                "Свежие роутеры, где Telegram partial/blocked и нужно открыть карточку.",
            },
            {
              key: "youtube_degraded",
              label: "YouTube сбои",
              tone: serviceCounts.youtube_degraded > 0 ? "warning" : "good",
              description:
                "Свежие роутеры, где YouTube partial/blocked и нужно открыть карточку.",
            },
            {
              key: "service_unknown",
              label: "Нет service-проб",
              tone: serviceCounts.service_unknown > 0 ? "warning" : "good",
              description:
                "Роутер на связи, но service-probes не пришли: между редкими проверками или из-за low-memory skip.",
            },
          ],
          counts: serviceCounts,
          total: args.routers.length,
          filterKind: "service",
        }),
      },
    ],
    alerts,
    routers: sortedRouters,
    totalAlerts: alerts.length,
  };
}
