import type { RouterInventory } from "@vectra/contracts";

export type RouterMemoryLevel = "good" | "warning" | "critical" | "unknown";

export type RouterMemoryResources =
  | Pick<RouterInventory["resources"], "memoryTotalMb" | "memoryAvailableMb">
  | {
      memoryTotalMb?: number | null;
      memoryAvailableMb?: number | null;
    };

export type RouterMemoryStatus = {
  level: RouterMemoryLevel;
  availableMb: number | null;
  totalMb: number | null;
  availablePercent: number | null;
  label: string;
  summary: string;
  detail: string;
};

const criticalAvailableMb = 48;
const warningAvailableMb = 64;
const criticalAvailablePercent = 20;
const warningAvailablePercent = 28;

function normalizeMemoryValue(value: number | null | undefined) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : null;
}

function formatMegabytes(value: number) {
  return `${Math.round(value)} МБ`;
}

function getMemoryLabel(level: RouterMemoryLevel) {
  switch (level) {
    case "good":
      return "RAM OK";
    case "warning":
      return "RAM низкая";
    case "critical":
      return "RAM критично";
    case "unknown":
      return "RAM нет данных";
  }
}

function getMemoryDetail(level: RouterMemoryLevel) {
  switch (level) {
    case "good":
      return "Контроллер видит нормальный запас RAM по последнему check-in.";
    case "warning":
      return "Контроллер видит запас RAM, но роутер уже близко к low-memory зоне.";
    case "critical":
      return "Свободной RAM критически мало: высокий риск OOM, убийства Xray и обрыва PassWall-связи.";
    case "unknown":
      return "Последний check-in не содержит пригодных данных по RAM.";
  }
}

function getMemoryLevel({
  availableMb,
  availablePercent,
}: {
  availableMb: number;
  availablePercent: number | null;
}): RouterMemoryLevel {
  if (
    availableMb < criticalAvailableMb ||
    (availablePercent !== null && availablePercent < criticalAvailablePercent)
  ) {
    return "critical";
  }

  if (
    availableMb < warningAvailableMb ||
    (availablePercent !== null && availablePercent < warningAvailablePercent)
  ) {
    return "warning";
  }

  return "good";
}

export function describeRouterMemory(
  resources: RouterMemoryResources | null | undefined,
): RouterMemoryStatus {
  const availableMb = normalizeMemoryValue(resources?.memoryAvailableMb);
  const totalMb = normalizeMemoryValue(resources?.memoryTotalMb);
  const hasAvailable = availableMb !== null;
  const hasTotal = totalMb !== null && totalMb > 0;

  if (!hasAvailable || (availableMb === 0 && !hasTotal)) {
    return {
      level: "unknown",
      availableMb: null,
      totalMb: hasTotal ? totalMb : null,
      availablePercent: null,
      label: getMemoryLabel("unknown"),
      summary: "RAM нет данных",
      detail: getMemoryDetail("unknown"),
    };
  }

  const availablePercent = hasTotal
    ? Math.max(0, Math.min(100, Math.round((availableMb / totalMb) * 100)))
    : null;
  const level = getMemoryLevel({ availableMb, availablePercent });
  const summary = hasTotal
    ? `${formatMegabytes(availableMb)} свободно из ${formatMegabytes(totalMb)}${
        availablePercent !== null ? ` (${availablePercent}%)` : ""
      }`
    : `${formatMegabytes(availableMb)} свободно`;

  return {
    level,
    availableMb,
    totalMb: hasTotal ? totalMb : null,
    availablePercent,
    label: getMemoryLabel(level),
    summary,
    detail: getMemoryDetail(level),
  };
}

export function getRouterMemoryTone(level: RouterMemoryLevel) {
  switch (level) {
    case "good":
      return "good";
    case "warning":
      return "warning";
    case "critical":
      return "danger";
    case "unknown":
      return "default";
  }
}
