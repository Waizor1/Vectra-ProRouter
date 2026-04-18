"use client";

import { useState } from "react";

import { Panel } from "~/components/panel";
import { TabBar } from "~/components/tab-bar";
import { GlobalTemplateRolloutWorkspace } from "~/components/global-template-rollout-workspace";
import { RolloutProfilesWorkspace } from "~/components/rollout-profiles-workspace";
import { UpdateVersionDriftWorkspace } from "~/components/update-version-drift-workspace";
import type { RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];
type ProfilesAndGroupsWorkspace = RouterOutputs["update"]["profilesAndGroupsWorkspace"];
type VersionDriftWorkspace = RouterOutputs["update"]["versionDriftWorkspace"];

type UpdatesControlTab = "baseline" | "groups" | "controller";

export function UpdatesControlWorkspace({
  initialGlobalTemplateWorkspace,
  initialProfilesAndGroupsWorkspace,
  initialVersionDriftWorkspace,
}: {
  initialGlobalTemplateWorkspace: GlobalTemplateWorkspaceData;
  initialProfilesAndGroupsWorkspace: ProfilesAndGroupsWorkspace;
  initialVersionDriftWorkspace: VersionDriftWorkspace;
}) {
  const [activeTab, setActiveTab] = useState<UpdatesControlTab>("baseline");

  return (
    <div className="space-y-4">
      <Panel eyebrow="Рабочие поверхности" title="Одна система для baseline, групп и update-контроля" tone="hero">
        <div className="space-y-4">
          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
            Разделите работу по ролям: в `Baseline` вы держите глобальный эталон, в `Группы и профили` собираете reusable профили и раскладываете парк по группам, а в `Контроллер версий` видите отстающие Xray / PassWall / controller и обновляете их адресно или пачкой.
          </div>

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
      </Panel>

      {activeTab === "baseline" ? (
        <GlobalTemplateRolloutWorkspace initialWorkspace={initialGlobalTemplateWorkspace} />
      ) : null}

      {activeTab === "groups" ? (
        <RolloutProfilesWorkspace initialWorkspace={initialProfilesAndGroupsWorkspace} />
      ) : null}

      {activeTab === "controller" ? (
        <UpdateVersionDriftWorkspace initialWorkspace={initialVersionDriftWorkspace} />
      ) : null}
    </div>
  );
}
