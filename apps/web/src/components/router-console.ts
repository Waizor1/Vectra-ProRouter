export type OperatorShellTab =
  | "fleet"
  | "drafts"
  | "updates"
  | "rescue"
  | "enrollment";

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

export const operatorShellTabs = [
  { id: "fleet", label: "Парк", href: "/fleet" },
  { id: "drafts", label: "Черновики", href: "/drafts" },
  { id: "updates", label: "Обновления", href: "/updates" },
  { id: "rescue", label: "Восстановление", href: "/rescue" },
  { id: "enrollment", label: "Установка", href: "/enrollment" },
] as const satisfies ReadonlyArray<{
  id: OperatorShellTab;
  label: string;
  href: string;
}>;

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
