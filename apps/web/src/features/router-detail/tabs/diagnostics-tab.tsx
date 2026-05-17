import { ScrollText } from "lucide-react";

import { RouterDetailPlaceholder } from "~/features/router-detail/tabs/_placeholder";

export interface DiagnosticsTabProps {
  routerId: string;
}

export function DiagnosticsTab({ routerId }: DiagnosticsTabProps) {
  return (
    <RouterDetailPlaceholder
      routerId={routerId}
      icon={ScrollText}
      title="Диагностика"
      description="Watch logs, geo-view, safety events и controller-runtime telemetry."
      legacyTab="watch-logs"
      hint="После Phase 6 distill watch-logs переедет на shadcn ScrollArea с live feed."
    />
  );
}
