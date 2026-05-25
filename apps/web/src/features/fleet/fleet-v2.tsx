"use client";

import { useMemo, useState } from "react";
import { MemoryStick, Network, ShieldAlert } from "lucide-react";

import { Button } from "~/components/ui/button";
import { EmptyState } from "~/components/vectra/empty-state";
import { cn } from "~/lib/utils";
import type { RouterOutputs } from "~/trpc/react";

import {
  FleetAlertStrip,
  type FleetAlertItem,
} from "./components/fleet-alert-strip";
import { FleetKpiStrip, type FleetKpiKey } from "./components/fleet-kpi-strip";
import { FleetTable, type FleetTableRouter } from "./components/fleet-table";

type FleetMonitoringSnapshot = RouterOutputs["fleet"]["monitoring"];
type FleetMonitoringRouter = FleetMonitoringSnapshot["routers"][number];
type FleetMonitoringAlert = FleetMonitoringSnapshot["alerts"][number];

type FleetFilter = FleetKpiKey | "ram" | "alerts";

export interface FleetV2Props {
  initialMonitoring: FleetMonitoringSnapshot;
  initialSearchQuery?: string;
}

const PROBLEM_STATES = new Set(["recovery", "review", "blocked"]);

function isRamRisk(router: FleetMonitoringRouter): boolean {
  const level = router.memory?.level;
  return level === "warning" || level === "critical";
}

function hasAlerts(router: FleetMonitoringRouter): boolean {
  return (router.alertKinds?.length ?? 0) > 0;
}

function matchesFilter(
  router: FleetMonitoringRouter,
  filter: FleetFilter,
): boolean {
  switch (filter) {
    case "all":
      return true;
    case "healthy":
      return router.operationalState === "stable";
    case "problem":
      return PROBLEM_STATES.has(router.operationalState);
    case "offline":
      return router.operationalState === "offline";
    case "ram":
      return isRamRisk(router);
    case "alerts":
      return hasAlerts(router);
  }
}

function mapRouter(router: FleetMonitoringRouter): FleetTableRouter {
  return {
    id: router.id,
    name: router.name || router.id,
    statusLabel: router.statusLabel || "—",
    operationalState: router.operationalState,
    lastSeen: router.lastSeen || "—",
    lastSeenAt: router.lastSeenAt,
    memoryLabel: router.memory?.label ?? "—",
    memoryDetail: router.memory?.summary ?? "",
    memoryLevel: router.memory?.level ?? "unknown",
    controllerVersion: router.controllerVersion || "—",
    alertCount: router.alertKinds?.length ?? 0,
  };
}

function mapAlert(alert: FleetMonitoringAlert): FleetAlertItem {
  return {
    id: alert.id,
    routerId: alert.routerId,
    routerName: alert.routerName || alert.routerId,
    href: alert.href || `/routers/${alert.routerId}`,
    title: alert.title || alert.kind,
    description: alert.description || "",
    severity: alert.severity,
  };
}

export function FleetV2({
  initialMonitoring,
  initialSearchQuery = "",
}: FleetV2Props) {
  const { routers, alerts } = initialMonitoring;
  const [filter, setFilter] = useState<FleetFilter>("all");

  const counts = useMemo(
    () => ({
      total: routers.length,
      healthy: routers.filter((r) => r.operationalState === "stable").length,
      problem: routers.filter((r) => PROBLEM_STATES.has(r.operationalState))
        .length,
      offline: routers.filter((r) => r.operationalState === "offline").length,
      ram: routers.filter(isRamRisk).length,
      alerts: routers.filter(hasAlerts).length,
    }),
    [routers],
  );

  const visibleRouters = useMemo(
    () => routers.filter((r) => matchesFilter(r, filter)).map(mapRouter),
    [routers, filter],
  );
  const alertItems = alerts.map(mapAlert);

  // KPI tiles toggle: re-click the active tile to clear back to "all".
  const onKpiSelect = (key: FleetKpiKey) =>
    setFilter((current) => (current === key ? "all" : key));
  const kpiActive: FleetKpiKey | undefined =
    filter === "ram" || filter === "alerts" ? undefined : filter;

  return (
    <section className="mx-auto w-full max-w-6xl space-y-4">
      <header className="space-y-1">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
          Парк
        </p>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          Fleet
        </h1>
        <p className="text-sm text-muted-foreground">
          Главный экран оператора. Нажмите на показатель или чип, чтобы
          отфильтровать таблицу.
        </p>
      </header>

      <FleetAlertStrip alerts={alertItems} />

      <FleetKpiStrip
        total={counts.total}
        healthy={counts.healthy}
        problem={counts.problem}
        offline={counts.offline}
        active={kpiActive}
        onSelect={onKpiSelect}
      />

      {counts.ram > 0 || counts.alerts > 0 || filter === "ram" || filter === "alerts" ? (
        <div className="flex flex-wrap gap-2">
          {counts.ram > 0 ? (
            <FilterChip
              icon={MemoryStick}
              label="Мало RAM"
              count={counts.ram}
              active={filter === "ram"}
              onClick={() =>
                setFilter((c) => (c === "ram" ? "all" : "ram"))
              }
            />
          ) : null}
          {counts.alerts > 0 ? (
            <FilterChip
              icon={ShieldAlert}
              label="Есть алерты"
              count={counts.alerts}
              active={filter === "alerts"}
              onClick={() =>
                setFilter((c) => (c === "alerts" ? "all" : "alerts"))
              }
            />
          ) : null}
        </div>
      ) : null}

      {counts.total === 0 ? (
        <EmptyState
          icon={Network}
          title="Парк пуст"
          description="Ни один роутер ещё не зарегистрирован. Запустите enrollment, чтобы увидеть роутеры тут."
          primaryAction={{ label: "Открыть enrollment", href: "/enrollment" }}
        />
      ) : (
        <FleetTable
          routers={visibleRouters}
          initialSearchQuery={initialSearchQuery}
        />
      )}
    </section>
  );
}

function FilterChip({
  icon: Icon,
  label,
  count,
  active,
  onClick,
}: {
  icon: typeof MemoryStick;
  label: string;
  count: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant={active ? "default" : "outline"}
      onClick={onClick}
      aria-pressed={active}
      className={cn("h-8")}
    >
      <Icon className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
      {label}
      <span className="ml-1.5 tabular-nums opacity-70">{count}</span>
    </Button>
  );
}
