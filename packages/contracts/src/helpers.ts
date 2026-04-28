import { z } from "zod";

import {
  type PasswallDesiredConfig,
  type RescuePolicy,
  type UpdatePolicy,
  rescueEvaluationInputSchema,
  rescueEvaluationResultSchema,
  rescuePolicySchema,
  updatePolicySchema,
} from "./schemas";

export type PasswallFieldDiff = {
  section: keyof PasswallDesiredConfig;
  path: string;
  previousValue: unknown;
  nextValue: unknown;
  changeType: "added" | "removed" | "changed";
};

export type PasswallOperationPreview = {
  kind: string;
  section: keyof PasswallDesiredConfig | "runtime";
  description: string;
  restartRequired: boolean;
  uciCommands: string[];
  commands: string[];
};

const TOP_LEVEL_SECTIONS = [
  "basicSettings",
  "nodes",
  "subscriptions",
  "appUpdate",
  "ruleManage",
] as const satisfies ReadonlyArray<keyof PasswallDesiredConfig>;

const FIELD_LABELS: Record<string, string> = {
  "basicSettings.main.mainSwitch": "Основной переключатель",
  "basicSettings.main.selectedNodeId": "Выбранный узел",
  "basicSettings.main.localhostProxy": "Прокси для localhost",
  "basicSettings.main.clientProxy": "Клиентский прокси",
  "basicSettings.main.nodeSocksPort": "SOCKS-порт основного узла",
  "basicSettings.main.nodeSocksBindLocal": "SOCKS слушает только localhost",
  "basicSettings.main.socksMainSwitch": "Главный переключатель SOCKS",
  "basicSettings.dns.directQueryStrategy": "Стратегия direct DNS",
  "basicSettings.dns.remoteDnsProtocol": "Протокол удалённого DNS",
  "basicSettings.dns.remoteDns": "Удалённый DNS",
  "basicSettings.dns.remoteDnsDoh": "DoH URL",
  "basicSettings.dns.remoteDnsClientIp": "Client IP для удалённого DNS",
  "basicSettings.dns.remoteDnsDetour": "Маршрут удалённого DNS",
  "basicSettings.dns.remoteFakeDns": "FakeDNS",
  "basicSettings.dns.remoteDnsQueryStrategy":
    "Стратегия запросов удалённого DNS",
  "basicSettings.dns.dnsHosts": "Локальные DNS-host записи",
  "basicSettings.dns.dnsRedirect": "Перенаправление DNS",
  "basicSettings.log.enableNodeLog": "Логи узлов",
  "basicSettings.log.level": "Уровень логирования",
  "basicSettings.maintenance.backupPaths": "Пути резервного копирования",
  "subscriptions.filterKeywordMode": "Режим фильтрации подписок",
  "subscriptions.discardList": "Список исключений",
  "subscriptions.keepList": "Список сохранения",
  "subscriptions.domainStrategy": "Стратегия доменов",
  "appUpdate.updateStrategy": "Стратегия обновления",
  "appUpdate.binaryPaths.xray": "Путь к Xray",
  "appUpdate.binaryPaths.singBox": "Путь к sing-box",
  "appUpdate.binaryPaths.hysteria": "Путь к Hysteria",
  "appUpdate.binaryPaths.geoview": "Путь к Geoview",
  "ruleManage.geoipUrl": "URL GeoIP",
  "ruleManage.geositeUrl": "URL GeoSite",
  "ruleManage.assetDirectory": "Каталог ассетов",
  "ruleManage.autoUpdate": "Автообновление правил",
  "ruleManage.scheduleMode": "Режим расписания",
  "ruleManage.scheduleDay": "День расписания",
  "ruleManage.scheduleHour": "Час расписания",
  "ruleManage.intervalHours": "Интервал часов",
  "ruleManage.enabledAssets": "Включённые rule assets",
};

export function createDefaultRescuePolicy(
  overrides: Partial<RescuePolicy> = {},
): RescuePolicy {
  return rescuePolicySchema.parse(overrides);
}

export function createDefaultUpdatePolicy(
  overrides: Partial<UpdatePolicy> = {},
): UpdatePolicy {
  return updatePolicySchema.parse(overrides);
}

function stableValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((entry) => stableValue(entry));
  }

  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, stableValue(entry)]),
    );
  }

  return value;
}

function sameValue(left: unknown, right: unknown) {
  return (
    JSON.stringify(stableValue(left)) === JSON.stringify(stableValue(right))
  );
}

function normalizeDiffText(value: unknown) {
  return typeof value === "string" && value.trim().length > 0
    ? value.trim()
    : null;
}

function normalizeDiffLowerText(value: unknown) {
  return normalizeDiffText(value)?.toLowerCase() ?? null;
}

function normalizeDiffNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function normalizeDiffBoolean(value: unknown) {
  return typeof value === "boolean" ? value : null;
}

function normalizeDiffExtraValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value
      .map((entry) => (typeof entry === "string" ? entry.trim() : entry))
      .filter((entry) => entry !== "");
  }

  if (typeof value === "string") {
    return value.trim();
  }

  if (
    typeof value === "number" ||
    typeof value === "boolean" ||
    value === null
  ) {
    return value;
  }

  return undefined;
}

function normalizeDiffExtras(
  extras: Record<string, unknown> | undefined,
  ignoredKeys: string[] = [],
) {
  const ignored = new Set(ignoredKeys);
  return Object.fromEntries(
    Object.entries(extras ?? {})
      .filter(([key]) => !ignored.has(key))
      .map(([key, value]) => [key, normalizeDiffExtraValue(value)] as const)
      .filter(([, value]) => value !== undefined)
      .sort(([left], [right]) => left.localeCompare(right)),
  );
}

function isManagedSubscriptionNodeForDiff(
  node: PasswallDesiredConfig["nodes"][number],
) {
  const extras = node.extras ?? {};
  return (
    extras.add_mode === "2" ||
    extras.add_mode === 2 ||
    extras.addMode === "2" ||
    extras.addMode === 2
  );
}

function buildManagedNodeDiffIdentity(
  node: PasswallDesiredConfig["nodes"][number],
) {
  return JSON.stringify(
    stableValue({
      label: normalizeDiffText(node.label),
      protocol: normalizeDiffLowerText(node.protocol),
      address: normalizeDiffLowerText(node.address),
      port: normalizeDiffNumber(node.port),
      username: normalizeDiffText(node.username),
      password: normalizeDiffText(node.password),
      transport: normalizeDiffLowerText(node.transport),
      tls: normalizeDiffBoolean(node.tls),
      extras: normalizeDiffExtras(node.extras, ["add_mode", "group"]),
    }),
  );
}

function canonicalizeArrayById<T extends { id: string }>(items: T[]) {
  return [...items].sort((left, right) => left.id.localeCompare(right.id));
}

function canonicalizeNodesForDiff(nodes: PasswallDesiredConfig["nodes"]) {
  return nodes
    .map((node, index) => {
      const managed = isManagedSubscriptionNodeForDiff(node);
      const comparableNode = managed
        ? Object.fromEntries(
            Object.entries(node).filter(([key]) => key !== "id"),
          )
        : node;

      return {
        identity: managed
          ? `managed:${buildManagedNodeDiffIdentity(node)}`
          : `manual:${node.id}`,
        index,
        value: stableValue(comparableNode),
      };
    })
    .sort(
      (left, right) =>
        left.identity.localeCompare(right.identity) ||
        JSON.stringify(left.value).localeCompare(JSON.stringify(right.value)) ||
        left.index - right.index,
    )
    .map((entry) => entry.value);
}

function buildSubscriptionDiffIdentity(
  item: PasswallDesiredConfig["subscriptions"]["items"][number],
) {
  return `${normalizeDiffLowerText(item.remark) ?? "subscription"}::${
    normalizeDiffText(item.url) ?? ""
  }`;
}

function canonicalizeSubscriptionsForDiff(
  items: PasswallDesiredConfig["subscriptions"]["items"],
) {
  return items
    .map((item, index) => ({
      identity: buildSubscriptionDiffIdentity(item),
      index,
      value: stableValue(
        Object.fromEntries(
          Object.entries(item).filter(([key]) => key !== "id"),
        ),
      ),
    }))
    .sort(
      (left, right) =>
        left.identity.localeCompare(right.identity) ||
        JSON.stringify(left.value).localeCompare(JSON.stringify(right.value)) ||
        left.index - right.index,
    )
    .map((entry) => entry.value);
}

function canonicalizePasswallForDiff(
  config: PasswallDesiredConfig | null | undefined,
): unknown {
  if (!config) {
    return null;
  }

  return {
    ...config,
    basicSettings: {
      ...config.basicSettings,
      socks: canonicalizeArrayById(config.basicSettings.socks),
      shuntRules: canonicalizeArrayById(config.basicSettings.shuntRules),
    },
    nodes: canonicalizeNodesForDiff(config.nodes),
    subscriptions: {
      ...config.subscriptions,
      items: canonicalizeSubscriptionsForDiff(config.subscriptions.items),
    },
    ruleManage: {
      ...config.ruleManage,
      shuntRules: canonicalizeArrayById(config.ruleManage.shuntRules),
    },
  };
}

function collectLeafPaths(value: unknown, prefix = ""): string[] {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return prefix ? [prefix] : [];
    }

    return value.flatMap((entry, index) =>
      collectLeafPaths(entry, `${prefix}[${index}]`),
    );
  }

  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0) {
      return prefix ? [prefix] : [];
    }

    return entries.flatMap(([key, entry]) =>
      collectLeafPaths(entry, prefix ? `${prefix}.${key}` : key),
    );
  }

  return prefix ? [prefix] : [];
}

function isTopLevelSection(
  value: string | undefined,
): value is (typeof TOP_LEVEL_SECTIONS)[number] {
  return TOP_LEVEL_SECTIONS.some((section) => section === value);
}

function sectionFromPath(path: string): keyof PasswallDesiredConfig {
  const [root] = path.split(/[.[\]]/).filter(Boolean);
  return isTopLevelSection(root) ? root : "basicSettings";
}

function pathMatchesPrefix(path: string, prefix: string) {
  return (
    path === prefix ||
    path.startsWith(`${prefix}.`) ||
    path.startsWith(`${prefix}[`)
  );
}

function hasDiffPath(
  fieldDiffs: PasswallFieldDiff[] | null,
  predicate: (path: string) => boolean,
) {
  return fieldDiffs === null || fieldDiffs.some((diff) => predicate(diff.path));
}

function changedSectionsFromDiffs(fieldDiffs: PasswallFieldDiff[]) {
  const changed = new Set(fieldDiffs.map((diff) => diff.section));
  return TOP_LEVEL_SECTIONS.filter((section) => changed.has(section));
}

function touchesGlobalApplyPath(path: string) {
  return (
    pathMatchesPrefix(path, "basicSettings.main") ||
    pathMatchesPrefix(path, "basicSettings.dns") ||
    pathMatchesPrefix(path, "basicSettings.log") ||
    pathMatchesPrefix(path, "basicSettings.maintenance") ||
    pathMatchesPrefix(path, "appUpdate.binaryPaths") ||
    pathMatchesPrefix(path, "appUpdate.updateStrategy") ||
    (pathMatchesPrefix(path, "ruleManage") &&
      !pathMatchesPrefix(path, "ruleManage.shuntRules"))
  );
}

function touchesNodeSyncPath(path: string) {
  return (
    pathMatchesPrefix(path, "nodes") ||
    pathMatchesPrefix(path, "basicSettings.socks") ||
    pathMatchesPrefix(path, "basicSettings.shuntRules") ||
    pathMatchesPrefix(path, "ruleManage.shuntRules")
  );
}

function touchesSubscriptionPath(path: string) {
  return pathMatchesPrefix(path, "subscriptions");
}

function touchesRuleRefreshPath(path: string) {
  return (
    pathMatchesPrefix(path, "ruleManage.geoipUrl") ||
    pathMatchesPrefix(path, "ruleManage.geositeUrl") ||
    pathMatchesPrefix(path, "ruleManage.assetDirectory") ||
    pathMatchesPrefix(path, "ruleManage.enabledAssets")
  );
}

function touchesPackageInstallPath(path: string) {
  return pathMatchesPrefix(path, "appUpdate.targetVersions");
}

function getAtPath(value: unknown, path: string): unknown {
  if (!path) {
    return value;
  }

  const segments = path
    .replace(/\[(\d+)\]/g, ".$1")
    .split(".")
    .filter(Boolean);

  let current: unknown = value;
  for (const segment of segments) {
    if (current === null || current === undefined) {
      return undefined;
    }

    if (Array.isArray(current)) {
      current = current[Number(segment)];
      continue;
    }

    if (typeof current === "object") {
      current = (current as Record<string, unknown>)[segment];
      continue;
    }

    return undefined;
  }

  return current;
}

function compactCommands(commands: Array<string | null | undefined>) {
  return commands.filter(
    (command): command is string =>
      typeof command === "string" && command.trim().length > 0,
  );
}

function quoteUci(value: string) {
  return `'${value.replaceAll("\\", "\\\\").replaceAll("'", "\\'")}'`;
}

function setValue(
  key: string,
  value: string | number | boolean | null | undefined,
) {
  if (value === null || value === undefined || value === "") {
    return null;
  }

  const encoded =
    typeof value === "boolean"
      ? quoteUci(value ? "1" : "0")
      : typeof value === "number"
        ? quoteUci(String(value))
        : quoteUci(value);

  return `set ${key}=${encoded}`;
}

function setList(key: string, values: string[]) {
  if (values.length === 0) {
    return [];
  }

  return [
    `delete ${key}`,
    ...values.map((value) => `add_list ${key}=${quoteUci(value)}`),
  ];
}

function truthyInt(value?: number) {
  return value && value > 0 ? value : undefined;
}

function assetEnabled(assets: string[], name: string) {
  return assets.includes(name);
}

function renderGlobalCommands(config: PasswallDesiredConfig) {
  const { basicSettings, subscriptions, appUpdate, ruleManage } = config;

  return compactCommands([
    "set passwall2.vectra_global=global",
    setValue("passwall2.vectra_global.enabled", basicSettings.main.mainSwitch),
    setValue("passwall2.vectra_global.node", basicSettings.main.selectedNodeId),
    setValue(
      "passwall2.vectra_global.localhost_proxy",
      basicSettings.main.localhostProxy,
    ),
    setValue(
      "passwall2.vectra_global.client_proxy",
      basicSettings.main.clientProxy,
    ),
    setValue(
      "passwall2.vectra_global.node_socks_port",
      truthyInt(basicSettings.main.nodeSocksPort),
    ),
    setValue(
      "passwall2.vectra_global.node_socks_bind_local",
      basicSettings.main.nodeSocksBindLocal,
    ),
    setValue(
      "passwall2.vectra_global.socks_enabled",
      basicSettings.main.socksMainSwitch,
    ),
    setValue(
      "passwall2.vectra_global.direct_dns_query_strategy",
      basicSettings.dns.directQueryStrategy,
    ),
    setValue(
      "passwall2.vectra_global.remote_dns_protocol",
      basicSettings.dns.remoteDnsProtocol,
    ),
    setValue("passwall2.vectra_global.remote_dns", basicSettings.dns.remoteDns),
    setValue(
      "passwall2.vectra_global.remote_dns_doh",
      basicSettings.dns.remoteDnsDoh,
    ),
    setValue(
      "passwall2.vectra_global.remote_dns_client_ip",
      basicSettings.dns.remoteDnsClientIp,
    ),
    setValue(
      "passwall2.vectra_global.remote_dns_detour",
      basicSettings.dns.remoteDnsDetour,
    ),
    setValue(
      "passwall2.vectra_global.remote_fakedns",
      basicSettings.dns.remoteFakeDns,
    ),
    setValue(
      "passwall2.vectra_global.remote_dns_query_strategy",
      basicSettings.dns.remoteDnsQueryStrategy,
    ),
    setValue(
      "passwall2.vectra_global.dns_hosts",
      basicSettings.dns.dnsHosts.join("\n"),
    ),
    setValue(
      "passwall2.vectra_global.log_node",
      basicSettings.log.enableNodeLog,
    ),
    setValue("passwall2.vectra_global.loglevel", basicSettings.log.level),
    "set passwall2.vectra_global_rules=global_rules",
    setValue("passwall2.vectra_global_rules.geoip_url", ruleManage.geoipUrl),
    setValue(
      "passwall2.vectra_global_rules.geosite_url",
      ruleManage.geositeUrl,
    ),
    setValue(
      "passwall2.vectra_global_rules.v2ray_location_asset",
      ruleManage.assetDirectory,
    ),
    setValue(
      "passwall2.vectra_global_rules.auto_update",
      ruleManage.autoUpdate,
    ),
    setValue(
      "passwall2.vectra_global_rules.geoip_update",
      assetEnabled(ruleManage.enabledAssets, "geoip"),
    ),
    setValue(
      "passwall2.vectra_global_rules.geosite_update",
      assetEnabled(ruleManage.enabledAssets, "geosite"),
    ),
    "set passwall2.vectra_global_app=global_app",
    setValue(
      "passwall2.vectra_global_app.xray_file",
      appUpdate.binaryPaths.xray,
    ),
    setValue(
      "passwall2.vectra_global_app.sing_box_file",
      appUpdate.binaryPaths.singBox,
    ),
    setValue(
      "passwall2.vectra_global_app.hysteria_file",
      appUpdate.binaryPaths.hysteria,
    ),
    setValue(
      "passwall2.vectra_global_app.geoview_file",
      appUpdate.binaryPaths.geoview,
    ),
    "set passwall2.vectra_global_subscribe=global_subscribe",
    setValue(
      "passwall2.vectra_global_subscribe.filter_keyword_mode",
      subscriptions.filterKeywordMode,
    ),
    ...setList(
      "passwall2.vectra_global_subscribe.filter_discard_list",
      subscriptions.discardList,
    ),
    ...setList(
      "passwall2.vectra_global_subscribe.filter_keep_list",
      subscriptions.keepList,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.ss_type",
      subscriptions.typePreferences.shadowsocks,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.trojan_type",
      subscriptions.typePreferences.trojan,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.vmess_type",
      subscriptions.typePreferences.vmess,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.vless_type",
      subscriptions.typePreferences.vless,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.hysteria2_type",
      subscriptions.typePreferences.hysteria2,
    ),
    setValue(
      "passwall2.vectra_global_subscribe.domain_strategy",
      subscriptions.domainStrategy === "auto"
        ? ""
        : subscriptions.domainStrategy,
    ),
  ]);
}

function renderNodeCommands(config: PasswallDesiredConfig) {
  const commands: string[] = [];

  for (const socks of config.basicSettings.socks) {
    const ref = `passwall2.vectra_socks_${socks.id.replaceAll("-", "_")}`;
    commands.push(
      `set ${ref}=socks`,
      ...compactCommands([
        setValue(`${ref}.enabled`, socks.enabled),
        setValue(`${ref}.node`, socks.nodeId),
        setValue(`${ref}.port`, truthyInt(socks.port)),
        setValue(`${ref}.http_port`, truthyInt(socks.httpPort)),
        setValue(`${ref}.bind_local`, socks.bindLocal),
      ]),
      ...setList(
        `${ref}.autoswitch_backup_node`,
        socks.autoswitchBackupNodeIds,
      ),
    );
  }

  for (const rule of config.basicSettings.shuntRules) {
    const ref = `passwall2.${rule.id.replaceAll("-", "_")}`;
    commands.push(
      `set ${ref}=shunt_rules`,
      ...compactCommands([
        setValue(`${ref}.remarks`, rule.label),
        setValue(`${ref}.domain_list`, rule.domainRules.join("\n")),
        setValue(`${ref}.ip_list`, rule.ipRules.join("\n")),
      ]),
    );
  }

  for (const node of config.nodes) {
    const safeId = node.id.replaceAll("-", "_");
    const ref = `passwall2.${safeId}`;
    commands.push(
      `set ${ref}=nodes`,
      ...compactCommands([
        setValue(`${ref}.remarks`, node.label),
        setValue(`${ref}.enabled`, node.enabled),
        setValue(`${ref}.group`, node.group),
        setValue(`${ref}.protocol`, node.protocol),
        setValue(`${ref}.transport`, node.transport),
        setValue(`${ref}.address`, node.address),
        setValue(`${ref}.port`, truthyInt(node.port)),
        setValue(`${ref}.username`, node.username),
        setValue(`${ref}.password`, node.password),
        setValue(`${ref}.tls`, node.tls),
      ]),
      ...setList(`${ref}.tag`, node.tags),
    );
  }

  return commands;
}

function renderSubscriptionCommands(config: PasswallDesiredConfig) {
  const commands: string[] = [];

  for (const item of config.subscriptions.items) {
    const ref = `passwall2.vectra_sub_${item.id.replaceAll("-", "_")}`;
    commands.push(
      `set ${ref}=subscribe_list`,
      ...compactCommands([
        setValue(`${ref}.remark`, item.remark),
        setValue(`${ref}.url`, item.url),
        setValue(`${ref}.enabled`, item.enabled),
        setValue(`${ref}.add_mode`, item.addMode),
        setValue(`${ref}.rem_traffic`, item.metadata.remainingTraffic),
        setValue(`${ref}.expired_date`, item.metadata.expiresAt),
      ]),
    );
  }

  return commands;
}

export function buildPasswallOperationPreview(
  config: PasswallDesiredConfig,
  fieldDiffs?: PasswallFieldDiff[],
): PasswallOperationPreview[] {
  const operations: PasswallOperationPreview[] = [];
  const scopedDiffs = fieldDiffs ?? null;
  const globalCommands = renderGlobalCommands(config);
  if (
    globalCommands.length > 0 &&
    hasDiffPath(scopedDiffs, touchesGlobalApplyPath)
  ) {
    operations.push({
      kind: "uci_apply",
      section: "basicSettings",
      description:
        "Записать основные настройки, DNS, app-path и rule settings в UCI.",
      restartRequired: true,
      uciCommands: globalCommands,
      commands: [],
    });
  }

  const nodeCommands = renderNodeCommands(config);
  if (
    nodeCommands.length > 0 &&
    hasDiffPath(scopedDiffs, touchesNodeSyncPath)
  ) {
    operations.push({
      kind: "node_sync",
      section: "nodes",
      description: "Синхронизировать узлы, SOCKS-профили и shunt rules.",
      restartRequired: true,
      uciCommands: nodeCommands,
      commands: [],
    });
  }

  const subscriptionCommands = renderSubscriptionCommands(config);
  const shouldRefreshSubscriptions = hasDiffPath(
    scopedDiffs,
    touchesSubscriptionPath,
  );
  if (subscriptionCommands.length > 0 && shouldRefreshSubscriptions) {
    operations.push({
      kind: "subscription_sync",
      section: "subscriptions",
      description:
        "Синхронизировать глобальные параметры подписок и список импортов.",
      restartRequired: true,
      uciCommands: subscriptionCommands,
      commands: config.subscriptions.items.length
        ? ["lua /usr/share/passwall2/subscribe.lua start all"]
        : [],
    });
  }

  if (
    config.ruleManage.enabledAssets.length > 0 &&
    hasDiffPath(scopedDiffs, touchesRuleRefreshPath)
  ) {
    operations.push({
      kind: "rule_refresh",
      section: "ruleManage",
      description: "Обновить geo-ассеты через штатный updater PassWall2.",
      restartRequired: false,
      uciCommands: [],
      commands: [
        `lua /usr/share/passwall2/rule_update.lua log ${config.ruleManage.enabledAssets.join(",")}`,
      ],
    });
  }

  if (
    Object.values(config.appUpdate.targetVersions).some(Boolean) &&
    hasDiffPath(scopedDiffs, touchesPackageInstallPath)
  ) {
    operations.push({
      kind: "package_update",
      section: "appUpdate",
      description:
        "Запустить package lane для PassWall2 и runtime-компонентов.",
      restartRequired: false,
      uciCommands: [],
      commands: ["opkg update", "opkg install <packageList>"],
    });
  }

  if (operations.some((operation) => operation.restartRequired)) {
    operations.push({
      kind: "service_restart",
      section: "runtime",
      description: "Перезапустить PassWall2 после применения UCI-изменений.",
      restartRequired: true,
      uciCommands: [],
      commands: ["/etc/init.d/passwall2 restart"],
    });
  }

  return operations;
}

export function buildPasswallFieldDiffs(
  previous: PasswallDesiredConfig | null,
  next: PasswallDesiredConfig,
): PasswallFieldDiff[] {
  const comparablePrevious = canonicalizePasswallForDiff(previous);
  const comparableNext = canonicalizePasswallForDiff(next);
  const paths = new Set<string>([
    ...collectLeafPaths(comparablePrevious),
    ...collectLeafPaths(comparableNext),
  ]);

  const diffs: PasswallFieldDiff[] = [];
  for (const path of paths) {
    const previousValue = getAtPath(comparablePrevious, path);
    const nextValue = getAtPath(comparableNext, path);
    if (sameValue(previousValue, nextValue)) {
      continue;
    }

    const section = sectionFromPath(path);
    const changeType =
      previousValue === undefined
        ? "added"
        : nextValue === undefined
          ? "removed"
          : "changed";

    diffs.push({
      section,
      path,
      previousValue,
      nextValue,
      changeType,
    });
  }

  return diffs.sort((left, right) => left.path.localeCompare(right.path));
}

export function labelPasswallField(path: string) {
  return FIELD_LABELS[path] ?? path;
}

export function summarizePasswallRevisionDiff(
  previous: PasswallDesiredConfig | null,
  next: PasswallDesiredConfig,
) {
  const fieldDiffs = buildPasswallFieldDiffs(previous, next);
  const changedSections = changedSectionsFromDiffs(fieldDiffs);
  if (fieldDiffs.length === 0) {
    return {
      changedSections,
      requiresRestart: false,
      refreshSubscriptions: false,
      refreshRules: false,
      packageInstall: false,
      firmwareValidation: false,
      fieldDiffs,
      operationPreview: [],
    };
  }
  const operationPreview = buildPasswallOperationPreview(next, fieldDiffs);

  return {
    changedSections,
    requiresRestart: operationPreview.some(
      (operation) => operation.restartRequired,
    ),
    refreshSubscriptions: operationPreview.some(
      (operation) => operation.kind === "subscription_sync",
    ),
    refreshRules: operationPreview.some(
      (operation) => operation.kind === "rule_refresh",
    ),
    packageInstall: operationPreview.some(
      (operation) => operation.kind === "package_update",
    ),
    firmwareValidation: false,
    fieldDiffs,
    operationPreview,
  };
}

export function evaluateRescueMode(
  input: z.input<typeof rescueEvaluationInputSchema>,
) {
  const parsed = rescueEvaluationInputSchema.parse(input);
  const lastTransitionMs = parsed.lastTransitionAt?.getTime() ?? 0;
  const cooldownMs = parsed.policy.cooldownSeconds * 1000;
  const cooldownActive =
    lastTransitionMs > 0 &&
    parsed.now.getTime() - lastTransitionMs < cooldownMs;

  if (
    parsed.currentMode === "proxy" &&
    !cooldownActive &&
    parsed.failedProxyChecks >= parsed.policy.triggerFailureCount &&
    (!parsed.policy.requireDirectPathSuccess ||
      parsed.successfulDirectChecks > 0)
  ) {
    return rescueEvaluationResultSchema.parse({
      nextMode: "direct",
      shouldTransition: true,
      reason: parsed.policy.directModeReason,
    });
  }

  if (
    parsed.currentMode === "direct" &&
    !cooldownActive &&
    parsed.successfulProxyChecks >= parsed.policy.recoverySuccessCount
  ) {
    return rescueEvaluationResultSchema.parse({
      nextMode: "proxy",
      shouldTransition: true,
      reason: "Proxy path recovered",
    });
  }

  return rescueEvaluationResultSchema.parse({
    nextMode: parsed.currentMode,
    shouldTransition: false,
    reason: null,
  });
}
