import { notFound } from "next/navigation";

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

  const router = await api.fleet.byId({ routerId }).catch(() => {
    notFound();
  });

  const payload = router.latestSnapshot?.payload;
  const routerReachable = isRouterReachable(router.router.lastSeenAt);
  const directModeActive = hasActiveDirectMode(
    router.router.status,
    router.router.lastSeenAt,
  );
  const needsRecoveryAction =
    router.router.status === "direct" ||
    router.latestSnapshot?.passwallEnabled === false ||
    Boolean(router.router.lastRescueReason) ||
    Boolean(payload?.lastRescue?.reason);

  return (
    <section>
      <RouterDetailWorkspace
        routerId={router.router.id}
        routerReachable={routerReachable}
        directModeActive={directModeActive}
        needsRecoveryAction={needsRecoveryAction}
        inventory={{
          controllerVersion:
            payload?.controllerVersion ??
            router.latestSnapshot?.controllerVersion ??
            null,
          passwallVersion:
            router.latestSnapshot?.passwallAppVersion ??
            payload?.packageVersions["luci-app-passwall2"] ??
            null,
          packageVersions: payload?.packageVersions ?? {},
          binaryVersions: payload?.binaryVersions ?? {},
          rulesAssets: payload?.rulesAssets ?? null,
          serviceHealth: payload?.serviceHealth ?? null,
          telegramReachability: payload?.telegramReachability ?? null,
        }}
      />
    </section>
  );
}
