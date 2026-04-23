"use client";

import dynamic from "next/dynamic";

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
      <div className="rounded-[1.4rem] border border-white/10 bg-[rgba(8,11,17,0.76)] px-4 py-4">
        <p className="vectra-kicker text-slate-500">Рабочие поверхности</p>
        <p className="mt-2 text-sm font-medium text-white">
          Подготавливаю active workspace
        </p>
        <p className="mt-1 text-sm leading-6 text-slate-400">
          Загружаю интерактивную рабочую поверхность обновлений.
        </p>
      </div>
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
