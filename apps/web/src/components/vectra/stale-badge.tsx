"use client";

import * as React from "react";

import { ToneBadge } from "~/components/vectra/tone-badge";
import type { Tone } from "~/lib/tone";

export interface StaleBadgeProps {
  sinceMs: number | null | undefined;
  warningAfterMs?: number;
  criticalAfterMs?: number;
  prefix?: string;
}

function pickTone(
  ageMs: number,
  warningAfterMs: number,
  criticalAfterMs: number,
): Tone {
  if (ageMs >= criticalAfterMs) return "critical";
  if (ageMs >= warningAfterMs) return "warning";
  return "good";
}

function formatAge(ageMs: number): string {
  const seconds = Math.floor(ageMs / 1000);
  if (seconds < 60) return `${seconds} с назад`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} мин назад`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ч назад`;
  const days = Math.floor(hours / 24);
  return `${days} д назад`;
}

const StaleBadge: React.FC<StaleBadgeProps> = ({
  sinceMs,
  warningAfterMs = 60_000,
  criticalAfterMs = 5 * 60_000,
  prefix,
}) => {
  if (sinceMs == null) {
    return (
      <ToneBadge tone="neutral" dot>
        {prefix ? `${prefix} ` : ""}нет данных
      </ToneBadge>
    );
  }

  const tone = pickTone(sinceMs, warningAfterMs, criticalAfterMs);
  return (
    <ToneBadge tone={tone} dot>
      {prefix ? `${prefix} ` : ""}
      {formatAge(sinceMs)}
    </ToneBadge>
  );
};

export { StaleBadge };
