import { Boxes } from "lucide-react";

import { RouterDetailPlaceholder } from "~/features/router-detail/tabs/_placeholder";

export interface NodesTabProps {
  routerId: string;
}

export function NodesTab({ routerId }: NodesTabProps) {
  return (
    <RouterDetailPlaceholder
      routerId={routerId}
      icon={Boxes}
      title="Узлы"
      description="Список узлов и подписок PassWall: выбор активного, редактирование, импорт."
      legacyTab="node-list"
      hint="После Phase 6 distill узлы и подписки разделятся на отдельные секции."
    />
  );
}
