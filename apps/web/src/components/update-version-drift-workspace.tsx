"use client";

import { useEffect, useMemo, useState } from "react";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import {
  MobileCard,
  MobileCardField,
  MobileCardGrid,
  MobileCardList,
} from "~/components/mobile-records";
import { Panel } from "~/components/panel";
import { StatusTile } from "~/components/status-tile";
import {
  UpdateLaunchMonitorDialog,
  type UpdateLaunchMonitorEntry,
  type UpdateLaunchMonitorSession,
} from "~/components/update-launch-monitor-dialog";
import { api, type RouterOutputs } from "~/trpc/react";

type VersionDriftWorkspace = RouterOutputs["update"]["versionDriftWorkspace"];
type BulkRebootConfirmationState = {
  routerIds: string[];
  routerNames: string[];
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
  const [launchMonitorSession, setLaunchMonitorSession] =
    useState<UpdateLaunchMonitorSession | null>(null);
  const [bulkRebootConfirmation, setBulkRebootConfirmation] =
    useState<BulkRebootConfirmationState | null>(null);

  const routerDisplayNameById = useMemo(
    () =>
      new Map(
        workspace.rows.map((row) => [row.id, row.displayName] as const),
      ),
    [workspace.rows],
  );
  const monitoredJobIds = useMemo(
    () =>
      launchMonitorSession
        ? launchMonitorSession.entries.flatMap((entry) =>
            entry.jobId ? [entry.jobId] : [],
          )
        : [],
    [launchMonitorSession],
  );
  const launchProgressQuery = api.update.launchProgress.useQuery(
    { jobIds: monitoredJobIds },
    {
      enabled: monitoredJobIds.length > 0,
      refetchOnWindowFocus: false,
      refetchInterval: launchMonitorSession ? 2_500 : false,
      refetchIntervalInBackground: true,
    },
  );

  function getRouterDisplayName(routerId: string) {
    return routerDisplayNameById.get(routerId) ?? routerId;
  }

  function openLaunchMonitor(
    actionLabel: string,
    entries: UpdateLaunchMonitorEntry[],
  ) {
    setLaunchMonitorSession({
      id: crypto.randomUUID(),
      actionLabel,
      createdAt: Date.now(),
      entries,
    });
  }

  function buildQueuedMonitorEntry(args: {
    routerId: string;
    status: "queued" | "failed";
    reason: string | null;
    jobId: string | null;
  }): UpdateLaunchMonitorEntry {
    return {
      routerId: args.routerId,
      displayName: getRouterDisplayName(args.routerId),
      queueStatus: args.status,
      queueError: args.reason,
      jobId: args.jobId,
    };
  }

  function buildBulkMonitorEntries(args: {
    routerIds: string[];
    results: Array<{
      routerId: string;
      status: "queued" | "failed";
      reason: string | null;
      jobId: string | null;
    }>;
  }) {
    const resultByRouterId = new Map(
      args.results.map((result) => [result.routerId, result] as const),
    );

    return args.routerIds.map((routerId) => {
      const result = resultByRouterId.get(routerId);
      return buildQueuedMonitorEntry({
        routerId,
        status: result?.status ?? "failed",
        reason:
          result?.reason ??
          "Панель не вернула итог постановки задачи в очередь.",
        jobId: result?.jobId ?? null,
      });
    });
  }

  function buildFailedMonitorEntries(
    routerIds: string[],
    reason: string,
  ): UpdateLaunchMonitorEntry[] {
    return routerIds.map((routerId) =>
      buildQueuedMonitorEntry({
        routerId,
        status: "failed",
        reason,
        jobId: null,
      }),
    );
  }

  const bulkPasswallMutation = api.update.queueBulkPasswallPackageUpdate.useMutation({
    onSuccess: async (data, variables) => {
      openLaunchMonitor(
        "Обновление PassWall stack",
        buildBulkMonitorEntries({
          routerIds: variables.routerIds,
          results: data.results,
        }),
      );
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
    onError: (error, variables) => {
      openLaunchMonitor(
        "Обновление PassWall stack",
        buildFailedMonitorEntries(variables.routerIds, error.message),
      );
    },
  });
  const bulkXrayMutation = api.update.queueBulkXrayUpdate.useMutation({
    onSuccess: async (data, variables) => {
      openLaunchMonitor(
        "Обновление Xray",
        buildBulkMonitorEntries({
          routerIds: variables.routerIds,
          results: data.results,
        }),
      );
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
    onError: (error, variables) => {
      openLaunchMonitor(
        "Обновление Xray",
        buildFailedMonitorEntries(variables.routerIds, error.message),
      );
    },
  });
  const bulkControllerMutation = api.update.queueBulkControllerUpdate.useMutation({
    onSuccess: async (data, variables) => {
      openLaunchMonitor(
        "Обновление controller",
        buildBulkMonitorEntries({
          routerIds: variables.routerIds,
          results: data.results,
        }),
      );
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
    onError: (error, variables) => {
      openLaunchMonitor(
        "Обновление controller",
        buildFailedMonitorEntries(variables.routerIds, error.message),
      );
    },
  });
  const bulkRebootMutation = api.update.queueBulkRouterReboot.useMutation({
    onSuccess: async (data, variables) => {
      openLaunchMonitor(
        "Перезагрузка роутеров",
        buildBulkMonitorEntries({
          routerIds: variables.routerIds,
          results: data.results,
        }),
      );
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
      setSelectedRouterIds([]);
    },
    onError: (error, variables) => {
      openLaunchMonitor(
        "Перезагрузка роутеров",
        buildFailedMonitorEntries(variables.routerIds, error.message),
      );
    },
  });
  const singlePasswallMutation = api.update.queuePasswallPackageUpdate.useMutation({
    onSuccess: async (job, variables) => {
      openLaunchMonitor("Обновление PassWall stack", [
        buildQueuedMonitorEntry({
          routerId: variables.routerId,
          status: job?.id ? "queued" : "failed",
          reason: job?.id
            ? null
            : "Панель не вернула id задачи после постановки в очередь.",
          jobId: job?.id ?? null,
        }),
      ]);
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
    },
    onError: (error, variables) => {
      openLaunchMonitor("Обновление PassWall stack", [
        buildQueuedMonitorEntry({
          routerId: variables.routerId,
          status: "failed",
          reason: error.message,
          jobId: null,
        }),
      ]);
    },
  });
  const singleXrayMutation = api.update.queuePasswallPackageUpdate.useMutation({
    onSuccess: async (job, variables) => {
      openLaunchMonitor("Обновление Xray", [
        buildQueuedMonitorEntry({
          routerId: variables.routerId,
          status: job?.id ? "queued" : "failed",
          reason: job?.id
            ? null
            : "Панель не вернула id задачи после постановки в очередь.",
          jobId: job?.id ?? null,
        }),
      ]);
      await Promise.all([
        utils.update.versionDriftWorkspace.invalidate(),
        utils.fleet.monitoring.invalidate(),
      ]);
    },
    onError: (error, variables) => {
      openLaunchMonitor("Обновление Xray", [
        buildQueuedMonitorEntry({
          routerId: variables.routerId,
          status: "failed",
          reason: error.message,
          jobId: null,
        }),
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

  const filteredRouterIds = useMemo(
    () => filteredRows.map((row) => row.id),
    [filteredRows],
  );
  const selectedRows = filteredRows.filter((row) => selectedRouterIds.includes(row.id));
  const allFilteredSelected =
    filteredRouterIds.length > 0 &&
    filteredRouterIds.every((routerId) => selectedRouterIds.includes(routerId));

  function selectAllFilteredRows() {
    setSelectedRouterIds((current) => {
      const next = new Set(current);
      for (const routerId of filteredRouterIds) {
        next.add(routerId);
      }
      return [...next];
    });
  }

  function clearSelection() {
    setSelectedRouterIds([]);
  }

  function openBulkRebootConfirmation() {
    if (selectedRows.length === 0) {
      return;
    }

    setBulkRebootConfirmation({
      routerIds: selectedRows.map((row) => row.id),
      routerNames: selectedRows.map((row) => row.displayName),
    });
  }

  function confirmBulkReboot() {
    if (!bulkRebootConfirmation) {
      return;
    }

    const routerIds = bulkRebootConfirmation.routerIds;
    setBulkRebootConfirmation(null);
    bulkRebootMutation.mutate({ routerIds });
  }

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
              disabled={filteredRouterIds.length === 0 || allFilteredSelected}
              onClick={selectAllFilteredRows}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Выбрать все по фильтру
            </button>
            <button
              type="button"
              disabled={selectedRouterIds.length === 0}
              onClick={clearSelection}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Снять выбор
            </button>
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
            <button
              type="button"
              disabled={selectedRows.length === 0 || bulkRebootMutation.isPending}
              onClick={openBulkRebootConfirmation}
              className="vectra-button-danger px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
            >
              Перезагрузить выбранные роутеры
            </button>
            <span className="text-sm text-slate-400">
              Выбрано {selectedRows.length} / {filteredRows.length} по фильтру
            </span>
          </ActionStrip>

          <MobileCardList
            title="Версионный drift по парку"
            hint="Телефонный режим"
          >
            {filteredRows.length > 0 ? (
              filteredRows.map((row) => {
                const selected = selectedRouterIds.includes(row.id);

                return (
                  <MobileCard
                    key={row.id}
                    tone={selected ? "accent" : row.blocked ? "warning" : "default"}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <label className="inline-flex min-w-0 items-start gap-3">
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
                          aria-label={`Выбрать ${row.displayName}`}
                          className="mt-1"
                        />
                        <div className="min-w-0">
                          <p className="text-sm font-semibold text-white">
                            {row.displayName}
                          </p>
                          <p className="mt-1 text-xs leading-5 text-slate-400">
                            import: {row.importState} · последний check-in:{" "}
                            {formatDateTime(row.lastSeenAt)}
                          </p>
                        </div>
                      </label>
                      <span
                        className={`rounded-full border px-2.5 py-1 text-[11px] ${
                          row.blocked
                            ? "border-amber-400/25 bg-amber-500/10 text-amber-100"
                            : "border-emerald-400/25 bg-emerald-500/10 text-emerald-100"
                        }`}
                      >
                        {row.blocked ? "ограничен" : "готов"}
                      </span>
                    </div>

                    {row.blockedReason ? (
                      <p className="mt-3 text-sm leading-6 text-amber-100">
                        {row.blockedReason}
                      </p>
                    ) : null}

                    <div className="mt-3">
                      <MobileCardGrid>
                        <MobileCardField
                          label="Группа"
                          value={row.rolloutGroupName ?? "Без группы"}
                        />
                        <MobileCardField
                          label="Controller"
                          value={row.controllerInstalled}
                          detail={`${row.controllerAvailable ?? "не опубликовано"}${row.controllerNeedsUpdate ? " · отстаёт" : " · актуально"}`}
                        />
                        <MobileCardField
                          label="PassWall"
                          value={row.passwallInstalled}
                          detail={`${row.passwallAvailableLabel}${row.passwallNeedsUpdate ? " · отстаёт" : " · актуально"}`}
                        />
                        <MobileCardField
                          label="Xray"
                          value={row.xrayInstalled}
                          detail={`${row.xrayAvailable}${row.xrayNeedsUpdate ? " · отстаёт" : " · актуально"}`}
                        />
                      </MobileCardGrid>
                    </div>

                    <ActionStrip justify="start" dense>
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
                    </ActionStrip>
                  </MobileCard>
                );
              })
            ) : (
              <MobileCard>
                <p className="text-sm leading-7 text-slate-300">
                  Под текущие фильтры роутеров нет.
                </p>
              </MobileCard>
            )}
          </MobileCardList>

          <div className="max-lg:hidden">
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
        </div>
      </Panel>

      {launchMonitorSession ? (
        <UpdateLaunchMonitorDialog
          session={launchMonitorSession}
          progress={launchProgressQuery.data}
          isLoading={launchProgressQuery.isLoading}
          isFetching={launchProgressQuery.isFetching}
          onClose={() => setLaunchMonitorSession(null)}
          onRefresh={() => {
            if (monitoredJobIds.length > 0) {
              void launchProgressQuery.refetch();
            }
          }}
        />
      ) : null}

      {bulkRebootConfirmation ? (
        <BulkRebootConfirmDialog
          routerNames={bulkRebootConfirmation.routerNames}
          onClose={() => setBulkRebootConfirmation(null)}
          onConfirm={confirmBulkReboot}
          isPending={bulkRebootMutation.isPending}
        />
      ) : null}
    </div>
  );
}

function BulkRebootConfirmDialog({
  routerNames,
  onClose,
  onConfirm,
  isPending,
}: {
  routerNames: string[];
  onClose: () => void;
  onConfirm: () => void;
  isPending: boolean;
}) {
  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !isPending) {
        onClose();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [isPending, onClose]);

  const visibleRouterNames = routerNames.slice(0, 4);
  const hiddenCount = Math.max(routerNames.length - visibleRouterNames.length, 0);

  return (
    <div
      className="vectra-dialog-backdrop fixed inset-0 z-[60] bg-[rgba(4,7,11,0.72)] backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="bulk-reboot-confirm-title"
      onClick={() => {
        if (!isPending) {
          onClose();
        }
      }}
    >
      <div className="mx-auto flex h-full max-w-2xl items-center justify-center">
        <div
          className="vectra-dialog-panel w-full overflow-y-auto rounded-[24px] border border-white/10 bg-[rgba(9,12,18,0.97)] shadow-[0_28px_120px_rgba(0,0,0,0.45)] sm:rounded-[28px]"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="border-b border-white/8 px-5 py-5 sm:px-6">
            <p className="vectra-kicker text-rose-300">Подтверждение действия</p>
            <h2
              id="bulk-reboot-confirm-title"
              className="mt-2 text-xl font-semibold tracking-[-0.02em] text-white"
            >
              Поставить перезагрузку в очередь?
            </h2>
            <p className="mt-3 text-sm leading-6 text-slate-300">
              Панель создаст задачу перезагрузки для {routerNames.length} выбранных{" "}
              {routerNames.length === 1 ? "роутера" : "роутеров"}. После подтверждения
              откроется встроенное окно с живым статусом запуска и возможными ошибками.
            </p>
          </div>

          <div className="space-y-4 px-5 py-5 sm:px-6">
            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
              <p className="vectra-kicker text-slate-500">Что уйдёт в очередь</p>
              <div className="mt-3 flex flex-wrap gap-2">
                {visibleRouterNames.map((routerName, index) => (
                  <span
                    key={`${routerName}-${index}`}
                    className="rounded-full border border-white/10 bg-black/10 px-3 py-1 text-sm text-slate-200"
                  >
                    {routerName}
                  </span>
                ))}
                {hiddenCount > 0 ? (
                  <span className="rounded-full border border-white/10 bg-black/10 px-3 py-1 text-sm text-slate-400">
                    + ещё {hiddenCount}
                  </span>
                ) : null}
              </div>
            </div>

            <div className="flex flex-wrap justify-end gap-3">
              <button
                type="button"
                onClick={onClose}
                disabled={isPending}
                className="vectra-button-secondary px-4 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
              >
                Отмена
              </button>
              <button
                type="button"
                onClick={onConfirm}
                disabled={isPending}
                className="vectra-button-danger px-4 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-60"
              >
                {isPending ? "Ставлю в очередь..." : "Подтвердить перезагрузку"}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
