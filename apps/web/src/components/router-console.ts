export type OperatorShellTab =
  | "fleet"
  | "drafts"
  | "updates"
  | "rescue"
  | "enrollment";

export type OperatorShellSection = {
  id: OperatorShellTab;
  label: string;
  href: string;
  badge: string;
  description: string;
  commandHint: string;
  keywords: readonly string[];
};

export type RouterPrimaryTab =
  | "basic-settings"
  | "node-list"
  | "node-subscribe"
  | "app-update"
  | "rule-manage"
  | "geo-view"
  | "watch-logs";

export type DisabledRouterTab =
  | "other-settings"
  | "access-control"
  | "server-side";

export type RouterTabId = RouterPrimaryTab | DisabledRouterTab;

export type RouterSecondaryTab =
  | "main"
  | "shunt-rule"
  | "dns"
  | "log"
  | "maintain";

export type RouterConsoleSelection = {
  primaryTab: RouterPrimaryTab;
  secondaryTab: RouterSecondaryTab | null;
};

export const operatorShellTabs = [
  {
    id: "fleet",
    label: "Парк",
    href: "/fleet",
    badge: "парк",
    description: "Операционный обзор парка, алерты и вход в нужный роутер.",
    commandHint: "/fleet · парк · роутер",
    keywords: ["fleet", "парк", "роутеры", "router", "monitor"],
  },
  {
    id: "drafts",
    label: "Черновики",
    href: "/drafts",
    badge: "эксперт",
    description: "Экспертный JSON-режим для нестандартных точечных правок.",
    commandHint: "/drafts · черновики · json",
    keywords: ["drafts", "черновики", "json", "expert"],
  },
  {
    id: "updates",
    label: "Обновления",
    href: "/updates",
    badge: "массово",
    description: "Глобальный baseline и массовые действия по уже подключённому парку.",
    commandHint: "/updates · обновления · rollout",
    keywords: ["updates", "обновления", "rollout", "baseline"],
  },
  {
    id: "rescue",
    label: "Восстановление",
    href: "/rescue",
    badge: "recovery",
    description: "Direct mode, rescue-сигналы и возврат роутеров в proxy.",
    commandHint: "/rescue · direct · incident",
    keywords: ["rescue", "восстановление", "direct", "incident"],
  },
  {
    id: "enrollment",
    label: "Установка",
    href: "/enrollment",
    badge: "bootstrap",
    description: "Bootstrap нового роутера, controller-agent и первого baseline.",
    commandHint: "/enrollment · установка · bootstrap",
    keywords: ["enrollment", "установка", "bootstrap", "install"],
  },
] as const satisfies ReadonlyArray<OperatorShellSection>;

export const routerPrimaryTabs = [
  { id: "basic-settings", label: "Basic Settings" },
  { id: "node-list", label: "Node List" },
  { id: "node-subscribe", label: "Node Subscribe" },
  { id: "other-settings", label: "Other Settings", disabled: true },
  { id: "app-update", label: "App Update" },
  { id: "rule-manage", label: "Rule Manage" },
  { id: "geo-view", label: "Geo View" },
  { id: "access-control", label: "Access Control", disabled: true },
  { id: "server-side", label: "Server-Side", disabled: true },
  { id: "watch-logs", label: "Watch Logs" },
] as const satisfies ReadonlyArray<{
  id: RouterTabId;
  label: string;
  disabled?: boolean;
}>;

export const basicSettingsSecondaryTabs = [
  { id: "main", label: "Main" },
  { id: "shunt-rule", label: "Shunt Rule" },
  { id: "dns", label: "DNS" },
  { id: "log", label: "Log" },
  { id: "maintain", label: "Maintain" },
] as const satisfies ReadonlyArray<{
  id: RouterSecondaryTab;
  label: string;
}>;

export const disabledRouterTabReasons: Record<DisabledRouterTab, string> = {
  "other-settings":
    "Раздел оставлен для совместимости с ментальной моделью PassWall, но в Vectra Stable V1 ещё не реализован.",
  "access-control":
    "Access Control появится только после отдельной реализации backend-покрытия и операторских действий.",
  "server-side":
    "Server-Side не поддерживается в Vectra Stable V1 и пока отображается только как ориентир для знакомой структуры.",
};

type RouterPrimaryTabItem = (typeof routerPrimaryTabs)[number];

const enabledRouterPrimaryTabs = routerPrimaryTabs
  .filter(
    (tab): tab is RouterPrimaryTabItem & { id: RouterPrimaryTab } =>
      !("disabled" in tab && tab.disabled),
  )
  .map((tab) => tab.id) as readonly RouterPrimaryTab[];

const allowedSecondaryTabs = new Set<RouterSecondaryTab>(
  basicSettingsSecondaryTabs.map((tab) => tab.id),
);

export function normalizeOperatorShellTab(value: string | null | undefined) {
  return operatorShellTabs.some((tab) => tab.id === value)
    ? (value as OperatorShellTab)
    : "fleet";
}

export function getOperatorShellSectionForPath(
  pathname: string | null | undefined,
): OperatorShellSection {
  if (!pathname) {
    return operatorShellTabs[0];
  }

  if (pathname.startsWith("/routers/")) {
    return operatorShellTabs[0];
  }

  return (
    operatorShellTabs.find(
      (tab) => pathname === tab.href || pathname.startsWith(`${tab.href}/`),
    ) ?? operatorShellTabs[0]
  );
}

export function buildFleetSearchHref(query: string) {
  const trimmed = query.trim();
  if (!trimmed) {
    return "/fleet";
  }

  const params = new URLSearchParams({ q: trimmed });
  return `/fleet?${params.toString()}`;
}

export function resolveOperatorCommand(query: string) {
  const normalized = query.trim().toLowerCase();
  if (!normalized) {
    return "/fleet";
  }

  const directHref = operatorShellTabs.find(
    (tab) =>
      normalized === tab.href ||
      normalized === tab.label.toLowerCase() ||
      normalized === tab.id ||
      tab.keywords.some((keyword) => keyword === normalized),
  );

  if (directHref) {
    return directHref.href;
  }

  if (normalized.startsWith("/")) {
    const withoutSlash = normalized.slice(1);
    const slashMatch = operatorShellTabs.find(
      (tab) =>
        tab.id === withoutSlash ||
        tab.keywords.some((keyword) => keyword === withoutSlash),
    );
    if (slashMatch) {
      return slashMatch.href;
    }
  }

  return null;
}

export function normalizeRouterPrimaryTab(
  value: string | null | undefined,
): RouterPrimaryTab {
  return enabledRouterPrimaryTabs.includes(value as RouterPrimaryTab)
    ? (value as RouterPrimaryTab)
    : "basic-settings";
}

export function normalizeRouterSecondaryTab(
  primaryTab: RouterPrimaryTab,
  value: string | null | undefined,
): RouterSecondaryTab | null {
  if (primaryTab !== "basic-settings") {
    return null;
  }

  return allowedSecondaryTabs.has(value as RouterSecondaryTab)
    ? (value as RouterSecondaryTab)
    : "main";
}

export function normalizeRouterConsoleSelection(
  primaryValue: string | null | undefined,
  secondaryValue: string | null | undefined,
): RouterConsoleSelection {
  const primaryTab = normalizeRouterPrimaryTab(primaryValue);

  return {
    primaryTab,
    secondaryTab: normalizeRouterSecondaryTab(primaryTab, secondaryValue),
  };
}

export function buildRouterConsoleQuery({
  existing,
  primaryTab,
  secondaryTab,
}: {
  existing: URLSearchParams;
  primaryTab: RouterPrimaryTab;
  secondaryTab?: RouterSecondaryTab | null;
}) {
  const params = new URLSearchParams(existing.toString());
  params.set("tab", primaryTab);

  if (primaryTab === "basic-settings" && secondaryTab) {
    params.set("section", secondaryTab);
  } else {
    params.delete("section");
  }

  return params;
}

export function describeDisabledTabs() {
  return routerPrimaryTabs
    .filter(
      (
        tab,
      ): tab is RouterPrimaryTabItem & {
        id: DisabledRouterTab;
        disabled: true;
      } => "disabled" in tab && tab.disabled,
    )
    .map((tab) => `${tab.label}: ${disabledRouterTabReasons[tab.id]}`);
}
