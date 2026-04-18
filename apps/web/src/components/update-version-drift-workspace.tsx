"use client";

import { useMemo, useState } from "react";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import { api, type RouterOutputs } from "~/trpc/react";

type VersionDriftWorkspace = RouterOutputs["update"]["versionDriftWorkspace"];

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

export function UpdateVersionDriftWorkspace({
  initialWorkspace,
}: {
  initialWorkspace: VersionDriftWorkspace;
}) {
  const utils = api.useUtils();
  const workspaceQuery = api.update.versionDriftWorkspace.useQuery(undefined, {
    initialData: initialWorkspace,
    refetchOnWindowFocus: false,
  });
  const workspace = workspaceQuery.data ?? initialWorkspace;
  const [selectedRouterIds, setSelectedRouterIds] = useState<string[]>([]);
  const [groupFilter, setGroupFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<"all" | "outdated" | "blocked">("all");

  const bulkPasswallMutation = api.update.queueBulkPasswallPackageUpdate.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
  });
  const bulkXrayMutation = api.update.queueBulkXrayUpdate.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
  });
  const bulkControllerMutation = api.update.queueBulkControllerUpdate.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
  });
  const singlePasswallMutation = api.update.queuePasswallPackageUpdate.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
    },
  });
  const singleXrayMutation = api.update.queuePasswallPackageUpdate.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
    },
  });

  const filteredRows = useMemo(() => {
    return workspace.rows.filter((row) => {
      const groupMatches =
        groupFilter === "all"
          ? true
          : groupFilter === "unassigned"
            ? !row.rolloutGroupId
            : row.rolloutGroupId === groupFilter;
      const statusMatches =
        statusFilter === "all"
          ? true
          : statusFilter === "blocked"
            ? row.blocked
            : row.passwallNeedsUpdate || row.xrayNeedsUpdate || row.controllerNeedsUpdate;

      return groupMatches && statusMatches;
    });
  }, [groupFilter, statusFilter, workspace.rows]);

  const selectedRows = filteredRows.filter((row) => selectedRouterIds.includes(row.id));

  return (
    <div className="space-y-4">
      <Panel eyebrow="Контроллер версий" title="Где парк отстал по Xray / PassWall / Controller" tone="hero">
        <div className="space-y-4">
          <div className="vectra-stat-grid">
            <StatusTile
              label="Outdated PassWall"
              value={String(workspace.summary.outdatedPasswallCount)}
              tone={workspace.summary.outdatedPasswallCount > 0 ? "warning" : "good"}
              compact
            />
            <StatusTile
              label="Outdated Xray"
              value={String(workspace.summary.outdatedXrayCount)}
              tone={workspace.summary.outdatedXrayCount > 0 ? "warning" : "good"}
              compact
            />
            <StatusTile
              label="Blocked"
              value={String(workspace.summary.blockedCount)}
              tone={workspace.summary.blockedCount > 0 ? "warning" : "good"}
              compact
            />
            <StatusTile
              label="Queued"
              value={String(workspace.summary.queuedCount)}
              tone={workspace.summary.queuedCount > 0 ? "warning" : "default"}
              compact
            />
          </div>

          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
            Здесь собраны роутеры, у которых версия controller, Xray или PassWall уже отстаёт от опубликованного stable-контура. Можно обновлять адресно по строке или быстро выбрать часть парка и поставить bulk jobs без перехода в каждую карточку роутера.
          </div>

          <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
            <label className="space-y-2 text-sm text-slate-300">
              <span className="vectra-kicker text-slate-500">Фильтр по группе</span>
              <select
                value={groupFilter}
                onChange={(event) => setGroupFilter(event.target.value)}
                className="vectra-field px-3 py-2 text-sm text-white"
              >
                <option value="all">Все группы</option>
                <option value="unassigned">Без группы</option>
                {Array.from(
                  new Map(
                    workspace.rows
                      .filter((row) => row.rolloutGroupId && row.rolloutGroupName)
                      .map((row) => [row.rolloutGroupId!, row.rolloutGroupName!]),
                  ),
                ).map(([id, name]) => (
                  <option key={id} value={id}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
            <label className="space-y-2 text-sm text-slate-300">
              <span className="vectra-kicker text-slate-500">Фильтр по статусу</span>
              <select
                value={statusFilter}
                onChange={(event) => setStatusFilter(event.target.value as "all" | "outdated" | "blocked")}
                className="vectra-field px-3 py-2 text-sm text-white"
              >
                <option value="all">Показать всё</option>
                <option value="outdated">Только устаревшие</option>
                <option value="blocked">Только заблокированные</option>
              </select>
            </label>
          </div>

          <ActionStrip justify="start">
            <button
              type="button"
              disabled={selectedRows.length === 0 || bulkPasswallMutation.isPending}
              onClick={() =>
                bulkPasswallMutation.mutate({
                  routerIds: selectedRows.map((row) => row.id),
                })
              }
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Обновить PassWall stack у выбранных
            </button>
            <button
              type="button"
              disabled={selectedRows.length === 0 || bulkXrayMutation.isPending}
              onClick={() =>
                bulkXrayMutation.mutate({
                  routerIds: selectedRows.map((row) => row.id),
                })
              }
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Обновить Xray у выбранных
            </button>
            <button
              type="button"
              disabled={selectedRows.length === 0 || bulkControllerMutation.isPending}
              onClick={() =>
                bulkControllerMutation.mutate({
                  routerIds: selectedRows.map((row) => row.id),
                })
              }
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Обновить controller у выбранных
            </button>
            <span className="text-sm text-slate-400">Выбрано {selectedRows.length}</span>
          </ActionStrip>

          <DataTable
            title="Версионный drift по парку"
            columns={[
              { key: "pick", label: "Выбор", className: "w-16" },
              { key: "router", label: "Роутер" },
              { key: "group", label: "Группа" },
              { key: "controller", label: "Controller" },
              { key: "passwall", label: "PassWall" },
              { key: "xray", label: "Xray" },
              { key: "actions", label: "Действия" },
            ]}
          >
            {filteredRows.length > 0 ? (
              filteredRows.map((row) => {
                const selected = selectedRouterIds.includes(row.id);

                return (
                  <tr key={row.id} className={`border-b border-white/6 ${selected ? "bg-white/[0.04]" : ""}`}>
                    <td className="px-3 py-3 align-top">
                      <input
                        type="checkbox"
                        checked={selected}
                        onChange={(event) =>
                          setSelectedRouterIds((current) =>
                            event.target.checked
                              ? [...new Set([...current, row.id])]
                              : current.filter((id) => id !== row.id),
                          )
                        }
                      />
                    </td>
                    <td className="px-3 py-3 align-top text-sm text-slate-100">
                      {row.displayName}
                      <p className="mt-1 text-xs leading-6 text-slate-500">
                        import: {row.importState} · последний check-in: {formatDateTime(row.lastSeenAt)}
                      </p>
                      {row.blockedReason ? (
                        <p className="mt-1 text-xs leading-6 text-amber-200">{row.blockedReason}</p>
                      ) : null}
                    </td>
                    <td className="px-3 py-3 align-top text-sm text-slate-300">
                      {row.rolloutGroupName ?? "Без группы"}
                    </td>
                    <td className="px-3 py-3 align-top text-sm text-slate-300">
                      <div>{row.controllerInstalled}</div>
                      <div className="mt-1 text-xs text-slate-500">
                        {row.controllerAvailable ?? "не опубликовано"}
                        {row.controllerNeedsUpdate ? " · отстаёт" : " · актуально"}
                      </div>
                    </td>
                    <td className="px-3 py-3 align-top text-sm text-slate-300">
                      <div>{row.passwallInstalled}</div>
                      <div className="mt-1 text-xs text-slate-500">
                        {row.passwallAvailableLabel}
                        {row.passwallNeedsUpdate ? " · отстаёт" : " · актуально"}
                      </div>
                    </td>
                    <td className="px-3 py-3 align-top text-sm text-slate-300">
                      <div>{row.xrayInstalled}</div>
                      <div className="mt-1 text-xs text-slate-500">
                        {row.xrayAvailable}
                        {row.xrayNeedsUpdate ? " · отстаёт" : " · актуально"}
                      </div>
                    </td>
                    <td className="px-3 py-3 align-top">
                      <div className="flex flex-col gap-2">
                        <button
                          type="button"
                          disabled={row.blocked || singlePasswallMutation.isPending}
                          onClick={() =>
                            singlePasswallMutation.mutate({
                              routerId: row.id,
                              artifactChannel: "stable",
                            })
                          }
                          className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          Обновить PassWall stack
                        </button>
                        <button
                          type="button"
                          disabled={row.blocked || singleXrayMutation.isPending}
                          onClick={() =>
                            singleXrayMutation.mutate({
                              routerId: row.id,
                              artifactChannel: "stable",
                              packages: ["xray-core"],
                            })
                          }
                          className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          Обновить Xray
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })
            ) : (
              <DataTableEmpty colSpan={7}>Под текущие фильтры роутеров нет.</DataTableEmpty>
            )}
          </DataTable>
        </div>
      </Panel>
    </div>
  );
}
