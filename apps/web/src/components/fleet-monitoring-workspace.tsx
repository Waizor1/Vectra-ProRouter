"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import {
  browserSupportsServiceWorkerPush,
  getVectraPushSubscription,
  subscribeToVectraPush,
} from "~/lib/browser-push";
import { pickFreshAlertsForBrowser } from "~/lib/fleet-browser-alerts";
import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
} from "~/lib/router-onboarding";
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
}: {
  initialMonitoring: FleetMonitoringSnapshot;
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
  const [notificationMode, setNotificationMode] =
    useState<NotificationMode>("unsupported");
  const [notificationsEnabled, setNotificationsEnabled] = useState(false);
  const [notificationPermission, setNotificationPermission] = useState<
    NotificationPermission | "unsupported"
  >("unsupported");
  const seededAlertsRef = useRef(false);
  const seenAlertIdsRef = useRef<Set<string>>(new Set());
  const syncedSubscriptionEndpointRef = useRef<string | null>(null);

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

  const filteredRouters = snapshot.routers.filter((router) =>
    filtersMatch(activeFilter, router),
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

  const notificationLabel =
    notificationPermission === "unsupported"
      ? "Этот браузер не поддерживает системные уведомления."
      : notificationMode === "push"
        ? notificationPermission === "denied"
          ? "Push-уведомления заблокированы браузером. Разрешение нужно менять в настройках сайта."
          : notificationsEnabled
            ? "Фоновые push-уведомления включены для этого браузера."
            : "Фоновые push-уведомления выключены."
        : notificationPermission === "denied"
          ? "Уведомления заблокированы браузером. Разрешение нужно менять в настройках сайта."
          : notificationsStatusQuery.data?.configured
            ? notificationsEnabled
              ? "Этот браузер не тянет background push, поэтому включён локальный alerting при открытой вкладке."
              : "Background push здесь недоступен, можно включить только локальный alerting при открытой вкладке."
            : notificationsEnabled
              ? "Серверный push ещё не настроен, поэтому работает только локальный alerting при открытой вкладке."
              : "Серверный push ещё не настроен, можно включить только локальный alerting при открытой вкладке.";

  const notificationFootnote =
    notificationMode === "push"
      ? "После включения критичные offline/direct/rescue события будут приходить даже с закрытой панелью."
      : notificationsStatusQuery.data?.configured
        ? "На сервере push уже включён, но этот браузер не умеет service worker push. Здесь останется только локальный alerting при открытой вкладке."
        : snapshot.notificationNote;

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
    <div className="space-y-4">
      <section className="rounded-md border border-white/10 bg-[linear-gradient(160deg,rgba(24,30,39,0.98),rgba(18,22,29,0.96),rgba(28,39,54,0.94))] px-3 py-3 shadow-[0_20px_60px_rgba(0,0,0,0.28)] sm:px-4 sm:py-4">
        <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
          <div>
            <p className="vectra-kicker text-[var(--vectra-accent)]">
              Обзор парка
            </p>
            <h2 className="mt-1 text-lg font-semibold tracking-[-0.01em] text-white sm:text-xl">
              Сводка, алерты и быстрый вход в проблему
            </h2>
            <p className="mt-2 text-[13px] leading-6 text-slate-300 sm:hidden">
              Что сломалось и куда зайти прямо сейчас.
            </p>
            <p className="mt-3 hidden max-w-3xl text-sm leading-7 text-slate-300 sm:block">
              Здесь видно, что сломалось, что ждёт import и какой роутер открыть.
            </p>
          </div>

          <div className="rounded-md border border-white/10 bg-[rgba(10,14,20,0.74)] px-3 py-3 xl:max-w-sm">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <p className="vectra-kicker text-slate-500">
                  Браузерные уведомления
                </p>
                <p className="mt-2 text-sm leading-6 text-slate-200">
                  {notificationLabel}
                </p>
              </div>
              <button
                type="button"
                onClick={handleNotificationToggle}
                disabled={notificationToggleDisabled}
                className="w-full rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-white transition hover:border-white/20 disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
              >
                {subscribePushMutation.isPending ||
                unsubscribePushMutation.isPending
                  ? "Сохраняю..."
                  : notificationsEnabled
                    ? "Выключить"
                    : "Включить"}
              </button>
            </div>
            <p className="mt-3 text-xs leading-6 text-slate-400">
              {notificationFootnote}
            </p>
            <p className="mt-2 text-xs leading-6 text-slate-500">
              Снимок обновлён {formatRelativeTime(snapshot.generatedAt)}.
              {monitoringQuery.isError
                ? " Последнее обновление с сервера сейчас не прошло, показываю предыдущий снимок."
                : ""}
            </p>
          </div>
        </div>

        <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.95fr)]">
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-2 sm:gap-3 md:grid-cols-2 2xl:grid-cols-3">
              {snapshot.stats.map((item) => (
                <article
                  key={item.label}
                  className="rounded-md border border-white/10 bg-[rgba(255,255,255,0.03)] px-3 py-3"
                >
                  <p className="vectra-kicker text-slate-500">
                    {item.label}
                  </p>
                  <p
                    className={`mt-2 text-2xl font-semibold tracking-[-0.03em] sm:text-3xl ${statToneMap[item.tone]}`}
                  >
                    {item.value}
                  </p>
                  <p className="mt-2 text-xs leading-5 text-slate-400 sm:text-sm sm:leading-6">
                    {item.hint}
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
          </div>

          <section className="rounded-md border border-white/10 bg-[rgba(8,11,17,0.76)] px-3 py-3">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <p className="vectra-kicker text-[var(--vectra-accent-warm)]">
                  Актуальные алерты
                </p>
                <h3 className="mt-1 text-lg font-semibold tracking-[-0.01em] text-white">
                  Что требует внимания прямо сейчас
                </h3>
              </div>
              <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs font-medium text-slate-200">
                {snapshot.totalAlerts} всего
              </span>
            </div>

            {filteredAlerts.length > 0 ? (
              <div className="mt-4 space-y-3">
                {filteredAlerts.slice(0, 8).map((alert) => (
                  <Link
                    key={alert.id}
                    href={alert.href}
                    className={`block rounded-md border px-3 py-3 transition hover:border-white/25 ${alertToneMap[alert.severity]}`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <p className="text-base font-semibold tracking-[-0.01em] text-white">
                          {alert.title}
                        </p>
                        <p className="mt-1 text-sm leading-6 text-slate-200">
                          {alert.routerName}
                        </p>
                      </div>
                      <span className="rounded-full border border-white/12 bg-white/8 px-2.5 py-1 text-[11px] font-semibold tracking-[0.08em] text-white uppercase">
                        {alertBadgeMap[alert.severity]}
                      </span>
                    </div>
                    <p className="mt-2 text-sm leading-6 text-slate-200">
                      {alert.description}
                    </p>
                    <p className="mt-2 text-xs leading-6 text-slate-300/80">
                      Открыть роутер · {formatRelativeTime(alert.openedAt)}
                    </p>
                  </Link>
                ))}
                {filteredAlerts.length > 8 ? (
                  <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] px-3 py-3 text-sm leading-6 text-slate-400">
                    Ещё алертов: {filteredAlerts.length - 8}. Сузьте срез
                    графиком или откройте нужный роутер ниже.
                  </div>
                ) : null}
              </div>
            ) : (
              <div className="mt-4 rounded-md border border-emerald-400/20 bg-emerald-500/10 px-3 py-6 text-sm leading-7 text-emerald-100">
                В этом срезе сейчас нет активных проблем.
              </div>
            )}
          </section>
        </div>
      </section>

      <Panel
        eyebrow="Анбординг"
        title={
          onboardingRouters.length > 0
            ? `Новые роутеры и import-задачи · ${onboardingRouters.length}`
            : "Подключение новых роутеров"
        }
        aside={
          <div className="flex flex-wrap gap-2">
            <Link
              href="/enrollment"
              className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
            >
              Установка
            </Link>
            <Link
              href="/updates"
              className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
            >
              Глобальный baseline
            </Link>
          </div>
        }
      >
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm leading-6 text-slate-300">
                <strong className="text-white">1. Установка</strong>
                <br />
                Запустите bootstrap на роутере.
              </div>
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm leading-6 text-slate-300">
                <strong className="text-white">2. Парк</strong>
                <br />
                Найдите новый router ID после check-in.
              </div>
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm leading-6 text-slate-300">
                <strong className="text-white">3. Import</strong>
                <br />
                Если состояние правильное, примите import как эталон.
              </div>
            </div>

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
                        <p className="text-sm font-semibold text-white">
                          {router.name}
                        </p>
                        <p className="mt-1 text-xs text-slate-400">
                          {formatRouterImportStateLabel(router.importState)} ·{" "}
                          {router.lastSeen}
                        </p>
                      </div>
                      <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-slate-200">
                        {onboarding.badge}
                      </span>
                    </div>
                    <p className="mt-3 text-sm font-medium text-white">
                      {onboarding.cardActionLabel}
                    </p>
                    <p className="mt-2 text-sm leading-6 text-slate-300">
                      {onboarding.cardHint}
                    </p>
                  </Link>
                );
              })
            ) : (
              <div className="rounded-md border border-emerald-400/20 bg-emerald-500/10 px-3 py-6 text-sm leading-7 text-emerald-100">
                Сейчас новых onboarding-задач нет.
              </div>
            )}
          </div>
        </div>
      </Panel>

      <Panel
        eyebrow="Парк"
        title={`Роутеры · ${activeFilterLabel}`}
        aside={
          <div className="text-left text-sm leading-6 text-slate-400 md:text-right">
            <p>
              Показано {filteredRouters.length} из {snapshot.routers.length}
            </p>
            <p>
              Алертов в срезе: {filteredAlerts.length}
            </p>
            {activeFilter ? (
              <button
                type="button"
                onClick={() => setActiveFilter(null)}
                className="mt-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-white transition hover:border-white/20"
              >
                Сбросить фильтр
              </button>
            ) : null}
          </div>
        }
      >
        <div className="grid gap-3 lg:grid-cols-2">
          {filteredRouters.length > 0 ? (
            filteredRouters.map((router) => (
              <RouterCard key={router.id} router={router} />
            ))
          ) : (
            <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300 lg:col-span-2">
              В этом фильтре роутеров нет. Смените сегмент сверху или сбросьте
              фильтр.
            </div>
          )}
        </div>
      </Panel>
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
