import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";
import { StatusTile } from "~/components/status-tile";
import { api } from "~/trpc/server";

export default async function RescuePage() {
  const [policy, incidents, directRouters] = await Promise.all([
    api.rescue.policy(),
    api.rescue.openIncidents(),
    api.rescue.directRouters(),
  ]);

  return (
    <section className="mx-auto max-w-6xl space-y-4">
      <PageHeader
        eyebrow="Восстановление"
        title="Rescue policy и direct-mode контур"
        description="Экран для текущих direct-mode случаев и открытых rescue-инцидентов. Политика остаётся компактной, а живой список устройств и инцидентов вынесен ниже."
        mobileDescription="Direct mode, policy и инциденты."
        compact
      />

      <section className="vectra-hero-panel space-y-4 rounded-[1.6rem] px-4 py-4 sm:px-5 sm:py-5">
        <div>
          <p className="vectra-kicker text-[var(--vectra-accent)]">Политика rescue</p>
          <h2 className="mt-1 text-xl font-semibold tracking-[-0.03em] text-white sm:text-2xl">
            Порог, возврат и сообщение direct mode
          </h2>
        </div>
        <div className="space-y-4">
          <div className="vectra-stat-grid">
            <StatusTile
              label="Триггер"
              value={String(policy.triggerFailureCount)}
              hint="провала до direct mode"
              compact
            />
            <StatusTile
              label="Возврат"
              value={String(policy.recoverySuccessCount)}
              hint="успешных проверок до proxy"
              compact
            />
            <StatusTile
              label="Cooldown"
              value={String(Math.round(policy.cooldownSeconds / 60))}
              hint="минут между циклами"
              compact
            />
            <StatusTile
              label="Direct path"
              value={policy.requireDirectPathSuccess ? "нужен" : "не обязателен"}
              hint="для выхода из аварийного режима"
              compact
            />
          </div>

          <div className="rounded-2xl border border-amber-500/20 bg-amber-500/10 px-3 py-3">
            <p className="vectra-kicker text-amber-200">Сообщение direct mode</p>
            <p className="mt-2 text-base font-semibold text-white sm:text-lg">
              {policy.directModeReason}
            </p>
            <p className="mt-2 text-sm leading-6 text-slate-200">
              Причина должна оставаться короткой и однозначной — без лишней
              операторской расшифровки на уровне shell.
            </p>
          </div>
        </div>
      </section>

      <div className="grid gap-4 xl:grid-cols-2">
        <Panel
          eyebrow="Текущее состояние"
          title="Роутеры в direct mode"
          tone="muted"
          aside={
            <div className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-sm text-slate-300">
              {directRouters.length}
            </div>
          }
        >
          <div className="space-y-3">
            {directRouters.length > 0 ? (
              directRouters.map((router) => (
                <div
                  key={router.id}
                  className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm text-slate-200"
                >
                  <p className="font-semibold text-white">
                    {router.displayName ?? router.hostname ?? router.deviceIdentifier}
                  </p>
                  <p className="mt-1 text-slate-400">
                    {router.lastRescueReason ?? "Причина не записана"}
                  </p>
                </div>
              ))
            ) : (
              <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300">
                Сейчас ни один роутер не зафиксирован в прямом режиме.
              </div>
            )}
          </div>
        </Panel>

        <Panel
          eyebrow="Инциденты"
          title="Открытые rescue-инциденты"
          tone="muted"
          aside={
            <div className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-sm text-slate-300">
              {incidents.length}
            </div>
          }
        >
          <div className="space-y-3">
            {incidents.length > 0 ? (
              incidents.map((incident) => (
                <div
                  key={incident.id}
                  className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm text-slate-200"
                >
                  <p className="font-semibold text-white">{incident.type}</p>
                  <p className="mt-1 text-slate-400">{incident.reason}</p>
                </div>
              ))
            ) : (
              <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300">
                Открытых инцидентов нет. Парк сейчас стабилен.
              </div>
            )}
          </div>
        </Panel>
      </div>
    </section>
  );
}
