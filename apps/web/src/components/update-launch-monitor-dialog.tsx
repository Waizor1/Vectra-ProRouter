"use client";

import { useEffect, useMemo } from "react";

import { StatusTile } from "~/components/status-tile";
import type { RouterOutputs } from "~/trpc/react";

type LaunchProgressWorkspace = RouterOutputs["update"]["launchProgress"];
type LaunchProgressItem = LaunchProgressWorkspace["items"][number];

export type UpdateLaunchMonitorEntry = {
  routerId: string;
  displayName: string;
  queueStatus: "queued" | "failed";
  queueError: string | null;
  jobId: string | null;
};

export type UpdateLaunchMonitorSession = {
  id: string;
  actionLabel: string;
  createdAt: number;
  entries: UpdateLaunchMonitorEntry[];
};

type MonitorEntryState =
  | "queue-failed"
  | "queued"
  | "running"
  | "success"
  | "problem"
  | "no-confirmation";

function formatDateTime(value: Date | string | number | null | undefined) {
  if (!value) {
    return "ещё нет";
  }

  const date =
    value instanceof Date
      ? value
      : typeof value === "number"
        ? new Date(value)
        : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}

function buildTargetLabel(item: LaunchProgressItem) {
  if (item.artifactVersion) {
    return item.artifactVersion;
  }

  if (item.targetVersion) {
    return item.targetVersion;
  }

  if (item.packageTargetVersion || item.runtimeTargetVersion) {
    return [item.packageTargetVersion, item.runtimeTargetVersion]
      .filter(Boolean)
      .join(" / ");
  }

  return null;
}

function classifyMonitorEntry(args: {
  seed: UpdateLaunchMonitorEntry;
  liveItem: LaunchProgressItem | null;
}): {
  state: MonitorEntryState;
  label: string;
  badgeClassName: string;
  cardClassName: string;
  tone: "default" | "good" | "warning" | "danger";
  detailsOpen: boolean;
} {
  if (args.seed.queueStatus === "failed") {
    return {
      state: "queue-failed",
      label: "не поставлено",
      badgeClassName: "border-rose-400/30 bg-rose-500/10 text-rose-100",
      cardClassName: "border-rose-400/18 bg-[rgba(38,12,16,0.6)]",
      tone: "danger",
      detailsOpen: true,
    };
  }

  if (!args.liveItem) {
    return {
      state: "queued",
      label: "жду ответ",
      badgeClassName: "border-white/15 bg-black/10 text-slate-200",
      cardClassName: "border-white/10 bg-[var(--vectra-panel-soft)]",
      tone: "default",
      detailsOpen: false,
    };
  }

  if (args.liveItem.deliveryBlocked) {
    return {
      state: "problem",
      label: "доставка блокируется",
      badgeClassName: "border-amber-400/30 bg-amber-500/10 text-amber-100",
      cardClassName: "border-amber-400/18 bg-[rgba(56,34,10,0.46)]",
      tone: "warning",
      detailsOpen: true,
    };
  }

  if (
    ["queued", "delivered", "running"].includes(args.liveItem.jobState) ||
    args.liveItem.resultStatus === "accepted"
  ) {
    return {
      state: args.liveItem.jobState === "running" ? "running" : "queued",
      label:
        args.liveItem.jobState === "running" || args.liveItem.resultStatus === "accepted"
          ? "в работе"
          : "в очереди",
      badgeClassName: "border-sky-400/30 bg-sky-500/10 text-sky-100",
      cardClassName: "border-sky-400/16 bg-[rgba(13,29,47,0.55)]",
      tone: "warning",
      detailsOpen: false,
    };
  }

  if (
    args.liveItem.resultStatus === "failure" ||
    args.liveItem.jobState === "failed"
  ) {
    return {
      state: "problem",
      label: "ошибка",
      badgeClassName: "border-rose-400/30 bg-rose-500/10 text-rose-100",
      cardClassName: "border-rose-400/18 bg-[rgba(38,12,16,0.6)]",
      tone: "danger",
      detailsOpen: true,
    };
  }

  if (args.liveItem.resultStatus === null) {
    return {
      state: "no-confirmation",
      label: "без подтверждения",
      badgeClassName: "border-white/15 bg-black/10 text-slate-200",
      cardClassName: "border-white/10 bg-[var(--vectra-panel-soft)]",
      tone: "default",
      detailsOpen: false,
    };
  }

  return {
    state: "success",
    label: "успешно",
    badgeClassName: "border-emerald-400/30 bg-emerald-500/10 text-emerald-100",
    cardClassName: "border-emerald-400/18 bg-[rgba(8,41,28,0.55)]",
    tone: "good",
    detailsOpen: false,
  };
}

function summarizeEntry(args: {
  seed: UpdateLaunchMonitorEntry;
  liveItem: LaunchProgressItem | null;
  state: MonitorEntryState;
}) {
  if (args.seed.queueStatus === "failed") {
    return args.seed.queueError ?? "Панель не смогла поставить задачу в очередь.";
  }

  if (!args.liveItem) {
    return "Задача уже принята панелью, жду первую телеметрию от роутера.";
  }

  if (args.liveItem.deliveryBlocked && args.liveItem.deliveryBlockedReason) {
    return `Delivery path сейчас ненадёжен: ${args.liveItem.deliveryBlockedReason}`;
  }

  if (args.state === "no-confirmation") {
    return "Задача ушла на роутер, но финального статуса пока нет.";
  }

  return args.liveItem.summary;
}

function formatPackageStatus(status: string | null) {
  switch (status) {
    case "updated":
    case "package-updated":
      return "обновлён";
    case "already-current":
      return "актуален";
    case "runtime-updated":
    case "runtime-only-converged":
      return "runtime";
    case "storage-blocked":
      return "нет места";
    case "delivery-blocked":
      return "delivery";
    case "failed":
      return "ошибка";
    case null:
      return "нет статуса";
    default:
      return status;
  }
}

function buildPackageChipClassName(status: string | null) {
  switch (status) {
    case "updated":
    case "package-updated":
    case "already-current":
    case "runtime-updated":
    case "runtime-only-converged":
      return "border-emerald-400/20 bg-emerald-500/10 text-emerald-100";
    case "storage-blocked":
    case "delivery-blocked":
      return "border-amber-400/20 bg-amber-500/10 text-amber-100";
    case "failed":
      return "border-rose-400/20 bg-rose-500/10 text-rose-100";
    default:
      return "border-white/10 bg-black/10 text-slate-300";
  }
}

function renderOutput(label: string, value: string | null) {
  if (!value) {
    return null;
  }

  return (
    <div className="space-y-2">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <pre className="overflow-x-auto rounded-xl border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
        {value}
      </pre>
    </div>
  );
}

export function UpdateLaunchMonitorDialog({
  session,
  progress,
  isLoading,
  isFetching,
  onClose,
  onRefresh,
}: {
  session: UpdateLaunchMonitorSession;
  progress: LaunchProgressWorkspace | undefined;
  isLoading: boolean;
  isFetching: boolean;
  onClose: () => void;
  onRefresh: () => void;
}) {
  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose]);

  const liveItemByJobId = useMemo(
    () => new Map((progress?.items ?? []).map((item) => [item.jobId, item] as const)),
    [progress?.items],
  );

  const entries = useMemo(() => {
    return session.entries.map((seed) => {
      const liveItem = seed.jobId ? (liveItemByJobId.get(seed.jobId) ?? null) : null;
      const classification = classifyMonitorEntry({
        seed,
        liveItem,
      });

      return {
        seed,
        liveItem,
        targetLabel: liveItem ? buildTargetLabel(liveItem) : null,
        summary: summarizeEntry({
          seed,
          liveItem,
          state: classification.state,
        }),
        classification,
      };
    });
  }, [liveItemByJobId, session.entries]);

  const summary = useMemo(() => {
    return entries.reduce(
      (accumulator, entry) => {
        accumulator.total += 1;

        switch (entry.classification.state) {
          case "success":
            accumulator.success += 1;
            break;
          case "problem":
          case "queue-failed":
            accumulator.problems += 1;
            break;
          case "running":
            accumulator.running += 1;
            break;
          case "queued":
          case "no-confirmation":
            accumulator.waiting += 1;
            break;
        }

        return accumulator;
      },
      {
        total: 0,
        success: 0,
        problems: 0,
        running: 0,
        waiting: 0,
      },
    );
  }, [entries]);

  const allSettled = summary.running === 0 && summary.waiting === 0;

  return (
    <div
      className="vectra-dialog-backdrop fixed inset-0 z-50 bg-[rgba(4,7,11,0.82)] backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="updates-launch-monitor-title"
    >
      <div className="vectra-dialog-panel mx-auto flex h-full max-w-6xl flex-col overflow-hidden rounded-[24px] border border-white/10 bg-[rgba(9,12,18,0.96)] shadow-[0_28px_120px_rgba(0,0,0,0.45)] sm:rounded-[28px]">
        <div className="border-b border-white/8 px-4 py-4 sm:px-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0 space-y-2">
              <p className="vectra-kicker text-slate-500">Live Progress</p>
              <div className="flex flex-wrap items-center gap-3">
                <h2
                  id="updates-launch-monitor-title"
                  className="text-lg font-semibold tracking-[-0.02em] text-white sm:text-xl"
                >
                  {session.actionLabel}
                </h2>
                <span className="rounded-full border border-white/10 bg-black/10 px-3 py-1 text-xs font-semibold tracking-[0.14em] text-slate-300 uppercase">
                  {isFetching ? "обновляю" : "live"}
                </span>
              </div>
              <p className="max-w-3xl text-sm leading-6 text-slate-300">
                Окно следит только за этим запуском, обновляется автоматически и показывает
                как ошибки постановки в очередь, так и ответ самого роутера.
              </p>
              <p className="text-xs text-slate-500">
                Запуск из панели: {formatDateTime(session.createdAt)}
              </p>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={onRefresh}
                disabled={isFetching}
                className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
              >
                {isFetching ? "Обновляю..." : "Обновить сейчас"}
              </button>
              <button
                type="button"
                onClick={onClose}
                className="vectra-button-secondary px-3 py-2 text-sm font-medium transition"
              >
                Закрыть окно
              </button>
            </div>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto px-4 py-4 sm:px-6 sm:py-5">
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
              <StatusTile
                label="Всего роутеров"
                value={String(summary.total)}
                compact
                emphasis
              />
              <StatusTile
                label="Ждут / нет финала"
                value={String(summary.waiting)}
                tone={summary.waiting > 0 ? "default" : "good"}
                compact
              />
              <StatusTile
                label="В работе"
                value={String(summary.running)}
                tone={summary.running > 0 ? "warning" : "good"}
                compact
              />
              <StatusTile
                label="Успешно"
                value={String(summary.success)}
                tone={summary.success > 0 ? "good" : "default"}
                compact
              />
              <StatusTile
                label="Проблемы"
                value={String(summary.problems)}
                tone={summary.problems > 0 ? "danger" : "good"}
                compact
              />
            </div>

            {allSettled ? (
              <div
                className={`rounded-2xl border px-4 py-3 text-sm leading-6 ${
                  summary.problems > 0
                    ? "border-amber-400/20 bg-amber-500/10 text-amber-50"
                    : "border-emerald-400/20 bg-emerald-500/10 text-emerald-50"
                }`}
              >
                {summary.problems > 0
                  ? "Все роутеры дошли до финального статуса, но есть задачи, которым нужно внимание."
                  : "Все роутеры этого запуска уже дошли до финального статуса без ошибок."}
              </div>
            ) : (
              <div className="rounded-2xl border border-sky-400/20 bg-sky-500/10 px-4 py-3 text-sm leading-6 text-sky-50">
                Следим за прогрессом в реальном времени. Если роутер офлайн, карточка останется в
                состоянии ожидания до следующего check-in.
              </div>
            )}

            {isLoading ? (
              <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-6 text-sm text-slate-300">
                Загружаю первый срез статусов по этому запуску...
              </div>
            ) : null}

            <div className="grid gap-4 xl:grid-cols-2">
              {entries.map((entry) => (
                <article
                  key={`${session.id}-${entry.seed.routerId}-${entry.seed.jobId ?? "queue-error"}`}
                  className={`rounded-2xl border px-4 py-4 ${entry.classification.cardClassName}`}
                >
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <p className="vectra-kicker text-slate-500">Роутер</p>
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-base font-semibold text-white">
                          {entry.liveItem?.displayName ?? entry.seed.displayName}
                        </h3>
                        {entry.targetLabel ? (
                          <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1 text-[11px] text-slate-300">
                            {entry.targetLabel}
                          </span>
                        ) : null}
                      </div>
                      <p className="text-sm text-slate-300">
                        {entry.liveItem?.label ?? session.actionLabel}
                      </p>
                    </div>

                    <span
                      className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.14em] uppercase ${entry.classification.badgeClassName}`}
                    >
                      {entry.classification.label}
                    </span>
                  </div>

                  <p
                    className={`mt-3 text-sm leading-6 ${
                      entry.classification.tone === "danger"
                        ? "text-rose-100"
                        : entry.classification.tone === "warning"
                          ? "text-amber-100"
                          : "text-slate-200"
                    }`}
                  >
                    {entry.summary}
                  </p>

                  <p className="mt-2 text-xs leading-6 text-slate-500">
                    Поставлено {formatDateTime(entry.liveItem?.createdAt ?? session.createdAt)}
                    {entry.liveItem?.reportedAt
                      ? ` · последний ответ ${formatDateTime(entry.liveItem.reportedAt)}`
                      : ""}
                  </p>

                  {entry.liveItem?.packageResults.length ? (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {entry.liveItem.packageResults.map((packageResult) => (
                        <span
                          key={`${entry.liveItem?.jobId}-${packageResult.package}`}
                          className={`rounded-full border px-2 py-1 text-[11px] ${buildPackageChipClassName(packageResult.status)}`}
                        >
                          {packageResult.package}: {formatPackageStatus(packageResult.status)}
                        </span>
                      ))}
                    </div>
                  ) : null}

                  {entry.seed.queueError && entry.seed.queueStatus === "failed" ? (
                    <div className="mt-3 rounded-xl border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-3 py-3 text-sm leading-6 text-rose-100">
                      {entry.seed.queueError}
                    </div>
                  ) : null}

                  {entry.liveItem &&
                  (entry.classification.detailsOpen ||
                    entry.liveItem.error ||
                    entry.liveItem.stdout ||
                    entry.liveItem.stderr ||
                    entry.liveItem.command ||
                    entry.liveItem.packageResults.some((packageResult) => packageResult.error)) ? (
                    <details
                      className="mt-4 rounded-2xl border border-white/10 bg-[rgba(11,14,20,0.74)] px-3 py-3"
                      open={entry.classification.detailsOpen}
                    >
                      <summary className="min-h-11 cursor-pointer list-none text-sm font-medium text-white">
                        Показать детали
                      </summary>
                      <div className="mt-3 space-y-3">
                        {entry.liveItem.error ? (
                          <div className="space-y-2">
                            <p className="vectra-kicker text-rose-300">Ошибка</p>
                            <pre className="overflow-x-auto rounded-xl border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-3 py-3 text-xs leading-6 text-rose-100">
                              {entry.liveItem.error}
                            </pre>
                          </div>
                        ) : null}

                        {entry.liveItem.command ? (
                          <div className="space-y-2">
                            <p className="vectra-kicker text-slate-500">Команда</p>
                            <pre className="overflow-x-auto rounded-xl border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
                              {entry.liveItem.command}
                            </pre>
                          </div>
                        ) : null}

                        {entry.liveItem.packageResults.length ? (
                          <div className="space-y-2">
                            <p className="vectra-kicker text-slate-500">Пакеты внутри задачи</p>
                            <div className="space-y-2">
                              {entry.liveItem.packageResults.map((packageResult) => (
                                <div
                                  key={`${entry.liveItem?.jobId ?? entry.seed.jobId ?? entry.seed.routerId}-${packageResult.package}-details`}
                                  className="rounded-xl border border-white/10 bg-[rgba(6,8,12,0.72)] px-3 py-3 text-sm text-slate-200"
                                >
                                  <div className="flex flex-wrap items-center justify-between gap-2">
                                    <span className="font-medium text-white">
                                      {packageResult.package}
                                    </span>
                                    <span className="text-xs text-slate-400">
                                      {formatPackageStatus(packageResult.status)}
                                    </span>
                                  </div>
                                  {(packageResult.pathUsed ||
                                    packageResult.packageVersionAfter ||
                                    packageResult.runtimeVersionAfter) ? (
                                    <p className="mt-2 text-xs leading-6 text-slate-400">
                                      {[
                                        packageResult.pathUsed
                                          ? `path ${packageResult.pathUsed}`
                                          : null,
                                        packageResult.packageVersionAfter
                                          ? `package ${packageResult.packageVersionAfter}`
                                          : null,
                                        packageResult.runtimeVersionAfter
                                          ? `runtime ${packageResult.runtimeVersionAfter}`
                                          : null,
                                      ]
                                        .filter(Boolean)
                                        .join(" · ")}
                                    </p>
                                  ) : null}
                                  {packageResult.error ? (
                                    <p className="mt-2 text-xs leading-6 text-rose-200">
                                      {packageResult.error}
                                    </p>
                                  ) : null}
                                </div>
                              ))}
                            </div>
                          </div>
                        ) : null}

                        {renderOutput("stdout", entry.liveItem.stdout)}
                        {renderOutput("stderr", entry.liveItem.stderr)}
                      </div>
                    </details>
                  ) : null}
                </article>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
