"use client";

import { skipToken } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { api, type RouterInputs } from "~/trpc/react";

type DraftConfigInput = RouterInputs["draft"]["save"]["config"];

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
}: {
  initialRouterId?: string;
}) {
  const router = useRouter();
  const utils = api.useUtils();

  const [selectedRouterId, setSelectedRouterId] = useState(initialRouterId ?? "");
  const [editorText, setEditorText] = useState("");
  const [note, setNote] = useState("");
  const [loadedRevisionId, setLoadedRevisionId] = useState<string | null>(null);
  const [savedRevisionId, setSavedRevisionId] = useState<string | null>(null);

  const workspace = api.draft.workspace.useQuery({
    routerId: selectedRouterId || undefined,
  });

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

  const preview = api.draft.preview.useQuery(
    previewTarget
      ? {
          previous: workspace.data?.activeRevision?.config ?? null,
          next: previewTarget,
        }
      : skipToken
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
        Загружаю экспертный рабочий режим...
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
      <div className="grid gap-4 lg:grid-cols-[0.95fr_1.05fr]">
        <article className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <p className="vectra-kicker text-slate-500">Роутер</p>
              <p className="mt-2 text-sm text-slate-300">
                Выберите устройство для экспертной правки.
              </p>
            </div>
          </div>
          <label
            htmlFor="draft-router-select"
            className="mt-3 block text-xs font-medium uppercase tracking-[0.08em] text-slate-400"
          >
            Выбор роутера
          </label>
          <select
            id="draft-router-select"
            name="draft-router-select"
            className="mt-3 w-full rounded-md border border-white/10 bg-[rgba(8,12,20,0.82)] px-3 py-2.5 text-sm text-white outline-none transition focus:border-[var(--vectra-accent)]/60"
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

          <div className="mt-5 flex flex-wrap gap-2">
            <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-300">
              Статус {formatRouterStatus(selectedRouter?.status)}
            </span>
            <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-300">
              Импорт {formatImportState(selectedRouter?.importState)}
            </span>
            <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-300">
              Активная {workspace.data.activeRevision?.revisionNumber ?? "нет"}
            </span>
            <span className="vectra-chip rounded-full border border-white/10 bg-white/5 px-3 py-1 text-slate-300">
              Рабочая {workspace.data.workspaceRevision?.revisionNumber ?? "нет"}
            </span>
          </div>

          <dl className="mt-5 grid gap-3 text-sm text-slate-300 sm:grid-cols-3">
            <div className="rounded-md border border-white/10 bg-black/10 px-3 py-3">
              <dt className="vectra-kicker text-slate-500">Импорт</dt>
              <dd className="mt-2 text-sm text-slate-200">
                {workspace.data.importedRevision
                  ? `#${workspace.data.importedRevision.revisionNumber}`
                  : "нет"}
              </dd>
            </div>
            <div className="rounded-md border border-white/10 bg-black/10 px-3 py-3">
              <dt className="vectra-kicker text-slate-500">Черновик</dt>
              <dd className="mt-2 text-sm text-slate-200">
                {workspace.data.latestDraft
                  ? `#${workspace.data.latestDraft.revisionNumber}`
                  : "нет"}
              </dd>
            </div>
            <div className="rounded-md border border-white/10 bg-black/10 px-3 py-3">
              <dt className="vectra-kicker text-slate-500">Источник</dt>
              <dd className="mt-2 text-sm text-slate-200">
                {selectedRouter?.importState === "approved"
                  ? "эталон подтверждён"
                  : "нужна проверка импорта"}
              </dd>
            </div>
          </dl>
        </article>

        <article className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <p className="vectra-kicker text-slate-500">Предпросмотр применения</p>
          {parseError ? (
            <div className="mt-3 rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-3 text-sm leading-7 text-rose-200">
              Некорректный JSON: {parseError}
            </div>
          ) : (
            <div className="mt-3 grid gap-3 md:grid-cols-2">
              <div className="rounded-md border border-white/10 bg-black/10 px-3 py-3">
                <p className="vectra-kicker text-slate-500">Изменённые секции</p>
                <p className="mt-3 text-sm leading-7 text-slate-200">
                  {preview.data?.changedSections.length
                    ? preview.data.changedSections.join(", ")
                    : "изменений нет"}
                </p>
              </div>
              <div className="rounded-md border border-white/10 bg-black/10 px-3 py-3">
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

          <div className="mt-3 rounded-md border border-white/10 bg-black/10 px-3 py-3">
            <label
              htmlFor="draft-note"
              className="vectra-kicker text-slate-500"
            >
              Комментарий к черновику
            </label>
            <input
              id="draft-note"
              name="draft-note"
              className="mt-2 w-full rounded-md border border-white/10 bg-[rgba(8,12,20,0.82)] px-3 py-2.5 text-sm text-white outline-none transition focus:border-[var(--vectra-accent)]/60"
              placeholder="Что меняется в этой ревизии"
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </div>
        </article>
      </div>

      <div className="rounded-md border border-white/10 bg-[rgba(8,12,20,0.86)] px-4 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p className="vectra-kicker text-slate-500">JSON-редактор</p>
            <p className="mt-2 text-sm text-slate-300">
              Прямая правка typed JSON. Секреты маскируются автоматически.
            </p>
          </div>
          <div className="flex flex-wrap gap-3">
            <button
              type="button"
              className="rounded-md border border-white/10 bg-white/5 px-3 py-2 text-sm font-medium text-slate-200 transition hover:border-white/20 hover:bg-white/10 disabled:cursor-not-allowed disabled:opacity-50"
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
              className="rounded-md bg-[var(--vectra-accent)] px-3 py-2 text-sm font-medium text-slate-950 transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)] disabled:cursor-not-allowed disabled:opacity-50"
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
                ? "Ставлю apply..."
                : "Сохранить и отправить на роутер"}
            </button>
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
          className="mt-4 h-[22rem] w-full rounded-md border border-white/10 bg-[rgba(6,10,18,0.94)] p-3 font-[family:var(--font-plex-mono)] text-[12px] leading-6 text-slate-100 outline-none transition focus:border-[var(--vectra-accent)]/60 sm:h-[28rem] sm:p-4 lg:h-[34rem]"
          spellCheck={false}
          value={editorText}
          onChange={(event) => setEditorText(event.target.value)}
        />
      </div>
    </div>
  );
}
