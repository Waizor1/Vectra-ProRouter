import { FleetMonitoringWorkspace } from "~/components/fleet-monitoring-workspace";
import { PageHeader } from "~/components/page-header";
import { api } from "~/trpc/server";

export default async function FleetPage() {
  const monitoring = await api.fleet.monitoring();

  return (
    <section className="space-y-6">
      <PageHeader
        eyebrow="Панель Vectra"
        title="Парк роутеров"
        description="Статус парка, алерты и быстрый переход в нужный роутер."
        mobileDescription="Статус парка и быстрый переход в нужный роутер."
      />

      <FleetMonitoringWorkspace initialMonitoring={monitoring} />
    </section>
  );
}
