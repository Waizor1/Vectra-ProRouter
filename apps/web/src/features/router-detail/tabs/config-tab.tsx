import { Wrench } from "lucide-react";

import { RouterDetailPlaceholder } from "~/features/router-detail/tabs/_placeholder";

export interface ConfigTabProps {
  routerId: string;
}

export function ConfigTab({ routerId }: ConfigTabProps) {
  return (
    <RouterDetailPlaceholder
      routerId={routerId}
      icon={Wrench}
      title="Конфигурация"
      description="UCI-секции PassWall, shunt-rules, DNS, log и maintain."
      legacyTab="basic-settings"
      hint="После Phase 6 distill этот таб переедет на компонуемые формы Vectra."
    />
  );
}
