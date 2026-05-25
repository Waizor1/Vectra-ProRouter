import { UpdatesV2 } from "~/features/updates/updates-v2";
import { api } from "~/trpc/server";

export default async function UpdatesPage() {
  const [artifacts, manifests, profilesWorkspace] = await Promise.all([
    api.update.artifacts(),
    api.update.firmwareMatrix(),
    api.update.profilesAndGroupsWorkspace(),
  ]);

  return (
    <UpdatesV2
      initialWorkspace={profilesWorkspace}
      artifacts={artifacts}
      manifests={manifests}
    />
  );
}
