import type { inferRouterOutputs } from "@trpc/server";

import type { AppRouter } from "~/server/api/root";
import type { RouterSummary } from "~/components/router-card";
import type { StatItem } from "~/components/stat-grid";
import { formatControllerVersion } from "~/lib/controller-version";
import { describeRouterMemory } from "~/lib/router-memory";
import {
  getEffectiveRouterStatus,
  hasActiveDirectMode,
  isRouterReachable,
} from "~/server/vectra/router-presence";

type RouterOutputs = inferRouterOutputs<AppRouter>;
type FleetOverview = RouterOutputs["fleet"]["overview"];
type FleetRouter = RouterOutputs["fleet"]["list"][number];
type RouterDetail = RouterOutputs["fleet"]["byId"];

function formatRelativeTime(value: Date | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const diffMs = Date.now() - value.getTime();
  const diffMinutes = Math.max(0, Math.round(diffMs / 60000));
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

function pickComponentVersions(router: FleetRouter) {
  const payload = router.latestSnapshot?.payload;
  const binaryVersions = payload?.binaryVersions ?? {};
  const packageVersions = payload?.packageVersions ?? {};

  return Object.fromEntries(
    ["xray", "sing-box", "hysteria", "geoview"].flatMap((key) => {
      const version =
        binaryVersions[key] ??
        packageVersions[key] ??
        packageVersions[`${key}-core`] ??
        null;
      return version ? [[key, version]] : [];
    }),
  );
}

function normalizeRouterSummaryConfigTrust(
  trust: FleetRouter["configTrust"] | null | undefined,
): RouterSummary["configTrust"] {
  return {
    liveConfigAvailable: Boolean(trust?.liveConfigAvailable),
    requiresReimport: Boolean(trust?.requiresReimport),
    digestMismatch: Boolean(trust?.digestMismatch),
    configSourceMode: trust?.configSourceMode ?? "inventory-only",
    lastLiveImportAt: trust?.lastLiveImportAt?.toISOString() ?? null,
    lastCheckInAt: trust?.lastCheckInAt?.toISOString() ?? null,
  };
}

function normalizeRouterSummaryFleetPolicy(
  compliance: FleetRouter["fleetPolicyCompliance"] | null | undefined,
): RouterSummary["fleetPolicyCompliance"] {
  return {
    status: compliance?.status ?? "unknown",
    summary:
      compliance?.summary ??
      "No full live PassWall import is available for fleet policy matching.",
    mismatches: (compliance?.mismatches ?? []).map((mismatch) => ({
      slot: mismatch.slot,
      actual: mismatch.actual,
      expected: mismatch.expected,
    })),
  };
}

export function buildFleetStats(overview: FleetOverview): StatItem[] {
  return [
    {
      label: "Всего роутеров",
      value: String(overview.totalRouters),
    },
    {
      label: "Активны сейчас",
      value: String(
        (overview.byStatus.active ?? 0) + (overview.byStatus.direct ?? 0),
      ),
      tone: "good",
    },
    {
      label: "Открытые инциденты",
      value: String(overview.openIncidents),
      tone: overview.openIncidents > 0 ? "warning" : "default",
    },
    {
      label: "Задания в очереди",
      value: String(overview.queuedJobs),
      tone: overview.queuedJobs > 0 ? "warning" : "default",
    },
  ];
}

export function buildRouterSummary(router: FleetRouter): RouterSummary {
  const payload = router.latestSnapshot?.payload;
  const reachable = isRouterReachable(router.lastSeenAt);
  const directMode = hasActiveDirectMode(router.status, router.lastSeenAt);
  const effectiveStatus = getEffectiveRouterStatus(
    router.status,
    router.lastSeenAt,
  );
  const offline = effectiveStatus === "offline";
  const passwallEnabled =
    !offline && (router.latestSnapshot?.passwallEnabled ?? false);
  const lastRescue =
    router.openIncident?.reason ??
    router.lastRescueReason ??
    "Нет недавних rescue-событий";

  return {
    id: router.id,
    name:
      router.displayName ??
      payload?.hostname ??
      router.hostname ??
      router.deviceIdentifier,
    selectedNode:
      router.latestSnapshot?.payload.selectedNodeLabel ??
      router.latestSnapshot?.selectedNodeId ??
      "Не выбрана",
    passwallEnabled,
    directMode,
    offline,
    statusLabel: offline
      ? "Нет связи"
      : directMode
        ? "Сейчас в прямом режиме"
        : passwallEnabled
          ? "Сейчас в прокси-режиме"
          : "PassWall2 сейчас выключен",
    nodeCount: router.latestSnapshot?.nodeCount ?? 0,
    subscriptionCount: router.latestSnapshot?.subscriptionCount ?? 0,
    controllerVersion: formatControllerVersion(
      router.latestSnapshot?.controllerVersion,
    ),
    passwallVersion:
      router.latestSnapshot?.passwallAppVersion ??
      payload?.packageVersions["luci-app-passwall2"] ??
      "неизвестно",
    components: pickComponentVersions(router),
    lastSeen: formatRelativeTime(router.lastSeenAt),
    reachable,
    pendingChanges: router.queuedJobCount,
    lastRescue: offline
      ? `Последний известный recovery-сигнал: ${lastRescue}`
      : lastRescue,
    telegramReachability: payload?.telegramReachability ?? null,
    youtubeReachability: payload?.youtubeReachability ?? null,
    instagramReachability: payload?.instagramReachability ?? null,
    memory: describeRouterMemory(payload?.resources ?? null),
    importState: router.importState,
    needsImportReview:
      router.importState !== "approved" ||
      router.configTrust.requiresReimport ||
      router.fleetPolicyCompliance?.status === "violation",
    configTrust: normalizeRouterSummaryConfigTrust(router.configTrust),
    fleetPolicyCompliance: normalizeRouterSummaryFleetPolicy(
      router.fleetPolicyCompliance,
    ),
  };
}

export function buildImpactLines(router: RouterDetail) {
  const latestRevision = router.revisions[0];
  if (!latestRevision) {
    return [
      "Для роутера пока нет desired revision. Сначала сохраните черновик.",
      "Применение остаётся task-based: сначала preview влияния, затем apply через job queue.",
    ];
  }

  const lines = [
    `Ревизия #${latestRevision.revisionNumber}: состояние ${latestRevision.status}.`,
  ];

  const config = latestRevision.config;
  if (config.nodes.length > 0) {
    lines.push(`Будет синхронизировано нод: ${config.nodes.length}.`);
  }
  if (config.subscriptions.items.length > 0) {
    lines.push(
      `Будет обновлено подписок: ${config.subscriptions.items.length}.`,
    );
  }
  if (config.ruleManage.enabledAssets.length > 0) {
    lines.push(
      `Канал правил затрагивает ассеты: ${config.ruleManage.enabledAssets.join(", ")}.`,
    );
  }
  if (Object.keys(config.appUpdate.targetVersions).length > 0) {
    lines.push("Будут зафиксированы целевые версии PassWall2 и компонентов.");
  }

  return lines;
}

export function describeVersionDrift(router: RouterDetail) {
  const payload = router.latestSnapshot?.payload;
  if (!payload) {
    return [];
  }

  return [
    [
      "Controller",
      formatControllerVersion(payload.controllerVersion),
      formatControllerVersion(payload.controllerVersion),
    ],
    [
      "PassWall2",
      payload.packageVersions["luci-app-passwall2"] ?? "неизвестно",
      payload.packageVersions["luci-app-passwall2"] ?? "неизвестно",
    ],
    ...["xray", "sing-box", "hysteria", "geoview"].map((name) => [
      name,
      payload.packageVersions[name] ??
        payload.packageVersions[`${name}-core`] ??
        "неизвестно",
      payload.binaryVersions[name] ?? "неизвестно",
    ]),
  ];
}

export function formatRouterStatus(router: RouterDetail) {
  const effectiveStatus = getEffectiveRouterStatus(
    router.router.status,
    router.router.lastSeenAt,
  );

  if (effectiveStatus === "offline") {
    return "Нет свежей связи с роутером";
  }
  if (effectiveStatus === "direct") {
    return "Прямой доступ";
  }
  if (router.latestSnapshot?.passwallEnabled) {
    return "PassWall2 включён";
  }
  return "Выключено";
}
