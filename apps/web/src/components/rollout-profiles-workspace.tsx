"use client";

import { useEffect, useMemo, useState } from "react";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import { TabBar } from "~/components/tab-bar";
import { api, type RouterOutputs } from "~/trpc/react";

type ProfilesAndGroupsWorkspace =
  RouterOutputs["update"]["profilesAndGroupsWorkspace"];
type VersionDriftWorkspace = RouterOutputs["update"]["versionDriftWorkspace"];
type WorkspaceTab = "profiles" | "groups";
type RemoteDnsProtocol = "tcp" | "udp" | "doh" | "tls" | "quic" | "http3";
type QueryStrategy = "UseIP" | "UseIPv4" | "UseIPv6";
type LogLevel = "debug" | "info" | "warning" | "error";

type SimpleProfileForm = {
  enabled: boolean;
  selectedNodeId: string;
  localhostProxy: boolean;
  clientProxy: boolean;
  socksEnabled: boolean;
  socksPort: string;
  remoteDns: string;
  remoteDnsProtocol: RemoteDnsProtocol;
  directDnsQueryStrategy: QueryStrategy;
  logLevel: LogLevel;
  autoUpdateGeoAssets: boolean;
  dayOfWeek: string;
  updateTime: string;
};

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  const day = String(date.getUTCDate()).padStart(2, "0");
  const month = String(date.getUTCMonth() + 1).padStart(2, "0");
  const year = String(date.getUTCFullYear());
  const hours = String(date.getUTCHours()).padStart(2, "0");
  const minutes = String(date.getUTCMinutes()).padStart(2, "0");

  return `${day}.${month}.${year}, ${hours}:${minutes}`;
}

function createEmptyProfileConfig(): PasswallDesiredConfig {
  return passwallDesiredConfigSchema.parse({
    schemaVersion: 1,
    basicSettings: {
      main: {
        mainSwitch: true,
        selectedNodeId: undefined,
        localhostProxy: true,
        clientProxy: true,
        nodeSocksPort: 1070,
        nodeSocksBindLocal: true,
        socksMainSwitch: false,
        extras: {},
      },
      socks: [],
      dns: {
        remoteDns: "8.8.8.8",
        remoteDnsProtocol: "udp",
        directQueryStrategy: "UseIP",
        remoteDnsDoh: "",
        remoteDnsDetour: "direct",
        remoteDnsQueryStrategy: "UseIPv4",
        remoteFakeDns: false,
        dnsRedirect: true,
        dnsHosts: ["dns.google.com 8.8.8.8", "cloudflare-dns.com 1.1.1.1"],
        extras: {},
      },
      log: {
        enableNodeLog: true,
        level: "error",
        extras: {},
      },
      maintenance: {
        backupPaths: [
          "/etc/config/passwall2",
          "/etc/config/passwall2_server",
          "/usr/share/passwall2/domains_excluded",
        ],
        extras: {},
      },
      shuntRules: [],
    },
    nodes: [],
    subscriptions: {
      filterKeywordMode: "0",
      discardList: [],
      keepList: [],
      typePreferences: {},
      domainStrategy: "auto",
      items: [],
    },
    appUpdate: {
      binaryPaths: {
        xray: "/usr/bin/xray",
        singBox: "/usr/bin/sing-box",
        hysteria: "/usr/bin/hysteria",
        geoview: "/usr/bin/geoview",
      },
      updateStrategy: "package-preferred",
      targetVersions: {},
      extras: {},
    },
    ruleManage: {
      geoipUrl:
        "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
      geositeUrl:
        "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
      assetDirectory: "/usr/share/v2ray/",
      autoUpdate: true,
      scheduleMode: "daily",
      scheduleHour: 6,
      enabledAssets: ["geoip", "geosite"],
      shuntRules: [],
      extras: {},
    },
  });
}

function stringifyConfig(config: PasswallDesiredConfig) {
  return JSON.stringify(config, null, 2);
}

function parseConfigObject(config: PasswallDesiredConfig): Record<string, unknown> {
  const parsed = JSON.parse(stringifyConfig(config)) as unknown;
  return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
}

function readObject(
  source: Record<string, unknown>,
  key: string,
): Record<string, unknown> {
  const value = source[key];
  return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function readString(source: Record<string, unknown>, key: string, fallback: string) {
  const value = source[key];
  return typeof value === "string" ? value : fallback;
}

function readNullableString(
  source: Record<string, unknown>,
  key: string,
  fallback: string,
) {
  const value = source[key];
  return typeof value === "string" ? value : fallback;
}

function readBoolean(source: Record<string, unknown>, key: string, fallback: boolean) {
  const value = source[key];
  return typeof value === "boolean" ? value : fallback;
}

function parseConfigInput(
  value: string,
): { ok: true; config: PasswallDesiredConfig } | { ok: false; message: string } {
  try {
    const parsed = passwallDesiredConfigSchema.parse(JSON.parse(value) as unknown);
    return { ok: true, config: parsed };
  } catch (error) {
    return {
      ok: false,
      message:
        error instanceof Error
          ? `Профиль не разобран: ${error.message}`
          : "Профиль не разобран.",
    };
  }
}

function buildSimpleProfileForm(config: PasswallDesiredConfig): SimpleProfileForm {
  const root = parseConfigObject(config);
  const basicSettings = readObject(root, "basicSettings");
  const main = readObject(basicSettings, "main");
  const dns = readObject(basicSettings, "dns");
  const log = readObject(basicSettings, "log");
  const ruleManage = readObject(root, "ruleManage");
  const scheduleMode = readString(ruleManage, "scheduleMode", "daily");
  const scheduleDayValue = ruleManage.scheduleDay;
  const scheduleHourValue = ruleManage.scheduleHour;

  return {
    enabled: readBoolean(main, "mainSwitch", true),
    selectedNodeId: readNullableString(main, "selectedNodeId", ""),
    localhostProxy: readBoolean(main, "localhostProxy", true),
    clientProxy: readBoolean(main, "clientProxy", true),
    socksEnabled: readBoolean(main, "socksMainSwitch", false),
    socksPort: String(
      typeof main.nodeSocksPort === "number" ? main.nodeSocksPort : 1070,
    ),
    remoteDns: readString(dns, "remoteDns", "8.8.8.8"),
    remoteDnsProtocol: readString(dns, "remoteDnsProtocol", "udp") as RemoteDnsProtocol,
    directDnsQueryStrategy: readString(dns, "directQueryStrategy", "UseIP") as QueryStrategy,
    logLevel: readString(log, "level", "error") as LogLevel,
    autoUpdateGeoAssets: readBoolean(ruleManage, "autoUpdate", true),
    dayOfWeek:
      scheduleMode === "weekly" && typeof scheduleDayValue === "number"
        ? String(scheduleDayValue)
        : "7",
    updateTime: String(typeof scheduleHourValue === "number" ? scheduleHourValue : 6),
  };
}

function applySimpleProfileForm(
  baseConfig: PasswallDesiredConfig,
  form: SimpleProfileForm,
): PasswallDesiredConfig {
  const draft = parseConfigObject(baseConfig);
  const basicSettings = readObject(draft, "basicSettings");
  const main = readObject(basicSettings, "main");
  const dns = readObject(basicSettings, "dns");
  const log = readObject(basicSettings, "log");
  const maintenance = readObject(basicSettings, "maintenance");
  const ruleManage = readObject(draft, "ruleManage");

  main.mainSwitch = form.enabled;
  main.selectedNodeId = form.selectedNodeId.trim() || undefined;
  main.localhostProxy = form.localhostProxy;
  main.clientProxy = form.clientProxy;
  main.nodeSocksPort = Number(form.socksPort) > 0 ? Number(form.socksPort) : 1070;
  main.nodeSocksBindLocal = true;
  main.socksMainSwitch = form.socksEnabled;

  dns.remoteDns = form.remoteDns.trim() || readString(dns, "remoteDns", "8.8.8.8");
  dns.remoteDnsProtocol = form.remoteDnsProtocol;
  dns.directQueryStrategy = form.directDnsQueryStrategy;
  dns.remoteDnsDoh =
    form.remoteDnsProtocol === "doh"
      ? readString(dns, "remoteDnsDoh", "https://1.1.1.1/dns-query")
      : "";
  dns.remoteDnsDetour =
    form.remoteDnsProtocol === "udp"
      ? "direct"
      : readString(dns, "remoteDnsDetour", "direct");
  dns.remoteDnsQueryStrategy = readString(dns, "remoteDnsQueryStrategy", "UseIPv4");
  dns.remoteFakeDns = readBoolean(dns, "remoteFakeDns", false);
  dns.dnsRedirect = readBoolean(dns, "dnsRedirect", true);

  log.enableNodeLog = readBoolean(log, "enableNodeLog", true);
  log.level = form.logLevel;

  maintenance.backupPaths = Array.isArray(maintenance.backupPaths)
    ? maintenance.backupPaths
    : [
        "/etc/config/passwall2",
        "/etc/config/passwall2_server",
        "/usr/share/passwall2/domains_excluded",
      ];

  ruleManage.geoipUrl = readString(
    ruleManage,
    "geoipUrl",
    "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
  );
  ruleManage.geositeUrl = readString(
    ruleManage,
    "geositeUrl",
    "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
  );
  ruleManage.assetDirectory = readString(ruleManage, "assetDirectory", "/usr/share/v2ray/");
  ruleManage.autoUpdate = form.autoUpdateGeoAssets;
  ruleManage.scheduleMode = form.dayOfWeek === "7" ? "daily" : "weekly";
  ruleManage.scheduleDay = form.dayOfWeek === "7" ? undefined : Number(form.dayOfWeek);
  ruleManage.scheduleHour = Number(form.updateTime) >= 0 ? Number(form.updateTime) : 6;
  ruleManage.enabledAssets = Array.isArray(ruleManage.enabledAssets)
    ? ruleManage.enabledAssets
    : ["geoip", "geosite"];

  basicSettings.main = main;
  basicSettings.socks = Array.isArray(basicSettings.socks)
    ? basicSettings.socks
    : [];
  basicSettings.dns = dns;
  basicSettings.log = log;
  basicSettings.maintenance = maintenance;
  draft.basicSettings = basicSettings;
  draft.ruleManage = ruleManage;

  return passwallDesiredConfigSchema.parse(draft);
}

function buildGroupReadiness(args: {
  routerCount: number;
  blockedCount: number;
  outdatedCount: number;
}) {
  if (args.routerCount === 0) {
    return {
      label: "Пустая группа",
      className: "border-white/10 bg-white/5 text-slate-300",
    };
  }

  if (args.blockedCount > 0) {
    return {
      label: "Нужно внимание",
      className: "border-amber-400/30 bg-amber-500/10 text-amber-200",
    };
  }

  if (args.outdatedCount > 0) {
    return {
      label: "Есть старые версии",
      className: "border-sky-400/30 bg-sky-500/10 text-sky-100",
    };
  }

  return {
    label: "Готова к rollout",
    className: "border-emerald-400/30 bg-emerald-500/10 text-emerald-100",
  };
}

export function RolloutProfilesWorkspace({
  initialWorkspace,
  initialVersionDriftWorkspace,
  onOpenVersionControl,
}: {
  initialWorkspace: ProfilesAndGroupsWorkspace;
  initialVersionDriftWorkspace: VersionDriftWorkspace;
  onOpenVersionControl?: () => void;
}) {
  const utils = api.useUtils();
  const emptyProfileConfig = useMemo(() => createEmptyProfileConfig(), []);

  const workspaceQuery = api.update.profilesAndGroupsWorkspace.useQuery(undefined, {
    initialData: initialWorkspace,
    refetchOnWindowFocus: false,
  });
  const versionDriftQuery = api.update.versionDriftWorkspace.useQuery(undefined, {
    initialData: initialVersionDriftWorkspace,
    refetchOnWindowFocus: false,
  });

  const workspace = workspaceQuery.data ?? initialWorkspace;
  const versionWorkspace = versionDriftQuery.data ?? initialVersionDriftWorkspace;

  const [activeTab, setActiveTab] = useState<WorkspaceTab>("profiles");
  const [selectedProfileId, setSelectedProfileId] = useState<string | null>(
    workspace.profiles[0]?.id ?? null,
  );
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(
    workspace.groups[0]?.id ?? null,
  );

  const [profileName, setProfileName] = useState("");
  const [profileDescription, setProfileDescription] = useState("");
  const [profileNote, setProfileNote] = useState("");
  const [profileJson, setProfileJson] = useState(stringifyConfig(emptyProfileConfig));
  const [simpleProfileForm, setSimpleProfileForm] = useState<SimpleProfileForm>(
    buildSimpleProfileForm(emptyProfileConfig),
  );
  const [advancedEditorOpen, setAdvancedEditorOpen] = useState(false);

  const [groupName, setGroupName] = useState("");
  const [groupDescription, setGroupDescription] = useState("");
  const [groupProfileId, setGroupProfileId] = useState<string | null>(null);
  const [selectedRouterIds, setSelectedRouterIds] = useState<string[]>([]);
  const [groupNote, setGroupNote] = useState("");

  const saveProfileMutation = api.update.saveRolloutProfile.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
      ]);
    },
  });
  const deleteProfileMutation = api.update.deleteRolloutProfile.useMutation({
    onSuccess: async () => {
      await utils.update.profilesAndGroupsWorkspace.invalidate();
      setSelectedProfileId(null);
    },
  });
  const saveGroupMutation = api.update.saveRouterGroup.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
      ]);
    },
  });
  const deleteGroupMutation = api.update.deleteRouterGroup.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
      ]);
      setSelectedGroupId(null);
      setSelectedRouterIds([]);
    },
  });
  const assignMutation = api.update.assignRoutersToGroup.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
  });
  const rolloutMutation = api.update.queueGroupProfileRollout.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
        utils.update.globalTemplateWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setGroupNote("");
    },
  });

  const selectedProfile =
    workspace.profiles.find((profile) => profile.id === selectedProfileId) ?? null;
  const selectedGroup =
    workspace.groups.find((group) => group.id === selectedGroupId) ?? null;

  const parsedProfile = parseConfigInput(profileJson);
  const baseProfileConfig = selectedProfile?.rolloutConfig ?? emptyProfileConfig;

  const groupRouters = useMemo(
    () =>
      selectedGroupId
        ? workspace.routers.filter((router) => router.rolloutGroupId === selectedGroupId)
        : [],
    [selectedGroupId, workspace.routers],
  );

  const availableRoutersForAdd = workspace.unassignedRouters;

  useEffect(() => {
    if (workspace.profiles.length === 0) {
      setSelectedProfileId(null);
      return;
    }

    if (!selectedProfileId) {
      setSelectedProfileId(workspace.profiles[0]?.id ?? null);
      return;
    }

    if (!workspace.profiles.some((profile) => profile.id === selectedProfileId)) {
      setSelectedProfileId(workspace.profiles[0]?.id ?? null);
    }
  }, [selectedProfileId, workspace.profiles]);

  useEffect(() => {
    if (workspace.groups.length === 0) {
      setSelectedGroupId(null);
      return;
    }

    if (!selectedGroupId) {
      setSelectedGroupId(workspace.groups[0]?.id ?? null);
      return;
    }

    if (!workspace.groups.some((group) => group.id === selectedGroupId)) {
      setSelectedGroupId(workspace.groups[0]?.id ?? null);
    }
  }, [selectedGroupId, workspace.groups]);

  useEffect(() => {
    const config = selectedProfile?.rolloutConfig ?? emptyProfileConfig;
    setProfileName(selectedProfile?.name ?? "");
    setProfileDescription(selectedProfile?.description ?? "");
    setProfileNote(selectedProfile?.note ?? "");
    setProfileJson(stringifyConfig(config));
    setSimpleProfileForm(buildSimpleProfileForm(config));
  }, [emptyProfileConfig, selectedProfile]);

  useEffect(() => {
    setGroupName(selectedGroup?.name ?? "");
    setGroupDescription(selectedGroup?.description ?? "");
    setGroupProfileId(selectedGroup?.rolloutProfileId ?? null);
  }, [selectedGroup]);

  useEffect(() => {
    setSelectedRouterIds((current) =>
      current.filter((routerId) =>
        availableRoutersForAdd.some((router) => router.id === routerId),
      ),
    );
  }, [availableRoutersForAdd]);

  const versionRowsByRouterId = useMemo(
    () => new Map(versionWorkspace.rows.map((row) => [row.id, row])),
    [versionWorkspace.rows],
  );

  const groupStatusById = useMemo(() => {
    const stats = new Map<
      string,
      {
        routerCount: number;
        controllerOutdated: number;
        passwallOutdated: number;
        xrayOutdated: number;
        blocked: number;
        queued: number;
      }
    >();

    for (const group of workspace.groups) {
      stats.set(group.id, {
        routerCount: 0,
        controllerOutdated: 0,
        passwallOutdated: 0,
        xrayOutdated: 0,
        blocked: 0,
        queued: 0,
      });
    }

    for (const row of versionWorkspace.rows) {
      if (!row.rolloutGroupId) {
        continue;
      }

      const current = stats.get(row.rolloutGroupId);
      if (!current) {
        continue;
      }

      current.routerCount += 1;
      if (row.controllerNeedsUpdate) current.controllerOutdated += 1;
      if (row.passwallNeedsUpdate) current.passwallOutdated += 1;
      if (row.xrayNeedsUpdate) current.xrayOutdated += 1;
      if (row.blocked) current.blocked += 1;
      if (row.hasQueuedUpdate) current.queued += 1;
    }

    return stats;
  }, [versionWorkspace.rows, workspace.groups]);

  const selectedGroupStatus = selectedGroupId
    ? groupStatusById.get(selectedGroupId) ?? {
        routerCount: 0,
        controllerOutdated: 0,
        passwallOutdated: 0,
        xrayOutdated: 0,
        blocked: 0,
        queued: 0,
      }
    : null;

  const profileDirty =
    profileName !== (selectedProfile?.name ?? "") ||
    profileDescription !== (selectedProfile?.description ?? "") ||
    profileNote !== (selectedProfile?.note ?? "") ||
    profileJson !== stringifyConfig(baseProfileConfig);

  const groupDirty =
    groupName !== (selectedGroup?.name ?? "") ||
    groupDescription !== (selectedGroup?.description ?? "") ||
    groupProfileId !== (selectedGroup?.rolloutProfileId ?? null);

  function resetProfileEditor() {
    const config = selectedProfile?.rolloutConfig ?? emptyProfileConfig;
    setProfileName(selectedProfile?.name ?? "");
    setProfileDescription(selectedProfile?.description ?? "");
    setProfileNote(selectedProfile?.note ?? "");
    setProfileJson(stringifyConfig(config));
    setSimpleProfileForm(buildSimpleProfileForm(config));
  }

  function resetGroupEditor() {
    setGroupName(selectedGroup?.name ?? "");
    setGroupDescription(selectedGroup?.description ?? "");
    setGroupProfileId(selectedGroup?.rolloutProfileId ?? null);
  }

  function updateSimpleProfileForm(patch: Partial<SimpleProfileForm>) {
    const nextForm = { ...simpleProfileForm, ...patch };
    const currentConfig = parsedProfile.ok ? parsedProfile.config : baseProfileConfig;
    const nextConfig = applySimpleProfileForm(currentConfig, nextForm);

    setSimpleProfileForm(nextForm);
    setProfileJson(stringifyConfig(nextConfig));
  }

  const totalOutdatedSignals =
    versionWorkspace.summary.outdatedPasswallCount +
    versionWorkspace.summary.outdatedXrayCount +
    versionWorkspace.summary.queuedCount;

  return (
    <div className="space-y-4">
      <Panel
        eyebrow="Группы и профили"
        title="Профили конфигурации и группы роутеров"
        tone="hero"
      >
        <div className="space-y-4">
          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
            Здесь вы ведёте reusable профили для подключённого парка и раскладываете роутеры по рабочим группам. По умолчанию это короткая операторская форма, а raw JSON остаётся только как резервный экспертный путь.
          </div>

          <div className="vectra-stat-grid">
            <StatusTile
              label="Профилей"
              value={String(workspace.profiles.length)}
              hint="Reusable шаблоны rollout"
              compact
            />
            <StatusTile
              label="Групп"
              value={String(workspace.groups.length)}
              hint="Сегменты парка"
              compact
            />
            <StatusTile
              label="Без группы"
              value={String(workspace.unassignedRouters.length)}
              tone={workspace.unassignedRouters.length > 0 ? "warning" : "good"}
              hint="Роутеры, которые ещё не распределены"
              compact
            />
            <StatusTile
              label="Нужен version-control"
              value={String(totalOutdatedSignals)}
              tone={totalOutdatedSignals > 0 ? "warning" : "good"}
              hint="Сигналы о старых версиях или очереди update"
              compact
            />
          </div>

          <TabBar
            ariaLabel="Профили и группы"
            items={[
              {
                id: "profiles",
                label: `Профили ${workspace.profiles.length}`,
                active: activeTab === "profiles",
                onSelect: () => setActiveTab("profiles"),
              },
              {
                id: "groups",
                label: `Группы ${workspace.groups.length}`,
                active: activeTab === "groups",
                onSelect: () => setActiveTab("groups"),
              },
            ]}
            variant="secondary"
          />

          {activeTab === "profiles" ? (
            <div className="grid gap-4 xl:grid-cols-[minmax(0,0.92fr)_minmax(0,1.08fr)]">
              <Panel eyebrow="Профили" title="Список reusable профилей" tone="muted" compact>
                <div className="space-y-3">
                  {workspace.profiles.length > 0 ? (
                    workspace.profiles.map((profile) => {
                      const selected = profile.id === selectedProfileId;

                      return (
                        <button
                          key={profile.id}
                          type="button"
                          onClick={() => {
                            setSelectedProfileId(profile.id);
                            setAdvancedEditorOpen(false);
                          }}
                          className={`w-full rounded-2xl border px-4 py-3 text-left transition ${
                            selected
                              ? "border-sky-400/30 bg-sky-500/10"
                              : "border-white/10 bg-[var(--vectra-panel-soft)] hover:border-white/20"
                          }`}
                        >
                          <div className="flex items-start justify-between gap-3">
                            <div>
                              <p className="text-sm font-semibold text-white">{profile.name}</p>
                              <p className="mt-1 text-sm leading-6 text-slate-400">
                                {profile.description ?? "Без описания."}
                              </p>
                            </div>
                            <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1 text-[11px] text-slate-300">
                              {profile.groupCount} групп
                            </span>
                          </div>
                          <p className="mt-3 text-xs leading-5 text-slate-500">
                            Shunt rules: {profile.shuntRuleCount} · managed nodes: {profile.managedNodeCount} · обновлено {formatDateTime(profile.updatedAt)}
                          </p>
                        </button>
                      );
                    })
                  ) : (
                    <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] px-4 py-6 text-sm leading-7 text-slate-400">
                      Профилей пока нет. Начните с первого reusable профиля для подключённого парка.
                    </div>
                  )}
                </div>
              </Panel>

              <Panel
                eyebrow="Редактор профиля"
                title={selectedProfile ? selectedProfile.name : "Новый rollout профиль"}
                tone="muted"
              >
                <div className="space-y-4">
                  <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
                    Сначала заполните понятные поля ниже. Если нужен точный контроль над нодами, shunt rules или редкими настройками, откройте advanced JSON fallback.
                  </div>

                  <div className="vectra-stat-grid">
                    <StatusTile
                      label="Групп на профиле"
                      value={String(selectedProfile?.groupCount ?? 0)}
                      compact
                    />
                    <StatusTile
                      label="Нод внутри"
                      value={String(
                        parsedProfile.ok
                          ? parsedProfile.config.nodes.length
                          : baseProfileConfig.nodes.length,
                      )}
                      hint="Точный состав нод остаётся в advanced JSON"
                      compact
                    />
                    <StatusTile
                      label="Shunt rules"
                      value={String(
                        parsedProfile.ok
                          ? parsedProfile.config.basicSettings.shuntRules.length
                          : baseProfileConfig.basicSettings.shuntRules.length,
                      )}
                      hint="Сложная маршрутизация не навязывается по умолчанию"
                      compact
                    />
                    <StatusTile
                      label="Локальный статус"
                      value={profileDirty ? "Есть несохранённые правки" : "Всё сохранено"}
                      tone={profileDirty ? "warning" : "good"}
                      compact
                    />
                  </div>

                  <div className="grid gap-4 md:grid-cols-2">
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Название профиля</span>
                      <input
                        value={profileName}
                        onChange={(event) => setProfileName(event.target.value)}
                        className="vectra-field px-3 py-2 text-sm text-white"
                        placeholder="Например: Общий production профиль"
                      />
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Описание</span>
                      <input
                        value={profileDescription}
                        onChange={(event) => setProfileDescription(event.target.value)}
                        className="vectra-field px-3 py-2 text-sm text-white"
                        placeholder="Для какой группы и какого режима нужен профиль"
                      />
                    </label>
                  </div>

                  <div className="grid gap-4 md:grid-cols-2">
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Основной режим</span>
                      <select
                        value={simpleProfileForm.enabled ? "enabled" : "disabled"}
                        onChange={(event) =>
                          updateSimpleProfileForm({
                            enabled: event.target.value === "enabled",
                          })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="enabled">PassWall включён</option>
                        <option value="disabled">PassWall выключен</option>
                      </select>
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Нода по умолчанию</span>
                      <input
                        value={simpleProfileForm.selectedNodeId}
                        onChange={(event) =>
                          updateSimpleProfileForm({ selectedNodeId: event.target.value })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                        placeholder="Оставьте пустым, если профиль не должен менять ноду"
                      />
                    </label>
                  </div>

                  <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
                    <label className="flex items-center gap-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm text-slate-200">
                      <input
                        type="checkbox"
                        checked={simpleProfileForm.localhostProxy}
                        onChange={(event) =>
                          updateSimpleProfileForm({ localhostProxy: event.target.checked })
                        }
                      />
                      <span>Proxy для самого роутера</span>
                    </label>
                    <label className="flex items-center gap-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm text-slate-200">
                      <input
                        type="checkbox"
                        checked={simpleProfileForm.clientProxy}
                        onChange={(event) =>
                          updateSimpleProfileForm({ clientProxy: event.target.checked })
                        }
                      />
                      <span>Proxy для клиентов LAN</span>
                    </label>
                    <label className="flex items-center gap-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm text-slate-200">
                      <input
                        type="checkbox"
                        checked={simpleProfileForm.socksEnabled}
                        onChange={(event) =>
                          updateSimpleProfileForm({ socksEnabled: event.target.checked })
                        }
                      />
                      <span>SOCKS-порт</span>
                    </label>
                    <label className="flex items-center gap-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm text-slate-200">
                      <input
                        type="checkbox"
                        checked={simpleProfileForm.autoUpdateGeoAssets}
                        onChange={(event) =>
                          updateSimpleProfileForm({
                            autoUpdateGeoAssets: event.target.checked,
                          })
                        }
                      />
                      <span>Автообновление геобаз</span>
                    </label>
                  </div>

                  <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Порт SOCKS</span>
                      <input
                        value={simpleProfileForm.socksPort}
                        onChange={(event) =>
                          updateSimpleProfileForm({ socksPort: event.target.value })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                        inputMode="numeric"
                      />
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Remote DNS</span>
                      <input
                        value={simpleProfileForm.remoteDns}
                        onChange={(event) =>
                          updateSimpleProfileForm({ remoteDns: event.target.value })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                        placeholder="1.1.1.1"
                      />
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Протокол DNS</span>
                      <select
                        value={simpleProfileForm.remoteDnsProtocol}
                        onChange={(event) =>
                          updateSimpleProfileForm({
                            remoteDnsProtocol: event.target.value as RemoteDnsProtocol,
                          })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="doh">DoH</option>
                        <option value="tls">TLS</option>
                        <option value="tcp">TCP</option>
                        <option value="udp">UDP</option>
                        <option value="quic">QUIC</option>
                        <option value="http3">HTTP/3</option>
                      </select>
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Стратегия direct DNS</span>
                      <select
                        value={simpleProfileForm.directDnsQueryStrategy}
                        onChange={(event) =>
                          updateSimpleProfileForm({
                            directDnsQueryStrategy: event.target.value as QueryStrategy,
                          })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="UseIP">UseIP</option>
                        <option value="UseIPv4">UseIPv4</option>
                        <option value="UseIPv6">UseIPv6</option>
                      </select>
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Логи</span>
                      <select
                        value={simpleProfileForm.logLevel}
                        onChange={(event) =>
                          updateSimpleProfileForm({
                            logLevel: event.target.value as LogLevel,
                          })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="error">error</option>
                        <option value="warning">warning</option>
                        <option value="info">info</option>
                        <option value="debug">debug</option>
                      </select>
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">День геообновления</span>
                      <select
                        value={simpleProfileForm.dayOfWeek}
                        onChange={(event) =>
                          updateSimpleProfileForm({ dayOfWeek: event.target.value })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="7">Каждый день</option>
                        <option value="1">Понедельник</option>
                        <option value="2">Вторник</option>
                        <option value="3">Среда</option>
                        <option value="4">Четверг</option>
                        <option value="5">Пятница</option>
                        <option value="6">Суббота</option>
                        <option value="0">Воскресенье</option>
                      </select>
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Час геообновления</span>
                      <select
                        value={simpleProfileForm.updateTime}
                        onChange={(event) =>
                          updateSimpleProfileForm({ updateTime: event.target.value })
                        }
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        {Array.from({ length: 24 }, (_, hour) => (
                          <option key={hour} value={String(hour)}>
                            {String(hour).padStart(2, "0")}:00
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>

                  <label className="space-y-2 text-sm text-slate-300">
                    <span className="vectra-kicker text-slate-500">Комментарий</span>
                    <input
                      value={profileNote}
                      onChange={(event) => setProfileNote(event.target.value)}
                      className="vectra-field px-3 py-2 text-sm text-white"
                      placeholder="Короткая operator-заметка к профилю"
                    />
                  </label>

                  {parsedProfile.ok ? null : (
                    <div className="rounded-md border border-amber-400/30 bg-amber-950/20 px-3 py-3 text-sm leading-7 text-amber-100">
                      {parsedProfile.message}
                    </div>
                  )}

                  <details
                    open={advancedEditorOpen}
                    onToggle={(event) => setAdvancedEditorOpen(event.currentTarget.open)}
                    className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3"
                  >
                    <summary className="min-h-11 cursor-pointer list-none text-sm font-medium text-white">
                      Advanced JSON fallback
                    </summary>
                    <p className="mt-2 text-sm leading-6 text-slate-400">
                      Используйте только если нужно править ноды, shunt rules, subscriptions или редкие поля, которые не вынесены в простой режим.
                    </p>
                    <label className="mt-3 block space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Rollout config профиля</span>
                      <textarea
                        value={profileJson}
                        onChange={(event) => setProfileJson(event.target.value)}
                        rows={18}
                        className="vectra-field min-h-[22rem] border-white/10 bg-black/30 px-3 py-3 font-[family:var(--font-plex-mono)] text-[12px] leading-6 text-slate-100"
                      />
                    </label>
                  </details>

                  <ActionStrip justify="start">
                    <button
                      type="button"
                      disabled={!parsedProfile.ok || saveProfileMutation.isPending}
                      onClick={() =>
                        parsedProfile.ok
                          ? saveProfileMutation.mutate({
                              profileId: selectedProfile?.id,
                              name: profileName,
                              description: profileDescription.trim() || undefined,
                              note: profileNote.trim() || undefined,
                              rolloutConfig: parsedProfile.config,
                            })
                          : undefined
                      }
                      className="vectra-button-primary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {saveProfileMutation.isPending
                        ? "Сохраняю профиль..."
                        : selectedProfile
                          ? "Сохранить профиль"
                          : "Создать профиль"}
                    </button>
                    <button
                      type="button"
                      onClick={resetProfileEditor}
                      disabled={!profileDirty}
                      className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      Сбросить локальные правки
                    </button>
                    {selectedProfile ? (
                      <button
                        type="button"
                        disabled={deleteProfileMutation.isPending}
                        onClick={() =>
                          window.confirm(`Удалить профиль \"${selectedProfile.name}\"?`) &&
                          deleteProfileMutation.mutate({ profileId: selectedProfile.id })
                        }
                        className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-rose-400/20 hover:text-rose-100"
                      >
                        Удалить профиль
                      </button>
                    ) : null}
                    <button
                      type="button"
                      onClick={() => {
                        setSelectedProfileId(null);
                        setAdvancedEditorOpen(false);
                      }}
                      className="vectra-button-secondary px-3 py-2 text-sm font-medium transition"
                    >
                      Новый профиль
                    </button>
                  </ActionStrip>
                </div>
              </Panel>
            </div>
          ) : null}

          {activeTab === "groups" ? (
            <div className="grid gap-4 xl:grid-cols-[minmax(0,0.92fr)_minmax(0,1.08fr)]">
              <Panel eyebrow="Группы" title="Состав и назначение профилей" tone="muted" compact>
                <div className="space-y-3">
                  {workspace.groups.map((group) => {
                    const selected = group.id === selectedGroupId;
                    const status = groupStatusById.get(group.id) ?? {
                      routerCount: 0,
                      controllerOutdated: 0,
                      passwallOutdated: 0,
                      xrayOutdated: 0,
                      blocked: 0,
                      queued: 0,
                    };
                    const readiness = buildGroupReadiness({
                      routerCount: status.routerCount,
                      blockedCount: status.blocked,
                      outdatedCount:
                        status.controllerOutdated +
                        status.passwallOutdated +
                        status.xrayOutdated,
                    });

                    return (
                      <button
                        key={group.id}
                        type="button"
                        onClick={() => setSelectedGroupId(group.id)}
                        className={`w-full rounded-2xl border px-4 py-3 text-left transition ${
                          selected
                            ? "border-sky-400/30 bg-sky-500/10"
                            : "border-white/10 bg-[var(--vectra-panel-soft)] hover:border-white/20"
                        }`}
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div>
                            <p className="text-sm font-semibold text-white">{group.name}</p>
                            <p className="mt-1 text-sm leading-6 text-slate-400">
                              {group.description ?? "Без описания."}
                            </p>
                          </div>
                          <span className={`rounded-full border px-2 py-1 text-[11px] ${readiness.className}`}>
                            {readiness.label}
                          </span>
                        </div>
                        <div className="mt-3 flex flex-wrap gap-2 text-xs leading-5 text-slate-400">
                          <span>{group.routerCount} роутеров</span>
                          <span>· профиль: {group.rolloutProfileName ?? "не выбран"}</span>
                          <span>· controller: {status.controllerOutdated}</span>
                          <span>· PassWall: {status.passwallOutdated}</span>
                          <span>· Xray: {status.xrayOutdated}</span>
                        </div>
                        <p className="mt-2 text-xs leading-5 text-slate-500">
                          Обновлено {formatDateTime(group.updatedAt)}
                        </p>
                      </button>
                    );
                  })}
                </div>
              </Panel>

              <Panel
                eyebrow="Редактор группы"
                title={selectedGroup ? selectedGroup.name : "Новая группа"}
                tone="muted"
              >
                <div className="space-y-4">
                  <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
                    Здесь одно главное действие за раз: выбираете профиль для группы, видите здоровье группы и только потом готовите draft-only rollout или queue apply.
                  </div>

                  <div className="grid gap-4 md:grid-cols-2">
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Название группы</span>
                      <input
                        value={groupName}
                        onChange={(event) => setGroupName(event.target.value)}
                        className="vectra-field px-3 py-2 text-sm text-white"
                        placeholder="Например: Тестовая группа"
                      />
                    </label>
                    <label className="space-y-2 text-sm text-slate-300">
                      <span className="vectra-kicker text-slate-500">Профиль группы</span>
                      <select
                        value={groupProfileId ?? ""}
                        onChange={(event) => setGroupProfileId(event.target.value || null)}
                        className="vectra-field px-3 py-2 text-sm text-white"
                      >
                        <option value="">Без профиля</option>
                        {workspace.profiles.map((profile) => (
                          <option key={profile.id} value={profile.id}>
                            {profile.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>

                  <label className="space-y-2 text-sm text-slate-300">
                    <span className="vectra-kicker text-slate-500">Описание группы</span>
                    <input
                      value={groupDescription}
                      onChange={(event) => setGroupDescription(event.target.value)}
                      className="vectra-field px-3 py-2 text-sm text-white"
                      placeholder="Какой парк сюда относится"
                    />
                  </label>

                  {selectedGroup && selectedGroupStatus ? (
                    <div className="space-y-4 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
                      <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                        <div>
                          <p className="vectra-kicker text-slate-500">Версионное состояние группы</p>
                          <p className="mt-2 text-sm leading-6 text-slate-300">
                            Отсюда видно, есть ли в группе старые controller, PassWall или Xray. Bulk updates остаются в отдельном контроллере версий, чтобы не дублировать backend-логику и не перегружать экран.
                          </p>
                        </div>
                        <button
                          type="button"
                          onClick={onOpenVersionControl}
                          className="vectra-button-secondary px-3 py-2 text-sm font-medium transition"
                        >
                          Открыть контроллер версий
                        </button>
                      </div>

                      <div className="vectra-stat-grid">
                        <StatusTile
                          label="Controller устарел"
                          value={String(selectedGroupStatus.controllerOutdated)}
                          tone={selectedGroupStatus.controllerOutdated > 0 ? "warning" : "good"}
                          compact
                        />
                        <StatusTile
                          label="PassWall устарел"
                          value={String(selectedGroupStatus.passwallOutdated)}
                          tone={selectedGroupStatus.passwallOutdated > 0 ? "warning" : "good"}
                          compact
                        />
                        <StatusTile
                          label="Xray устарел"
                          value={String(selectedGroupStatus.xrayOutdated)}
                          tone={selectedGroupStatus.xrayOutdated > 0 ? "warning" : "good"}
                          compact
                        />
                        <StatusTile
                          label="Blocked / queued"
                          value={`${selectedGroupStatus.blocked} / ${selectedGroupStatus.queued}`}
                          tone={selectedGroupStatus.blocked > 0 ? "warning" : "default"}
                          compact
                        />
                      </div>
                    </div>
                  ) : null}

                  <ActionStrip justify="start">
                    <button
                      type="button"
                      disabled={saveGroupMutation.isPending}
                      onClick={() =>
                        saveGroupMutation.mutate({
                          groupId: selectedGroup?.id,
                          name: groupName,
                          description: groupDescription.trim() || undefined,
                          rolloutProfileId: groupProfileId,
                        })
                      }
                      className="vectra-button-primary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {saveGroupMutation.isPending
                        ? "Сохраняю группу..."
                        : selectedGroup
                          ? "Сохранить группу"
                          : "Создать группу"}
                    </button>
                    <button
                      type="button"
                      onClick={resetGroupEditor}
                      disabled={!groupDirty}
                      className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      Сбросить локальные правки
                    </button>
                    {selectedGroup ? (
                      <button
                        type="button"
                        disabled={deleteGroupMutation.isPending}
                        onClick={() =>
                          window.confirm(`Удалить группу \"${selectedGroup.name}\"?`) &&
                          deleteGroupMutation.mutate({ groupId: selectedGroup.id })
                        }
                        className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-rose-400/20 hover:text-rose-100"
                      >
                        Удалить группу
                      </button>
                    ) : null}
                    <button
                      type="button"
                      onClick={() => setSelectedGroupId(null)}
                      className="vectra-button-secondary px-3 py-2 text-sm font-medium transition"
                    >
                      Новая группа
                    </button>
                  </ActionStrip>

                  {selectedGroup ? (
                    <div className="space-y-4">
                      <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3">
                        <p className="vectra-kicker text-slate-500">Роутеры в группе</p>
                        <p className="mt-2 text-sm leading-6 text-slate-300">
                          Для каждого роутера видно import/support статус и наличие старых controller, PassWall или Xray прямо в карточке группы.
                        </p>
                      </div>

                      <div className="space-y-3">
                        {groupRouters.length > 0 ? (
                          groupRouters.map((router) => {
                            const versionRow = versionRowsByRouterId.get(router.id);
                            const outdatedLabels = [
                              versionRow?.controllerNeedsUpdate ? "controller" : null,
                              versionRow?.passwallNeedsUpdate ? "PassWall" : null,
                              versionRow?.xrayNeedsUpdate ? "Xray" : null,
                            ].filter(Boolean);

                            return (
                              <div
                                key={router.id}
                                className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4"
                              >
                                <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                                  <div className="min-w-0">
                                    <p className="text-sm font-semibold text-white">{router.displayName}</p>
                                    <p className="mt-1 text-sm leading-6 text-slate-400">
                                      {router.hostname ?? router.deviceIdentifier}
                                    </p>
                                    <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-300">
                                      <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1">
                                        {router.importState} · {router.status}
                                      </span>
                                      <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1">
                                        {router.supportTitle}
                                      </span>
                                      {versionRow?.blockedReason ? (
                                        <span className="rounded-full border border-amber-400/30 bg-amber-500/10 px-2 py-1 text-amber-200">
                                          {versionRow.blockedReason}
                                        </span>
                                      ) : null}
                                    </div>
                                  </div>

                                  <div className="w-full lg:w-[15rem]">
                                    <label className="space-y-2 text-sm text-slate-300">
                                      <span className="vectra-kicker text-slate-500">Перенести в группу</span>
                                      <select
                                        value={router.rolloutGroupId ?? ""}
                                        onChange={(event) =>
                                          assignMutation.mutate({
                                            routerIds: [router.id],
                                            groupId: event.target.value || null,
                                          })
                                        }
                                        className="vectra-field px-3 py-2 text-sm text-white"
                                      >
                                        <option value="">Без группы</option>
                                        {workspace.groups.map((group) => (
                                          <option key={group.id} value={group.id}>
                                            {group.name}
                                          </option>
                                        ))}
                                      </select>
                                    </label>
                                  </div>
                                </div>

                                <div className="mt-3 grid gap-2 md:grid-cols-3">
                                  <div className="rounded-xl border border-white/10 bg-black/10 px-3 py-3 text-sm text-slate-200">
                                    <p className="vectra-kicker text-slate-500">Controller</p>
                                    <p className="mt-1">{versionRow?.controllerInstalled ?? "нет данных"}</p>
                                    <p className="mt-1 text-xs text-slate-500">
                                      {versionRow?.controllerAvailable ?? "не опубликовано"}
                                    </p>
                                  </div>
                                  <div className="rounded-xl border border-white/10 bg-black/10 px-3 py-3 text-sm text-slate-200">
                                    <p className="vectra-kicker text-slate-500">PassWall</p>
                                    <p className="mt-1">{versionRow?.passwallInstalled ?? "нет данных"}</p>
                                    <p className="mt-1 text-xs text-slate-500">
                                      {versionRow?.passwallAvailableLabel ?? "не опубликовано"}
                                    </p>
                                  </div>
                                  <div className="rounded-xl border border-white/10 bg-black/10 px-3 py-3 text-sm text-slate-200">
                                    <p className="vectra-kicker text-slate-500">Xray</p>
                                    <p className="mt-1">{versionRow?.xrayInstalled ?? "нет данных"}</p>
                                    <p className="mt-1 text-xs text-slate-500">
                                      {versionRow?.xrayAvailable ?? "не опубликовано"}
                                    </p>
                                  </div>
                                </div>

                                <p className="mt-3 text-sm leading-6 text-slate-300">
                                  {outdatedLabels.length > 0
                                    ? `Нужно обновить: ${outdatedLabels.join(", ")}.`
                                    : "По stable-контуру здесь нет сигнала о старых версиях."}
                                </p>
                              </div>
                            );
                          })
                        ) : (
                          <div className="rounded-2xl border border-dashed border-white/12 bg-white/[0.03] px-4 py-6 text-sm leading-7 text-slate-400">
                            В этой группе пока нет роутеров.
                          </div>
                        )}
                      </div>

                      <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3">
                        <p className="vectra-kicker text-slate-500">Добавить роутеры</p>
                        <p className="mt-2 text-sm leading-6 text-slate-300">
                          Здесь показываются только ещё не назначенные роутеры, чтобы массовое распределение по группам было коротким и понятным.
                        </p>
                      </div>

                      <div className="space-y-3">
                        {availableRoutersForAdd.length > 0 ? (
                          availableRoutersForAdd.map((router) => {
                            const selected = selectedRouterIds.includes(router.id);

                            return (
                              <label
                                key={router.id}
                                className={`flex cursor-pointer items-start gap-3 rounded-2xl border px-4 py-4 transition ${
                                  selected
                                    ? "border-sky-400/30 bg-sky-500/10"
                                    : "border-white/10 bg-[var(--vectra-panel-soft)] hover:border-white/20"
                                }`}
                              >
                                <input
                                  type="checkbox"
                                  checked={selected}
                                  onChange={(event) =>
                                    setSelectedRouterIds((current) =>
                                      event.target.checked
                                        ? [...new Set([...current, router.id])]
                                        : current.filter((id) => id !== router.id),
                                    )
                                  }
                                  className="mt-1"
                                />
                                <div className="min-w-0">
                                  <p className="text-sm font-semibold text-white">{router.displayName}</p>
                                  <p className="mt-1 text-sm leading-6 text-slate-400">
                                    {router.hostname ?? router.deviceIdentifier}
                                  </p>
                                  <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-300">
                                    <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1">
                                      {router.importState} · {router.status}
                                    </span>
                                    <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1">
                                      {router.supportTitle}
                                    </span>
                                  </div>
                                </div>
                              </label>
                            );
                          })
                        ) : (
                          <div className="rounded-2xl border border-dashed border-white/12 bg-white/[0.03] px-4 py-6 text-sm leading-7 text-slate-400">
                            Свободных роутеров сейчас нет.
                          </div>
                        )}
                      </div>

                      <ActionStrip justify="start">
                        <button
                          type="button"
                          disabled={selectedRouterIds.length === 0 || assignMutation.isPending}
                          onClick={() =>
                            assignMutation.mutate({
                              routerIds: selectedRouterIds,
                              groupId: selectedGroup.id,
                            })
                          }
                          className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          Добавить выбранные в группу
                        </button>
                        <span className="text-sm text-slate-400">
                          Выбрано {selectedRouterIds.length}
                        </span>
                        <button
                          type="button"
                          disabled={!selectedGroup.rolloutProfileId || rolloutMutation.isPending}
                          onClick={() =>
                            rolloutMutation.mutate({
                              groupId: selectedGroup.id,
                              mode: "draft_only",
                              note: groupNote.trim() || undefined,
                            })
                          }
                          className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          Подготовить черновики по группе
                        </button>
                        <button
                          type="button"
                          disabled={!selectedGroup.rolloutProfileId || rolloutMutation.isPending}
                          onClick={() =>
                            rolloutMutation.mutate({
                              groupId: selectedGroup.id,
                              mode: "queue_apply",
                              note: groupNote.trim() || undefined,
                            })
                          }
                          className="vectra-button-primary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          Разослать группе и поставить apply
                        </button>
                        <input
                          value={groupNote}
                          onChange={(event) => setGroupNote(event.target.value)}
                          className="vectra-field w-full px-3 py-2 text-sm text-white sm:min-w-[260px] sm:flex-1"
                          placeholder="Комментарий к групповому rollout"
                        />
                      </ActionStrip>
                    </div>
                  ) : null}
                </div>
              </Panel>
            </div>
          ) : null}
        </div>
      </Panel>
    </div>
  );
}
