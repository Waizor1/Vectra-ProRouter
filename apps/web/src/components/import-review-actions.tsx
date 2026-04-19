"use client";

import { useRouter } from "next/navigation";

import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
} from "~/lib/router-onboarding";
import { api } from "~/trpc/react";

type ImportReviewActionsProps = {
  routerId: string;
  revisionId?: string | null;
  importState: string;
  configTrust?: {
    liveConfigAvailable?: boolean | null;
    requiresReimport?: boolean | null;
    digestMismatch?: boolean | null;
    configSourceMode?: string | null;
  } | null;
};

export function ImportReviewActions({
  routerId,
  revisionId,
  importState,
  configTrust,
}: ImportReviewActionsProps) {
  const router = useRouter();
  const utils = api.useUtils();
  const onboarding = describeRouterOnboarding(importState, configTrust);
  const canApprove = Boolean(revisionId) && importState !== "approved";
  const approved = importState === "approved" && !configTrust?.requiresReimport;
  const heading = approved ? "Роутер уже готов к обычной работе" : onboarding.title;
  const summary = approved
    ? "Эталон подтверждён. Дальше здесь обычные локальные правки и apply."
    : onboarding.summary;

  const approveMutation = api.fleet.approveImportedBaseline.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.fleet.pendingImportReviews.invalidate(),
        utils.draft.workspace.invalidate({ routerId }),
        utils.draft.editorSurface.invalidate({ routerId }),
      ]);
      router.refresh();
    },
  });

  const reimportMutation = api.fleet.requestReimport.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.fleet.pendingImportReviews.invalidate(),
        utils.draft.workspace.invalidate({ routerId }),
        utils.draft.editorSurface.invalidate({ routerId }),
      ]);
      router.refresh();
    },
  });

  const toneClassName =
    onboarding.tone === "good"
      ? "border-emerald-400/20 bg-emerald-500/10"
      : onboarding.tone === "warning"
        ? "border-amber-400/20 bg-amber-500/10"
        : "border-white/10 bg-[rgba(10,14,20,0.74)]";

  const badgeClassName =
    onboarding.tone === "good"
      ? "text-emerald-200"
      : onboarding.tone === "warning"
        ? "text-amber-200"
        : "text-slate-400";

  return (
    <section className={`rounded-md border px-3 py-3 ${toneClassName}`}>
      <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between">
        <div>
          <p className={`vectra-kicker ${badgeClassName}`}>{onboarding.badge}</p>
          <h3 className="mt-2 text-base font-semibold tracking-[-0.01em] text-white">
            {heading}
          </h3>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-slate-200">
            {summary}
          </p>
        </div>
        <span className="rounded-full border border-white/12 bg-white/6 px-3 py-1 text-xs font-medium text-slate-100">
          {formatRouterImportStateLabel(importState)}
        </span>
      </div>

      {!approved ? (
        <details className="mt-4 rounded-md border border-white/10 bg-black/10 px-3 py-3">
          <summary className="cursor-pointer list-none text-sm font-medium text-white">
            Шаги подключения
          </summary>
          <div className="mt-3 grid gap-2 sm:grid-cols-3">
            {onboarding.steps.map((step, index) => (
              <div
                key={`${index + 1}-${step}`}
                className="rounded-md border border-white/10 bg-black/10 px-3 py-3"
              >
                <p className="vectra-kicker text-slate-500">Шаг {index + 1}</p>
                <p className="mt-2 text-sm leading-6 text-slate-200">{step}</p>
              </div>
            ))}
          </div>
        </details>
      ) : null}

      <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:flex-wrap">
        {!approved ? (
          <button
            type="button"
            className="w-full rounded-md bg-[var(--vectra-accent)] px-3 py-2 text-sm font-medium text-slate-950 transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)] disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
            disabled={!canApprove || approveMutation.isPending}
            onClick={() =>
              revisionId
                ? approveMutation.mutate({
                    routerId,
                    revisionId,
                  })
                : undefined
            }
          >
            {approveMutation.isPending
              ? "Подтверждаю..."
              : canApprove
                ? onboarding.approveLabel
                : onboarding.approveUnavailableLabel}
          </button>
        ) : (
          <span className="w-full rounded-md border border-emerald-400/20 bg-emerald-500/10 px-3 py-2 text-sm font-medium text-emerald-100 sm:w-auto">
            Эталон уже подтверждён
          </span>
        )}
        <button
          type="button"
          className="w-full rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
          disabled={reimportMutation.isPending}
          onClick={() =>
            reimportMutation.mutate({
              routerId,
            })
          }
        >
          {reimportMutation.isPending
            ? "Запрашиваю import..."
            : onboarding.reimportLabel}
        </button>
      </div>

      <p className="mt-3 text-sm leading-6 text-slate-400">
        {configTrust?.requiresReimport
          ? "Свежий snapshot уже есть, но глубокая PassWall-конфигурация ещё не подтверждена live import-ом. До re-import панель продолжает опираться на эталон."
          : "После подтверждения все черновики и apply считаются уже от этой базы."}
      </p>
    </section>
  );
}
