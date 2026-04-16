"use client";

import { skipToken } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { ActionStrip } from "~/components/action-strip";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import { api, type RouterInputs, type RouterOutputs } from "~/trpc/react";

type DraftConfigInput = RouterInputs["draft"]["save"]["config"];
type DraftWorkspaceData = RouterOutputs["draft"]["workspace"];
type DraftPreviewData = RouterOutputs["draft"]["preview"];

function formatRouterStatus(value?: string | null) {
  switch (value) {
    case "active":
      return "активен";
    case "pending":
      return "ожидает";
    case "offline":
      return "нет связи";
    case "direct":
      return "прямой режим";
    case "disabled":
      return "отключен";
    default:
      return value ?? "неизвестно";
  }
}

function formatImportState(value?: string | null) {
  switch (value) {
    case "approved":
      return "одобрен";
    case "import_review":
      return "на проверке";
    case "out_of_sync":
      return "требует повторной проверки";
    case "awaiting_import":
      return "ожидает импорт";
    default:
      return value ?? "неизвестно";
  }
}

export function DraftWorkspace({
  initialRouterId,
  initialWorkspace,
  initialPreview,
}: {
  initialRouterId?: string;
  initialWorkspace: DraftWorkspaceData;
  initialPreview: DraftPreviewData | null;
}) {
  const router = useRouter();
  const utils = api.useUtils();

  const [selectedRouterId, setSelectedRouterId] = useState(
    initialRouterId ?? initialWorkspace.selectedRouter?.id ?? "",
  );
  const [editorText, setEditorText] = useState(() =>
    initialWorkspace.workspaceRevision
      ? JSON.stringify(initialWorkspace.workspaceRevision.config, null, 2)
      : "",
  );
  const [note, setNote] = useState("");
  const [loadedRevisionId, setLoadedRevisionId] = useState<string | null>(
    initialWorkspace.workspaceRevision?.id ?? null,
  );
  const [savedRevisionId, setSavedRevisionId] = useState<string | null>(null);

  const workspace = api.draft.workspace.useQuery(
    {
      routerId: selectedRouterId || undefined,
    },
    {
      initialData: initialWorkspace,
    },
  );

  useEffect(() => {
    if (!selectedRouterId && workspace.data?.selectedRouter?.id) {
      setSelectedRouterId(workspace.data.selectedRouter.id);
    }
  }, [selectedRouterId, workspace.data?.selectedRouter?.id]);

  useEffect(() => {
    if (!workspace.data?.workspaceRevision) {
      return;
    }

    if (loadedRevisionId !== workspace.data.workspaceRevision.id) {
      setEditorText(
        JSON.stringify(workspace.data.workspaceRevision.config, null, 2)
      );
      setLoadedRevisionId(workspace.data.workspaceRevision.id);
      setSavedRevisionId(null);
    }
  }, [loadedRevisionId, workspace.data?.workspaceRevision]);

  let parsedConfig: DraftConfigInput | null = null;
  let parseError: string | null = null;

  try {
    if (editorText.trim().length > 0) {
      parsedConfig = JSON.parse(editorText) as DraftConfigInput;
    }
  } catch (error) {
    parseError =
      error instanceof Error ? error.message : "Не удалось разобрать JSON.";
  }

  const previewTarget =
    parsedConfig ??
    workspace.data?.workspaceRevision?.config ??
    workspace.data?.activeRevision?.config ??
    null;

  const initialPreviewTarget =
    initialWorkspace.workspaceRevision?.config ??
    initialWorkspace.activeRevision?.config ??
    null;

  const preview = api.draft.preview.useQuery(
    previewTarget
      ? {
          previous: workspace.data?.activeRevision?.config ?? null,
          next: previewTarget,
        }
      : skipToken,
    {
      initialData:
        selectedRouterId === initialWorkspace.selectedRouter?.id &&
        previewTarget === initialPreviewTarget
          ? initialPreview ?? undefined
          : undefined,
    },
  );

  const saveMutation = api.draft.save.useMutation({
    onSuccess: async (revision) => {
      if (!revision) {
        return;
      }
      setSavedRevisionId(revision.id);
      await Promise.all([
        utils.draft.list.invalidate(),
        utils.draft.workspace.invalidate({
          routerId: selectedRouterId || undefined,
        }),
        utils.fleet.byId.invalidate({
          routerId: selectedRouterId,
        }),
      ]);
      router.refresh();
    },
  });

  const queueMutation = api.draft.queueApply.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.list.invalidate(),
        utils.draft.workspace.invalidate({
          routerId: selectedRouterId || undefined,
        }),
        utils.fleet.byId.invalidate({
          routerId: selectedRouterId,
        }),
      ]);
      router.refresh();
    },
  });

  const selectedRouter = workspace.data?.selectedRouter ?? null;
  const latestDraftId = savedRevisionId ?? workspace.data?.latestDraft?.id ?? null;
  const canSave =
    Boolean(selectedRouterId) &&
    Boolean(parsedConfig) &&
    !parseError &&
    !saveMutation.isPending;
  const canQueue =
    Boolean(selectedRouterId) &&
    Boolean(latestDraftId) &&
    selectedRouter?.importState === "approved" &&
    !queueMutation.isPending;

  if (workspace.isLoading) {
    return (
      <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4 text-sm text-slate-300">
        Загружаю резервный JSON-режим...
      </div>
    );
  }

  if (!workspace.data || workspace.data.routers.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] px-4 py-4 text-sm leading-7 text-slate-300">
        Роутеров пока нет. Сначала подключите устройство через `Установка`, а
        потом возвращайтесь сюда для экспертного JSON-редактирования.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="vectra-main-grid gap-4">
        <Panel eyebrow="JSON workspace" title="Выбор роутера и контекст" tone="hero">
          <p className="text-sm leading-6 text-slate-400">
            Сначала выберите роутер, затем проверьте preview. Применение по-прежнему идёт только из сохранённого черновика.
          </p>
          <label
            htmlFor="draft-router-select"
            className="mt-3 block text-xs font-medium uppercase tracking-[0.08em] text-slate-400"
          >
            Выбор роутера
          </label>
          <select
            id="draft-router-select"
            name="draft-router-select"
            className="vectra-field mt-3 px-3 py-2.5 text-sm text-white"
            value={selectedRouterId}
            onChange={(event) => setSelectedRouterId(event.target.value)}
          >
            {workspace.data.routers.map((item) => (
              <option key={item.id} value={item.id}>
                {(item.displayName ?? item.hostname ?? item.deviceIdentifier).trim()} ·{" "}
                {formatImportState(item.importState)}
              </option>
            ))}
          </select>

          <div className="mt-4 vectra-stat-grid">
            <StatusTile
              label="Статус"
              value={formatRouterStatus(selectedRouter?.status)}
              compact
            />
            <StatusTile
              label="Импорт"
              value={formatImportState(selectedRouter?.importState)}
              compact
              emphasis={selectedRouter?.importState === "approved"}
            />
            <StatusTile
              label="Импорт-ревизия"
              value={workspace.data.importedRevision ? `#${workspace.data.importedRevision.revisionNumber}` : "нет"}
              compact
            />
            <StatusTile
              label="Черновик"
              value={workspace.data.latestDraft ? `#${workspace.data.latestDraft.revisionNumber}` : "нет"}
              compact
            />
          </div>

          <div className="mt-4 grid gap-3 md:grid-cols-3">
            {[
              {
                title: "1. Проверяете JSON",
                body: "Редактор ниже не меняет поведение сервера, но позволяет точечно описать desired config.",
              },
              {
                title: "2. Сохраняете ревизию",
                body: "Сначала создаётся или обновляется черновик внутри панели.",
              },
              {
                title: "3. Потом применяете",
                body: "Отправка на роутер доступна только для approved-роутера и только из сохранённой ревизии.",
              },
            ].map((item) => (
              <div
                key={item.title}
                className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3"
              >
                <p className="text-sm font-semibold text-white">{item.title}</p>
                <p className="mt-2 text-sm leading-6 text-slate-300">{item.body}</p>
              </div>
            ))}
          </div>
        </Panel>

        <Panel eyebrow="Preview" title="Что изменится" tone="muted">
          {parseError ? (
            <div className="mt-3 rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-3 text-sm leading-7 text-rose-200">
              Некорректный JSON: {parseError}
            </div>
          ) : (
            <div className="mt-3 grid gap-3 md:grid-cols-2">
              <div className="rounded-2xl border border-white/10 bg-black/10 px-3 py-3">
                <p className="vectra-kicker text-slate-500">Изменённые секции</p>
                <p className="mt-3 text-sm leading-7 text-slate-200">
                  {preview.data?.changedSections.length
                    ? preview.data.changedSections.join(", ")
                    : "изменений нет"}
                </p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-black/10 px-3 py-3">
                <p className="vectra-kicker text-slate-500">Выполнение</p>
                <p className="mt-3 text-sm leading-7 text-slate-200">
                  Перезапуск{" "}
                  {preview.data?.requiresRestart ? "нужен" : "не нужен"} ·
                  Подписки{" "}
                  {preview.data?.refreshSubscriptions
                    ? "обновить"
                    : "без изменений"}{" "}
                  · Правила{" "}
                  {preview.data?.refreshRules ? "обновить" : "без изменений"}
                </p>
              </div>
            </div>
          )}

          <div className="mt-3 rounded-2xl border border-white/10 bg-black/10 px-3 py-3">
            <label
              htmlFor="draft-note"
              className="vectra-kicker text-slate-500"
            >
              Комментарий к черновику
            </label>
            <input
              id="draft-note"
              name="draft-note"
              className="vectra-field mt-2 px-3 py-2.5 text-sm text-white"
              placeholder="Что меняется в этой ревизии"
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </div>
        </Panel>
      </div>

      <Panel eyebrow="JSON editor" title="Desired config JSON" tone="muted">
        <div className="space-y-3">
          <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-start">
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
              <p className="vectra-kicker text-slate-500">Режим работы</p>
              <p className="mt-2 text-sm text-white">Секреты маскируются автоматически.</p>
              <p className="mt-1 text-sm text-slate-400">
                Если JSON валиден, сначала сохраните ревизию в панели. Применение остаётся вторым шагом и использует только уже записанный черновик.
              </p>
            </div>

            <ActionStrip justify="start" dense>
              <button
                type="button"
                className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:bg-white/10 disabled:cursor-not-allowed disabled:opacity-50"
                disabled={!canSave}
                onClick={() =>
                  parsedConfig && selectedRouterId
                    ? saveMutation.mutate({
                        routerId: selectedRouterId,
                        note: note.trim().length > 0 ? note.trim() : undefined,
                        config: parsedConfig,
                      })
                    : undefined
                }
              >
                {saveMutation.isPending ? "Сохраняю..." : "Сохранить черновик"}
              </button>
              <button
                type="button"
                className="vectra-button-primary px-3 py-2 text-sm font-medium transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)] disabled:cursor-not-allowed disabled:opacity-50"
                disabled={!canQueue}
                onClick={() =>
                  latestDraftId && selectedRouterId
                    ? queueMutation.mutate({
                        routerId: selectedRouterId,
                        desiredRevisionId: latestDraftId,
                      })
                    : undefined
                }
              >
                {queueMutation.isPending
                  ? "Ставлю применение в очередь..."
                  : "Сохранить и отправить на роутер"}
              </button>
            </ActionStrip>
          </div>
        </div>

        <label
          htmlFor="draft-json-editor"
          className="mt-4 block text-xs font-medium uppercase tracking-[0.08em] text-slate-400"
        >
          Desired config JSON
        </label>
        <textarea
          id="draft-json-editor"
          name="draft-json-editor"
          className="vectra-field mt-4 h-[22rem] border-white/10 bg-[rgba(6,10,18,0.94)] p-3 font-[family:var(--font-plex-mono)] text-[12px] leading-6 text-slate-100 sm:h-[28rem] sm:p-4 lg:h-[34rem]"
          spellCheck={false}
          value={editorText}
          onChange={(event) => setEditorText(event.target.value)}
        />
      </Panel>
    </div>
  );
}
