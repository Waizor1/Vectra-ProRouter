import Link from "next/link";
import { AlertOctagon, AlertTriangle, ArrowRight, Info } from "lucide-react";

import { cn } from "~/lib/utils";
import { ToneBadge } from "~/components/vectra/tone-badge";
import type { Tone } from "~/lib/tone";

interface FleetAlertItem {
  id: string;
  routerId: string;
  routerName: string;
  href: string;
  title: string;
  description: string;
  severity: "critical" | "warning" | "info";
}

export interface FleetAlertStripProps {
  alerts: FleetAlertItem[];
  className?: string;
}

const severityTone: Record<FleetAlertItem["severity"], Tone> = {
  critical: "critical",
  warning: "warning",
  info: "info",
};

const severityLabel: Record<FleetAlertItem["severity"], string> = {
  critical: "сейчас важно",
  warning: "проверить",
  info: "для сведения",
};

const severityRank: Record<FleetAlertItem["severity"], number> = {
  critical: 0,
  warning: 1,
  info: 2,
};

const severityIcon = {
  critical: AlertOctagon,
  warning: AlertTriangle,
  info: Info,
} as const;

export function FleetAlertStrip({ alerts, className }: FleetAlertStripProps) {
  const actionable = alerts.filter(
    (alert) => alert.severity === "critical" || alert.severity === "warning",
  );
  if (actionable.length === 0) {
    return null;
  }

  const sorted = [...actionable].sort(
    (left, right) => severityRank[left.severity] - severityRank[right.severity],
  );
  const topSeverity = sorted[0]?.severity ?? "warning";

  return (
    <aside
      role="region"
      aria-label="Активные алерты парка"
      className={cn(
        "sticky top-0 z-10 -mx-4 border-y border-border/40 bg-background/95 px-4 py-2 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:mx-0 sm:rounded-md sm:border",
        topSeverity === "critical"
          ? "border-rose-500/30 bg-rose-500/5"
          : "border-amber-500/30 bg-amber-500/5",
        className,
      )}
    >
      <ul className="flex flex-col divide-y divide-border/40">
        {sorted.map((alert) => {
          const Icon = severityIcon[alert.severity];
          return (
            <li
              key={alert.id}
              className="flex items-center gap-3 py-1.5 text-sm first:pt-1 last:pb-1"
            >
              <Icon
                className={cn(
                  "h-4 w-4 shrink-0",
                  alert.severity === "critical"
                    ? "text-rose-300"
                    : "text-amber-300",
                )}
                strokeWidth={1.75}
              />
              <ToneBadge tone={severityTone[alert.severity]} className="shrink-0">
                {severityLabel[alert.severity]}
              </ToneBadge>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium text-foreground">
                  {alert.routerName}
                  <span className="ml-2 font-normal text-muted-foreground">
                    {alert.title}
                  </span>
                </p>
                <p className="truncate text-xs text-muted-foreground">
                  {alert.description}
                </p>
              </div>
              <Link
                href={alert.href}
                className="inline-flex shrink-0 items-center gap-1 rounded-md border border-border/40 bg-card/60 px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:border-border hover:bg-secondary/40"
              >
                Открыть
                <ArrowRight className="h-3 w-3" />
              </Link>
            </li>
          );
        })}
      </ul>
    </aside>
  );
}

export type { FleetAlertItem };
