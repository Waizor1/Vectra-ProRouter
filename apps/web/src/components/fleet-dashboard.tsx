import { Panel } from "./panel";
import { RouterCard } from "./router-card";
import type { RouterSummary } from "./router-card";
import type { StatItem } from "./stat-grid";
import { StatGrid } from "./stat-grid";

export function FleetDashboard({
  fleetStats,
  routers,
}: {
  fleetStats: StatItem[];
  routers: RouterSummary[];
}) {
  return (
    <section className="space-y-4">
      <StatGrid items={fleetStats} />

      <Panel
        eyebrow="Парк"
        title="Роутеры"
        aside={
          <p className="max-w-sm text-sm leading-7 text-slate-400">
            Откройте роутер, чтобы увидеть текущую конфигурацию, эталонное
            состояние, предпросмотр применения и историю. Если нужен fleet-wide
            rollout, точкой входа должен быть раздел `Обновления`.
          </p>
        }
      >
        <div className="grid gap-3 lg:grid-cols-2">
          {routers.length > 0 ? (
            routers.map((router) => (
              <RouterCard key={router.id} router={router} />
            ))
          ) : (
            <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300 lg:col-span-2">
              Роутеры ещё не зарегистрированы. Установите пакеты контроллера,
              задайте домен панели локально на роутере, и устройство появится
              здесь после первого опроса панели управления.
            </div>
          )}
        </div>
      </Panel>
    </section>
  );
}
