"use client";

import dynamic from "next/dynamic";

import type { RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];
type ProfilesAndGroupsWorkspace = RouterOutputs["update"]["profilesAndGroupsWorkspace"];
type VersionDriftWorkspace = RouterOutputs["update"]["versionDriftWorkspace"];

const UpdatesControlWorkspace = dynamic(
  () =>
    import("~/components/updates-control-workspace").then((module) => ({
      default: module.UpdatesControlWorkspace,
    })),
  {
    ssr: false,
    loading: () => null,
  },
);

export function UpdatesWorkspaceClientBoundary({
  initialGlobalTemplateWorkspace,
  initialProfilesAndGroupsWorkspace,
  initialVersionDriftWorkspace,
}: {
  initialGlobalTemplateWorkspace: GlobalTemplateWorkspaceData;
  initialProfilesAndGroupsWorkspace: ProfilesAndGroupsWorkspace;
  initialVersionDriftWorkspace: VersionDriftWorkspace;
}) {
  return (
    <UpdatesControlWorkspace
      initialGlobalTemplateWorkspace={initialGlobalTemplateWorkspace}
      initialProfilesAndGroupsWorkspace={initialProfilesAndGroupsWorkspace}
      initialVersionDriftWorkspace={initialVersionDriftWorkspace}
    />
  );
}
