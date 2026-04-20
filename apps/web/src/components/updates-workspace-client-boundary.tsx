"use client";

import dynamic from "next/dynamic";

import { Panel } from "~/components/panel";
import type { RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];

const UpdatesControlWorkspace = dynamic(
  () =>
    import("~/components/updates-control-workspace").then(
      (module) => module.UpdatesControlWorkspace,
    ),
  {
    ssr: false,
    loading: () => (
      <Panel
        eyebrow="Рабочие поверхности"
        title="Подготавливаю baseline, группы и version-control"
        tone="hero"
      >
        <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
          Загружаю интерактивные рабочие поверхности обновлений. Статические сводки и опубликованные артефакты уже доступны выше.
        </div>
      </Panel>
    ),
  },
);

export function UpdatesWorkspaceClientBoundary({
  initialGlobalTemplateWorkspace,
}: {
  initialGlobalTemplateWorkspace: GlobalTemplateWorkspaceData;
}) {
  return (
    <UpdatesControlWorkspace
      initialGlobalTemplateWorkspace={initialGlobalTemplateWorkspace}
    />
  );
}
