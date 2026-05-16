import { RefreshCw } from "lucide-react";

import { RouterDetailPlaceholder } from "~/features/router-detail/tabs/_placeholder";

export interface UpdatesTabProps {
  routerId: string;
}

export function UpdatesTab({ routerId }: UpdatesTabProps) {
  return (
    <RouterDetailPlaceholder
      routerId={routerId}
      icon={RefreshCw}
      title="Обновления"
      description="Controller-agent, PassWall и связанные апдейты конкретно для этого роутера."
      legacyTab="app-update"
      hint="Глобальные раскатки остаются в разделе /updates."
    />
  );
}
