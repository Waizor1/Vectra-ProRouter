import { notFound } from "next/navigation";

import { TRPCError } from "@trpc/server";

import { RouterDetailWorkspace } from "~/components/router-detail-workspace";
import {
  hasActiveDirectMode,
  isRouterReachable,
} from "~/server/vectra/router-presence";
import { api } from "~/trpc/server";

export default async function RouterDetailPage({
  params,
}: {
  params: Promise<{ routerId: string }>;
}) {
  const { routerId } = await params;

  const surface = await api.draft.editorSurface({ routerId }).catch((error: unknown) => {
    if (error instanceof TRPCError && error.code === "NOT_FOUND") {
      notFound();
    }

    throw error;
  });

  const routerReachable = isRouterReachable(surface.routerRuntimeSummary.lastSeenAt);
  const directModeActive = hasActiveDirectMode(
    (surface.routerRuntimeSummary.status ?? "offline") as
      | "pending"
      | "active"
      | "offline"
      | "direct"
      | "rescue"
      | "disabled",
    surface.routerRuntimeSummary.lastSeenAt,
  );
  const needsRecoveryAction =
    surface.routerRuntimeSummary.status === "direct" ||
    surface.routerRuntimeSummary.passwallEnabled === false ||
    Boolean(surface.routerRuntimeSummary.lastRescueReason);

  return (
    <section>
      <RouterDetailWorkspace
        routerId={surface.routerRuntimeSummary.id}
        initialSurface={surface}
        routerReachable={routerReachable}
        directModeActive={directModeActive}
        needsRecoveryAction={needsRecoveryAction}
      />
    </section>
  );
}
