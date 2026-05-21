"use client";

import { useState } from "react";
import Link from "next/link";
import {
  AlertOctagon,
  AlertTriangle,
  ArrowRight,
  ChevronDown,
  Info,
} from "lucide-react";

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

const COLLAPSED_LIMIT = 4;

export function FleetAlertStrip({ alerts, className }: FleetAlertStripProps) {
  const [expanded, setExpanded] = useState(false);

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

  const overflowCount = Math.max(0, sorted.length - COLLAPSED_LIMIT);
  const hasOverflow = overflowCount > 0;
  const visible = expanded ? sorted : sorted.slice(0, COLLAPSED_LIMIT);

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
      <ul
        className={cn(
          "flex flex-col divide-y divide-border/40",
          expanded && hasOverflow && "max-h-[40vh] overflow-y-auto",
        )}
      >
        {visible.map((alert) => {
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
      {hasOverflow ? (
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          aria-expanded={expanded}
          className="mt-1.5 flex w-full items-center justify-center gap-1 rounded-md border border-border/40 bg-card/40 py-1 text-xs font-medium text-muted-foreground transition-colors hover:border-border hover:bg-secondary/40 hover:text-foreground"
        >
          {expanded ? "Свернуть" : `Показать ещё ${overflowCount}`}
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 transition-transform",
              expanded && "rotate-180",
            )}
            strokeWidth={1.75}
          />
        </button>
      ) : null}
    </aside>
  );
}

export type { FleetAlertItem };
