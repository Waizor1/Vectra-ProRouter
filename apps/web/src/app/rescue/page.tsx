import { OperatorWorkflowMap } from "~/components/operator-workflow-map";
import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";
import { api } from "~/trpc/server";

export default async function RescuePage() {
  const [policy, incidents, directRouters] = await Promise.all([
    api.rescue.policy(),
    api.rescue.openIncidents(),
    api.rescue.directRouters(),
  ]);

  return (
    <section className="space-y-4">
      <PageHeader
        eyebrow="Центр восстановления"
        title="Восстановление и direct mode"
        description="Аварийная политика, роутеры в direct mode и открытые инциденты."
        mobileDescription="Direct mode и инциденты."
      />

      <OperatorWorkflowMap current="rescue" compact />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <p className="vectra-kicker text-slate-500">Триггер</p>
          <p className="mt-2 text-2xl font-semibold text-white">
            {policy.triggerFailureCount}
          </p>
          <p className="mt-2 text-sm text-slate-300">провала до direct mode</p>
        </div>
        <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <p className="vectra-kicker text-slate-500">Возврат</p>
          <p className="mt-2 text-2xl font-semibold text-white">
            {policy.recoverySuccessCount}
          </p>
          <p className="mt-2 text-sm text-slate-300">успешных проверок до proxy</p>
        </div>
        <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <p className="vectra-kicker text-slate-500">Cooldown</p>
          <p className="mt-2 text-2xl font-semibold text-white">
            {Math.round(policy.cooldownSeconds / 60)}
          </p>
          <p className="mt-2 text-sm text-slate-300">минут между циклами</p>
        </div>
        <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <p className="vectra-kicker text-slate-500">Direct path</p>
          <p className="mt-2 text-2xl font-semibold text-white">
            {policy.requireDirectPathSuccess ? "нужен" : "не обязателен"}
          </p>
          <p className="mt-2 text-sm text-slate-300">для выхода из аварийного режима</p>
        </div>
      </div>

      <Panel eyebrow="Причина" title="Сообщение direct mode">
        <div className="rounded-md border border-amber-500/20 bg-amber-500/10 px-4 py-4">
          <p className="vectra-kicker text-amber-200">Что увидит оператор</p>
          <p className="mt-3 text-2xl font-semibold text-white">
            {policy.directModeReason}
          </p>
          <p className="mt-3 text-sm text-slate-200">
            Причина должна быть короткой и однозначной.
          </p>
        </div>
      </Panel>

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel
          eyebrow="Текущее состояние"
          title="Роутеры в direct mode"
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
                  className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm text-slate-200"
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
                  className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm text-slate-200"
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
