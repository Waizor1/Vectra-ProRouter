"use client";

import Link from "next/link";

import type { RouterTelegramReachability } from "@vectra/contracts";

import { formatControllerVersion } from "~/lib/controller-version";
import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
  isRouterOnboardingPending,
} from "~/lib/router-onboarding";
import {
  formatTelegramReachabilityLabel,
  getTelegramReachabilityStatus,
  hasTelegramReachabilityProblem,
} from "~/lib/telegram-reachability";

export type RouterSummary = {
  id: string;
  name: string;
  selectedNode: string;
  passwallEnabled: boolean;
  directMode: boolean;
  offline: boolean;
  reachable: boolean;
  statusLabel: string;
  nodeCount: number;
  subscriptionCount: number;
  controllerVersion: string;
  passwallVersion: string;
  components: Record<string, string>;
  lastSeen: string;
  pendingChanges: number;
  lastRescue: string;
  telegramReachability?: RouterTelegramReachability | null;
  importState: string;
  needsImportReview: boolean;
};

export function RouterCard({ router }: { router: RouterSummary }) {
  const onboarding = describeRouterOnboarding(router.importState);
  const onboardingPending = isRouterOnboardingPending(router.importState);
  const controllerVersion = formatControllerVersion(router.controllerVersion);
  const telegramStatus = getTelegramReachabilityStatus(
    router.telegramReachability,
  );
  const telegramProblem = hasTelegramReachabilityProblem(
    router.telegramReachability,
  );

  return (
    <article className="group overflow-hidden rounded-2xl border border-white/10 bg-[var(--vectra-panel)] shadow-[var(--vectra-shadow-md)]">
      <Link
        href={`/routers/${router.id}`}
        className="block p-3 transition hover:border-[var(--vectra-line-strong)] hover:bg-[rgba(22,28,41,0.98)] sm:p-4"
      >
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <p className="vectra-kicker text-slate-500">Роутер</p>
            <p className="mt-1 text-lg font-semibold tracking-[-0.01em] text-white sm:text-xl">
              {router.name}
            </p>
            <p className="mt-1 text-sm leading-6 text-slate-400">
              {router.selectedNode} · очередь {router.pendingChanges}
            </p>
          </div>
          <span
            className={`vectra-chip inline-flex self-start rounded-full px-3 py-1 ${
              router.offline
                ? "bg-rose-500/15 text-rose-200"
                : router.directMode
                  ? "bg-amber-500/15 text-amber-200"
                  : router.passwallEnabled
                    ? "bg-emerald-500/15 text-emerald-200"
                    : "bg-slate-500/15 text-slate-300"
            }`}
          >
            {router.statusLabel}
          </span>
        </div>
        <div className="mt-3 flex flex-wrap gap-1.5 sm:gap-2">
          <span
            className={`vectra-chip rounded-full border px-3 py-1 ${
              onboardingPending
                ? "border-amber-500/30 bg-amber-500/10 text-amber-200"
                : "border-emerald-500/30 bg-emerald-500/10 text-emerald-100"
            }`}
          >
            {formatRouterImportStateLabel(router.importState)}
          </span>
          <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-200">
            {router.nodeCount} нод
          </span>
          <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-200">
            {router.subscriptionCount} подписок
          </span>
          <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-200">
            {router.lastSeen}
          </span>
        </div>

        <div
          className={`mt-3 rounded-2xl border px-3 py-3 ${
            onboardingPending
              ? "border-amber-400/20 bg-amber-500/10"
              : "border-emerald-400/20 bg-emerald-500/10"
          }`}
        >
          <div className="flex items-center justify-between gap-3">
            <p className="vectra-kicker text-slate-300">{onboarding.badge}</p>
          </div>
          <p className="mt-2 text-sm font-medium text-white">
            {onboardingPending ? onboarding.cardActionLabel : "Открыть роутер"}
          </p>
        </div>

        <div className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <CompactRouterFact
            label="Нода"
            value={router.selectedNode}
            emphasis
            clamp
          />
          <CompactRouterFact label="Статус" value={router.statusLabel} />
          <CompactRouterFact
            label="Telegram"
            value={formatTelegramReachabilityLabel(router.telegramReachability)}
            tone={
              telegramStatus === "reachable"
                ? "good"
                : telegramStatus === "partial"
                  ? "warning"
                  : telegramProblem
                    ? "danger"
                    : "default"
            }
          />
          <CompactRouterFact
            label="Controller"
            value={controllerVersion}
            emphasis
            clamp
          />
        </div>
      </Link>

      <div className="border-t border-white/10 px-3 py-3 sm:px-4">
        <Link
          href={`/routers/${router.id}`}
          className="rounded-xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-center text-sm font-medium text-slate-200 transition hover:border-white/20 hover:text-white sm:border-transparent sm:bg-transparent sm:px-0 sm:py-0 sm:text-left"
        >
          {onboarding.cardActionLabel}
        </Link>
      </div>
    </article>
  );
}

function CompactRouterFact({
  label,
  value,
  tone = "default",
  emphasis = false,
  clamp = false,
}: {
  label: string;
  value: string;
  tone?: "default" | "good" | "warning" | "danger";
  emphasis?: boolean;
  clamp?: boolean;
}) {
  const toneClassName =
    tone === "good"
      ? "text-emerald-100"
      : tone === "warning"
        ? "text-amber-200"
        : tone === "danger"
          ? "text-rose-200"
          : "text-slate-100";

  return (
    <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <p
        className={`mt-2 text-sm ${toneClassName} ${emphasis ? "font-medium" : ""} ${
          clamp ? "truncate" : ""
        }`}
        title={value}
      >
        {value}
      </p>
    </div>
  );
}
