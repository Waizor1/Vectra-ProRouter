import { RescueV2 } from "~/features/rescue/rescue-v2";
import { api } from "~/trpc/server";

export default async function RescuePage() {
  const [policy, incidents, directRouters, rescueCases] = await Promise.all([
    api.rescue.policy(),
    api.rescue.openIncidents(),
    api.rescue.directRouters(),
    api.rescue.cases(),
  ]);

  return (
    <RescueV2
      policy={policy}
      incidents={incidents}
      directRouters={directRouters}
      rescueCases={rescueCases}
    />
  );
}
