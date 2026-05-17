import type { LucideIcon } from "lucide-react";
import { CheckCircle2, Network, ShieldAlert, WifiOff } from "lucide-react";

import { cn } from "~/lib/utils";
import { StatusDot } from "~/components/vectra/status-dot";
import { toneClasses, type Tone } from "~/lib/tone";

export interface FleetKpiStripProps {
  total: number;
  healthy: number;
  problem: number;
  offline: number;
  className?: string;
}

interface KpiTile {
  key: string;
  label: string;
  value: number;
  tone: Tone;
  icon: LucideIcon;
  hint: string;
}

export function FleetKpiStrip({
  total,
  healthy,
  problem,
  offline,
  className,
}: FleetKpiStripProps) {
  const tiles: KpiTile[] = [
    {
      key: "total",
      label: "Всего",
      value: total,
      tone: "neutral",
      icon: Network,
      hint: "роутеров в парке",
    },
    {
      key: "healthy",
      label: "Здоровые",
      value: healthy,
      tone: "good",
      icon: CheckCircle2,
      hint: "online и стабильны",
    },
    {
      key: "problem",
      label: "С проблемой",
      value: problem,
      tone: problem > 0 ? "warning" : "neutral",
      icon: ShieldAlert,
      hint: "rescue, direct или review",
    },
    {
      key: "offline",
      label: "Offline",
      value: offline,
      tone: offline > 0 ? "critical" : "neutral",
      icon: WifiOff,
      hint: "нет связи",
    },
  ];

  return (
    <div
      className={cn(
        "grid grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3",
        className,
      )}
    >
      {tiles.map((tile) => {
        const t = toneClasses[tile.tone];
        const Icon = tile.icon;
        return (
          <div
            key={tile.key}
            className={cn(
              "flex flex-col gap-1 rounded-lg border bg-card/40 px-3 py-2.5",
              t.border,
            )}
          >
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              <StatusDot tone={tile.tone} />
              <span className="truncate">{tile.label}</span>
              <Icon
                className={cn("ml-auto h-3.5 w-3.5", t.text)}
                strokeWidth={1.75}
              />
            </div>
            <div className="flex items-baseline gap-2">
              <span
                className={cn(
                  "font-[family:var(--font-plex-mono,inherit)] text-2xl font-semibold tabular-nums leading-none",
                  tile.tone === "neutral" ? "text-foreground" : t.text,
                )}
              >
                {tile.value}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {tile.hint}
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
