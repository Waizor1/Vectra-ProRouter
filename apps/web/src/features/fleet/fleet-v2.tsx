import { Network } from "lucide-react";

import { EmptyState } from "~/components/vectra/empty-state";
import type { RouterOutputs } from "~/trpc/react";

import { FleetAlertStrip, type FleetAlertItem } from "./components/fleet-alert-strip";
import { FleetKpiStrip } from "./components/fleet-kpi-strip";
import { FleetTable, type FleetTableRouter } from "./components/fleet-table";

type FleetMonitoringSnapshot = RouterOutputs["fleet"]["monitoring"];
type FleetMonitoringRouter = FleetMonitoringSnapshot["routers"][number];
type FleetMonitoringAlert = FleetMonitoringSnapshot["alerts"][number];

export interface FleetV2Props {
  initialMonitoring: FleetMonitoringSnapshot;
  initialSearchQuery?: string;
}

const PROBLEM_STATES = new Set([
  "recovery",
  "review",
  "blocked",
]);

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

  const tableRouters = routers.map(mapRouter);
  const alertItems = alerts.map(mapAlert);

  const total = routers.length;
  const offline = routers.filter((r) => r.operationalState === "offline").length;
  const problem = routers.filter((r) =>
    PROBLEM_STATES.has(r.operationalState),
  ).length;
  const healthy = routers.filter((r) => r.operationalState === "stable").length;

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
          Главный экран оператора. Алерты сверху, KPI по парку и одна таблица
          для всех роутеров. Аналитика и графики живут в отдельных surfaces.
        </p>
      </header>

      <FleetAlertStrip alerts={alertItems} />

      <FleetKpiStrip
        total={total}
        healthy={healthy}
        problem={problem}
        offline={offline}
      />

      {total === 0 ? (
        <EmptyState
          icon={Network}
          title="Парк пуст"
          description="Ни один роутер ещё не зарегистрирован. Запустите enrollment, чтобы увидеть карточки тут."
          primaryAction={{ label: "Открыть enrollment", href: "/enrollment" }}
        />
      ) : (
        <FleetTable
          routers={tableRouters}
          initialSearchQuery={initialSearchQuery}
        />
      )}
    </section>
  );
}
