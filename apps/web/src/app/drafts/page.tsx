import { DraftWorkspace } from "~/components/draft-workspace";
import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";
import { api } from "~/trpc/server";

function formatRevisionStatus(value?: string | null) {
  switch (value) {
    case "draft":
      return "черновик";
    case "queued":
      return "в очереди";
    case "applied":
      return "применено";
    case "approved":
      return "утверждено";
    case "import_review":
      return "на проверке";
    case "superseded":
      return "замещено";
    default:
      return value ?? "неизвестно";
  }
}

export default async function DraftsPage({
  searchParams,
}: {
  searchParams: Promise<{ routerId?: string }>;
}) {
  const { routerId } = await searchParams;
  const [drafts, initialWorkspace] = await Promise.all([
    api.draft.list(),
    api.draft.workspace({ routerId }),
  ]);
  const initialPreviewTarget =
    initialWorkspace.workspaceRevision?.config ??
    initialWorkspace.activeRevision?.config ??
    null;
  const initialPreview = initialPreviewTarget
    ? await api.draft.preview({
        previous: initialWorkspace.activeRevision?.config ?? null,
        next: initialPreviewTarget,
      })
    : null;

  return (
    <section className="space-y-4">
      <PageHeader
        eyebrow="Черновики"
        title="Экспертные ревизии"
        description="Это резервный экран для точечной JSON-правки и быстрой работы с ревизиями. Обычный операторский поток по-прежнему начинается со страницы конкретного роутера."
        mobileDescription="Резервный JSON-режим и история ревизий."
        compact
      />

      <Panel eyebrow="Когда идти сюда" title="Не основной путь" tone="muted" compact>
        <div className="grid gap-3 md:grid-cols-3">
          {[
            "Если обычной формы на странице роутера уже не хватает.",
            "Если нужно быстро сравнить preview и сохранить ревизию как JSON.",
            "Если apply должен идти только из уже сохранённого черновика.",
          ].map((item) => (
            <div
              key={item}
              className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300"
            >
              {item}
            </div>
          ))}
        </div>
      </Panel>

      <DraftWorkspace
        initialRouterId={routerId}
        initialWorkspace={initialWorkspace}
        initialPreview={initialPreview}
      />

      <Panel eyebrow="История" title="Последние ревизии" tone="muted" compact>
        <div className="space-y-3">
          {drafts.length > 0 ? (
            drafts.slice(0, 8).map((draft) => (
              <div
                key={draft.id}
                className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3"
              >
                <div className="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
                  <div>
                    <p className="text-sm font-semibold text-white">
                      Ревизия #{draft.revisionNumber}
                    </p>
                    <p className="mt-1 text-sm text-slate-400">
                      Роутер {draft.routerId} ·{" "}
                      {formatRevisionStatus(draft.status)}
                    </p>
                  </div>
                  <p className="text-sm font-[family:var(--font-plex-mono)] text-slate-300">
                    {draft.impact.changedSections.join(", ") || "изменений нет"}
                  </p>
                </div>
                <p className="mt-3 text-sm leading-6 text-slate-300">
                  Перезапуск: {draft.impact.requiresRestart ? "да" : "нет"} ·
                  Подписки:{" "}
                  {draft.impact.refreshSubscriptions
                    ? "обновить"
                    : "без изменений"}{" "}
                  · Правила:{" "}
                  {draft.impact.refreshRules ? "обновить" : "без изменений"} ·
                  Пакеты:{" "}
                  {draft.impact.packageInstall ? "установить" : "без изменений"}
                </p>
              </div>
            ))
          ) : (
            <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300">
              Черновиков пока нет. После первого сохранения здесь появится
              короткая история ревизий.
            </div>
          )}
        </div>
      </Panel>
    </section>
  );
}
