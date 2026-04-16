"use client";

import { useMemo, useState } from "react";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import {
  compareControllerVersions,
  formatControllerVersion,
  normalizeControllerVersion,
} from "~/lib/controller-version";
import { api, type RouterOutputs } from "~/trpc/react";

type RouterTerminalHistory = RouterOutputs["terminal"]["history"];
type RouterTerminalHistoryItem = RouterTerminalHistory["history"][number];

const timeoutOptions = [15, 30, 60, 90, 120];

export function RouterTerminalSection({
  routerId,
  routerReachable,
  controllerVersion,
  minimumControllerVersion,
}: {
  routerId: string;
  routerReachable: boolean;
  controllerVersion?: string | null;
  minimumControllerVersion: string;
}) {
  const utils = api.useUtils();
  const [command, setCommand] = useState("ubus call system board");
  const [timeoutSeconds, setTimeoutSeconds] = useState(30);

  const terminalSupported = useMemo(
    () =>
      supportsTerminalFeature(
        controllerVersion ?? null,
        minimumControllerVersion,
      ),
    [controllerVersion, minimumControllerVersion],
  );

  const history = api.terminal.history.useQuery(
    { routerId },
    { enabled: terminalSupported },
  );
  const queueMutation = api.terminal.queueCommand.useMutation({
    onSuccess: async () => {
      await utils.terminal.history.invalidate({ routerId });
    },
  });

  const trimmedCommand = command.trim();
  const activeRequest = history.data?.activeRequest ?? null;
  const latestResult = history.data?.latestResult ?? null;

  if (!terminalSupported) {
    return (
      <section className="rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
        <h3 className="text-base font-semibold text-white">Терминал роутера</h3>
        <p className="mt-2 text-sm leading-7 text-slate-300">
          One-shot shell-команды появятся после обновления controller-agent до{" "}
          {minimumControllerVersion} или новее. Сейчас роутер сообщает версию{" "}
          {formatControllerVersion(controllerVersion)}.
        </p>
      </section>
    );
  }

  return (
    <section className="space-y-4 rounded-lg border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-base font-semibold text-white">
            Терминал роутера
          </h3>
          <p className="mt-1 text-sm text-slate-400">
            Команда выполняется как one-shot shell через controller-agent, а не
            как live SSH/TTY.
          </p>
        </div>
        <span className="rounded-full border border-white/10 bg-[rgba(11,14,20,0.78)] px-3 py-1 text-xs font-semibold tracking-[0.14em] text-slate-200 uppercase">
          root / sh -c
        </span>
      </div>

      <div className="rounded-lg border border-amber-400/20 bg-amber-500/10 px-4 py-3 text-sm leading-6 text-amber-50">
        Команда и вывод сохраняются в истории панели. Не запускайте здесь команды с секретами. Если роутер офлайн, запрос дождётся следующего check-in.
        {!routerReachable ? " Сейчас устройство не на связи." : ""}
      </div>

      <label className="block">
        <span className="vectra-kicker text-slate-500">Команда</span>
        <textarea
          name="router-terminal-command"
          rows={4}
          value={command}
          onChange={(event) => setCommand(event.target.value)}
          placeholder="ubus call system board"
          className="mt-2 w-full rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-sm leading-6 font-[var(--font-vectra-mono)] text-slate-100 transition outline-none focus:border-[var(--vectra-line-strong)]"
        />
      </label>

      <ActionStrip justify="start">
        <label className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[rgba(11,14,20,0.78)] px-3 py-2 text-sm text-slate-200">
          <span>Таймаут</span>
          <select
            name="router-terminal-timeout"
            value={String(timeoutSeconds)}
            onChange={(event) => setTimeoutSeconds(Number(event.target.value))}
            className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-2 py-1 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
          >
            {timeoutOptions.map((value) => (
              <option key={value} value={value}>
                {value} сек
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() =>
            queueMutation.mutate({
              routerId,
              command: trimmedCommand,
              timeoutSeconds,
            })
          }
          disabled={
            queueMutation.isPending ||
            trimmedCommand.length === 0 ||
            Boolean(activeRequest)
          }
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {queueMutation.isPending ? "Ставлю команду..." : "Выполнить команду"}
        </button>
        <button
          type="button"
          onClick={() => history.refetch()}
          disabled={history.isFetching}
          className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.78)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          Обновить историю
        </button>
      </ActionStrip>

      {queueMutation.error ? (
        <div className="rounded-md border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-4 py-3 text-sm leading-7 text-rose-100">
          {queueMutation.error.message}
        </div>
      ) : null}

      {history.error ? (
        <div className="rounded-md border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-4 py-3 text-sm leading-7 text-rose-100">
          Не удалось загрузить историю терминала: {history.error.message}
        </div>
      ) : null}

      {activeRequest ? (
        <section className="rounded-lg border border-white/10 bg-[rgba(11,14,20,0.78)] px-4 py-4">
          <h4 className="text-sm font-semibold text-white">Активная команда</h4>
          <pre className="mt-3 overflow-x-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
            {activeRequest.request.command || "команда не указана"}
          </pre>
          <p className="mt-3 text-sm text-slate-400">
            {formatTerminalState(activeRequest.state)} · таймаут{" "}
            {activeRequest.request.timeoutSeconds} сек · создано{" "}
            {formatDateTime(activeRequest.createdAt)}
          </p>
        </section>
      ) : null}

      {history.isLoading ? (
        <div className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.78)] px-4 py-4 text-sm text-slate-300">
          Загружаю историю terminal-команд...
        </div>
      ) : latestResult ? (
        <section className="space-y-4 rounded-lg border border-white/10 bg-[rgba(11,14,20,0.78)] px-4 py-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h4 className="text-sm font-semibold text-white">
                Последний результат
              </h4>
              <p className="mt-1 text-sm text-slate-400">
                {formatDateTime(
                  latestResult.finishedAt ?? latestResult.completedAt,
                )}{" "}
                · {formatTerminalResultMeta(latestResult)}
              </p>
            </div>
            <span
              className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.14em] uppercase ${
                latestResult.resultStatus === "failure"
                  ? "border-rose-400/30 bg-rose-500/10 text-rose-100"
                  : "border-emerald-400/30 bg-emerald-500/10 text-emerald-100"
              }`}
            >
              {latestResult.resultStatus === "failure" ? "ошибка" : "успешно"}
            </span>
          </div>

          <pre className="overflow-x-auto rounded-md border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3 text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
            {latestResult.request.command}
          </pre>

          {latestResult.error ? (
            <pre className="overflow-x-auto rounded-md border border-rose-400/20 bg-[rgba(38,12,16,0.74)] px-3 py-3 text-xs leading-6 text-rose-100">
              {latestResult.error}
            </pre>
          ) : null}

          <TerminalOutput
            label="stdout"
            value={latestResult.stdout}
            truncated={latestResult.stdoutTruncated}
          />
          <TerminalOutput
            label="stderr"
            value={latestResult.stderr}
            truncated={latestResult.stderrTruncated}
          />
        </section>
      ) : (
        <div className="rounded-md border border-dashed border-white/12 bg-[rgba(11,14,20,0.78)] px-3 py-6 text-sm leading-7 text-slate-400">
          История терминала пока пустая. Запросите первую команду сверху.
        </div>
      )}

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <p className="vectra-kicker text-slate-500">История terminal-команд</p>
          <span className="text-[11px] text-slate-500">последние one-shot запросы</span>
        </div>

        <DataTable
          columns={[
            { key: "command", label: "Команда" },
            { key: "state", label: "Статус" },
            { key: "created", label: "Создана" },
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
                  <div className="max-w-[26rem] overflow-hidden text-xs font-[var(--font-vectra-mono)] text-ellipsis whitespace-nowrap text-white">
                    {item.request.command || "команда не указана"}
                  </div>
                  <div className="text-xs text-slate-500">
                    таймаут {item.request.timeoutSeconds} сек
                  </div>
                </td>
                <td className="px-3 py-2">{formatTerminalState(item.state)}</td>
                <td className="px-3 py-2">{formatDateTime(item.createdAt)}</td>
                <td className="px-3 py-2">{formatTerminalResult(item)}</td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={4}>
              История terminal-команд пока пустая.
            </DataTableEmpty>
          )}
        </DataTable>
      </div>
    </section>
  );
}

function TerminalOutput({
  label,
  value,
  truncated,
}: {
  label: string;
  value: string | null;
  truncated: boolean;
}) {
  return (
    <section className="rounded-lg border border-white/10 bg-[rgba(6,8,12,0.92)] px-3 py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs font-semibold tracking-[0.14em] text-slate-300 uppercase">
          {label}
        </p>
        {truncated ? (
          <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-2 py-1 text-[11px] font-semibold tracking-[0.14em] text-amber-100 uppercase">
            truncated
          </span>
        ) : null}
      </div>
      <pre className="mt-3 overflow-x-auto text-xs leading-6 font-[var(--font-vectra-mono)] text-slate-200">
        {value && value.length > 0 ? value : "Нет вывода."}
      </pre>
    </section>
  );
}

function supportsTerminalFeature(
  currentVersion: string | null,
  minimumVersion: string,
) {
  if (!normalizeControllerVersion(currentVersion)) {
    return false;
  }

  return (compareControllerVersions(currentVersion, minimumVersion) ?? -1) >= 0;
}

function formatTerminalState(state: RouterTerminalHistoryItem["state"]) {
  switch (state) {
    case "queued":
      return "в очереди";
    case "delivered":
    case "running":
      return "выполняется";
    case "succeeded":
      return "завершена";
    case "failed":
      return "с ошибкой";
    case "cancelled":
      return "отменена";
    default:
      return state;
  }
}

function formatTerminalResult(item: RouterTerminalHistoryItem) {
  if (item.resultStatus === "success") {
    return formatTerminalResultMeta(item);
  }
  if (item.resultStatus === "failure") {
    return item.error ?? formatTerminalResultMeta(item);
  }
  if (item.resultStatus === "accepted") {
    return "принята агентом";
  }
  return "ожидает";
}

function formatTerminalResultMeta(item: RouterTerminalHistoryItem) {
  const fragments: string[] = [];
  if (item.timedOut) {
    fragments.push("таймаут");
  }
  if (item.exitCode !== null) {
    fragments.push(`exit ${item.exitCode}`);
  }
  if (typeof item.durationMs === "number") {
    fragments.push(`${(item.durationMs / 1000).toFixed(1)} сек`);
  }
  return fragments.length > 0 ? fragments.join(" · ") : "результат получен";
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
