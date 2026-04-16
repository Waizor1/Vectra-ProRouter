"use client";

import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";

import {
  browserSupportsServiceWorkerPush,
  getVectraPushSubscription,
  subscribeToVectraPush,
} from "~/lib/browser-push";
import { formatControllerVersion } from "~/lib/controller-version";
import { pickFreshAlertsForBrowser } from "~/lib/fleet-browser-alerts";
import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
} from "~/lib/router-onboarding";
import {
  formatTelegramReachabilityLabel,
  getTelegramReachabilityStatus,
  hasTelegramReachabilityProblem,
} from "~/lib/telegram-reachability";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { Panel } from "~/components/panel";
import { RouterCard } from "~/components/router-card";
import { api, type RouterOutputs } from "~/trpc/react";

type FleetMonitoringSnapshot = RouterOutputs["fleet"]["monitoring"];
type NotificationsStatus = RouterOutputs["notifications"]["status"];
type FleetMonitoringChart = FleetMonitoringSnapshot["charts"][number];
type FleetMonitoringSlice = FleetMonitoringChart["slices"][number];
type FleetMonitoringAlert = FleetMonitoringSnapshot["alerts"][number];
type FleetMonitoringRouter = FleetMonitoringSnapshot["routers"][number];
type ActiveFilter = FleetMonitoringSlice["filter"] | null;
type NotificationMode = "push" | "polling" | "unsupported";

const browserNotificationStorageKey = "vectra:fleet-browser-alerts";

const statToneMap = {
  default: "text-white",
  good: "text-emerald-200",
  warning: "text-amber-200",
} as const;

const sliceToneMap = {
  default:
    "bg-slate-500/18 text-slate-200 ring-1 ring-inset ring-white/10 hover:bg-slate-500/24",
  good:
    "bg-emerald-500/14 text-emerald-100 ring-1 ring-inset ring-emerald-400/20 hover:bg-emerald-500/20",
  warning:
    "bg-amber-500/14 text-amber-100 ring-1 ring-inset ring-amber-400/20 hover:bg-amber-500/20",
  critical:
    "bg-rose-500/14 text-rose-100 ring-1 ring-inset ring-rose-400/20 hover:bg-rose-500/20",
} as const;

const stackedToneMap = {
  default: "bg-slate-500/55",
  good: "bg-emerald-400/70",
  warning: "bg-amber-400/75",
  critical: "bg-rose-400/75",
} as const;

const alertToneMap = {
  critical:
    "border-rose-400/25 bg-rose-500/10 text-rose-50",
  warning:
    "border-amber-400/25 bg-amber-500/10 text-amber-50",
  info:
    "border-sky-400/25 bg-sky-500/10 text-sky-50",
} as const;

const alertBadgeMap = {
  critical: "сейчас важно",
  warning: "нужно проверить",
  info: "для сведения",
} as const;

const routerStateLabelMap = {
  stable: "в строю",
  recovery: "recovery",
  offline: "offline",
  review: "import/drift",
  blocked: "blocked",
} as const;

const routerStateToneMap = {
  stable: "border-emerald-400/30 bg-emerald-500/10 text-emerald-100",
  recovery: "border-amber-400/30 bg-amber-500/10 text-amber-100",
  offline: "border-rose-400/30 bg-rose-500/10 text-rose-100",
  review: "border-sky-400/30 bg-sky-500/10 text-sky-100",
  blocked: "border-white/15 bg-white/5 text-slate-200",
} as const;

const freshnessLabelMap = {
  fresh: "fresh",
  watch: "watch",
  offline: "offline",
  never: "no check-in",
} as const;

function normalizeSearchQuery(value: string) {
  return value.trim().toLowerCase();
}

function matchesSearch(router: FleetMonitoringRouter, searchQuery: string) {
  if (!searchQuery) {
    return true;
  }

  const telegramState = router.telegramReachability?.status ?? "нет данных";
  const haystack = [
    router.name,
    router.id,
    router.selectedNode,
    router.statusLabel,
    router.operationalState,
    router.freshnessState,
    router.importState,
    router.lastRescue,
    router.controllerVersion,
    router.passwallVersion,
    telegramState,
    ...router.alertKinds,
    ...Object.values(router.components),
  ]
    .join(" ")
    .toLowerCase();

  return haystack.includes(searchQuery);
}

function formatRelativeTime(value: string | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) {
    return "неизвестно";
  }

  const diffMs = Math.max(0, Date.now() - timestamp);
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

function filtersMatch(
  activeFilter: ActiveFilter,
  router: FleetMonitoringRouter,
) {
  if (!activeFilter) {
    return true;
  }

  if (activeFilter.kind === "operational") {
    return router.operationalState === activeFilter.value;
  }

  return router.freshnessState === activeFilter.value;
}

function alertMatchesFilter(activeFilter: ActiveFilter, alert: FleetMonitoringAlert) {
  if (!activeFilter) {
    return true;
  }

  return activeFilter.kind === "operational"
    ? alert.filters.operational === activeFilter.value
    : alert.filters.freshness === activeFilter.value;
}

function sameFilter(left: ActiveFilter, right: ActiveFilter) {
  return left?.kind === right?.kind && left?.value === right?.value;
}

function getOnboardingPriority(importState: string) {
  switch (importState) {
    case "import_review":
      return 0;
    case "awaiting_import":
      return 1;
    case "out_of_sync":
      return 2;
    default:
      return 3;
  }
}

function createBrowserNotification(
  title: string,
  body: string,
  tag: string,
  href: string,
) {
  const notification = new Notification(title, {
    body,
    tag,
    requireInteraction: false,
  });

  notification.onclick = () => {
    window.focus();
    window.location.assign(href);
    notification.close();
  };

  window.setTimeout(() => notification.close(), 14_000);
}

function isSerializedPushSubscription(
  value: PushSubscriptionJSON,
): value is Required<PushSubscriptionJSON> & {
  keys: {
    auth: string;
    p256dh: string;
  };
} {
  return Boolean(value.endpoint && value.keys?.auth && value.keys?.p256dh);
}

export function FleetMonitoringWorkspace({
  initialMonitoring,
  initialSearchQuery = "",
}: {
  initialMonitoring: FleetMonitoringSnapshot;
  initialSearchQuery?: string;
}) {
  const monitoringQuery = api.fleet.monitoring.useQuery(undefined, {
    initialData: initialMonitoring,
    refetchInterval: 20_000,
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: true,
  });
  const notificationsStatusQuery = api.notifications.status.useQuery();
  const subscribePushMutation = api.notifications.subscribe.useMutation();
  const unsubscribePushMutation = api.notifications.unsubscribe.useMutation();

  const snapshot = monitoringQuery.data ?? initialMonitoring;
  const [activeFilter, setActiveFilter] = useState<ActiveFilter>(null);
  const [searchInput, setSearchInput] = useState(initialSearchQuery);
  const [notificationMode, setNotificationMode] =
    useState<NotificationMode>("unsupported");
  const [notificationsEnabled, setNotificationsEnabled] = useState(false);
  const [notificationPermission, setNotificationPermission] = useState<
    NotificationPermission | "unsupported"
  >("unsupported");
  const seededAlertsRef = useRef(false);
  const seenAlertIdsRef = useRef<Set<string>>(new Set());
  const syncedSubscriptionEndpointRef = useRef<string | null>(null);
  const normalizedSearchQuery = useMemo(
    () => normalizeSearchQuery(searchInput),
    [searchInput],
  );

  useEffect(() => {
    setSearchInput(initialSearchQuery);
  }, [initialSearchQuery]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    if (!("Notification" in window)) {
      setNotificationMode("unsupported");
      setNotificationPermission("unsupported");
      setNotificationsEnabled(false);
      return;
    }

    let cancelled = false;

    const syncNotificationState = async (status: NotificationsStatus | undefined) => {
      const nextPermission = Notification.permission;
      setNotificationPermission(nextPermission);

      if (status?.configured && browserSupportsServiceWorkerPush()) {
        try {
          const { subscription } = await getVectraPushSubscription();
          if (cancelled) {
            return;
          }

          setNotificationMode("push");
          setNotificationsEnabled(
            nextPermission === "granted" && Boolean(subscription),
          );

          if (subscription) {
            const serialized = subscription.toJSON();
            if (
              isSerializedPushSubscription(serialized) &&
              serialized.endpoint !== syncedSubscriptionEndpointRef.current
            ) {
              syncedSubscriptionEndpointRef.current = serialized.endpoint;
              try {
                await subscribePushMutation.mutateAsync({
                  subscription: serialized,
                  userAgent: navigator.userAgent,
                });
              } catch (error) {
                syncedSubscriptionEndpointRef.current = null;
                throw error;
              }
            }
          } else {
            syncedSubscriptionEndpointRef.current = null;
          }
          return;
        } catch (error) {
          console.error("[fleet-notifications] push sync failed", error);
        }
      }

      const storedEnabled =
        window.localStorage.getItem(browserNotificationStorageKey) === "enabled";
      setNotificationMode("polling");
      setNotificationsEnabled(storedEnabled && nextPermission === "granted");
    };

    void syncNotificationState(notificationsStatusQuery.data);

    return () => {
      cancelled = true;
    };
  }, [
    notificationsStatusQuery.data,
    subscribePushMutation,
  ]);

  useEffect(() => {
    const currentNotifiableIds = new Set(
      snapshot.alerts
        .filter((alert) => alert.severity !== "info")
        .map((alert) => alert.id),
    );

    if (!seededAlertsRef.current) {
      seenAlertIdsRef.current = currentNotifiableIds;
      seededAlertsRef.current = true;
      return;
    }

    if (
      notificationMode !== "polling" ||
      notificationPermission !== "granted" ||
      !notificationsEnabled ||
      document.visibilityState === "visible"
    ) {
      seenAlertIdsRef.current = currentNotifiableIds;
      return;
    }

    const freshAlerts = pickFreshAlertsForBrowser(
      snapshot.alerts,
      seenAlertIdsRef.current,
    );
    seenAlertIdsRef.current = currentNotifiableIds;

    if (freshAlerts.length === 0) {
      return;
    }

    for (const alert of freshAlerts.slice(0, 3)) {
      createBrowserNotification(
        alert.title,
        `${alert.routerName} · ${alert.description}`,
        alert.id,
        alert.href,
      );
    }

    if (freshAlerts.length > 3) {
      createBrowserNotification(
        "В парке появились новые проблемы",
        `Новых точек внимания: ${freshAlerts.length}. Откройте парк, чтобы быстро перейти к нужному роутеру.`,
        "fleet-summary",
        "/fleet",
      );
    }
  }, [
    notificationMode,
    notificationPermission,
    notificationsEnabled,
    snapshot.alerts,
  ]);

  const filteredRouters = snapshot.routers.filter(
    (router) =>
      filtersMatch(activeFilter, router) &&
      matchesSearch(router, normalizedSearchQuery),
  );
  const filteredAlerts = snapshot.alerts.filter((alert) =>
    alertMatchesFilter(activeFilter, alert),
  );
  const onboardingRouters = snapshot.routers.filter(
    (router) => router.importState !== "approved",
  );
  const nextOnboardingRouters = [...onboardingRouters]
    .sort(
      (left, right) =>
        getOnboardingPriority(left.importState) -
        getOnboardingPriority(right.importState),
    )
    .slice(0, 3);
  const awaitingImportCount = onboardingRouters.filter(
    (router) => router.importState === "awaiting_import",
  ).length;
  const reviewCount = onboardingRouters.filter(
    (router) => router.importState === "import_review",
  ).length;
  const driftCount = onboardingRouters.filter(
    (router) => router.importState === "out_of_sync",
  ).length;

  const handleNotificationToggle = async () => {
    if (typeof window === "undefined" || !("Notification" in window)) {
      return;
    }

    if (
      notificationMode === "push" &&
      notificationsStatusQuery.data?.configured &&
      notificationsStatusQuery.data.publicKey
    ) {
      if (notificationsEnabled) {
        const { subscription } = await getVectraPushSubscription();
        if (subscription) {
          await unsubscribePushMutation.mutateAsync({
            endpoint: subscription.endpoint,
          });
          await subscription.unsubscribe();
          syncedSubscriptionEndpointRef.current = null;
        }

        setNotificationsEnabled(false);
        return;
      }

      const permission =
        Notification.permission === "default"
          ? await Notification.requestPermission()
          : Notification.permission;

      setNotificationPermission(permission);

      if (permission !== "granted") {
        setNotificationsEnabled(false);
        return;
      }

      const subscription = await subscribeToVectraPush(
        notificationsStatusQuery.data.publicKey,
      );
      const serialized = subscription.toJSON();

      if (!isSerializedPushSubscription(serialized)) {
        throw new Error("Browser returned an incomplete push subscription.");
      }

      await subscribePushMutation.mutateAsync({
        subscription: serialized,
        userAgent: navigator.userAgent,
      });
      syncedSubscriptionEndpointRef.current = serialized.endpoint;

      setNotificationsEnabled(true);
      return;
    }

    if (notificationsEnabled) {
      window.localStorage.setItem(browserNotificationStorageKey, "disabled");
      setNotificationsEnabled(false);
      return;
    }

    const permission =
      Notification.permission === "default"
        ? await Notification.requestPermission()
        : Notification.permission;

    setNotificationPermission(permission);

    if (permission === "granted") {
      window.localStorage.setItem(browserNotificationStorageKey, "enabled");
      setNotificationsEnabled(true);
      return;
    }

    window.localStorage.setItem(browserNotificationStorageKey, "disabled");
    setNotificationsEnabled(false);
  };

  const notificationToggleDisabled =
    notificationPermission === "unsupported" ||
    (notificationPermission === "denied" && !notificationsEnabled) ||
    notificationsStatusQuery.isLoading ||
    subscribePushMutation.isPending ||
    unsubscribePushMutation.isPending;

  const activeFilterLabel = (() => {
    if (!activeFilter) {
      return "Все роутеры";
    }

    const slice = snapshot.charts
      .flatMap((chart) => chart.slices)
      .find((entry) => sameFilter(entry.filter, activeFilter));

    return slice ? slice.label : "Фильтр";
  })();

  return (
    <div className="flex flex-col gap-4 xl:gap-5">
      <div className="order-1 lg:order-1">
        <Panel
          eyebrow="Парк"
          title={`Роутеры · ${activeFilterLabel}`}
          tone="hero"
          aside={
            <div className="flex flex-col gap-2 text-left text-sm leading-6 text-slate-400 md:items-end md:text-right">
              <p>Показано {filteredRouters.length} из {snapshot.routers.length}</p>
              <p>Алертов: {filteredAlerts.length}</p>
              <p>Снимок {formatRelativeTime(snapshot.generatedAt)}</p>
              <div className="flex flex-wrap gap-2 md:justify-end">
                {activeFilter ? (
                  <button
                    type="button"
                    onClick={() => setActiveFilter(null)}
                    className="vectra-button-secondary px-3 py-2 text-sm text-white transition hover:border-white/20"
                  >
                    Сбросить фильтр
                  </button>
                ) : null}
                <button
                  type="button"
                  onClick={handleNotificationToggle}
                  disabled={notificationToggleDisabled}
                  className="vectra-button-secondary px-3 py-2 text-sm text-white transition hover:border-white/20 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {subscribePushMutation.isPending || unsubscribePushMutation.isPending
                    ? "Сохраняю..."
                    : notificationsEnabled
                      ? "Уведомления: вкл"
                      : "Уведомления: выкл"}
                </button>
              </div>
            </div>
          }
        >
          <div className="space-y-4">
            <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <label className="block lg:min-w-[24rem] lg:flex-1">
                <span className="vectra-kicker text-slate-500">
                  Фильтр по имени, ID, версии, статусу или ноде
                </span>
                <input
                  name="fleet-operations-search"
                  value={searchInput}
                  onChange={(event) => setSearchInput(event.target.value)}
                  placeholder="AX3000T, direct, import review, 0.1.12-r10, WorldProxy..."
                  className="vectra-field mt-2 min-h-11 px-3 py-2 text-sm text-white placeholder:text-slate-500"
                />
              </label>

              <div className="flex flex-wrap gap-2">
                {normalizedSearchQuery ? (
                  <button
                    type="button"
                    onClick={() => setSearchInput("")}
                    className="vectra-button-secondary px-3 py-2 text-sm text-white transition hover:border-white/20"
                  >
                    Сбросить поиск
                  </button>
                ) : null}
                {activeFilter ? (
                  <span className="rounded-full border border-white/10 bg-white/5 px-3 py-2 text-xs text-slate-300">
                    Срез: {activeFilterLabel}
                  </span>
                ) : null}
              </div>
            </div>

            <div className="grid gap-4 xl:grid-cols-[minmax(0,1.9fr)_minmax(320px,0.7fr)] xl:items-start">
              <section className="rounded-[1.4rem] border border-white/10 bg-[rgba(8,11,17,0.76)] px-4 py-4">
                <h3 className="text-base font-semibold text-white sm:text-lg">
                  Плотная таблица парка
                </h3>

                <div className="mt-4">
                  <DataTable
                    title="Операционная таблица"
                    hint="Свайп/скролл по горизонтали сохраняется →"
                    columns={[
                      { key: "router", label: "Роутер" },
                      { key: "state", label: "Состояние" },
                      { key: "control", label: "Контур" },
                      { key: "versions", label: "Версии" },
                      { key: "actions", label: "Действие", className: "text-right" },
                    ]}
                  >
                    {filteredRouters.length > 0 ? (
                      filteredRouters.map((router) => (
                        <FleetOperationsRow key={router.id} router={router} />
                      ))
                    ) : (
                      <DataTableEmpty colSpan={5}>
                        В этом срезе роутеров нет. Смените сегмент сверху,
                        уточните поиск или сбросьте фильтр.
                      </DataTableEmpty>
                    )}
                  </DataTable>
                </div>
              </section>

              <section className="space-y-3 rounded-[1.4rem] border border-white/10 bg-[rgba(8,11,17,0.76)] px-4 py-4 xl:sticky xl:top-4">
                <div>
                  <p className="vectra-kicker text-[var(--vectra-accent-warm)]">Алерты</p>
                  <h3 className="mt-1 text-base font-semibold text-white sm:text-lg">
                    Что требует внимания
                  </h3>
                </div>

                <div className="space-y-2">
                  {filteredAlerts.length > 0 ? (
                    filteredAlerts.slice(0, 6).map((alert) => (
                      <Link
                        key={alert.id}
                        href={alert.href}
                        className={`block rounded-2xl border px-3 py-3 transition hover:border-white/20 ${alertToneMap[alert.severity]}`}
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div>
                            <p className="text-sm font-semibold text-white">{alert.title}</p>
                            <p className="mt-1 text-xs leading-5 text-slate-300">
                              {alert.routerName} · {formatRelativeTime(alert.openedAt)}
                            </p>
                          </div>
                          <span className="rounded-full border border-white/12 bg-white/8 px-2 py-1 text-[10px] font-semibold tracking-[0.08em] text-white uppercase">
                            {alertBadgeMap[alert.severity]}
                          </span>
                        </div>
                        <p className="mt-2 text-sm leading-6 text-slate-300">{alert.description}</p>
                      </Link>
                    ))
                  ) : (
                    <div className="rounded-2xl border border-dashed border-white/12 bg-white/[0.03] px-3 py-6 text-sm leading-7 text-slate-400">
                      В этом срезе сейчас нет активных проблем.
                    </div>
                  )}
                </div>
              </section>
            </div>

            <details className="rounded-2xl border border-white/10 bg-[rgba(255,255,255,0.02)] px-3 py-3">
              <summary className="cursor-pointer list-none">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <p className="vectra-kicker text-slate-500">Контекст парка</p>
                    <p className="mt-1 text-sm text-slate-400">Вторичный слой: обзор, срезы и onboarding.</p>
                  </div>
                  <span className="text-xs text-slate-400">раскрыть</span>
                </div>
              </summary>

              <div className="mt-4 space-y-4">
                <div className="vectra-stat-grid lg:grid-cols-none 2xl:grid-cols-none">
                  {snapshot.stats.map((item) => (
                    <article
                      key={item.label}
                      className="rounded-2xl border border-white/10 bg-[rgba(255,255,255,0.03)] px-3 py-3"
                    >
                      <p className="vectra-kicker text-slate-500">{item.label}</p>
                      <p className={`mt-2 text-xl font-semibold tracking-[-0.03em] sm:text-2xl ${statToneMap[item.tone]}`}>
                        {item.value}
                      </p>
                    </article>
                  ))}
                </div>

                <div className="grid gap-3 lg:grid-cols-2">
                  {snapshot.charts.map((chart) => (
                    <MonitoringChartCard
                      key={chart.id}
                      chart={chart}
                      activeFilter={activeFilter}
                      onFilterChange={(nextFilter) =>
                        setActiveFilter((previous) =>
                          sameFilter(previous, nextFilter) ? null : nextFilter,
                        )
                      }
                    />
                  ))}
                </div>

                <div className="grid gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(340px,0.75fr)]">
                  <div className="space-y-4">
                    <div className="flex flex-wrap gap-2">
                      <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-sm text-slate-200">
                        Ждут первый import: {awaitingImportCount}
                      </span>
                      <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-3 py-1 text-sm text-amber-100">
                        На проверке: {reviewCount}
                      </span>
                      <span className="rounded-full border border-rose-400/25 bg-rose-500/10 px-3 py-1 text-sm text-rose-100">
                        Есть расхождение: {driftCount}
                      </span>
                    </div>
                  </div>

                  <div className="space-y-3">
                    {nextOnboardingRouters.length > 0 ? (
                      nextOnboardingRouters.map((router) => {
                        const onboarding = describeRouterOnboarding(router.importState);

                        return (
                          <Link
                            key={router.id}
                            href={`/routers/${router.id}`}
                            className="block rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 transition hover:border-white/20"
                          >
                            <div className="flex items-start justify-between gap-3">
                              <div>
                                <p className="text-sm font-semibold text-white">{router.name}</p>
                                <p className="mt-1 text-xs text-slate-400">
                                  {formatRouterImportStateLabel(router.importState)} · {router.lastSeen}
                                </p>
                              </div>
                              <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-slate-200">
                                {onboarding.badge}
                              </span>
                            </div>
                            <p className="mt-3 text-sm font-medium text-white">
                              {onboarding.cardActionLabel}
                            </p>
                          </Link>
                        );
                      })
                    ) : (
                      <div className="rounded-md border border-emerald-400/20 bg-emerald-500/10 px-3 py-6 text-sm leading-7 text-emerald-100">
                        Новых onboarding-задач нет.
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </details>

            <section className="space-y-3 lg:hidden">
              <div className="flex items-center justify-between gap-3">
                <p className="text-sm text-slate-400">Карточки.</p>
              </div>

              <div className="grid gap-3 lg:grid-cols-2">
                {filteredRouters.length > 0 ? (
                  filteredRouters.map((router) => (
                    <RouterCard key={router.id} router={router} />
                  ))
                ) : (
                  <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300 lg:col-span-2">
                    В этом фильтре роутеров нет. Смените сегмент сверху или
                    сбросьте фильтр.
                  </div>
                )}
              </div>
            </section>
          </div>
        </Panel>
      </div>

    </div>
  );
}

function MonitoringChartCard({
  chart,
  activeFilter,
  onFilterChange,
}: {
  chart: FleetMonitoringChart;
  activeFilter: ActiveFilter;
  onFilterChange: (filter: ActiveFilter) => void;
}) {
  return (
    <section className="rounded-md border border-white/10 bg-[rgba(10,14,20,0.74)] px-3 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="vectra-kicker text-slate-500">{chart.title}</p>
          <p className="mt-2 text-xs leading-5 text-slate-300 sm:text-sm sm:leading-6">
            {chart.description}
          </p>
        </div>
        <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-xs text-slate-200">
          {chart.slices.reduce((sum, slice) => sum + slice.count, 0)}
        </span>
      </div>

      <div className="mt-4 flex h-3 overflow-hidden rounded-full bg-white/6">
        {chart.slices.map((slice) => (
          <button
            key={slice.key}
            type="button"
            onClick={() => onFilterChange(slice.filter)}
            disabled={slice.count === 0}
            aria-label={`${slice.label}: ${slice.count}`}
            style={{
              flexBasis: 0,
              flexGrow: slice.count,
            }}
            className={`transition ${stackedToneMap[slice.tone]} disabled:opacity-30`}
          />
        ))}
      </div>

      <div className="mt-4 space-y-2">
        {chart.slices.map((slice) => {
          const selected = sameFilter(activeFilter, slice.filter);

          return (
            <button
              key={slice.key}
              type="button"
              onClick={() => onFilterChange(slice.filter)}
              disabled={slice.count === 0}
              className={`flex w-full items-start justify-between gap-4 rounded-md px-3 py-3 text-left transition disabled:cursor-not-allowed disabled:opacity-45 ${
                sliceToneMap[slice.tone]
              } ${selected ? "outline outline-1 outline-offset-[-1px] outline-white/40" : ""}`}
            >
              <div>
                <p className="text-sm font-semibold text-white">
                  {slice.label}
                </p>
                <p className="mt-1 text-xs leading-5 text-slate-300 sm:leading-6">
                  {slice.description}
                </p>
              </div>
              <div className="text-right">
                <p className="text-xl font-semibold tracking-[-0.02em] text-white">
                  {slice.count}
                </p>
                <p className="text-xs text-slate-300">{slice.percent}%</p>
              </div>
            </button>
          );
        })}
      </div>
    </section>
  );
}

function FleetOperationsRow({
  router,
}: {
  router: FleetMonitoringRouter;
}) {
  const onboarding = describeRouterOnboarding(router.importState);
  const controllerVersion = formatControllerVersion(router.controllerVersion);
  const telegramStatus = getTelegramReachabilityStatus(router.telegramReachability);
  const telegramProblem = hasTelegramReachabilityProblem(router.telegramReachability);

  const telegramToneClassName =
    telegramStatus === "reachable"
      ? "text-emerald-100"
      : telegramStatus === "partial"
        ? "text-amber-100"
        : telegramProblem
          ? "text-rose-100"
          : "text-slate-300";

  return (
    <tr className="border-t border-white/10 align-top text-slate-200">
      <td className="px-3 py-3">
          <div className="min-w-[15rem]">
            <div className="flex flex-wrap items-center gap-2">
              <Link
                href={`/routers/${router.id}`}
              className="text-sm font-semibold text-white transition hover:text-[var(--vectra-accent)]"
            >
              {router.name}
            </Link>
            </div>
            <div className="mt-2 flex flex-wrap gap-2 text-xs leading-5 text-slate-400">
              <span className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5">
                ID {router.id.slice(0, 8)}
              </span>
              <span className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5">
                Очередь {router.pendingChanges}
              </span>
            </div>
          </div>
      </td>
      <td className="px-3 py-3">
        <div className="min-w-[11rem] space-y-2 text-xs leading-5">
          <span
            className={`inline-flex rounded-full border px-2.5 py-1 font-semibold tracking-[0.08em] uppercase ${routerStateToneMap[router.operationalState]}`}
          >
            {routerStateLabelMap[router.operationalState]}
          </span>
          <div>
            <p className="text-sm font-medium text-white">{router.statusLabel}</p>
            <p className="text-slate-400">
              Freshness: {freshnessLabelMap[router.freshnessState]} · {router.lastSeen}
            </p>
          </div>
          {router.lastRescue !== "никогда" ? (
            <p className="text-slate-500">Recovery: {router.lastRescue}</p>
          ) : null}
        </div>
      </td>
      <td className="px-3 py-3">
        <div className="min-w-[10rem] space-y-2 text-xs leading-5 text-slate-400">
          <p>
            Импорт <span className="font-medium text-white">{formatRouterImportStateLabel(router.importState)}</span>
          </p>
          <p>
            Telegram <span className={telegramToneClassName}>{formatTelegramReachabilityLabel(router.telegramReachability)}</span>
          </p>
        </div>
      </td>
      <td className="px-3 py-3">
        <div className="min-w-[11rem] space-y-2 text-xs leading-5 text-slate-400">
          <p>
            Controller <span className="font-medium text-white">{controllerVersion}</span>
          </p>
          <p>
            PassWall2 <span className="font-medium text-white">{router.passwallVersion}</span>
          </p>
        </div>
      </td>
      <td className="px-3 py-3 text-right">
        <div className="flex min-w-[11rem] flex-col items-stretch gap-2 sm:items-end">
          <Link
            href={`/routers/${router.id}`}
            className="rounded-md border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-3 py-2 text-center text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)]"
          >
            {onboarding.cardActionLabel}
          </Link>
        </div>
      </td>
    </tr>
  );
}
