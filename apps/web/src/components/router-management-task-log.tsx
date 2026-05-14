"use client";

import type { RouterManagementTaskLogItem } from "~/server/vectra/editor-surface";

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "ещё нет";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  return new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function buildStatusBadge(item: RouterManagementTaskLogItem) {
  if (item.deliveryBlocked) {
    return {
      label: "доставка блокируется",
      className: "border-amber-400/30 bg-amber-500/10 text-amber-100",
    };
  }

  if (["queued", "delivered", "running"].includes(item.jobState) || item.resultStatus === "accepted") {
    return {
      label: "в процессе",
      className: "border-sky-400/30 bg-sky-500/10 text-sky-100",
    };
  }

  if (item.resultStatus === "failure" || item.jobState === "failed") {
    return {
      label: "ошибка",
      className: "border-rose-400/30 bg-rose-500/10 text-rose-100",
    };
  }

  if (item.resultStatus === null) {
    return {
      label: "нет подтверждения",
      className: "border-white/15 bg-black/10 text-slate-200",
    };
  }

  return {
    label: "успешно",
    className: "border-emerald-400/30 bg-emerald-500/10 text-emerald-100",
  };
}

function buildTargetLabel(item: RouterManagementTaskLogItem) {
  if (item.artifactVersion) {
    return item.artifactVersion;
  }

  if (item.targetVersion) {
    return item.targetVersion;
  }

  if (item.packageTargetVersion || item.runtimeTargetVersion) {
    return [item.packageTargetVersion, item.runtimeTargetVersion].filter(Boolean).join(" / ");
  }

  return null;
}

function renderOutput(label: string, value: string | null) {
  if (!value) {
    return null;
  }

  return (
    <div className="space-y-2">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <pre className="overflow-x-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
        {value}
      </pre>
    </div>
  );
}

export function RouterManagementTaskLog({
  items,
}: {
  items: RouterManagementTaskLogItem[];
}) {
  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-white/12 bg-[var(--vectra-panel-soft)] px-4 py-4 text-sm leading-7 text-slate-400">
        Для этого роутера пока нет panel-issued задач controller / PassWall / reboot в текущем окне истории.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {items.map((item) => {
        const badge = buildStatusBadge(item);
        const targetLabel = buildTargetLabel(item);

        return (
          <section
            key={item.jobId}
            className="rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4"
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="text-sm font-semibold text-white">{item.label}</p>
                  {targetLabel ? (
                    <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1 text-[11px] text-slate-300">
                      {targetLabel}
                    </span>
                  ) : null}
                </div>
                <p className="text-sm leading-6 text-slate-300">{item.summary}</p>
                {item.deliveryBlocked && item.deliveryBlockedReason ? (
                  <p className="text-xs leading-6 text-amber-200">
                    Delivery path сейчас ненадёжен: {item.deliveryBlockedReason}
                  </p>
                ) : null}
                <p className="text-xs text-slate-500">
                  Отправлено {formatDateTime(item.createdAt)} · ответ роутера {formatDateTime(item.reportedAt)}
                </p>
              </div>
              <span className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.14em] uppercase ${badge.className}`}>
                {badge.label}
              </span>
            </div>

            <details className="mt-4 rounded-lg border border-white/10 bg-[rgba(11,14,20,0.78)] px-3 py-3">
              <summary className="min-h-11 cursor-pointer list-none text-sm font-medium text-white">
                Показать детали ответа роутера
              </summary>
              <div className="mt-3 space-y-3">
                {item.error ? (
                  <div className="space-y-2">
                    <p className="vectra-kicker text-rose-300">Ошибка</p>
                    <pre className="overflow-x-auto rounded-md border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-3 py-3 text-xs leading-6 text-rose-100">
                      {item.error}
                    </pre>
                  </div>
                ) : null}

                {item.command ? (
                  <div className="space-y-2">
                    <p className="vectra-kicker text-slate-500">Команда</p>
                    <pre className="overflow-x-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
                      {item.command}
                    </pre>
                  </div>
                ) : null}

                {item.packageResults.length > 0 ? (
                  <div className="space-y-2">
                    <p className="vectra-kicker text-slate-500">Пакеты внутри задачи</p>
                    <div className="space-y-2">
                      {item.packageResults.map((entry) => (
                        <div
                          key={`${item.jobId}-${entry.package}`}
                          className="rounded-md border border-white/10 bg-[rgba(6,8,12,0.68)] px-3 py-3 text-sm text-slate-200"
                        >
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-medium text-white">{entry.package}</span>
                            <span className="text-xs text-slate-400">{entry.status ?? "без статуса"}</span>
                          </div>
                          {(entry.pathUsed || entry.packageVersionAfter || entry.runtimeVersionAfter) ? (
                            <p className="mt-2 text-xs leading-6 text-slate-400">
                              {[
                                entry.pathUsed ? `path ${entry.pathUsed}` : null,
                                entry.packageVersionAfter ? `package ${entry.packageVersionAfter}` : null,
                                entry.runtimeVersionAfter ? `runtime ${entry.runtimeVersionAfter}` : null,
                              ].filter(Boolean).join(" · ")}
                            </p>
                          ) : null}
                          {entry.error ? (
                            <p className="mt-2 text-xs leading-6 text-rose-200">{entry.error}</p>
                          ) : null}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}

                {renderOutput("stdout", item.stdout)}
                {renderOutput("stderr", item.stderr)}
              </div>
            </details>
          </section>
        );
      })}
    </div>
  );
}
