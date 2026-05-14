"use client";

import type { ReactNode } from "react";

import Link from "next/link";
import { useRouter } from "next/navigation";

import { Panel } from "~/components/panel";
import { RouterManagementTaskLog } from "~/components/router-management-task-log";
import { api, type RouterOutputs } from "~/trpc/react";

type RescueCaseDetails = RouterOutputs["rescue"]["caseById"];

type RescueCaseCockpitProps = {
  initialDetails: RescueCaseDetails;
};

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
    second: "2-digit",
  }).format(date);
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function prettyJson(value: unknown) {
  return JSON.stringify(value ?? {}, null, 2);
}

function statusTone(state: string) {
  switch (state) {
    case "resolved":
      return "border-emerald-400/30 bg-emerald-500/10 text-emerald-100";
    case "escalated":
      return "border-rose-400/30 bg-rose-500/10 text-rose-100";
    case "repairing":
      return "border-sky-400/30 bg-sky-500/10 text-sky-100";
    case "silenced":
      return "border-amber-400/30 bg-amber-500/10 text-amber-100";
    default:
      return "border-white/15 bg-white/5 text-slate-100";
  }
}

function ActionButton({
  children,
  disabled,
  onClick,
  tone = "secondary",
}: {
  children: ReactNode;
  disabled?: boolean;
  onClick: () => void;
  tone?: "primary" | "secondary" | "danger";
}) {
  const className =
    tone === "primary"
      ? "vectra-button-primary px-3 py-2 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-50"
      : tone === "danger"
        ? "rounded-md border border-amber-400/30 bg-amber-500/10 px-3 py-2 text-sm font-medium text-amber-100 transition hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        : "vectra-button-secondary px-3 py-2 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-50";

  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={className}
    >
      {children}
    </button>
  );
}

export function RescueCaseCockpit({ initialDetails }: RescueCaseCockpitProps) {
  const router = useRouter();
  const utils = api.useUtils();
  const caseId = initialDetails.case.id;
  const detailsQuery = api.rescue.caseById.useQuery(
    { caseId },
    {
      initialData: initialDetails,
      refetchInterval: 5000,
    },
  );
  const details = detailsQuery.data ?? initialDetails;
  const rescueCase = details.case;
  const triggerDetails = asRecord(rescueCase.triggerDetails);
  const diagnosis = asRecord(rescueCase.diagnosis);
  const actionDisabled = ["resolved", "silenced"].includes(rescueCase.state);
  const pendingMutation = false;

  const invalidate = async () => {
    await Promise.all([
      utils.rescue.caseById.invalidate({ caseId }),
      utils.rescue.cases.invalidate(),
      details.router
        ? utils.rescue.activeCaseForRouter.invalidate({
            routerId: details.router.id,
          })
        : Promise.resolve(),
    ]);
    router.refresh();
  };

  const safeRepairMutation = api.rescue.runCaseSafeRepair.useMutation({
    onSuccess: invalidate,
  });
  const reconnectMutation = api.rescue.reconnectCase.useMutation({
    onSuccess: invalidate,
  });
  const logsMutation = api.rescue.collectCaseLogs.useMutation({
    onSuccess: invalidate,
  });
  const silenceMutation = api.rescue.silenceCase.useMutation({
    onSuccess: invalidate,
  });
  const anyPending =
    pendingMutation ||
    safeRepairMutation.isPending ||
    reconnectMutation.isPending ||
    logsMutation.isPending ||
    silenceMutation.isPending;

  const errorMessage =
    safeRepairMutation.error?.message ??
    reconnectMutation.error?.message ??
    logsMutation.error?.message ??
    silenceMutation.error?.message ??
    null;

  return (
    <section className="mx-auto max-w-6xl space-y-4">
      <div className="flex flex-col justify-between gap-4 rounded-3xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 py-4 sm:px-5 lg:flex-row lg:items-start">
        <div>
          <p className="vectra-kicker text-slate-500">Guided rescue cockpit</p>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white sm:text-3xl">
            Auto-Rescue case
          </h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-300">
            Панель уже собрала compact evidence и запускает только safe repair:
            restart сервисов, refresh rules/subscriptions и reconnect proxy.
            Terminal остаётся запасной ручной секцией после входа оператора, а
            не ChatOps из Telegram.
          </p>
        </div>
        <span
          className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.16em] uppercase ${statusTone(
            rescueCase.state,
          )}`}
        >
          {rescueCase.state}
        </span>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
        <Panel
          eyebrow="Причина"
          title="Почему automation открыла case"
          tone="muted"
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
              <p className="vectra-kicker text-slate-500">Trigger</p>
              <p className="mt-2 text-lg font-semibold text-white">
                {rescueCase.trigger}
              </p>
              <p className="mt-2 text-sm leading-6 text-slate-400">
                {typeof triggerDetails.reason === "string"
                  ? triggerDetails.reason
                  : "Причина сохранена в evidence bundle."}
              </p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
              <p className="vectra-kicker text-slate-500">Router</p>
              <p className="mt-2 text-lg font-semibold text-white">
                {details.router?.displayName ??
                  details.router?.hostname ??
                  details.router?.deviceIdentifier ??
                  rescueCase.routerId}
              </p>
              <div className="mt-2 flex flex-wrap gap-2 text-sm">
                <Link
                  href={`/routers/${rescueCase.routerId}`}
                  className="text-[var(--vectra-accent)] hover:text-white"
                >
                  открыть detail
                </Link>
                <Link
                  href="/rescue"
                  className="text-[var(--vectra-accent)] hover:text-white"
                >
                  все rescue cases
                </Link>
              </div>
            </div>
          </div>
        </Panel>

        <Panel eyebrow="Safe actions" title="Операторские кнопки" tone="muted">
          <div className="flex flex-col gap-2">
            <ActionButton
              tone="primary"
              disabled={actionDisabled || anyPending}
              onClick={() => safeRepairMutation.mutate({ caseId })}
            >
              {safeRepairMutation.isPending
                ? "Ставлю safe repair..."
                : "Run safe repair"}
            </ActionButton>
            <ActionButton
              disabled={actionDisabled || anyPending}
              onClick={() => reconnectMutation.mutate({ caseId })}
            >
              {reconnectMutation.isPending
                ? "Ставлю reconnect..."
                : "Reconnect proxy"}
            </ActionButton>
            <ActionButton
              disabled={anyPending}
              onClick={() => logsMutation.mutate({ caseId })}
            >
              {logsMutation.isPending
                ? "Ставлю collect logs..."
                : "Collect logs all/200"}
            </ActionButton>
            <ActionButton
              tone="danger"
              disabled={rescueCase.state === "resolved" || anyPending}
              onClick={() =>
                silenceMutation.mutate({ caseId, durationSeconds: 60 * 60 })
              }
            >
              {silenceMutation.isPending ? "Глушу alert..." : "Silence 1h"}
            </ActionButton>
          </div>
          {errorMessage ? (
            <p className="mt-3 rounded-md border border-rose-400/20 bg-rose-500/10 px-3 py-2 text-sm leading-6 text-rose-100">
              {errorMessage}
            </p>
          ) : null}
        </Panel>
      </div>

      <Panel eyebrow="Timeline" title="Состояние case" tone="muted">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {[
            ["Started", rescueCase.startedAt],
            ["Last attempt", rescueCase.lastAttemptAt],
            ["Escalated", rescueCase.escalatedAt],
            ["Resolved", rescueCase.resolvedAt],
          ].map(([label, value]) => (
            <div
              key={String(label)}
              className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3"
            >
              <p className="vectra-kicker text-slate-500">{String(label)}</p>
              <p className="mt-2 text-sm font-medium text-white">
                {formatDateTime(value as Date | string | null)}
              </p>
            </div>
          ))}
        </div>
      </Panel>

      <div className="grid gap-4 xl:grid-cols-2">
        <Panel eyebrow="Evidence" title="Compact evidence bundle" tone="muted">
          <pre className="max-h-[480px] overflow-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
            {prettyJson(rescueCase.evidence)}
          </pre>
        </Panel>
        <Panel eyebrow="Diagnosis" title="Attempts and diagnosis" tone="muted">
          <pre className="max-h-[480px] overflow-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
            {prettyJson({
              diagnosis,
              repairAttempts: rescueCase.repairAttempts,
            })}
          </pre>
        </Panel>
      </div>

      <Panel eyebrow="Live jobs" title="Progress from router jobs" tone="muted">
        <RouterManagementTaskLog items={details.managementTaskLog} />
      </Panel>

      <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 py-3">
        <summary className="min-h-11 cursor-pointer list-none text-sm font-medium text-white">
          Manual terminal section
        </summary>
        <p className="mt-3 text-sm leading-6 text-slate-300">
          V1 Telegram не принимает shell-команды. Если guided actions не
          хватает, откройте router detail и используйте terminal/watch logs как
          вторичный ручной путь после operator login.
        </p>
        <Link
          href={`/routers/${rescueCase.routerId}?tab=terminal`}
          className="mt-3 inline-flex text-sm font-medium text-[var(--vectra-accent)] hover:text-white"
        >
          Перейти к router detail / terminal
        </Link>
      </details>
    </section>
  );
}
