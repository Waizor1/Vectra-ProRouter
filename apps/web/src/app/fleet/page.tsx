import { FleetV2 } from "~/features/fleet/fleet-v2";
import { api } from "~/trpc/server";

export default async function FleetPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const [monitoring, params] = await Promise.all([
    api.fleet.monitoring(),
    searchParams,
  ]);

  return (
    <FleetV2 initialMonitoring={monitoring} initialSearchQuery={params.q ?? ""} />
  );
}
