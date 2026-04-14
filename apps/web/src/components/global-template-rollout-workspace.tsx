"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import { TabBar } from "~/components/tab-bar";
import { api, type RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];
type GlobalTemplateRolloutResponse =
  RouterOutputs["update"]["queueGlobalTemplateRollout"];
type UpdatesWorkspaceTab = "baseline" | "rollout" | "history";
type BaselineEditorTab = "install" | "rollout";

function normalizeUpdatesWorkspaceTab(
  value: string | null | undefined,
): UpdatesWorkspaceTab {
  switch (value) {
    case "rollout":
    case "history":
      return value;
    default:
      return "baseline";
  }
}

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  return date.toLocaleString("ru-RU", { hour12: false });
}

function formatImportState(value: string) {
  switch (value) {
    case "approved":
      return "утверждено";
    case "out_of_sync":
      return "нужно проверить";
    case "import_review":
      return "на проверке";
    case "awaiting_import":
      return "ждёт импорт";
    default:
      return value;
  }
}

function formatSupportState(value: string) {
  switch (value) {
    case "certified":
      return "сертифицирован";
    case "pilot":
      return "пилот";
    case "blocked":
      return "заблокирован";
    default:
      return value;
  }
}

function getTemplateMarker(template: GlobalTemplateWorkspaceData["template"]) {
  return template.updatedAt instanceof Date
    ? template.updatedAt.getTime()
    : new Date(template.updatedAt).getTime();
}

function validateInstallBaselineLocally(text: string) {
  const issues: string[] = [];

  if (text.trim().length === 0) {
    issues.push("Install baseline не может быть пустым.");
  }
  if (text.includes("\r")) {
    issues.push("Install baseline должен храниться в LF-формате без CRLF.");
  }
  if (!text.includes("config nodes 'myshunt'")) {
    issues.push("В install baseline должен оставаться shunt-узел myshunt.");
  }
  if (/^config subscribe_list\b/m.test(text)) {
    issues.push("Install baseline не должен содержать subscription-секции.");
  }
  if (/^config nodes '(?!myshunt')/m.test(text)) {
    issues.push(
      "Install baseline не должен публиковать реальные proxy-node секции.",
    );
  }
  if (!text.includes("option default_fakedns '0'")) {
    issues.push("Для Default в install baseline должен оставаться FakeDNS = 0.");
  }
  if (!text.includes("option direct_fakedns '0'")) {
    issues.push("Для Direct в install baseline должен оставаться FakeDNS = 0.");
  }

  return issues;
}

function validateRolloutConfigLocally(config: PasswallDesiredConfig) {
  const issues: string[] = [];

  if (config.subscriptions.items.length > 0) {
    issues.push(
      "Fleet-template не должен хранить subscription items: ссылки остаются локальными.",
    );
  }
  if (config.nodes.some((node) => node.protocol !== "shunt")) {
    issues.push(
      "Fleet-template не должен хранить реальные proxy-node секции: разрешены только shunt nodes.",
    );
  }

  return issues;
}

function describeHistorySummary(metadata: Record<string, unknown>) {
  const summary =
    metadata.summary &&
    typeof metadata.summary === "object" &&
    !Array.isArray(metadata.summary)
      ? (metadata.summary as Record<string, unknown>)
      : null;

  if (!summary) {
    return null;
  }

  const requested =
    typeof summary.requestedRouterCount === "number"
      ? summary.requestedRouterCount
      : null;
  const prepared =
    typeof summary.preparedCount === "number" ? summary.preparedCount : 0;
  const queued = typeof summary.queuedCount === "number" ? summary.queuedCount : 0;
  const blocked =
    typeof summary.blockedCount === "number" ? summary.blockedCount : 0;
  const failed = typeof summary.failedCount === "number" ? summary.failedCount : 0;

  if (requested === null) {
    return null;
  }

  return `Запрошено ${requested} · подготовлено ${prepared} · в очереди ${queued} · заблокировано ${blocked} · ошибок ${failed}`;
}

function describeLatestResult(result: GlobalTemplateRolloutResponse | null) {
  if (!result?.ok) {
    return null;
  }

  return `Запрошено ${result.summary.requestedRouterCount} · подготовлено ${result.summary.preparedCount} · в очереди ${result.summary.queuedCount} · заблокировано ${result.summary.blockedCount} · ошибок ${result.summary.failedCount}`;
}

const baselineEditorTabs = [
  {
    id: "install" as const,
    label: "Новые роутеры",
    title: "Install baseline UCI",
    description: "Устанавливается новым роутерам из раздела «Установка».",
  },
  {
    id: "rollout" as const,
    label: "Текущий парк",
    title: "Fleet-template JSON",
    description: "Идёт в массовую рассылку по уже подключённым роутерам.",
  },
] as const;

export function GlobalTemplateRolloutWorkspace({
  initialWorkspace,
}: {
  initialWorkspace: GlobalTemplateWorkspaceData;
}) {
  const utils = api.useUtils();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const workspaceQuery = api.update.globalTemplateWorkspace.useQuery(undefined, {
    initialData: initialWorkspace,
    refetchOnWindowFocus: false,
  });
  const workspace = workspaceQuery.data ?? initialWorkspace;
  const activeTab = normalizeUpdatesWorkspaceTab(searchParams.get("workspace"));
  const [loadedTemplateMarker, setLoadedTemplateMarker] = useState(() =>
    getTemplateMarker(initialWorkspace.template),
  );
  const [installBaselineUci, setInstallBaselineUci] = useState(
    initialWorkspace.template.installBaselineUci,
  );
  const [rolloutJson, setRolloutJson] = useState(() =>
    JSON.stringify(initialWorkspace.template.rolloutConfig, null, 2),
  );
  const [saveNote, setSaveNote] = useState(initialWorkspace.template.note ?? "");
  const [rolloutNote, setRolloutNote] = useState("");
  const [selectedRouterIds, setSelectedRouterIds] = useState<string[]>([]);
  const [localIssues, setLocalIssues] = useState<string[]>([]);
  const [baselineEditor, setBaselineEditor] =
    useState<BaselineEditorTab>("install");
  const [lastRolloutResult, setLastRolloutResult] =
    useState<GlobalTemplateRolloutResponse | null>(null);

  useEffect(() => {
    const nextMarker = getTemplateMarker(workspace.template);
    if (nextMarker === loadedTemplateMarker) {
      return;
    }

    setInstallBaselineUci(workspace.template.installBaselineUci);
    setRolloutJson(JSON.stringify(workspace.template.rolloutConfig, null, 2));
    setSaveNote(workspace.template.note ?? "");
    setLoadedTemplateMarker(nextMarker);
  }, [loadedTemplateMarker, workspace.template]);

  useEffect(() => {
    const eligibleIds = new Set(
      workspace.rolloutTargets
        .filter((target) => target.rolloutEligible)
        .map((target) => target.id),
    );

    setSelectedRouterIds((previous) =>
      previous.filter((routerId) => eligibleIds.has(routerId)),
    );
  }, [workspace.rolloutTargets]);

  const saveMutation = api.update.saveGlobalTemplate.useMutation({
    onSuccess: async (result) => {
      setLocalIssues(result.issues);

      if (!result.ok) {
        return;
      }

      await utils.update.globalTemplateWorkspace.invalidate();
    },
  });

  const rolloutMutation = api.update.queueGlobalTemplateRollout.useMutation({
    onSuccess: async (result) => {
      setLastRolloutResult(result);
      setLocalIssues(result.issues);

      if (!result.ok) {
        return;
      }

      await Promise.all([
        utils.update.globalTemplateWorkspace.invalidate(),
        utils.draft.list.invalidate(),
        utils.fleet.list.invalidate(),
        utils.fleet.overview.invalidate(),
        utils.fleet.pendingImportReviews.invalidate(),
      ]);

      setWorkspaceTab(router, pathname, searchParams, "history");
    },
  });

  const parsedRolloutJson = parseRolloutJson(rolloutJson);
  const localInstallIssues = validateInstallBaselineLocally(installBaselineUci);
  const localRolloutIssues = parsedRolloutJson.ok
    ? validateRolloutConfigLocally(parsedRolloutJson.config)
    : [parsedRolloutJson.message];
  const combinedIssues = dedupeIssues([
    ...localIssues,
    ...localInstallIssues,
    ...localRolloutIssues,
  ]);
  const eligibleTargets = workspace.rolloutTargets.filter(
    (target) => target.rolloutEligible,
  );
  const selectedEligibleCount = selectedRouterIds.filter((routerId) =>
    eligibleTargets.some((target) => target.id === routerId),
  ).length;
  const lastResultSummary = describeLatestResult(lastRolloutResult);
  const activeBaselineEditor =
    baselineEditorTabs.find((item) => item.id === baselineEditor) ??
    baselineEditorTabs[0];

  const handleSave = async () => {
    if (!parsedRolloutJson.ok) {
      setLocalIssues([parsedRolloutJson.message]);
      return;
    }

    const issues = [
      ...validateInstallBaselineLocally(installBaselineUci),
      ...validateRolloutConfigLocally(parsedRolloutJson.config),
    ];
    if (issues.length > 0) {
      setLocalIssues(issues);
      return;
    }

    setLocalIssues([]);
    await saveMutation.mutateAsync({
      installBaselineUci,
      rolloutConfig: parsedRolloutJson.config,
      note: saveNote.trim() || undefined,
    });
  };

  const handleRollout = async (mode: "draft_only" | "queue_apply") => {
    if (selectedRouterIds.length === 0) {
      setLocalIssues(["Сначала выберите хотя бы один подходящий роутер."]);
      return;
    }

    if (
      mode === "queue_apply" &&
      !window.confirm(
        `Поставить глобальный baseline в очередь применения для ${selectedRouterIds.length} роутеров?\n\n` +
          "Это создаст реальные apply-задачи на выбранных устройствах. Перед боевым окном можно использовать режим 'Подготовить только черновики'.",
      )
    ) {
      return;
    }

    setLocalIssues([]);
    await rolloutMutation.mutateAsync({
      routerIds: selectedRouterIds,
      mode,
      note: rolloutNote.trim() || undefined,
    });
  };

  if (workspaceQuery.isError) {
    return (
      <Panel eyebrow="Fleet template" title="Глобальный baseline">
        <div className="rounded-md border border-rose-400/30 bg-rose-950/20 px-3 py-3 text-sm leading-7 text-rose-100">
          Не удалось загрузить workspace глобального baseline.
        </div>
      </Panel>
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-4">
        <StatusTile
          label="Install baseline"
          value={workspace.installBaselineIssues.length > 0 ? "требует правки" : "готов"}
          tone={workspace.installBaselineIssues.length > 0 ? "warning" : "good"}
          hint="Для новых роутеров."
        />
        <StatusTile
          label="Fleet-template"
          value={
            workspace.rolloutTemplateIssues.length > 0 ? "требует правки" : "готов"
          }
          tone={workspace.rolloutTemplateIssues.length > 0 ? "warning" : "good"}
          hint="Для массовой рассылки."
        />
        <StatusTile
          label="Подходят для рассылки"
          value={String(workspace.summary.eligibleRouterCount)}
          tone={workspace.summary.eligibleRouterCount > 0 ? "good" : "warning"}
          hint="Только approved роутеры на разрешённой pilot/certified платформе."
        />
        <StatusTile
          label="Managed shunt rules"
          value={String(workspace.summary.shuntRuleCount)}
          hint={`Managed nodes: ${workspace.summary.managedNodeCount}`}
        />
      </div>

      <Panel
        eyebrow="Глобальный baseline"
        title="Шаблоны парка"
      >
        <div className="space-y-4">
          <div className="grid gap-3 lg:grid-cols-3">
            <TaskChoiceCard
              eyebrow="Новый роутер"
              title="Править install baseline"
              description="Этот шаблон получает роутер во время первичной установки."
              actionLabel="Открыть эталон для новых роутеров"
              onClick={() => {
                setBaselineEditor("install");
                setWorkspaceTab(router, pathname, searchParams, "baseline");
              }}
            />
            <TaskChoiceCard
              eyebrow="Текущий парк"
              title="Править fleet-template"
              description="Этот шаблон готовит settings-only merge для уже подключённых роутеров."
              actionLabel="Открыть эталон для текущего парка"
              onClick={() => {
                setBaselineEditor("rollout");
                setWorkspaceTab(router, pathname, searchParams, "baseline");
              }}
            />
            <TaskChoiceCard
              eyebrow="Массовое действие"
              title="Разослать по выбранным роутерам"
              description="Сначала можно подготовить черновики, затем поставить apply в очередь."
              actionLabel="Перейти к рассылке"
              onClick={() =>
                setWorkspaceTab(router, pathname, searchParams, "rollout")
              }
            />
          </div>

          <TabBar
            ariaLabel="Вкладки глобального baseline"
            items={[
              {
                id: "baseline",
                label: "Эталон",
                active: activeTab === "baseline",
                onSelect: () =>
                  setWorkspaceTab(router, pathname, searchParams, "baseline"),
              },
              {
                id: "rollout",
                label: "Рассылка",
                active: activeTab === "rollout",
                badge:
                  workspace.summary.eligibleRouterCount > 0
                    ? String(workspace.summary.eligibleRouterCount)
                    : undefined,
                onSelect: () =>
                  setWorkspaceTab(router, pathname, searchParams, "rollout"),
              },
              {
                id: "history",
                label: "История",
                active: activeTab === "history",
                badge:
                  workspace.history.length > 0
                    ? String(workspace.history.length)
                    : undefined,
                onSelect: () =>
                  setWorkspaceTab(router, pathname, searchParams, "history"),
              },
            ]}
            variant="secondary"
          />

          {combinedIssues.length > 0 ? (
            <div className="rounded-md border border-amber-400/30 bg-amber-950/20 px-3 py-3 text-sm leading-7 text-amber-50">
              {combinedIssues.map((issue) => (
                <p key={issue}>{issue}</p>
              ))}
            </div>
          ) : null}

          {activeTab === "baseline" ? (
            <div className="space-y-4">
              <ActionStrip justify="start">
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={saveMutation.isPending}
                  className="rounded-md border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:border-white/20 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {saveMutation.isPending
                    ? "Сохраняю эталон..."
                    : "Сохранить эталон"}
                </button>
                <div className="min-w-[260px] flex-1">
                  <label
                    htmlFor="global-template-save-note"
                    className="sr-only"
                  >
                    Комментарий к глобальному эталону
                  </label>
                  <input
                    id="global-template-save-note"
                    name="global-template-save-note"
                    type="text"
                    value={saveNote}
                    onChange={(event) => setSaveNote(event.target.value)}
                    placeholder="Комментарий к baseline или rollout template"
                    className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white outline-none transition focus:border-[var(--vectra-line-strong)]"
                  />
                </div>
              </ActionStrip>
              <div className="space-y-3">
                <TabBar
                  ariaLabel="Выбор редактора эталона"
                  items={baselineEditorTabs.map((item) => ({
                    id: item.id,
                    label: item.label,
                    active: baselineEditor === item.id,
                    onSelect: () => setBaselineEditor(item.id),
                  }))}
                  variant="secondary"
                />

                <TemplateEditorPane
                  title={activeBaselineEditor.title}
                  description={activeBaselineEditor.description}
                  value={
                    activeBaselineEditor.id === "install"
                      ? installBaselineUci
                      : rolloutJson
                  }
                  onChange={
                    activeBaselineEditor.id === "install"
                      ? setInstallBaselineUci
                      : setRolloutJson
                  }
                  rows={22}
                />
              </div>
            </div>
          ) : null}

          {activeTab === "rollout" ? (
            <div className="space-y-4">
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm leading-7 text-slate-300">
                <strong>Подготовить черновики без apply</strong> только создаёт
                локальные draft-ревизии. <strong>Разослать и поставить apply</strong>{" "}
                создаёт уже реальные apply-задачи.
              </div>

              <ActionStrip justify="start">
                <button
                  type="button"
                  onClick={() =>
                    setSelectedRouterIds(eligibleTargets.map((target) => target.id))
                  }
                  className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-slate-200 transition hover:border-white/20 hover:text-white"
                >
                  Выбрать все подходящие
                </button>
                <button
                  type="button"
                  onClick={() => setSelectedRouterIds([])}
                  className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-slate-200 transition hover:border-white/20 hover:text-white"
                >
                  Очистить выбор
                </button>
                <button
                  type="button"
                  onClick={() => handleRollout("draft_only")}
                  disabled={rolloutMutation.isPending}
                  className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-slate-100 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                >
                  Подготовить черновики без apply
                </button>
                <button
                  type="button"
                  onClick={() => handleRollout("queue_apply")}
                  disabled={rolloutMutation.isPending}
                  className="rounded-md border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:border-white/20 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {rolloutMutation.isPending
                    ? "Готовлю массовую рассылку..."
                    : "Разослать и поставить apply"}
                </button>
                <span className="text-sm text-slate-400">
                  Выбрано {selectedEligibleCount} из {eligibleTargets.length}
                </span>
              </ActionStrip>

              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
                <label
                  htmlFor="global-rollout-note"
                  className="block text-sm text-slate-300"
                >
                  <span className="vectra-kicker text-slate-500">
                    Комментарий к рассылке
                  </span>
                  <input
                    id="global-rollout-note"
                    name="global-rollout-note"
                    type="text"
                    value={rolloutNote}
                    onChange={(event) => setRolloutNote(event.target.value)}
                    placeholder="Например: rollout нового DNS baseline"
                    className="mt-2 w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white outline-none transition focus:border-[var(--vectra-line-strong)]"
                  />
                </label>
              </div>

              <DataTable
                columns={[
                  { key: "pick", label: "Выбор", className: "w-16" },
                  { key: "router", label: "Роутер" },
                  { key: "state", label: "Статус" },
                  { key: "selected", label: "Текущий node" },
                  { key: "support", label: "Поддержка" },
                  { key: "reason", label: "Ограничение" },
                ]}
              >
                {workspace.rolloutTargets.length > 0 ? (
                  workspace.rolloutTargets.map((target) => {
                    const selected = selectedRouterIds.includes(target.id);

                    return (
                      <tr
                        key={target.id}
                        className={`border-b border-white/6 ${
                          selected ? "bg-white/[0.04]" : ""
                        }`}
                      >
                        <td className="px-3 py-3 align-top">
                          <input
                            type="checkbox"
                            aria-label={`Выбрать ${target.displayName} для массовой рассылки`}
                            checked={selected}
                            disabled={!target.rolloutEligible}
                            onChange={(event) =>
                              setSelectedRouterIds((previous) => {
                                if (event.target.checked) {
                                  return [...new Set([...previous, target.id])];
                                }
                                return previous.filter(
                                  (routerId) => routerId !== target.id,
                                );
                              })
                            }
                          />
                        </td>
                        <td className="px-3 py-3 align-top text-sm text-slate-100">
                          <Link
                            href={`/routers/${target.id}`}
                            className="font-medium text-white underline decoration-white/10 underline-offset-4"
                          >
                            {target.displayName}
                          </Link>
                          <p className="mt-1 text-xs leading-6 text-slate-400">
                            {target.hostname ?? target.deviceIdentifier} ·{" "}
                            {target.reachable ? "на связи" : "нет свежей связи"} ·{" "}
                            {formatDateTime(target.lastSeenAt)}
                          </p>
                        </td>
                        <td className="px-3 py-3 align-top text-sm text-slate-200">
                          {target.rolloutEligible ? "готов к рассылке" : "заблокирован"}
                          <p className="mt-1 text-xs leading-6 text-slate-400">
                            import: {formatImportState(target.importState)}
                          </p>
                        </td>
                        <td className="px-3 py-3 align-top text-sm text-slate-300">
                          {target.selectedNodeLabel ?? "не выбрана"}
                        </td>
                        <td className="px-3 py-3 align-top text-sm text-slate-300">
                          {target.supportTitle}
                          <p className="mt-1 text-xs leading-6 text-slate-400">
                            {formatSupportState(target.supportState)}
                          </p>
                        </td>
                        <td className="px-3 py-3 align-top text-sm text-slate-400">
                          {target.blockedReason ?? "можно готовить rollout"}
                        </td>
                      </tr>
                    );
                  })
                ) : (
                  <DataTableEmpty colSpan={6}>
                    Роутеров пока нет. Сначала подключите устройства через `Установка`.
                  </DataTableEmpty>
                )}
              </DataTable>

              {lastRolloutResult ? (
                <Panel
                  eyebrow="Последний запуск"
                  title="Результат последней массовой операции"
                >
                  <div className="space-y-4">
                    {lastResultSummary ? (
                      <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm text-slate-200">
                        {lastResultSummary}
                      </div>
                    ) : null}
                    <DataTable
                      columns={[
                        { key: "router", label: "Роутер" },
                        { key: "status", label: "Результат" },
                        { key: "revision", label: "Draft" },
                        { key: "job", label: "Apply job" },
                        { key: "reason", label: "Комментарий" },
                      ]}
                    >
                      {lastRolloutResult.results.length > 0 ? (
                        lastRolloutResult.results.map((result) => (
                          <tr key={result.routerId} className="border-b border-white/6">
                            <td className="px-3 py-3 align-top text-sm text-slate-100">
                              <Link
                                href={`/routers/${result.routerId}`}
                                className="font-medium text-white underline decoration-white/10 underline-offset-4"
                              >
                                {result.displayName}
                              </Link>
                            </td>
                            <td className="px-3 py-3 align-top text-sm text-slate-200">
                              {result.status}
                            </td>
                            <td className="px-3 py-3 align-top font-[family:var(--font-plex-mono)] text-xs text-slate-300">
                              {result.revisionId ?? "—"}
                            </td>
                            <td className="px-3 py-3 align-top font-[family:var(--font-plex-mono)] text-xs text-slate-300">
                              {result.jobId ?? "—"}
                            </td>
                            <td className="px-3 py-3 align-top text-sm text-slate-400">
                              {result.reason ?? "без ошибок"}
                            </td>
                          </tr>
                        ))
                      ) : (
                        <DataTableEmpty colSpan={5}>
                          Результатов пока нет.
                        </DataTableEmpty>
                      )}
                    </DataTable>
                  </div>
                </Panel>
              ) : null}
            </div>
          ) : null}

          {activeTab === "history" ? (
            <div className="space-y-4">
              <div className="space-y-3">
                {workspace.history.length > 0 ? (
                  workspace.history.map((entry) => (
                    <article
                      key={entry.id}
                      className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3"
                    >
                      <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
                        <div>
                          <p className="text-sm font-semibold text-white">
                            {entry.message}
                          </p>
                          <p className="mt-1 text-xs leading-6 text-slate-500">
                            {entry.type} · {formatDateTime(entry.createdAt)}
                          </p>
                        </div>
                        <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1 text-[11px] font-semibold tracking-[0.12em] text-slate-300 uppercase">
                          {entry.severity}
                        </span>
                      </div>
                      {describeHistorySummary(entry.metadata) ? (
                        <p className="mt-3 text-sm text-slate-300">
                          {describeHistorySummary(entry.metadata)}
                        </p>
                      ) : null}
                      {"note" in entry.metadata &&
                      typeof entry.metadata.note === "string" &&
                      entry.metadata.note.length > 0 ? (
                        <p className="mt-2 text-sm leading-7 text-slate-400">
                          Комментарий: {entry.metadata.note}
                        </p>
                      ) : null}
                    </article>
                  ))
                ) : (
                  <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] px-3 py-6 text-sm leading-7 text-slate-400">
                    История массовых операций пока пуста.
                  </div>
                )}
              </div>
            </div>
          ) : null}
        </div>
      </Panel>
    </div>
  );
}

function TemplateEditorPane({
  title,
  description,
  value,
  onChange,
  rows,
}: {
  title: string;
  description: string;
  value: string;
  onChange: (value: string) => void;
  rows: number;
}) {
  const editorId = `template-editor-${title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")}`;

  return (
    <div className="space-y-3">
      <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
        <p className="vectra-kicker text-slate-500">{title}</p>
        <p className="mt-2 text-sm leading-6 text-slate-300 sm:leading-7">
          {description}
        </p>
      </div>
      <label htmlFor={editorId} className="sr-only">
        {title}
      </label>
      <textarea
        id={editorId}
        name={editorId}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        rows={rows}
        className="min-h-[18rem] w-full rounded-md border border-white/10 bg-black/30 px-3 py-3 font-[family:var(--font-plex-mono)] text-[11.5px] leading-5 text-slate-100 outline-none transition focus:border-[var(--vectra-line-strong)] sm:min-h-[24rem] sm:text-[12px] sm:leading-6 xl:min-h-[28rem]"
      />
    </div>
  );
}

function TaskChoiceCard({
  eyebrow,
  title,
  description,
  actionLabel,
  onClick,
}: {
  eyebrow: string;
  title: string;
  description: string;
  actionLabel: string;
  onClick: () => void;
}) {
  return (
    <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <p className="vectra-kicker text-slate-500">{eyebrow}</p>
      <p className="mt-2 text-sm font-medium text-white">{title}</p>
      <p className="mt-2 text-sm leading-6 text-slate-300">{description}</p>
      <button
        type="button"
        onClick={onClick}
        className="mt-3 rounded-md border border-white/10 bg-black/10 px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
      >
        {actionLabel}
      </button>
    </div>
  );
}

function parseRolloutJson(value: string):
  | { ok: true; config: PasswallDesiredConfig }
  | { ok: false; message: string } {
  try {
    const parsed = JSON.parse(value) as unknown;
    const config = passwallDesiredConfigSchema.parse(parsed);
    return { ok: true, config };
  } catch (error) {
    return {
      ok: false,
      message:
        error instanceof Error
          ? `Fleet-template JSON не разобран: ${error.message}`
          : "Fleet-template JSON не разобран.",
    };
  }
}

function dedupeIssues(issues: string[]) {
  return [...new Set(issues.filter((issue) => issue.trim().length > 0))];
}

function setWorkspaceTab(
  router: ReturnType<typeof useRouter>,
  pathname: string,
  searchParams: ReturnType<typeof useSearchParams>,
  tab: UpdatesWorkspaceTab,
) {
  const next = new URLSearchParams(searchParams.toString());
  next.set("workspace", tab);
  router.replace(`${pathname}?${next.toString()}`, { scroll: false });
}
