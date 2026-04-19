"use client";

import { UpdatesControlWorkspace } from "~/components/updates-control-workspace";
import type { RouterOutputs } from "~/trpc/react";

type GlobalTemplateWorkspaceData = RouterOutputs["update"]["globalTemplateWorkspace"];

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
