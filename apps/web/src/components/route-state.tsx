import Link from "next/link";
import type { ReactNode } from "react";

import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";

type RouteAction = {
  href: string;
  label: string;
  tone?: "primary" | "secondary";
};

function RouteActions({ actions }: { actions: RouteAction[] }) {
  if (actions.length === 0) {
    return null;
  }

  return (
    <div className="flex flex-wrap gap-2">
      {actions.map((action) => (
        <Link
          key={`${action.href}:${action.label}`}
          href={action.href}
          className={`${
            action.tone === "primary"
              ? "vectra-button-primary"
              : "vectra-button-secondary"
          } px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white`}
        >
          {action.label}
        </Link>
      ))}
    </div>
  );
}

function RouteStateShell({
  eyebrow,
  title,
  description,
  panelEyebrow,
  panelTitle,
  tone = "hero",
  statusLabel,
  summary,
  details,
  children,
}: {
  eyebrow: string;
  title: string;
  description: string;
  panelEyebrow: string;
  panelTitle: string;
  tone?: "default" | "hero" | "muted";
  statusLabel: string;
  summary: string;
  details?: string;
  children?: ReactNode;
}) {
  return (
    <section className="space-y-4">
      <PageHeader
        eyebrow={eyebrow}
        title={title}
        description={description}
        mobileDescription={description}
        compact
      />

      <Panel eyebrow={panelEyebrow} title={panelTitle} tone={tone}>
        <div className="space-y-4">
          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4 sm:px-5">
            <p className="vectra-kicker text-[var(--vectra-accent)]">
              {statusLabel}
            </p>
            <p className="mt-2 text-lg font-semibold tracking-[-0.02em] text-white sm:text-xl">
              {summary}
            </p>
            {details ? (
              <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-300">
                {details}
              </p>
            ) : null}
          </div>

          {children}
        </div>
      </Panel>
    </section>
  );
}

export function RouteLoadingState({
  eyebrow,
  title,
  description,
  summary,
  details,
  checkpoints,
}: {
  eyebrow: string;
  title: string;
  description: string;
  summary: string;
  details: string;
  checkpoints: readonly string[];
}) {
  return (
    <RouteStateShell
      eyebrow={eyebrow}
      title={title}
      description={description}
      panelEyebrow="Переход между экранами"
      panelTitle="Подготавливаем рабочую поверхность"
      tone="hero"
      statusLabel="Явная загрузка маршрута"
      summary={summary}
      details={details}
    >
      <div className="grid gap-3 lg:grid-cols-3">
        {checkpoints.map((checkpoint, index) => (
          <div
            key={checkpoint}
            className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 py-3"
          >
            <p className="vectra-kicker text-slate-500">Шаг {index + 1}</p>
            <p className="mt-2 text-sm font-medium text-white">{checkpoint}</p>
            <div className="mt-3 h-2 w-full overflow-hidden rounded-full bg-white/8">
              <div className="h-full w-2/3 animate-pulse rounded-full bg-[var(--vectra-accent)]/70" />
            </div>
          </div>
        ))}
      </div>
    </RouteStateShell>
  );
}

export function RouteEmptyState({
  eyebrow,
  title,
  description,
  summary,
  details,
  actions,
}: {
  eyebrow: string;
  title: string;
  description: string;
  summary: string;
  details: string;
  actions: RouteAction[];
}) {
  return (
    <RouteStateShell
      eyebrow={eyebrow}
      title={title}
      description={description}
      panelEyebrow="Пустое состояние"
      panelTitle="Пока нет данных для работы"
      tone="muted"
      statusLabel="Ожидаем следующий операторский шаг"
      summary={summary}
      details={details}
    >
      <RouteActions actions={actions} />
    </RouteStateShell>
  );
}

export function RouteNotFoundState({
  eyebrow,
  title,
  description,
  summary,
  details,
  actions,
}: {
  eyebrow: string;
  title: string;
  description: string;
  summary: string;
  details: string;
  actions: RouteAction[];
}) {
  return (
    <RouteStateShell
      eyebrow={eyebrow}
      title={title}
      description={description}
      panelEyebrow="Маршрут не найден"
      panelTitle="Не удалось открыть целевой объект"
      tone="muted"
      statusLabel="Подтверждённое отсутствие объекта"
      summary={summary}
      details={details}
    >
      <RouteActions actions={actions} />
    </RouteStateShell>
  );
}

export function RouteErrorState({
  eyebrow,
  title,
  description,
  summary,
  details,
  errorMessage,
  onRetry,
  retryLabel,
  actions,
}: {
  eyebrow: string;
  title: string;
  description: string;
  summary: string;
  details: string;
  errorMessage?: string;
  onRetry: () => void;
  retryLabel: string;
  actions: RouteAction[];
}) {
  return (
    <RouteStateShell
      eyebrow={eyebrow}
      title={title}
      description={description}
      panelEyebrow="Сбой чтения маршрута"
      panelTitle="Нужна повторная попытка или обходной переход"
      tone="muted"
      statusLabel="Явный сбой чтения маршрута"
      summary={summary}
      details={details}
    >
      {errorMessage ? (
        <div className="rounded-2xl border border-rose-400/25 bg-rose-500/10 px-4 py-3 text-sm leading-6 text-rose-50">
          <p className="vectra-kicker text-rose-200">Последняя ошибка</p>
          <p className="mt-2 font-[family:var(--font-plex-mono)] text-xs sm:text-sm">
            {errorMessage}
          </p>
        </div>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={onRetry}
          className="vectra-button-primary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
        >
          {retryLabel}
        </button>
        <RouteActions actions={actions} />
      </div>
    </RouteStateShell>
  );
}
