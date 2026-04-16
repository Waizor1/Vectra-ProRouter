import { FleetMonitoringWorkspace } from "~/components/fleet-monitoring-workspace";
import { api } from "~/trpc/server";

export default async function FleetPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { q } = await searchParams;
  const monitoring = await api.fleet.monitoring();

  return (
    <section>
      <FleetMonitoringWorkspace
        initialMonitoring={monitoring}
        initialSearchQuery={q ?? ""}
      />
    </section>
  );
}
