import { FleetMonitoringWorkspace } from "~/components/fleet-monitoring-workspace";
import { FleetV2 } from "~/features/fleet/fleet-v2";
import { isUiV2 } from "~/lib/feature-flag";
import { api } from "~/trpc/server";

export default async function FleetPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string; ui?: string }>;
}) {
  const [monitoring, v2, params] = await Promise.all([
    api.fleet.monitoring(),
    isUiV2(),
    searchParams,
  ]);

  if (v2 && params.ui !== "v1") {
    return (
      <FleetV2
        initialMonitoring={monitoring}
        initialSearchQuery={params.q ?? ""}
      />
    );
  }

  return (
    <section>
      <FleetMonitoringWorkspace
        initialMonitoring={monitoring}
        initialSearchQuery={params.q ?? ""}
      />
    </section>
  );
}
