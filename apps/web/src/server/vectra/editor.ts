import {
  MASKED_SECRET_PLACEHOLDER,
  summarizePasswallRevisionDiff,
  type PasswallDesiredConfig,
  type SupportState,
} from "@vectra/contracts";

import type { ConfigSourceMode } from "./config-trust";

type FieldKind =
  | "boolean"
  | "string"
  | "number"
  | "list"
  | "enum"
  | "secret"
  | "multiline";

export type EditorFieldMeta = {
  path: string;
  section: string;
  label: string;
  kind: FieldKind;
  secret?: boolean;
  expert?: boolean;
};

export type EditorFieldDiff = {
  path: string;
  section: string;
  label: string;
  kind: FieldKind;
  currentValue: unknown;
  authoritativeValue: unknown;
  draftValue: unknown;
  source:
    | "live-import"
    | "authoritative"
    | "stale-authoritative"
    | "inventory-only"
    | "masked";
  currentMatchesAuthoritative: boolean;
  draftChanged: boolean;
};

export type EditorOperation = {
  key: string;
  label: string;
  description: string;
  restartRequired: boolean;
  details: string[];
};

export type EditorSurfaceData = {
  routerRuntimeSummary: {
    status: string;
    importState: string;
    lastSeenAt: Date | null;
    passwallEnabled: boolean;
    selectedNodeId: string | null;
    selectedNodeLabel: string | null;
    pendingChanges: number;
    supportState: SupportState;
    supportTitle: string;
    supportReason: string;
    updateActionsAllowed?: boolean;
  };
  currentLiveConfig: PasswallDesiredConfig;
  authoritativeConfig: PasswallDesiredConfig | null;
  draftConfig: PasswallDesiredConfig;
  fieldMeta: EditorFieldMeta[];
  fieldDiffs: EditorFieldDiff[];
  operationPreview: {
    changedSections: string[];
    requiresRestart: boolean;
    refreshSubscriptions: boolean;
    refreshRules: boolean;
    packageInstall: boolean;
    firmwareValidation: boolean;
    operations: EditorOperation[];
  };
  maskedFields: string[];
  currentConfigFreshness: "live" | "stale";
};

type BuildEditorSurfaceArgs = {
  routerRuntimeSummary: EditorSurfaceData["routerRuntimeSummary"];
  currentLiveConfig: PasswallDesiredConfig;
  authoritativeConfig: PasswallDesiredConfig | null;
  draftConfig: PasswallDesiredConfig;
  currentConfigFreshness: "live" | "stale";
  configSourceMode: ConfigSourceMode;
};

type FieldRegistration = {
  meta: EditorFieldMeta;
  currentValue: unknown;
  authoritativeValue: unknown;
  draftValue: unknown;
};

export function buildEditorSurface(
  args: BuildEditorSurfaceArgs
): EditorSurfaceData {
  const fields: FieldRegistration[] = [];
  const maskedFields = new Set<string>();

  const registerField = (
    meta: EditorFieldMeta,
    currentValue: unknown,
    authoritativeValue: unknown,
    draftValue: unknown
  ) => {
    if (
      currentValue === undefined &&
      authoritativeValue === undefined &&
      draftValue === undefined
    ) {
      return;
    }

    if (
      currentValue === MASKED_SECRET_PLACEHOLDER ||
      authoritativeValue === MASKED_SECRET_PLACEHOLDER ||
      draftValue === MASKED_SECRET_PLACEHOLDER
    ) {
      maskedFields.add(meta.path);
    }

    fields.push({
      meta,
      currentValue,
      authoritativeValue,
      draftValue,
    });
  };

  registerScalarFields(args, registerField);
  registerDynamicFields(args, registerField);

  const fieldMeta = fields.map((field) => field.meta);
  const fieldDiffs = fields
    .map((field) => toFieldDiff(field, args.configSourceMode))
    .filter((field) => field.currentValue !== undefined || field.draftChanged);

  const coarse = summarizePasswallRevisionDiff(
    args.authoritativeConfig,
    args.draftConfig
  );
  const operations = buildOperationsFromDiffs(fieldDiffs, coarse);

  return {
    ...args,
    fieldMeta,
    fieldDiffs,
    maskedFields: [...maskedFields].sort(),
    operationPreview: {
      ...coarse,
      operations,
    },
  };
}

export function buildDraftPreview(
  previous: PasswallDesiredConfig | null,
  next: PasswallDesiredConfig
) {
  const surface = buildEditorSurface({
    routerRuntimeSummary: {
      status: "unknown",
      importState: "approved",
      lastSeenAt: null,
      passwallEnabled: next.basicSettings.main.mainSwitch,
      selectedNodeId: next.basicSettings.main.selectedNodeId ?? null,
      selectedNodeLabel: next.basicSettings.main.selectedNodeId ?? null,
      pendingChanges: 0,
      supportState: "certified",
      supportTitle: "Сертифицировано",
      supportReason: "Локальный preview не зависит от support-state.",
    },
    currentLiveConfig: previous ?? next,
    authoritativeConfig: previous,
    draftConfig: next,
    currentConfigFreshness: previous ? "live" : "stale",
    configSourceMode: previous ? "authoritative" : "inventory-only",
  });

  return {
    fieldDiffs: surface.fieldDiffs.filter((field) => field.draftChanged),
    operationPreview: surface.operationPreview,
    maskedFields: surface.maskedFields,
  };
}

function registerScalarFields(
  args: BuildEditorSurfaceArgs,
  registerField: (
    meta: EditorFieldMeta,
    currentValue: unknown,
    authoritativeValue: unknown,
    draftValue: unknown
  ) => void
) {
  const defs: EditorFieldMeta[] = [
    {
      path: "basicSettings.main.mainSwitch",
      section: "Основные настройки",
      label: "Главный переключатель",
      kind: "boolean",
    },
    {
      path: "basicSettings.main.selectedNodeId",
      section: "Основные настройки",
      label: "Выбранная нода",
      kind: "string",
    },
    {
      path: "basicSettings.main.localhostProxy",
      section: "Основные настройки",
      label: "Проксировать localhost",
      kind: "boolean",
    },
    {
      path: "basicSettings.main.clientProxy",
      section: "Основные настройки",
      label: "Проксировать клиентский трафик",
      kind: "boolean",
    },
    {
      path: "basicSettings.main.nodeSocksPort",
      section: "Основные настройки",
      label: "SOCKS-порт основной ноды",
      kind: "number",
    },
    {
      path: "basicSettings.main.nodeSocksBindLocal",
      section: "Основные настройки",
      label: "SOCKS слушает только localhost",
      kind: "boolean",
    },
    {
      path: "basicSettings.main.socksMainSwitch",
      section: "Основные настройки",
      label: "Главный переключатель SOCKS",
      kind: "boolean",
    },
    {
      path: "basicSettings.dns.directQueryStrategy",
      section: "DNS",
      label: "Стратегия прямого DNS",
      kind: "enum",
    },
    {
      path: "basicSettings.dns.remoteDnsProtocol",
      section: "DNS",
      label: "Протокол удалённого DNS",
      kind: "enum",
    },
    {
      path: "basicSettings.dns.remoteDns",
      section: "DNS",
      label: "Удалённый DNS",
      kind: "string",
    },
    {
      path: "basicSettings.dns.remoteDnsDoh",
      section: "DNS",
      label: "DoH URL",
      kind: "string",
    },
    {
      path: "basicSettings.dns.remoteDnsClientIp",
      section: "DNS",
      label: "EDNS Client IP",
      kind: "string",
    },
    {
      path: "basicSettings.dns.remoteDnsDetour",
      section: "DNS",
      label: "Маршрут удалённого DNS",
      kind: "enum",
    },
    {
      path: "basicSettings.dns.remoteFakeDns",
      section: "DNS",
      label: "Использовать FakeDNS",
      kind: "boolean",
    },
    {
      path: "basicSettings.dns.remoteDnsQueryStrategy",
      section: "DNS",
      label: "Стратегия удалённого DNS",
      kind: "enum",
    },
    {
      path: "basicSettings.dns.dnsHosts",
      section: "DNS",
      label: "Статические DNS hosts",
      kind: "multiline",
    },
    {
      path: "basicSettings.dns.dnsRedirect",
      section: "DNS",
      label: "Перехватывать DNS",
      kind: "boolean",
    },
    {
      path: "basicSettings.log.enableNodeLog",
      section: "Журнал",
      label: "Лог ноды",
      kind: "boolean",
    },
    {
      path: "basicSettings.log.level",
      section: "Журнал",
      label: "Уровень логирования",
      kind: "enum",
    },
    {
      path: "basicSettings.maintenance.backupPaths",
      section: "Резервирование",
      label: "Пути резервного копирования",
      kind: "multiline",
    },
    {
      path: "subscriptions.filterKeywordMode",
      section: "Подписки",
      label: "Режим фильтрации",
      kind: "enum",
    },
    {
      path: "subscriptions.discardList",
      section: "Подписки",
      label: "Список исключений",
      kind: "multiline",
    },
    {
      path: "subscriptions.keepList",
      section: "Подписки",
      label: "Список сохранения",
      kind: "multiline",
    },
    {
      path: "subscriptions.domainStrategy",
      section: "Подписки",
      label: "Стратегия доменов",
      kind: "enum",
    },
    {
      path: "appUpdate.binaryPaths.xray",
      section: "Обновление приложений",
      label: "Путь к Xray",
      kind: "string",
    },
    {
      path: "appUpdate.binaryPaths.singBox",
      section: "Обновление приложений",
      label: "Путь к sing-box",
      kind: "string",
    },
    {
      path: "appUpdate.binaryPaths.hysteria",
      section: "Обновление приложений",
      label: "Путь к Hysteria",
      kind: "string",
    },
    {
      path: "appUpdate.binaryPaths.geoview",
      section: "Обновление приложений",
      label: "Путь к Geoview",
      kind: "string",
    },
    {
      path: "appUpdate.updateStrategy",
      section: "Обновление приложений",
      label: "Стратегия обновления",
      kind: "enum",
    },
    {
      path: "appUpdate.targetVersions.appVersion",
      section: "Обновление приложений",
      label: "Целевая версия PassWall2",
      kind: "string",
    },
    {
      path: "appUpdate.targetVersions.xray",
      section: "Обновление приложений",
      label: "Целевая версия Xray",
      kind: "string",
    },
    {
      path: "appUpdate.targetVersions.singBox",
      section: "Обновление приложений",
      label: "Целевая версия sing-box",
      kind: "string",
    },
    {
      path: "appUpdate.targetVersions.hysteria",
      section: "Обновление приложений",
      label: "Целевая версия Hysteria",
      kind: "string",
    },
    {
      path: "appUpdate.targetVersions.geoview",
      section: "Обновление приложений",
      label: "Целевая версия Geoview",
      kind: "string",
    },
    {
      path: "ruleManage.geoipUrl",
      section: "Управление правилами",
      label: "URL GeoIP",
      kind: "string",
    },
    {
      path: "ruleManage.geositeUrl",
      section: "Управление правилами",
      label: "URL GeoSite",
      kind: "string",
    },
    {
      path: "ruleManage.assetDirectory",
      section: "Управление правилами",
      label: "Каталог ассетов",
      kind: "string",
    },
    {
      path: "ruleManage.autoUpdate",
      section: "Управление правилами",
      label: "Автообновление правил",
      kind: "boolean",
    },
    {
      path: "ruleManage.scheduleMode",
      section: "Управление правилами",
      label: "Режим расписания",
      kind: "enum",
    },
    {
      path: "ruleManage.scheduleDay",
      section: "Управление правилами",
      label: "День расписания",
      kind: "number",
    },
    {
      path: "ruleManage.scheduleHour",
      section: "Управление правилами",
      label: "Час расписания",
      kind: "number",
    },
    {
      path: "ruleManage.intervalHours",
      section: "Управление правилами",
      label: "Интервал часов",
      kind: "number",
    },
    {
      path: "ruleManage.enabledAssets",
      section: "Управление правилами",
      label: "Включённые ассеты",
      kind: "list",
    },
  ];

  for (const def of defs) {
    registerField(
      def,
      getPathValue(args.currentLiveConfig, def.path),
      getPathValue(args.authoritativeConfig, def.path),
      getPathValue(args.draftConfig, def.path)
    );
  }
}

function registerDynamicFields(
  args: BuildEditorSurfaceArgs,
  registerField: (
    meta: EditorFieldMeta,
    currentValue: unknown,
    authoritativeValue: unknown,
    draftValue: unknown
  ) => void
) {
  registerArrayFields({
    section: "SOCKS",
    currentItems: args.currentLiveConfig.basicSettings.socks,
    authoritativeItems: args.authoritativeConfig?.basicSettings.socks ?? [],
    draftItems: args.draftConfig.basicSettings.socks,
    fields: [
      { key: "enabled", label: "Включено", kind: "boolean" },
      { key: "nodeId", label: "Нода", kind: "string" },
      { key: "port", label: "SOCKS-порт", kind: "number" },
      { key: "httpPort", label: "HTTP-порт", kind: "number" },
      { key: "bindLocal", label: "Только localhost", kind: "boolean" },
      {
        key: "autoswitchBackupNodeIds",
        label: "Резервные ноды",
        kind: "list",
      },
    ],
    registerField,
  });

  registerArrayFields({
    section: "Shunt",
    currentItems: args.currentLiveConfig.basicSettings.shuntRules,
    authoritativeItems: args.authoritativeConfig?.basicSettings.shuntRules ?? [],
    draftItems: args.draftConfig.basicSettings.shuntRules,
    fields: [
      { key: "label", label: "Название правила", kind: "string" },
      { key: "outboundNodeId", label: "Исходящая нода", kind: "string" },
      { key: "domainRules", label: "Доменные правила", kind: "multiline" },
      { key: "ipRules", label: "IP-правила", kind: "multiline" },
    ],
    registerField,
    itemLabel: (item) => item.label || item.id,
  });

  registerArrayFields({
    section: "Ноды",
    currentItems: args.currentLiveConfig.nodes,
    authoritativeItems: args.authoritativeConfig?.nodes ?? [],
    draftItems: args.draftConfig.nodes,
    fields: [
      { key: "label", label: "Название ноды", kind: "string" },
      { key: "protocol", label: "Протокол", kind: "enum" },
      { key: "enabled", label: "Включено", kind: "boolean" },
      { key: "group", label: "Группа", kind: "string" },
      { key: "address", label: "Адрес", kind: "string" },
      { key: "port", label: "Порт", kind: "number" },
      { key: "username", label: "Логин", kind: "secret", secret: true },
      { key: "password", label: "Пароль", kind: "secret", secret: true },
      { key: "transport", label: "Транспорт", kind: "enum" },
      { key: "tls", label: "TLS", kind: "boolean" },
      { key: "tags", label: "Теги", kind: "list" },
    ],
    registerField,
    itemLabel: (item) => item.label || item.id,
  });

  registerArrayFields({
    section: "Подписки",
    currentItems: args.currentLiveConfig.subscriptions.items,
    authoritativeItems: args.authoritativeConfig?.subscriptions.items ?? [],
    draftItems: args.draftConfig.subscriptions.items,
    fields: [
      { key: "remark", label: "Название подписки", kind: "string" },
      { key: "url", label: "URL подписки", kind: "secret", secret: true },
      { key: "enabled", label: "Включено", kind: "boolean" },
      { key: "addMode", label: "Режим добавления", kind: "enum" },
      {
        key: "metadata.remainingTraffic",
        label: "Остаток трафика",
        kind: "string",
      },
      { key: "metadata.expiresAt", label: "Дата окончания", kind: "string" },
    ],
    registerField,
    itemLabel: (item) => item.remark || item.id,
  });

  registerArrayFields({
    section: "Управление правилами",
    currentItems: args.currentLiveConfig.ruleManage.shuntRules,
    authoritativeItems: args.authoritativeConfig?.ruleManage.shuntRules ?? [],
    draftItems: args.draftConfig.ruleManage.shuntRules,
    fields: [
      { key: "label", label: "Название правила", kind: "string" },
      { key: "extras.protocol", label: "Protocol", kind: "list" },
      { key: "extras.inbound", label: "Inbound Tag", kind: "list" },
      { key: "extras.network", label: "Network", kind: "enum" },
      { key: "extras.source", label: "Source", kind: "string" },
      { key: "extras.port", label: "Port", kind: "string" },
      { key: "domainRules", label: "Доменные правила", kind: "multiline" },
      { key: "ipRules", label: "IP-правила", kind: "multiline" },
      { key: "extras.invert", label: "Invert", kind: "boolean" },
    ],
    registerField,
    itemLabel: (item) => item.label || item.id,
  });
}

function registerArrayFields<T extends { id: string }>(args: {
  section: string;
  currentItems: T[];
  authoritativeItems: T[];
  draftItems: T[];
  fields: Array<{
    key: string;
    label: string;
    kind: FieldKind;
    secret?: boolean;
  }>;
  registerField: (
    meta: EditorFieldMeta,
    currentValue: unknown,
    authoritativeValue: unknown,
    draftValue: unknown
  ) => void;
  itemLabel?: (item: T) => string;
}) {
  const byId = (items: T[]) =>
    new Map(items.map((item) => [item.id, item] as const));

  const currentById = byId(args.currentItems);
  const authoritativeById = byId(args.authoritativeItems);
  const draftById = byId(args.draftItems);
  const ids = new Set<string>([
    ...currentById.keys(),
    ...authoritativeById.keys(),
    ...draftById.keys(),
  ]);

  for (const id of [...ids].sort()) {
    const currentItem = currentById.get(id);
    const authoritativeItem = authoritativeById.get(id);
    const draftItem = draftById.get(id);
    const itemName =
      args.itemLabel?.(draftItem ?? currentItem ?? authoritativeItem ?? ({ id } as T)) ??
      id;

    for (const field of args.fields) {
      args.registerField(
        {
          path: `${args.section}[${id}].${field.key}`,
          section: args.section,
          label: `${itemName}: ${field.label}`,
          kind: field.kind,
          secret: field.secret,
        },
        getNestedValue(currentItem, field.key),
        getNestedValue(authoritativeItem, field.key),
        getNestedValue(draftItem, field.key)
      );
    }
  }
}

function toFieldDiff(
  field: FieldRegistration,
  configSourceMode: ConfigSourceMode
): EditorFieldDiff {
  const currentMatchesAuthoritative = isEqual(
    field.currentValue,
    field.authoritativeValue
  );
  const draftChanged = !isEqual(field.draftValue, field.authoritativeValue);

  const source =
    field.meta.secret &&
    (field.currentValue === MASKED_SECRET_PLACEHOLDER ||
      field.authoritativeValue === MASKED_SECRET_PLACEHOLDER ||
      field.draftValue === MASKED_SECRET_PLACEHOLDER)
      ? "masked"
      : configSourceMode === "inventory-only"
        ? "inventory-only"
        : configSourceMode === "live-import"
          ? "live-import"
          : configSourceMode === "stale-authoritative"
            ? "stale-authoritative"
            : "authoritative";

  return {
    path: field.meta.path,
    section: field.meta.section,
    label: field.meta.label,
    kind: field.meta.kind,
    currentValue: field.currentValue,
    authoritativeValue: field.authoritativeValue,
    draftValue: field.draftValue,
    source,
    currentMatchesAuthoritative,
    draftChanged,
  };
}

function buildOperationsFromDiffs(
  fieldDiffs: EditorFieldDiff[],
  coarse: ReturnType<typeof summarizePasswallRevisionDiff>
): EditorOperation[] {
  const changed = fieldDiffs.filter((field) => field.draftChanged);
  const bySection = new Map<string, string[]>();

  for (const field of changed) {
    bySection.set(field.section, [
      ...(bySection.get(field.section) ?? []),
      field.label,
    ]);
  }

  const operations: EditorOperation[] = [];
  for (const [section, details] of bySection) {
    operations.push({
      key: section,
      label: operationLabel(section),
      description: operationDescription(section),
      restartRequired: ["Основные настройки", "DNS", "Журнал", "Резервирование", "SOCKS", "Ноды", "Подписки", "Управление правилами", "Shunt"].includes(
        section
      ),
      details: unique(details),
    });
  }

  if (coarse.refreshSubscriptions) {
    operations.push({
      key: "refresh-subscriptions",
      label: "Обновление подписок",
      description:
        "После записи конфигурации агент запустит refresh подписок через PassWall2 subscribe.lua.",
      restartRequired: false,
      details: ["Синхронизация импортированных подписок и групп."],
    });
  }

  if (coarse.refreshRules) {
    operations.push({
      key: "refresh-rules",
      label: "Обновление правил",
      description:
        "После записи конфигурации агент обновит geo-ассеты через PassWall2 rule_update.lua.",
      restartRequired: false,
      details: ["Обновление GeoIP и GeoSite по заданным URL."],
    });
  }

  if (coarse.packageInstall) {
    operations.push({
      key: "package-update",
      label: "Пакетное обновление",
      description:
        "Конфигурация затрагивает lane обновлений. Пакеты и версии будут зафиксированы отдельным job-контрактом.",
      restartRequired: false,
      details: ["Проверка целевых версий PassWall2 и компонентов."],
    });
  }

  if (coarse.requiresRestart) {
    operations.push({
      key: "restart-passwall",
      label: "Перезапуск PassWall2",
      description:
        "После применения управляемых UCI-секций агент перезапустит сервис PassWall2.",
      restartRequired: true,
      details: ["Нужно для вступления в силу новых настроек маршрутизации."],
    });
  }

  return operations;
}

function operationLabel(section: string) {
  return `Применение секции ${section}`;
}

function operationDescription(section: string) {
  switch (section) {
    case "Основные настройки":
    case "DNS":
    case "Журнал":
    case "Резервирование":
      return "Секция будет записана в управляемые UCI-поля PassWall2.";
    case "SOCKS":
      return "Список SOCKS-профилей будет синхронизирован как управляемые UCI-секции.";
    case "Ноды":
      return "Список нод будет пересобран в deterministic managed-section режиме.";
    case "Подписки":
      return "Параметры подписок и элементы import-листа будут записаны в UCI.";
    case "Обновление приложений":
      return "Будут обновлены пути бинарей и целевые package/runtime параметры.";
    case "Управление правилами":
    case "Shunt":
      return "Будут обновлены URL правил, расписание и shunt-параметры.";
    default:
      return "Изменённая секция будет приведена к desired state.";
  }
}

function getPathValue(config: PasswallDesiredConfig | null, path: string) {
  return getNestedValue(config, path);
}

function getNestedValue(target: unknown, path: string) {
  if (!target) {
    return undefined;
  }

  const parts = path.split(".");
  let current: unknown = target;
  for (const part of parts) {
    if (current === null || current === undefined) {
      return undefined;
    }
    if (Array.isArray(current)) {
      return undefined;
    }
    current = (current as Record<string, unknown>)[part];
  }

  return current;
}

function isEqual(left: unknown, right: unknown) {
  return JSON.stringify(left ?? null) === JSON.stringify(right ?? null);
}

function unique(values: string[]) {
  return [...new Set(values)];
}
