"use client";

import { useState } from "react";

import type { RouterLogSource } from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import {
  MobileCard,
  MobileCardField,
  MobileCardGrid,
  MobileCardList,
} from "~/components/mobile-records";
import { RouterTerminalSection } from "~/components/router-terminal-section";
import { api, type RouterOutputs } from "~/trpc/react";

type RouterLogHistory = RouterOutputs["logs"]["history"];
type RouterLogHistoryItem = RouterLogHistory["history"][number];

const sourceOptions: Array<{ value: RouterLogSource; label: string }> = [
  { value: "all", label: "Все источники" },
  { value: "controller", label: "Vectra Controller" },
  { value: "passwall", label: "PassWall / Proxy" },
  { value: "dnsmasq", label: "dnsmasq" },
  { value: "system", label: "System Log" },
];

const lineOptions = [50, 100, 160, 200, 300, 400];

export function RouterWatchLogsSection({
  routerId,
  routerReachable,
  controllerVersion,
  minimumTerminalControllerVersion,
}: {
  routerId: string;
  routerReachable: boolean;
  controllerVersion?: string | null;
  minimumTerminalControllerVersion: string;
}) {
  const utils = api.useUtils();
  const [source, setSource] = useState<RouterLogSource>("all");
  const [lines, setLines] = useState(200);

  const history = api.logs.history.useQuery({ routerId });
  const queueMutation = api.logs.queueSnapshot.useMutation({
    onSuccess: async () => {
      await utils.logs.history.invalidate({ routerId });
    },
  });

  const activeRequest = history.data?.activeRequest ?? null;
  const latestSnapshot = history.data?.latestSnapshot ?? null;

  return (
    <div className="space-y-4">
      <RouterTerminalSection
        routerId={routerId}
        routerReachable={routerReachable}
        controllerVersion={controllerVersion}
        minimumControllerVersion={minimumTerminalControllerVersion}
      />

      <ActionStrip justify="start">
        <label className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200">
          <span>Источник</span>
          <select
            name="watch-logs-source"
            value={source}
            onChange={(event) =>
              setSource(event.target.value as RouterLogSource)
            }
            className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-2 py-1 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
          >
            {sourceOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <label className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200">
          <span>Строк</span>
          <select
            name="watch-logs-lines"
            value={String(lines)}
            onChange={(event) => setLines(Number(event.target.value))}
            className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-2 py-1 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
          >
            {lineOptions.map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() =>
            queueMutation.mutate({
              routerId,
              source,
              lines,
            })
          }
          disabled={queueMutation.isPending}
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {queueMutation.isPending
            ? "Ставлю запрос..."
            : "Запросить снимок логов"}
        </button>
        <button
          type="button"
          onClick={() => history.refetch()}
          disabled={history.isFetching}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          Обновить вкладку
        </button>
      </ActionStrip>

      <div className="rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
        Snapshot, не live tail. Если роутер сейчас офлайн, запрос останется в очереди до следующего check-in.
        {!routerReachable ? " Сейчас устройство не на связи." : ""}
      </div>

      {activeRequest ? (
        <section className="rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <h3 className="text-base font-semibold text-white">
            Активный запрос
          </h3>
          <p className="mt-2 text-sm text-slate-300">
            {formatLogState(activeRequest.state)} ·{" "}
            {formatSource(activeRequest.request.source)} ·{" "}
            {activeRequest.request.lines} строк
          </p>
          <p className="mt-1 text-sm text-slate-400">
            Создан: {formatDateTime(activeRequest.createdAt)}
          </p>
        </section>
      ) : null}

      {history.isLoading ? (
        <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4 text-sm text-slate-300">
          Загружаю историю Watch Logs...
        </div>
      ) : latestSnapshot ? (
        <section className="space-y-4 rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-base font-semibold text-white">
                Последний снимок
              </h3>
              <p className="mt-1 text-sm text-slate-400">
                {formatSource(latestSnapshot.request.source)} ·{" "}
                {latestSnapshot.request.lines} строк ·{" "}
                {latestSnapshot.collectedAt
                  ? formatDateTime(latestSnapshot.collectedAt)
                  : "время не указано"}
              </p>
            </div>
            <span
              className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.14em] uppercase ${
                latestSnapshot.resultStatus === "failure"
                  ? "border-rose-400/30 bg-rose-500/10 text-rose-100"
                  : "border-emerald-400/30 bg-emerald-500/10 text-emerald-100"
              }`}
            >
              {latestSnapshot.resultStatus === "failure" ? "ошибка" : "успешно"}
            </span>
          </div>

          {latestSnapshot.error ? (
            <pre className="overflow-x-auto rounded-md border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-3 py-3 text-xs leading-6 text-rose-100">
              {latestSnapshot.error}
              {latestSnapshot.stderr ? `\n\n${latestSnapshot.stderr}` : ""}
            </pre>
          ) : null}

          {latestSnapshot.snapshots.length > 0 ? (
            latestSnapshot.snapshots.map((snapshot) => (
              <section
                key={`${latestSnapshot.jobId}-${snapshot.id}`}
                className="rounded-lg border border-white/10 bg-[rgba(11,14,20,0.78)] px-3 py-3"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <p className="text-sm font-semibold text-white">
                      {snapshot.label}
                    </p>
                    <p className="mt-1 text-xs text-slate-500">
                      {snapshot.command}
                    </p>
                  </div>
                  {snapshot.truncated ? (
                    <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-2 py-1 text-[11px] font-semibold tracking-[0.14em] text-amber-100 uppercase">
                      truncated
                    </span>
                  ) : null}
                </div>
                <pre className="mt-3 overflow-x-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
                  {snapshot.content || "Совпадений не найдено."}
                </pre>
              </section>
            ))
          ) : (
            <div className="rounded-md border border-dashed border-white/12 bg-[rgba(11,14,20,0.78)] px-3 py-6 text-sm leading-7 text-slate-400">
              Агент выполнил запрос, но не вернул отдельных log sections.
            </div>
          )}
        </section>
      ) : (
        <div className="rounded-md border border-dashed border-white/12 bg-[var(--vectra-panel-soft)] px-3 py-6 text-sm leading-7 text-slate-400">
          Снимков логов ещё нет. Запросите первый snapshot через кнопку сверху.
        </div>
      )}

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <p className="vectra-kicker text-slate-500">История snapshot-запросов</p>
          <span className="text-[11px] text-slate-500">последние команды и результаты</span>
        </div>

        <MobileCardList
          title="История snapshot-запросов"
          hint="Телефонный режим"
        >
          {history.data?.history.length ? (
            history.data.history.map((item) => (
              <MobileCard
                key={item.jobId}
                tone={item.resultStatus === "failure" ? "danger" : "default"}
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {formatSource(item.request.source)}
                    </p>
                    <p className="mt-1 text-xs leading-5 text-slate-400">
                      {item.request.lines} строк
                    </p>
                  </div>
                  <span
                    className={`rounded-full border px-2.5 py-1 text-[11px] uppercase ${
                      item.resultStatus === "failure"
                        ? "border-rose-400/25 bg-rose-500/10 text-rose-100"
                        : item.state === "queued" ||
                            item.state === "delivered" ||
                            item.state === "running"
                          ? "border-amber-400/25 bg-amber-500/10 text-amber-100"
                          : "border-emerald-400/25 bg-emerald-500/10 text-emerald-100"
                    }`}
                  >
                    {formatLogState(item.state)}
                  </span>
                </div>

                <div className="mt-3">
                  <MobileCardGrid>
                    <MobileCardField
                      label="Создан"
                      value={formatDateTime(item.createdAt)}
                    />
                    <MobileCardField
                      label="Результат"
                      value={formatResult(item)}
                    />
                    <MobileCardField
                      label="Источник"
                      value={formatSource(item.request.source)}
                    />
                    <MobileCardField label="Job" value={item.jobId} mono />
                  </MobileCardGrid>
                </div>
              </MobileCard>
            ))
          ) : (
            <MobileCard>
              <p className="text-sm leading-7 text-slate-300">
                История запросов пока пустая.
              </p>
            </MobileCard>
          )}
        </MobileCardList>

        <div className="max-lg:hidden">
          <DataTable
            columns={[
              { key: "request", label: "Запрос" },
              { key: "state", label: "Статус" },
              { key: "created", label: "Создан" },
              { key: "result", label: "Результат" },
            ]}
          >
            {history.data?.history.length ? (
              history.data.history.map((item) => (
                <tr
                  key={item.jobId}
                  className="border-t border-white/10 text-slate-200"
                >
                  <td className="px-3 py-2">
                    <div className="font-medium text-white">
                      {formatSource(item.request.source)}
                    </div>
                    <div className="text-xs text-slate-500">
                      {item.request.lines} строк
                    </div>
                  </td>
                  <td className="px-3 py-2">{formatLogState(item.state)}</td>
                  <td className="px-3 py-2">{formatDateTime(item.createdAt)}</td>
                  <td className="px-3 py-2">{formatResult(item)}</td>
                </tr>
              ))
            ) : (
              <DataTableEmpty colSpan={4}>
                История запросов пока пустая.
              </DataTableEmpty>
            )}
          </DataTable>
        </div>
      </div>
    </div>
  );
}

function formatSource(source: RouterLogSource) {
  return (
    sourceOptions.find((option) => option.value === source)?.label ?? source
  );
}

function formatLogState(state: RouterLogHistoryItem["state"]) {
  switch (state) {
    case "queued":
      return "в очереди";
    case "delivered":
    case "running":
      return "выполняется";
    case "succeeded":
      return "завершён";
    case "failed":
      return "с ошибкой";
    case "cancelled":
      return "отменён";
    default:
      return state;
  }
}

function formatResult(item: RouterLogHistoryItem) {
  if (item.resultStatus === "success") {
    return item.collectedAt
      ? `получен ${formatDateTime(item.collectedAt)}`
      : "успешно";
  }
  if (item.resultStatus === "failure") {
    return item.error ?? "ошибка";
  }
  if (item.resultStatus === "accepted") {
    return "принят агентом";
  }
  return "ожидает";
}

function formatDateTime(value: string | null) {
  if (!value) {
    return "неизвестно";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}
