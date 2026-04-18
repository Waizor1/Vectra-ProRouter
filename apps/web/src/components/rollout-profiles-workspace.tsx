"use client";

import { useMemo, useState } from "react";

import { passwallDesiredConfigSchema, type PasswallDesiredConfig } from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { Panel } from "~/components/panel";
import { TabBar } from "~/components/tab-bar";
import { api, type RouterOutputs } from "~/trpc/react";

type ProfilesAndGroupsWorkspace = RouterOutputs["update"]["profilesAndGroupsWorkspace"];

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  const pad = (part: number) => String(part).padStart(2, "0");
  return `${pad(date.getDate())}.${pad(date.getMonth() + 1)}.${date.getFullYear()}, ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function parseConfigInput(value: string):
  | { ok: true; config: PasswallDesiredConfig }
  | { ok: false; message: string } {
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

export function RolloutProfilesWorkspace({
  initialWorkspace,
}: {
  initialWorkspace: ProfilesAndGroupsWorkspace;
}) {
  const utils = api.useUtils();
  const workspaceQuery = api.update.profilesAndGroupsWorkspace.useQuery(undefined, {
    initialData: initialWorkspace,
    refetchOnWindowFocus: false,
  });
  const workspace = workspaceQuery.data ?? initialWorkspace;

  const [activeTab, setActiveTab] = useState<"profiles" | "groups">("profiles");
  const [selectedProfileId, setSelectedProfileId] = useState<string | null>(
    workspace.profiles[0]?.id ?? null,
  );
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(
    workspace.groups[0]?.id ?? null,
  );
  const [profileName, setProfileName] = useState(workspace.profiles[0]?.name ?? "");
  const [profileDescription, setProfileDescription] = useState(
    workspace.profiles[0]?.description ?? "",
  );
  const [profileNote, setProfileNote] = useState(workspace.profiles[0]?.note ?? "");
  const [profileJson, setProfileJson] = useState(
    workspace.profiles[0]
      ? JSON.stringify(workspace.profiles[0].rolloutConfig, null, 2)
      : JSON.stringify(passwallDesiredConfigSchema.parse({
          schemaVersion: 1,
          basicSettings: {
            main: {
              enabled: true,
              selectedNodeId: null,
              localhostProxy: true,
              clientProxy: true,
            },
            socks: {
              enabled: false,
              bindLocal: true,
              port: 1070,
            },
            dns: {
              remoteDns: "1.1.1.1",
              remoteDnsProtocol: "doh",
              remoteDnsQueryStrategy: "UseIPv4",
              remoteDnsDetour: "direct",
              directDnsQueryStrategy: "UseIP",
              remoteFakeDns: false,
              dnsRedirect: true,
              dnsHosts: [],
            },
            logging: {
              level: "error",
              logNodeTraffic: false,
            },
            shuntRules: [],
          },
          nodes: [],
          subscriptions: { items: [] },
          appUpdate: {
            binaryPaths: {
              xray: "/usr/bin/xray",
              singBox: "/usr/bin/sing-box",
              hysteria: "/usr/bin/hysteria",
              geoview: "/usr/bin/geoview",
            },
            updateStrategy: "package-first",
            targetVersions: {},
          },
          ruleManage: {
            autoUpdateGeoAssets: true,
            dayOfWeek: "7",
            updateTime: "5",
          },
        }), null, 2),
  );
  const [groupName, setGroupName] = useState(workspace.groups[0]?.name ?? "");
  const [groupDescription, setGroupDescription] = useState(
    workspace.groups[0]?.description ?? "",
  );
  const [groupProfileId, setGroupProfileId] = useState<string | null>(
    workspace.groups[0]?.rolloutProfileId ?? null,
  );
  const [selectedRouterIds, setSelectedRouterIds] = useState<string[]>([]);
  const [groupNote, setGroupNote] = useState("");

  const saveProfileMutation = api.update.saveRolloutProfile.useMutation({
    onSuccess: async () => {
      await utils.update.profilesAndGroupsWorkspace.invalidate();
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
      await utils.update.profilesAndGroupsWorkspace.invalidate();
    },
  });
  const deleteGroupMutation = api.update.deleteRouterGroup.useMutation({
    onSuccess: async () => {
      await utils.update.profilesAndGroupsWorkspace.invalidate();
      setSelectedGroupId(null);
      setSelectedRouterIds([]);
    },
  });
  const assignMutation = api.update.assignRoutersToGroup.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
  });
  const rolloutMutation = api.update.queueGroupProfileRollout.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.profilesAndGroupsWorkspace.invalidate(),
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
  const groupRouters = useMemo(
    () =>
      selectedGroupId
        ? workspace.routers.filter((router) => router.rolloutGroupId === selectedGroupId)
        : [],
    [selectedGroupId, workspace.routers],
  );
  const availableRoutersForAdd = workspace.unassignedRouters;
  const parsedProfile = parseConfigInput(profileJson);

  return (
    <div className="space-y-4">
      <Panel
        eyebrow="Группы и профили"
        title="Профили конфигурации и группы роутеров"
        tone="hero"
      >
        <div className="space-y-4">
          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
            Здесь вы ведёте reusable профили для подключённого парка и раскладываете роутеры по рабочим группам. Один профиль можно назначать нескольким группам, а у каждого роутера в группе остаётся быстрый перенос без ручной текстовой рутины.
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
                            setProfileName(profile.name);
                            setProfileDescription(profile.description ?? "");
                            setProfileNote(profile.note ?? "");
                            setProfileJson(JSON.stringify(profile.rolloutConfig, null, 2));
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

                  <label className="space-y-2 text-sm text-slate-300">
                    <span className="vectra-kicker text-slate-500">Rollout config профиля</span>
                    <textarea
                      value={profileJson}
                      onChange={(event) => setProfileJson(event.target.value)}
                      rows={22}
                      className="vectra-field min-h-[26rem] border-white/10 bg-black/30 px-3 py-3 font-[family:var(--font-plex-mono)] text-[12px] leading-6 text-slate-100"
                    />
                  </label>

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
                      {saveProfileMutation.isPending ? "Сохраняю профиль..." : selectedProfile ? "Сохранить профиль" : "Создать профиль"}
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
                        setProfileName("");
                        setProfileDescription("");
                        setProfileNote("");
                        setProfileJson(selectedProfile ? JSON.stringify(selectedProfile.rolloutConfig, null, 2) : profileJson);
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
                    return (
                      <button
                        key={group.id}
                        type="button"
                        onClick={() => {
                          setSelectedGroupId(group.id);
                          setGroupName(group.name);
                          setGroupDescription(group.description ?? "");
                          setGroupProfileId(group.rolloutProfileId ?? null);
                        }}
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
                          <span className="rounded-full border border-white/10 bg-black/10 px-2 py-1 text-[11px] text-slate-300">
                            {group.routerCount} роутеров
                          </span>
                        </div>
                        <p className="mt-3 text-xs leading-5 text-slate-500">
                          Профиль: {group.rolloutProfileName ?? "не выбран"} · обновлено {formatDateTime(group.updatedAt)}
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
                      {saveGroupMutation.isPending ? "Сохраняю группу..." : selectedGroup ? "Сохранить группу" : "Создать группу"}
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
                      onClick={() => {
                        setSelectedGroupId(null);
                        setGroupName("");
                        setGroupDescription("");
                        setGroupProfileId(null);
                      }}
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
                          Уже добавленные роутеры не появляются ниже в секции добавления. Для быстрого переноса можно выбрать устройства и переназначить их в другую группу одним действием.
                        </p>
                      </div>

                      <DataTable
                        title="Состав группы"
                        columns={[
                          { key: "router", label: "Роутер" },
                          { key: "state", label: "Состояние" },
                          { key: "support", label: "Поддержка" },
                          { key: "move", label: "Группа" },
                        ]}
                      >
                        {groupRouters.length > 0 ? (
                          groupRouters.map((router) => (
                            <tr key={router.id} className="border-b border-white/6">
                              <td className="px-3 py-3 align-top text-sm text-slate-100">
                                {router.displayName}
                                <p className="mt-1 text-xs leading-6 text-slate-500">
                                  {router.hostname ?? router.deviceIdentifier}
                                </p>
                              </td>
                              <td className="px-3 py-3 align-top text-sm text-slate-300">
                                {router.importState} · {router.status}
                              </td>
                              <td className="px-3 py-3 align-top text-sm text-slate-400">
                                {router.supportTitle}
                              </td>
                              <td className="px-3 py-3 align-top">
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
                              </td>
                            </tr>
                          ))
                        ) : (
                          <DataTableEmpty colSpan={4}>В этой группе пока нет роутеров.</DataTableEmpty>
                        )}
                      </DataTable>

                      <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3">
                        <p className="vectra-kicker text-slate-500">Добавить роутеры</p>
                        <p className="mt-2 text-sm leading-6 text-slate-300">
                          Список ниже показывает только ещё не назначенные роутеры, чтобы вы не перебирали уже разложенный парк вручную.
                        </p>
                      </div>

                      <DataTable
                        title="Свободные роутеры"
                        columns={[
                          { key: "pick", label: "Выбор", className: "w-16" },
                          { key: "router", label: "Роутер" },
                          { key: "state", label: "Состояние" },
                          { key: "support", label: "Поддержка" },
                        ]}
                      >
                        {availableRoutersForAdd.length > 0 ? (
                          availableRoutersForAdd.map((router) => {
                            const selected = selectedRouterIds.includes(router.id);
                            return (
                              <tr key={router.id} className={`border-b border-white/6 ${selected ? "bg-white/[0.04]" : ""}`}>
                                <td className="px-3 py-3 align-top">
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
                                  />
                                </td>
                                <td className="px-3 py-3 align-top text-sm text-slate-100">
                                  {router.displayName}
                                  <p className="mt-1 text-xs leading-6 text-slate-500">
                                    {router.hostname ?? router.deviceIdentifier}
                                  </p>
                                </td>
                                <td className="px-3 py-3 align-top text-sm text-slate-300">
                                  {router.importState} · {router.status}
                                </td>
                                <td className="px-3 py-3 align-top text-sm text-slate-400">
                                  {router.supportTitle}
                                </td>
                              </tr>
                            );
                          })
                        ) : (
                          <DataTableEmpty colSpan={4}>Свободных роутеров сейчас нет.</DataTableEmpty>
                        )}
                      </DataTable>

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
