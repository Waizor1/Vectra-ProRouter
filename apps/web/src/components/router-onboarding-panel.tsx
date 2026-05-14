"use client";

import { useEffect, useState } from "react";

import { ActionStrip } from "~/components/action-strip";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import { api, type RouterOutputs } from "~/trpc/react";

type RouterOnboardingPanelProps = {
  routerId: string;
  defaultHostname: string;
  defaultDisplayName: string;
  canRunJobs: boolean;
};

type OnboardingState = RouterOutputs["onboarding"]["get"];
type OnboardingRun = NonNullable<OnboardingState["run"]>;
type OnboardingRunListItem = NonNullable<
  RouterOutputs["onboarding"]["listRuns"][number]
>;

const baselineOptions = [
  {
    value: "standard-non-hh",
    label: "Standard non-hh",
    description: "Подписка, ShuntRules, Discord UDP и route-smoke.",
  },
  {
    value: "subscription-only",
    label: "Только подписка",
    description: "Без нормализации маршрутов; для ручного профиля.",
  },
  {
    value: "hh-exempt",
    label: "hh exempt",
    description: "Не трогать стандартный non-hh baseline.",
  },
] as const;

const runStateLabels: Record<string, string> = {
  created: "Создано",
  preflight: "Проверяем вход",
  request_initial_import: "Ждём первое чтение",
  approve_initial_import: "Подтверждаем базу",
  rename_router: "Переименовываем",
  ensure_runtime: "Проверяем runtime",
  apply_subscription: "Применяем подписку",
  refresh_subscription: "Обновляем подписку",
  resolve_route_baseline: "Подбираем серверы",
  apply_route_baseline: "Применяем маршруты",
  verify_runtime: "Проверяем маршруты",
  repair_runtime: "Чиним runtime",
  final_reimport: "Фиксируем live",
  done: "Готов к работе",
};

const runStatusLabels: Record<string, string> = {
  running: "в работе",
  waiting: "ждёт",
  blocked: "заблокировано",
  failed: "ошибка",
  done: "готово",
  paused: "пауза",
};

function formatRunState(run: OnboardingRun | null | undefined) {
  if (!run) {
    return "Профиль ещё не запускался";
  }
  return runStateLabels[run.state] ?? run.state;
}

function formatRunStatus(run: OnboardingRun | null | undefined) {
  if (!run) {
    return "нет run";
  }
  return runStatusLabels[run.status] ?? run.status;
}

function runTone(run: OnboardingRun | null | undefined) {
  switch (run?.status) {
    case "done":
      return "good" as const;
    case "blocked":
    case "failed":
      return "warning" as const;
    case "paused":
      return "default" as const;
    default:
      return "default" as const;
  }
}

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "—";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function RouterOnboardingPanel({
  routerId,
  defaultHostname,
  defaultDisplayName,
  canRunJobs,
}: RouterOnboardingPanelProps) {
  const utils = api.useUtils();
  const stateQuery = api.onboarding.get.useQuery(
    { routerId },
    {
      refetchOnMount: "always",
      refetchOnWindowFocus: "always",
    },
  );
  const runsQuery = api.onboarding.listRuns.useQuery(
    { routerId },
    {
      refetchOnMount: "always",
      refetchOnWindowFocus: "always",
    },
  );
  const [initializedFromProfileId, setInitializedFromProfileId] = useState<
    string | null
  >(null);
  const [targetHostname, setTargetHostname] = useState(defaultHostname);
  const [displayName, setDisplayName] = useState(defaultDisplayName);
  const [subscriptionUrl, setSubscriptionUrl] = useState("");
  const [subscriptionRemark, setSubscriptionRemark] = useState("StarMY");
  const [baseline, setBaseline] =
    useState<(typeof baselineOptions)[number]["value"]>("standard-non-hh");
  const [verifyPolicy, setVerifyPolicy] = useState<
    "route-smoke" | "services-only"
  >("route-smoke");
  const [notes, setNotes] = useState("");

  useEffect(() => {
    const profile = stateQuery.data?.profile;
    const profileId = profile?.id ?? "new";
    if (initializedFromProfileId === profileId) {
      return;
    }
    setInitializedFromProfileId(profileId);
    setTargetHostname(profile?.targetHostname ?? defaultHostname);
    setDisplayName(profile?.displayName ?? defaultDisplayName);
    setSubscriptionRemark(profile?.subscriptionRemark ?? "StarMY");
    setBaseline(profile?.baseline ?? "standard-non-hh");
    setVerifyPolicy(profile?.verifyPolicy ?? "route-smoke");
    setNotes(profile?.notes ?? "");
  }, [
    defaultDisplayName,
    defaultHostname,
    initializedFromProfileId,
    stateQuery.data?.profile,
  ]);

  const refreshOnboardingState = async () => {
    await Promise.all([
      utils.onboarding.get.invalidate({ routerId }),
      utils.onboarding.listRuns.invalidate({ routerId }),
      utils.draft.editorSurface.invalidate({ routerId }),
      utils.fleet.byId.invalidate({ routerId }),
      utils.fleet.monitoring.invalidate(),
    ]);
  };

  const saveMutation = api.onboarding.saveProfile.useMutation({
    onSuccess: async () => {
      setSubscriptionUrl("");
      setInitializedFromProfileId(null);
      await refreshOnboardingState();
    },
  });
  const advanceMutation = api.onboarding.advance.useMutation({
    onSuccess: refreshOnboardingState,
  });
  const retryMutation = api.onboarding.retry.useMutation({
    onSuccess: refreshOnboardingState,
  });
  const pauseMutation = api.onboarding.pause.useMutation({
    onSuccess: refreshOnboardingState,
  });

  const state = stateQuery.data;
  const profile = state?.profile ?? null;
  const run = state?.run ?? null;
  const runs: OnboardingRunListItem[] = (runsQuery.data ?? []).filter(
    (item): item is OnboardingRunListItem => Boolean(item),
  );
  const busy =
    saveMutation.isPending ||
    advanceMutation.isPending ||
    retryMutation.isPending ||
    pauseMutation.isPending;
  const canAdvance =
    Boolean(profile?.enabled) &&
    canRunJobs &&
    !busy &&
    run?.status !== "done" &&
    run?.status !== "paused";
  const canRetry =
    Boolean(profile) &&
    canRunJobs &&
    !busy &&
    (run?.status === "blocked" ||
      run?.status === "failed" ||
      run?.status === "paused");
  const actionResult =
    advanceMutation.data?.reason ??
    retryMutation.data?.result?.reason ??
    saveMutation.error?.message ??
    advanceMutation.error?.message ??
    retryMutation.error?.message ??
    pauseMutation.error?.message ??
    null;

  return (
    <Panel
      eyebrow="Автонастройка роутера"
      title="Профиль и run из панели"
      tone="muted"
    >
      <div className="space-y-4">
        <div className="grid gap-3 md:grid-cols-4">
          <StatusTile
            label="Автозапуск"
            value={state?.featureEnabled ? "включён" : "выключен"}
            tone={state?.featureEnabled ? "good" : "warning"}
            hint={
              state?.featureEnabled
                ? "register/check-in/job-result сами двигают run"
                : "профиль можно подготовить, но авто-run flag пока off"
            }
            compact
          />
          <StatusTile
            label="Профиль"
            value={profile?.enabled ? "активен" : profile ? "пауза" : "нет"}
            tone={profile?.enabled ? "good" : "default"}
            hint={
              profile?.hasSubscription
                ? `подписка сохранена · ${
                    profile.subscriptionUrlHash
                      ? `${profile.subscriptionUrlHash.slice(0, 10)}…`
                      : "hash n/a"
                  }`
                : "подписка не сохранена"
            }
            compact
          />
          <StatusTile
            label="Текущий шаг"
            value={formatRunState(run)}
            tone={runTone(run)}
            hint={formatRunStatus(run)}
            compact
          />
          <StatusTile
            label="Последняя задача"
            value={run?.lastJobId ? "есть" : "нет"}
            hint={run?.lastJobId ? run.lastJobId.slice(0, 8) : "очередь чистая"}
            compact
          />
        </div>

        {run?.status === "done" ? (
          <p className="rounded-2xl border border-emerald-400/20 bg-emerald-400/10 px-3 py-2 text-sm leading-6 text-emerald-50">
            Run завершён и не перезапускается автоматически. Сохранение
            профиля меняет только сохранённые поля; для новой настройки нужен
            новый run или ручной takeover.
          </p>
        ) : null}

        <form
          className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4"
          onSubmit={(event) => {
            event.preventDefault();
            saveMutation.mutate({
              routerId,
              enabled: true,
              targetHostname: targetHostname.trim() || null,
              displayName: displayName.trim() || null,
              subscriptionUrl: subscriptionUrl.trim() || undefined,
              subscriptionRemark: subscriptionRemark.trim() || null,
              baseline,
              runtimePolicy: "auto-minimal-passwall-xray",
              verifyPolicy,
              notes: notes.trim() || null,
            });
          }}
        >
          <div className="grid gap-3 lg:grid-cols-2">
            <label className="block">
              <span className="vectra-kicker text-slate-500">Hostname</span>
              <input
                name="onboarding-hostname"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={targetHostname}
                onChange={(event) => setTargetHostname(event.target.value)}
                placeholder="client-msk"
                maxLength={63}
              />
            </label>
            <label className="block">
              <span className="vectra-kicker text-slate-500">Имя в панели</span>
              <input
                name="onboarding-display-name"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={displayName}
                onChange={(event) => setDisplayName(event.target.value)}
                placeholder="Client-msk"
                maxLength={120}
              />
            </label>
            <label className="block">
              <span className="vectra-kicker text-slate-500">URL подписки</span>
              <input
                name="onboarding-subscription-url"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={subscriptionUrl}
                onChange={(event) => setSubscriptionUrl(event.target.value)}
                placeholder={
                  profile?.hasSubscription
                    ? "сохранена, оставьте пустым чтобы не менять"
                    : "https://..."
                }
                type="url"
              />
            </label>
            <label className="block">
              <span className="vectra-kicker text-slate-500">
                Remark подписки
              </span>
              <input
                name="onboarding-subscription-remark"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={subscriptionRemark}
                onChange={(event) => setSubscriptionRemark(event.target.value)}
                placeholder="StarMY"
                maxLength={120}
              />
            </label>
          </div>

          <div className="mt-3 grid gap-3 lg:grid-cols-2">
            <label className="block">
              <span className="vectra-kicker text-slate-500">Baseline</span>
              <select
                name="onboarding-baseline"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={baseline}
                onChange={(event) =>
                  setBaseline(
                    event.target
                      .value as (typeof baselineOptions)[number]["value"],
                  )
                }
              >
                {baselineOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
              <p className="mt-2 text-xs leading-5 text-slate-500">
                {
                  baselineOptions.find((option) => option.value === baseline)
                    ?.description
                }
              </p>
            </label>
            <label className="block">
              <span className="vectra-kicker text-slate-500">Проверка</span>
              <select
                name="onboarding-verify-policy"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={verifyPolicy}
                onChange={(event) =>
                  setVerifyPolicy(
                    event.target.value as "route-smoke" | "services-only",
                  )
                }
              >
                <option value="route-smoke">
                  route-smoke: 5 PassWall url_test_node
                </option>
                <option value="services-only">services-only</option>
              </select>
              <p className="mt-2 text-xs leading-5 text-slate-500">
                Для обычных роутеров держим route-smoke: run завершится только
                после typed `verify_passwall_routes`.
              </p>
            </label>
          </div>

          <label className="mt-3 block">
            <span className="vectra-kicker text-slate-500">Заметка</span>
            <textarea
              name="onboarding-notes"
              className="vectra-field mt-2 min-h-20 px-3 py-2 text-sm text-white"
              value={notes}
              onChange={(event) => setNotes(event.target.value)}
              placeholder="Например: новый клиент, Москва, StarMY"
              maxLength={1000}
            />
          </label>

          <ActionStrip justify="start" dense>
            <button
              type="submit"
              disabled={saveMutation.isPending}
              className="vectra-button-primary px-3 py-2 text-sm font-medium transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)] disabled:cursor-not-allowed disabled:opacity-50"
            >
              {saveMutation.isPending
                ? "Сохраняю профиль..."
                : "Сохранить профиль"}
            </button>
            <button
              type="button"
              disabled={!canAdvance}
              onClick={() => advanceMutation.mutate({ routerId })}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            >
              {advanceMutation.isPending ? "Двигаю run..." : "Продвинуть run"}
            </button>
            <button
              type="button"
              disabled={!canRetry}
              onClick={() => retryMutation.mutate({ routerId })}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            >
              {retryMutation.isPending ? "Перезапускаю..." : "Повторить"}
            </button>
            <button
              type="button"
              disabled={!profile || pauseMutation.isPending}
              onClick={() => pauseMutation.mutate({ routerId })}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            >
              {pauseMutation.isPending ? "Ставлю паузу..." : "Ручной takeover"}
            </button>
          </ActionStrip>
        </form>

        {actionResult ? (
          <p className="rounded-2xl border border-white/10 bg-black/10 px-3 py-2 text-sm leading-6 text-slate-300">
            {actionResult}
          </p>
        ) : null}

        <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3">
          <summary className="min-h-11 cursor-pointer list-none">
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="vectra-kicker text-slate-500">Timeline</p>
                <p className="mt-1 text-sm font-medium text-white">
                  Последние onboarding runs
                </p>
              </div>
              <span className="text-xs text-slate-400">{runs.length || 0}</span>
            </div>
          </summary>
          <div className="mt-3 space-y-2">
            {runs.length > 0 ? (
              runs.slice(0, 6).map((item) => (
                <div
                  key={item.id}
                  className="rounded-xl border border-white/10 bg-black/10 px-3 py-2 text-sm"
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="font-medium text-white">
                      {runStateLabels[item.state] ?? item.state}
                    </p>
                    <span className="text-xs text-slate-400">
                      {runStatusLabels[item.status] ?? item.status} ·{" "}
                      {formatDateTime(item.updatedAt)}
                    </span>
                  </div>
                  {item.lastError ? (
                    <p className="mt-1 text-xs leading-5 text-amber-100">
                      {item.lastError}
                    </p>
                  ) : null}
                </div>
              ))
            ) : (
              <p className="text-sm leading-6 text-slate-400">
                Run ещё не создавался. Сохраните профиль и нажмите `Продвинуть
                run` или включите автозапуск flag.
              </p>
            )}
          </div>
        </details>
      </div>
    </Panel>
  );
}
