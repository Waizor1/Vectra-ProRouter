"use client";

import { useState } from "react";

import { Panel } from "~/components/panel";
import { TabBar } from "~/components/tab-bar";
import { GlobalTemplateRolloutWorkspace } from "~/components/global-template-rollout-workspace";
import { RolloutProfilesWorkspace } from "~/components/rollout-profiles-workspace";
import { UpdateVersionDriftWorkspace } from "~/components/update-version-drift-workspace";
import { api, type RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];

type UpdatesControlTab = "baseline" | "groups" | "controller";

function formatUpdatesControlTabLabel(tab: UpdatesControlTab) {
  switch (tab) {
    case "baseline":
      return "Baseline";
    case "groups":
      return "Группы и профили";
    case "controller":
      return "Контроллер версий";
  }
}

export function UpdatesControlWorkspace({
  initialGlobalTemplateWorkspace,
}: {
  initialGlobalTemplateWorkspace: GlobalTemplateWorkspaceData;
}) {
  const [activeTab, setActiveTab] = useState<UpdatesControlTab>("baseline");
  const profilesAndGroupsQuery = api.update.profilesAndGroupsWorkspace.useQuery(undefined, {
    enabled: activeTab === "groups",
    refetchOnWindowFocus: false,
  });
  const versionDriftQuery = api.update.versionDriftWorkspace.useQuery(undefined, {
    enabled: activeTab === "controller" || activeTab === "groups",
    refetchOnWindowFocus: false,
  });

  return (
    <div className="space-y-4">
      <section className="rounded-[1.4rem] border border-white/10 bg-[rgba(8,11,17,0.76)] px-4 py-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <p className="vectra-kicker text-slate-500">Рабочие поверхности</p>
            <p className="mt-1 text-sm leading-6 text-slate-300">
              Сейчас открыт{" "}
              <span className="font-medium text-white">
                {formatUpdatesControlTabLabel(activeTab)}
              </span>
              .
            </p>
          </div>
          <span className="w-fit rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-slate-300">
            Основная рабочая поверхность ниже
          </span>
        </div>

        <div className="mt-3">
          <TabBar
            ariaLabel="Рабочие поверхности обновлений"
            items={[
              {
                id: "baseline",
                label: "Baseline",
                active: activeTab === "baseline",
                onSelect: () => setActiveTab("baseline"),
              },
              {
                id: "groups",
                label: "Группы и профили",
                active: activeTab === "groups",
                onSelect: () => setActiveTab("groups"),
              },
              {
                id: "controller",
                label: "Контроллер версий",
                active: activeTab === "controller",
                onSelect: () => setActiveTab("controller"),
              },
            ]}
            variant="secondary"
          />
        </div>
      </section>

      {activeTab === "baseline" ? (
        <GlobalTemplateRolloutWorkspace initialWorkspace={initialGlobalTemplateWorkspace} />
      ) : null}

      {activeTab === "groups" ? (
        profilesAndGroupsQuery.data && versionDriftQuery.data ? (
          <RolloutProfilesWorkspace
            initialWorkspace={profilesAndGroupsQuery.data}
            initialVersionDriftWorkspace={versionDriftQuery.data}
            onOpenVersionControl={() => setActiveTab("controller")}
          />
        ) : (
          <Panel eyebrow="Группы и профили" title="Загрузка рабочих поверхностей" tone="muted">
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
              Загружаю reusable профили, группы и их version-control сводку.
            </div>
          </Panel>
        )
      ) : null}

      {activeTab === "controller" ? (
        versionDriftQuery.data ? (
          <UpdateVersionDriftWorkspace initialWorkspace={versionDriftQuery.data} />
        ) : (
          <Panel eyebrow="Контроллер версий" title="Загрузка version-control" tone="muted">
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
              Загружаю сводку по controller, PassWall и Xray.
            </div>
          </Panel>
        )
      ) : null}
    </div>
  );
}
