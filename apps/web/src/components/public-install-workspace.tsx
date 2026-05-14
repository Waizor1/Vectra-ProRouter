"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import { CopyTextButton } from "~/components/copy-text-button";
import { Panel } from "~/components/panel";
import {
  detectHelperDesktopPlatform,
  fetchHelperHealth,
  getHelperDownloadOptions,
  installStageOrder,
  isProbablyMobileUserAgent,
  mergeChecklistDelta,
  runHelperScan,
  selectRecommendedCandidate,
  startHelperInstall,
  type HelperDesktopPlatform,
  type HelperHealthResponse,
  type HelperInstallEvent,
  type HelperScanCandidate,
  type InstallChecklistItem,
  type InstallStage,
} from "~/lib/public-install";

type HelperAvailability = "checking" | "available" | "unavailable" | "mobile";

const stageLabelMap: Record<InstallStage, string> = {
  "helper detected": "Helper найден",
  "router found": "Роутер найден",
  "ssh authenticated": "SSH авторизация",
  "bootstrap downloaded": "Bootstrap загружен",
  "packages installed": "Пакеты установлены",
  "controller running": "Controller запущен",
  "passwall verified": "PassWall2 проверен",
  completed: "Готово",
};

function helperTone(availability: HelperAvailability) {
  switch (availability) {
    case "available":
      return {
        label: "готов",
        tone: "text-emerald-300",
      };
    case "mobile":
      return {
        label: "ручной режим",
        tone: "text-amber-200",
      };
    case "unavailable":
      return {
        label: "нужен helper",
        tone: "text-amber-200",
      };
    default:
      return {
        label: "проверяем",
        tone: "text-slate-300",
      };
  }
}

function stageClassName(state: HelperInstallEvent["state"] | "idle") {
  if (state === "success") {
    return "border-emerald-400/30 bg-emerald-500/10 text-emerald-100";
  }

  if (state === "failure") {
    return "border-rose-400/30 bg-rose-500/10 text-rose-100";
  }

  if (state === "running") {
    return "border-sky-400/30 bg-sky-500/10 text-sky-100";
  }

  return "border-white/10 bg-[var(--vectra-panel-soft)] text-slate-300";
}

function parseHelperInstallEvent(raw: string): HelperInstallEvent {
  const parsed = JSON.parse(raw) as Record<string, unknown>;
  const code: HelperInstallEvent["code"] =
    parsed.code === "auth_failed" ||
    parsed.code === "host_key_mismatch" ||
    parsed.code === "router_not_found" ||
    parsed.code === "bootstrap_failed" ||
    parsed.code === "verification_failed" ||
    parsed.code === "internal_error"
      ? parsed.code
      : undefined;
  return {
    stage: String(parsed.stage) as InstallStage,
    state: String(parsed.state) as HelperInstallEvent["state"],
    message: String(parsed.message),
    timestamp: String(parsed.timestamp),
    copyableLogChunk:
      typeof parsed.copyableLogChunk === "string"
        ? parsed.copyableLogChunk
        : null,
    checklistDelta: Array.isArray(parsed.checklistDelta)
      ? (parsed.checklistDelta as InstallChecklistItem[])
      : null,
    code,
  };
}

export function PublicInstallWorkspace({
  quickCommand,
  bootstrapScriptUrl,
}: {
  quickCommand: string;
  bootstrapScriptUrl: string;
}) {
  const [helperAvailability, setHelperAvailability] =
    useState<HelperAvailability>("checking");
  const [desktopPlatform, setDesktopPlatform] =
    useState<HelperDesktopPlatform>("unknown");
  const [helperHealth, setHelperHealth] = useState<HelperHealthResponse | null>(
    null,
  );
  const [selectedCandidate, setSelectedCandidate] =
    useState<HelperScanCandidate | null>(null);
  const [scanCandidates, setScanCandidates] = useState<HelperScanCandidate[]>(
    [],
  );
  const [eventsByStage, setEventsByStage] = useState<
    Partial<Record<InstallStage, HelperInstallEvent>>
  >({});
  const [checklist, setChecklist] = useState<InstallChecklistItem[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [statusMessage, setStatusMessage] = useState(
    "Проверяем, уже запущен ли helper на этом компьютере.",
  );
  const [installError, setInstallError] = useState<string | null>(null);
  const [needsManualPassword, setNeedsManualPassword] = useState(false);
  const [manualPassword, setManualPassword] = useState("");
  const [saveProfile, setSaveProfile] = useState(true);
  const [isInstalling, setIsInstalling] = useState(false);
  const [isCheckingHelper, setIsCheckingHelper] = useState(false);
  const eventSourceRef = useRef<EventSource | null>(null);

  const detectHelper = async (manualRetry = false) => {
    if (typeof navigator === "undefined") {
      return;
    }

    if (isProbablyMobileUserAgent(navigator.userAgent)) {
      setHelperAvailability("mobile");
      setHelperHealth(null);
      setStatusMessage(
        "На mobile one-click через helper не работает. Используйте короткую команду ниже.",
      );
      return;
    }

    setIsCheckingHelper(true);

    if (manualRetry) {
      setStatusMessage("Проверяем, запущен ли helper на этом компьютере.");
    }

    try {
      const health = await fetchHelperHealth();
      setHelperHealth(health);
      setHelperAvailability("available");
      setInstallError(null);
      setStatusMessage(
        `Helper ${health.version} найден. Теперь нажмите «Установить на роутер».`,
      );
    } catch {
      setHelperHealth(null);
      setHelperAvailability("unavailable");
      setStatusMessage(
        "Браузер не нашёл локальный helper. Скачайте helper, запустите его и затем нажмите «Я уже запустил helper».",
      );
    } finally {
      setIsCheckingHelper(false);
    }
  };

  useEffect(() => {
    if (typeof navigator === "undefined") {
      return;
    }

    setDesktopPlatform(
      detectHelperDesktopPlatform(navigator.userAgent, navigator.platform),
    );
    void detectHelper();
  }, []);

  useEffect(() => {
    return () => {
      eventSourceRef.current?.close();
    };
  }, []);

  const startStreaming = (sessionId: string, sessionToken: string) => {
    eventSourceRef.current?.close();

    const source = new EventSource(
      `http://127.0.0.1:38471/events/${sessionId}?token=${encodeURIComponent(sessionToken)}`,
    );
    eventSourceRef.current = source;

    source.onmessage = (message) => {
      if (typeof message.data !== "string") {
        return;
      }

      const payload = parseHelperInstallEvent(message.data);
      setEventsByStage((current) => ({
        ...current,
        [payload.stage]: payload,
      }));
      setStatusMessage(payload.message);

      if (
        typeof payload.copyableLogChunk === "string" &&
        payload.copyableLogChunk.length > 0
      ) {
        const nextLogChunk = payload.copyableLogChunk;
        setLogs((current) => [...current, nextLogChunk]);
      }

      if (payload.checklistDelta) {
        setChecklist((current) =>
          mergeChecklistDelta(current, payload.checklistDelta),
        );
      }

      if (payload.state === "failure") {
        setIsInstalling(false);
        setInstallError(payload.message);
        setNeedsManualPassword(payload.code === "auth_failed");
        source.close();
      } else if (payload.stage === "completed" && payload.state === "success") {
        setIsInstalling(false);
        setNeedsManualPassword(false);
        setInstallError(null);
        source.close();
      }
    };

    source.onerror = () => {
      setIsInstalling(false);
      setInstallError(
        "Связь с локальным helper прервалась до завершения установки. Журнал уже сохранён на странице и его можно скопировать.",
      );
      source.close();
    };
  };

  const runInstallAttempt = async (passwordOverride?: string) => {
    if (!helperHealth) {
      setInstallError("Сначала нужно запустить helper на этом компьютере.");
      return;
    }

    setInstallError(null);
    setNeedsManualPassword(false);
    setIsInstalling(true);
    setEventsByStage({});
    setChecklist([]);
    setLogs([]);

    try {
      const scan = await runHelperScan(helperHealth.sessionToken);
      setScanCandidates(scan.candidates);

      const nextTarget =
        selectedCandidate ??
        selectRecommendedCandidate(scan) ??
        scan.candidates[0] ??
        null;

      if (!nextTarget) {
        setSelectedCandidate(null);
        setInstallError(
          "Роутер по ожидаемым адресам не найден. Проверьте Wi-Fi и используйте публичный bootstrap ниже, если helper не должен участвовать в этом запуске.",
        );
        setStatusMessage("Роутер не найден.");
        setIsInstalling(false);
        return;
      }

      setSelectedCandidate(nextTarget);
      setStatusMessage(
        `Работаем с ${nextTarget.ip}. fingerprint: ${nextTarget.fingerprintState}.`,
      );

      const install = await startHelperInstall({
        sessionToken: helperHealth.sessionToken,
        targetIp: nextTarget.ip,
        password: passwordOverride,
        saveProfile: Boolean(passwordOverride) && saveProfile,
      });

      startStreaming(install.sessionId, helperHealth.sessionToken);
    } catch (error) {
      setIsInstalling(false);
      setInstallError(
        error instanceof Error
          ? error.message
          : "Не удалось запустить установку через helper.",
      );
    }
  };

  const helperBadge = helperTone(helperAvailability);
  const helperDownloads = useMemo(
    () => getHelperDownloadOptions(desktopPlatform),
    [desktopPlatform],
  );
  const recommendedHelperDownload =
    desktopPlatform === "unknown" ? null : (helperDownloads[0] ?? null);
  const extraHelperDownloads = recommendedHelperDownload
    ? helperDownloads.filter(
        (download) => download.id !== recommendedHelperDownload.id,
      )
    : helperDownloads;
  const launcherLabel =
    recommendedHelperDownload?.launcher ?? "файл запуска helper";
  const assembledLog = logs.join("\n");
  const latestStageIndex = useMemo(() => {
    let lastIndex = -1;
    for (let index = installStageOrder.length - 1; index >= 0; index -= 1) {
      const stage = installStageOrder[index];
      if (stage && eventsByStage[stage]) {
        lastIndex = index;
        break;
      }
    }
    return lastIndex;
  }, [eventsByStage]);
  const hasCompletedSuccessfully = eventsByStage.completed?.state === "success";
  const hasInstallActivity =
    latestStageIndex >= 0 ||
    isInstalling ||
    Boolean(installError) ||
    needsManualPassword ||
    checklist.length > 0 ||
    logs.length > 0;

  return (
    <section className="mx-auto w-full max-w-3xl space-y-4">
      <section className="rounded-[28px] border border-white/10 bg-[rgba(9,12,18,0.92)] px-5 py-6 shadow-[var(--vectra-shadow-md)] backdrop-blur sm:px-6 sm:py-7">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <p className="vectra-kicker text-[var(--vectra-accent)]">
                Публичная установка
              </p>
              <h1 className="mt-1 text-2xl font-semibold tracking-[-0.03em] text-white sm:text-3xl">
                Установить Vectra на роутер
              </h1>
              <p className="mt-3 max-w-2xl text-sm leading-6 text-slate-300">
                {helperAvailability === "available"
                  ? "Компьютер готов. Остаётся нажать одну кнопку."
                  : helperAvailability === "mobile"
                    ? "На телефоне one-click не работает. Здесь остаётся только короткая команда."
                    : "Сначала скачайте helper, откройте его и вернитесь на эту страницу."}
              </p>
            </div>

            <div className="rounded-full border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-xs text-slate-300">
              helper:{" "}
              <span className={helperBadge.tone}>{helperBadge.label}</span>
            </div>
          </div>

          <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4 sm:px-5">
            <p className="text-sm leading-6 text-slate-300">{statusMessage}</p>

            {helperAvailability === "available" ? (
              <div className="mt-4 space-y-3">
                <button
                  type="button"
                  onClick={() => void runInstallAttempt()}
                  disabled={isInstalling}
                  className="vectra-button-primary min-h-12 w-full px-5 py-3 text-base font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {isInstalling
                    ? "Выполняем установку..."
                    : "Установить на роутер"}
                </button>
                <p className="text-xs leading-5 text-slate-400">
                  Если helper не подберёт пароль сам, страница попросит ввести
                  пароль от роутера.
                </p>
              </div>
            ) : helperAvailability === "mobile" ? (
              <div className="mt-4 space-y-3">
                <div className="flex flex-wrap gap-2">
                  <CopyTextButton
                    text={quickCommand}
                    label="Копировать команду"
                  />
                  <a
                    href={bootstrapScriptUrl}
                    className="vectra-button-secondary px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
                  >
                    Открыть скрипт
                  </a>
                </div>
                <pre className="overflow-x-auto rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] break-all whitespace-pre-wrap text-slate-100">
                  <code>{quickCommand}</code>
                </pre>
              </div>
            ) : (
              <div className="mt-4 space-y-3">
                <div className="flex flex-col gap-2 sm:flex-row">
                  {recommendedHelperDownload ? (
                    <a
                      href={recommendedHelperDownload.url}
                      download
                      className="vectra-button-primary min-h-12 flex-1 px-5 py-3 text-center text-base font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)]"
                    >
                      Скачать helper
                    </a>
                  ) : null}

                  <button
                    type="button"
                    onClick={() => void detectHelper(true)}
                    disabled={isCheckingHelper}
                    className="vectra-button-secondary min-h-12 px-4 py-3 text-sm font-medium text-white transition hover:border-white/20 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {isCheckingHelper
                      ? "Проверяем helper..."
                      : "Я уже запустил helper"}
                  </button>
                </div>

                <div className="space-y-2 text-sm leading-6 text-slate-300">
                  <p>1. Скачайте архив под свой компьютер.</p>
                  <p>2. Откройте {launcherLabel}.</p>
                  <p>3. Вернитесь сюда и нажмите «Я уже запустил helper».</p>
                </div>

                {extraHelperDownloads.length > 0 ? (
                  <details className="rounded-md border border-white/10 bg-black/20 px-3 py-3">
                    <summary className="flex min-h-11 cursor-pointer list-none items-center text-sm font-medium text-white">
                      Другие версии helper
                    </summary>
                    <div className="mt-3 flex flex-wrap gap-2">
                      {extraHelperDownloads.map((download) => (
                        <a
                          key={download.id}
                          href={download.url}
                          download
                          className="vectra-button-secondary inline-flex min-h-11 items-center px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
                        >
                          {download.label}
                        </a>
                      ))}
                    </div>
                  </details>
                ) : null}
              </div>
            )}
          </div>

          {selectedCandidate ? (
            <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
              <p className="text-sm font-medium text-white">
                Текущий роутер: {selectedCandidate.ip}
              </p>
              <p className="mt-1 text-xs text-slate-400">
                источник: {selectedCandidate.source} · SSH:{" "}
                {selectedCandidate.sshReachable ? "доступен" : "нет"} ·
                fingerprint: {selectedCandidate.fingerprintState}
              </p>
            </div>
          ) : null}

          {scanCandidates.length > 1 ? (
            <details className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
              <summary className="flex min-h-11 cursor-pointer list-none items-center text-sm font-medium text-white">
                Выбрать другой найденный роутер
              </summary>
              <div className="mt-3 flex flex-wrap gap-2">
                {scanCandidates.map((candidate) => (
                  <button
                    key={candidate.ip}
                    type="button"
                    onClick={() => setSelectedCandidate(candidate)}
                    className={`min-h-11 rounded-full border px-3 py-2 text-xs transition ${
                      selectedCandidate?.ip === candidate.ip
                        ? "border-sky-400/40 bg-sky-500/15 text-sky-100"
                        : "border-white/10 bg-black/20 text-slate-300 hover:border-white/20 hover:text-white"
                    }`}
                  >
                    {candidate.ip}
                  </button>
                ))}
              </div>
            </details>
          ) : null}

          {needsManualPassword ? (
            <div className="space-y-3 rounded-md border border-amber-400/25 bg-amber-500/10 px-3 py-3">
              <p className="text-sm font-medium text-white">
                Нужен пароль от роутера
              </p>
              <label
                className="block text-xs text-slate-300"
                htmlFor="install-password"
              >
                Пароль администратора / root
              </label>
              <input
                id="install-password"
                name="install-password"
                type="password"
                value={manualPassword}
                onChange={(event) => setManualPassword(event.target.value)}
                className="vectra-field min-h-11 w-full px-4 py-2.5 text-sm text-white placeholder:text-slate-500"
                placeholder="Введите пароль от роутера"
              />
              <label className="flex items-center gap-2 text-xs text-slate-300">
                <input
                  type="checkbox"
                  checked={saveProfile}
                  onChange={(event) => setSaveProfile(event.target.checked)}
                />
                Сохранить этот пароль локально в helper
              </label>
              <button
                type="button"
                onClick={() => void runInstallAttempt(manualPassword)}
                disabled={isInstalling || manualPassword.trim().length === 0}
                className="vectra-button-primary min-h-11 px-4 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-60"
              >
                Повторить с паролем
              </button>
            </div>
          ) : null}

          {helperAvailability !== "mobile" ? (
            <details className="rounded-md border border-white/10 bg-black/20 px-3 py-3">
              <summary className="flex min-h-11 cursor-pointer list-none items-center text-sm font-medium text-white">
                Нужна ручная команда вместо helper?
              </summary>
              <div className="mt-3 space-y-4 text-sm leading-6 text-slate-300">
                <pre className="overflow-x-auto rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] break-all whitespace-pre-wrap text-slate-100">
                  <code>{quickCommand}</code>
                </pre>

                <div className="flex flex-wrap gap-2">
                  <CopyTextButton
                    text={quickCommand}
                    label="Копировать команду"
                  />
                  <a
                    href={bootstrapScriptUrl}
                    className="vectra-button-secondary inline-flex min-h-11 items-center px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
                  >
                    Открыть bootstrap-скрипт
                  </a>
                </div>
              </div>
            </details>
          ) : null}
        </div>
      </section>

      {hasInstallActivity ? (
        <>
          <Panel eyebrow="Статус" title="Ход установки" compact>
            <div className="grid gap-2 md:grid-cols-2">
              {installStageOrder.map((stage, index) => {
                const event = eventsByStage[stage];
                const inferredState =
                  event?.state ??
                  (index <= latestStageIndex && isInstalling
                    ? "running"
                    : "idle");

                return (
                  <div
                    key={stage}
                    className={`rounded-md border px-3 py-3 ${stageClassName(inferredState)}`}
                  >
                    <p className="text-sm font-medium text-white">
                      {stageLabelMap[stage]}
                    </p>
                    <p className="mt-1 text-xs leading-5">
                      {event?.message ??
                        (stage === "completed" && hasCompletedSuccessfully
                          ? "Bootstrap завершён, роутер должен появиться в панели и ждать review."
                          : "ожидает запуска")}
                    </p>
                  </div>
                );
              })}
            </div>
          </Panel>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.9fr)]">
            <Panel
              eyebrow="Логи"
              title="Ошибки и копируемый журнал"
              compact
              aside={
                assembledLog ? (
                  <CopyTextButton text={assembledLog} label="Копировать лог" />
                ) : null
              }
            >
              <div className="space-y-3">
                {installError ? (
                  <div className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-3 text-sm text-rose-100">
                    {installError}
                  </div>
                ) : null}

                <pre className="min-h-40 overflow-x-auto rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] break-all whitespace-pre-wrap text-slate-100">
                  <code>
                    {assembledLog ||
                      "Лог появится здесь после старта helper-flow или при ошибке bootstrap."}
                  </code>
                </pre>
              </div>
            </Panel>

            <Panel eyebrow="Checklist" title="Что было подтверждено" compact>
              <div className="space-y-2">
                {checklist.length > 0 ? (
                  checklist.map((item) => (
                    <div
                      key={item.id}
                      className={`rounded-md border px-3 py-3 ${
                        item.status === "success"
                          ? "border-emerald-400/30 bg-emerald-500/10"
                          : item.status === "failure"
                            ? "border-rose-400/30 bg-rose-500/10"
                            : "border-white/10 bg-[var(--vectra-panel-soft)]"
                      }`}
                    >
                      <p className="text-sm font-medium text-white">
                        {item.label}
                      </p>
                      {item.details ? (
                        <p className="mt-1 text-xs leading-5 text-slate-300">
                          {item.details}
                        </p>
                      ) : null}
                    </div>
                  ))
                ) : (
                  <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm leading-6 text-slate-400">
                    После успешного bootstrap helper подтвердит controller,
                    PassWall2, нужные пакеты и то, что новый роутер должен ждать
                    review в панели.
                  </div>
                )}
              </div>
            </Panel>
          </div>
        </>
      ) : null}
    </section>
  );
}
