import Link from "next/link";
import type { ReactNode } from "react";

import { env } from "~/env";
import { CopyTextButton } from "~/components/copy-text-button";
import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";
import {
  ax3000tEnrollmentPreset,
  buildAx3000tBaselineUrl,
  buildAx3000tBootstrapCommand,
  buildAx3000tBootstrapScript,
  buildAx3000tBootstrapScriptUrl,
  buildAx3000tFeedUrl,
  buildAx3000tShuntRebindCommand,
  buildAx3000tShuntRebindScriptUrl,
  DEFAULT_ARTIFACT_BASE_URL,
  DEFAULT_CONTROL_DOMAIN,
  DEFAULT_ROUTER_API_BASE_URL,
} from "~/app/enrollment/install-presets";

const installHighlights = [
  "fresh / upgrade / repair выбираются автоматически",
  "preflight останавливает установку до изменений, если стек не помещается",
  "существующие subscribe_list и приватные ноды старается сохранить",
  "optional-компоненты вроде sing-box и hysteria не ставятся по умолчанию",
] as const;

const nextActionSteps = [
  {
    step: "1",
    title: "Запустите bootstrap",
    body: "Команду выше выполняете на роутере.",
  },
  {
    step: "2",
    title: "Откройте роутер в «Парке»",
    body: "После первого check-in устройство появится в панели.",
  },
  {
    step: "3",
    title: "Примите import как эталон",
    body: "Только если текущая конфигурация вас устраивает.",
  },
] as const;

export default function EnrollmentPage() {
  const controlDomain =
    env.VECTRA_DEFAULT_CONTROL_DOMAIN ?? DEFAULT_CONTROL_DOMAIN;
  const routerApiBase =
    env.VECTRA_ROUTER_API_BASE_URL ?? DEFAULT_ROUTER_API_BASE_URL;
  const artifactBase =
    env.VECTRA_ARTIFACT_BASE_URL ?? DEFAULT_ARTIFACT_BASE_URL;
  const feedUrl = buildAx3000tFeedUrl(artifactBase);
  const baselineUrl = buildAx3000tBaselineUrl(controlDomain);
  const bootstrapScriptUrl = buildAx3000tBootstrapScriptUrl(controlDomain);
  const quickCommand = buildAx3000tBootstrapCommand(controlDomain);
  const shuntRebindScriptUrl = buildAx3000tShuntRebindScriptUrl(controlDomain);
  const shuntRebindCommand = buildAx3000tShuntRebindCommand(controlDomain);
  const bootstrapScript = buildAx3000tBootstrapScript({
    controlDomain,
    routerApiBase,
    artifactBase,
  });

  return (
    <section className="min-w-0 space-y-4">
      <PageHeader
        eyebrow="Установка"
        title="Подключение роутера"
        description="Bootstrap-команда и ближайшие шаги вынесены на первый экран. Технические материалы остаются ниже по раскрытию."
        mobileDescription="Bootstrap и ближайшие шаги."
        compact
      />

      <div className="min-w-0 grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
        <div className="min-w-0 overflow-hidden">
          <Panel
            eyebrow="Bootstrap"
            title="Команда для роутера"
            tone="hero"
            aside={
              <CopyTextButton text={quickCommand} label="Копировать команду" />
            }
          >
            <div className="space-y-4 min-w-0">
            <div className="flex flex-wrap gap-2 text-slate-400">
              <span className="vectra-chip rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-2 py-1">
                {ax3000tEnrollmentPreset.architecture}
              </span>
              <span className="vectra-chip rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-2 py-1">
                shunt rules: {ax3000tEnrollmentPreset.shuntRuleCount}
              </span>
              <span className="vectra-chip rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-2 py-1">
                myshunt: {ax3000tEnrollmentPreset.sourceShuntRemark}
              </span>
            </div>

            <pre className="w-full max-w-full overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] text-slate-100 [overflow-wrap:anywhere]">
              <code className="whitespace-pre-wrap break-all [overflow-wrap:anywhere]">
                {quickCommand}
              </code>
            </pre>

            <div className="flex flex-wrap gap-2">
              <Link
                href="/fleet"
                className="vectra-button-primary px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
              >
                Открыть Парк
              </Link>
              <a
                href={bootstrapScriptUrl}
                className="vectra-button-secondary px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
              >
                Открыть bootstrap-скрипт
              </a>
            </div>
            </div>
          </Panel>
        </div>

        <div className="min-w-0 overflow-hidden">
          <Panel eyebrow="После bootstrap" title="Следующие действия" tone="muted">
            <div className="space-y-3 min-w-0">
            {nextActionSteps.map((item) => (
              <div
                key={item.step}
                className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2.5"
              >
                <div className="flex items-start gap-3">
                  <span className="inline-flex h-7 w-7 items-center justify-center rounded-full border border-white/10 bg-black/10 text-sm font-semibold text-white">
                    {item.step}
                  </span>
                  <div>
                    <p className="text-sm font-medium text-white">
                      {item.title}
                    </p>
                    <p className="mt-2 text-sm leading-6 text-slate-300">
                      {item.body}
                    </p>
                  </div>
                </div>
              </div>
            ))}

            <div className="rounded-md border border-amber-400/20 bg-amber-500/10 px-3 py-2.5 text-sm leading-6 text-amber-100">
              <strong className="text-white">«Принять import как эталон»</strong>{" "}
              нажимайте только если текущее состояние роутера действительно
              должно стать стартовой базой.
            </div>

            <div className="flex flex-wrap gap-2">
              <Link
                href="/fleet"
                className="vectra-button-secondary px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
              >
                Перейти в Парк
              </Link>
              <Link
                href="/updates"
                className="vectra-button-secondary px-3 py-2 text-sm font-medium text-white transition hover:border-white/20"
              >
                Открыть массовую рассылку
              </Link>
            </div>
            </div>
          </Panel>
        </div>
      </div>

      <Panel eyebrow="Утилиты" title="Технические материалы" tone="muted">
        <div className="space-y-3">
          <DisclosureSection
            title="Что именно делает bootstrap"
            summary="Коротко про режимы, preflight и то, что не ставится по умолчанию."
            defaultOpen
          >
            <ul className="space-y-2 text-sm leading-7 text-slate-300">
              {installHighlights.map((item) => (
                <li key={item} className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
                  {item}
                </li>
              ))}
            </ul>
          </DisclosureSection>

          <DisclosureSection
            title="После своей подписки: вернуть привязки myshunt"
            summary="Команда и вспомогательный скрипт для возврата shunt-target привязок после импорта своей подписки."
          >
            <div className="min-w-0 grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(300px,0.8fr)]">
              <div className="min-w-0 space-y-4 text-sm leading-7 text-slate-300">
                <div className="flex flex-wrap gap-2">
                  <CopyTextButton
                    text={shuntRebindCommand}
                    label="Копировать команду"
                  />
                </div>

                <p>
                  В baseline нет ваших реальных proxy nodes, поэтому после импорта
                  своей подписки нужно один раз вернуть target-привязки для
                  <code> myshunt</code>.
                </p>

                <pre className="w-full max-w-full overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] text-slate-100 [overflow-wrap:anywhere]">
                  <code className="whitespace-pre-wrap break-all [overflow-wrap:anywhere]">
                    {shuntRebindCommand}
                  </code>
                </pre>

                <p>
                  Скрипт ищет ноды по <code>remark</code> и восстанавливает нужные
                  назначения. Скрипт доступен по адресу{" "}
                  <a
                    href={shuntRebindScriptUrl}
                    className="break-all font-[family:var(--font-plex-mono)] text-[var(--vectra-accent)] underline decoration-white/20 underline-offset-4"
                  >
                    {shuntRebindScriptUrl}
                  </a>
                  .
                </p>
              </div>

              <div className="min-w-0 space-y-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4">
                <p className="vectra-kicker text-slate-500">Какие targets вернутся</p>
                {ax3000tEnrollmentPreset.sourceShuntTargets.map((target) => (
                  <div
                    key={target.slot}
                    className="rounded-md border border-white/10 bg-black/10 px-3 py-2"
                  >
                    <p className="text-sm font-medium text-white">{target.slot}</p>
                    <p className="mt-1 text-xs font-[family:var(--font-plex-mono)] text-slate-300">
                      {target.remark}
                    </p>
                  </div>
                ))}
              </div>
            </div>
          </DisclosureSection>

          <DisclosureSection
            title="Что установится на роутер"
            summary="Контроллер, обязательный mirrored set PassWall2 и требуемые OpenWrt runtime-пакеты."
          >
            <div className="space-y-3 text-sm leading-7 text-slate-300">
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4">
                <p className="vectra-kicker text-slate-500">Контроллер</p>
                <p className="mt-2 text-xs font-[family:var(--font-plex-mono)] text-slate-100">
                  {ax3000tEnrollmentPreset.controllerPackages.join(" ")}
                </p>
              </div>

              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4">
                <p className="vectra-kicker text-slate-500">
                  Обязательный mirrored set PassWall2
                </p>
                <p className="mt-2 text-xs text-slate-400">
                  tag {ax3000tEnrollmentPreset.passwallReleaseTag}
                </p>
                <p className="mt-2 text-xs font-[family:var(--font-plex-mono)] text-slate-100">
                  {[
                    ax3000tEnrollmentPreset.passwallAppPackage,
                    ...ax3000tEnrollmentPreset.requiredMirroredPackages.filter(
                      (pkg) => pkg !== ax3000tEnrollmentPreset.passwallAppPackage,
                    ),
                  ].join(" ")}
                </p>
              </div>

              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4">
                <p className="vectra-kicker text-slate-500">
                  OpenWrt feeds и runtime-пакеты
                </p>
                <p className="mt-2 break-all text-xs font-[family:var(--font-plex-mono)] text-slate-100">
                  {[
                    ...ax3000tEnrollmentPreset.openWrtFeedProvidedDependencies,
                    ...ax3000tEnrollmentPreset.requiredOpenWrtPackages,
                  ].join(" ")}
                </p>
              </div>

              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4">
                <p className="vectra-kicker text-slate-500">
                  Не ставятся по умолчанию
                </p>
                <p className="mt-2 break-all text-xs font-[family:var(--font-plex-mono)] text-slate-100">
                  {[
                    ...ax3000tEnrollmentPreset.optionalMirroredPackages,
                    ...ax3000tEnrollmentPreset.optionalOpenWrtPackages,
                  ].join(" ")}
                </p>
                <p className="mt-2 text-xs leading-6 text-slate-400">
                  Эти компоненты доступны как резерв, но дефолтный bootstrap от
                  них не зависит.
                </p>
              </div>
            </div>
          </DisclosureSection>

          <DisclosureSection
            title="Что уже очищено в baseline"
            summary="Стартовый конфиг взят с живого AX3000T, но из него убраны приватные данные."
          >
            <div className="space-y-3 text-sm leading-7 text-slate-300">
              <p>
                В baseline сохранены общие настройки PassWall2, DNS, App
                Update, Rule Manage и структура shunt-правил вместе с узлом{" "}
                <code>{ax3000tEnrollmentPreset.sourceShuntRemark}</code>.
              </p>
              <p>
                Из baseline удалены{" "}
                <strong>{ax3000tEnrollmentPreset.removedSubscriptions}</strong>{" "}
                подписка и{" "}
                <strong>{ax3000tEnrollmentPreset.removedProxyNodes}</strong>{" "}
                реальные прокси-ноды. Поэтому install baseline оставляет
                безопасные <code>_default</code> там, где заранее нельзя знать
                ваши node ids.
              </p>
              <p>
                Сам baseline-файл доступен по адресу{" "}
                <a
                  href={baselineUrl}
                    className="break-all font-[family:var(--font-plex-mono)] text-[var(--vectra-accent)] underline decoration-white/20 underline-offset-4"
                >
                  {baselineUrl}
                </a>
                .
              </p>
            </div>
          </DisclosureSection>

          <DisclosureSection
            title="Полный bootstrap shell"
            summary="Полный текст генерируемого bootstrap-скрипта для ручной проверки."
          >
              <div className="min-w-0 space-y-3">
                <CopyTextButton text={bootstrapScript} label="Копировать скрипт" />
              <pre className="max-h-[36rem] w-full max-w-full overflow-auto whitespace-pre-wrap break-all rounded-md border border-white/10 bg-black/30 p-4 text-[12px] leading-6 font-[family:var(--font-plex-mono)] text-slate-100 [overflow-wrap:anywhere]">
                <code className="whitespace-pre-wrap break-all [overflow-wrap:anywhere]">
                  {bootstrapScript}
                </code>
              </pre>
            </div>
          </DisclosureSection>

          <DisclosureSection
            title="Рабочие адреса"
            summary="Куда смотрит операторская панель, router API и artifact storage."
          >
            <div className="min-w-0 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-200">
                <p className="vectra-kicker text-slate-500">Панель оператора</p>
                <p className="mt-3 break-all text-sm font-[family:var(--font-plex-mono)] text-white">
                  {controlDomain}
                </p>
              </div>
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-200">
                <p className="vectra-kicker text-slate-500">API для роутеров</p>
                <p className="mt-3 break-all text-sm font-[family:var(--font-plex-mono)] text-white">
                  {routerApiBase}
                </p>
              </div>
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-200">
                <p className="vectra-kicker text-slate-500">Адрес артефактов</p>
                <p className="mt-3 break-all text-sm font-[family:var(--font-plex-mono)] text-white">
                  {artifactBase}
                </p>
              </div>
              <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-200">
                <p className="vectra-kicker text-slate-500">AX3000T feed</p>
                <p className="mt-3 break-all text-sm font-[family:var(--font-plex-mono)] text-white">
                  {feedUrl}
                </p>
              </div>
            </div>
          </DisclosureSection>
        </div>
      </Panel>
    </section>
  );
}

function DisclosureSection({
  title,
  summary,
  defaultOpen = false,
  children,
}: {
  title: string;
  summary: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  return (
      <details
        open={defaultOpen}
        className="min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-[rgba(10,14,20,0.74)]"
      >
      <summary className="cursor-pointer list-none px-4 py-3">
        <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
          <div>
            <p className="text-sm font-semibold text-white">{title}</p>
            <p className="mt-1 text-sm leading-6 text-slate-400">{summary}</p>
          </div>
          <span className="text-xs text-slate-500">показать / скрыть</span>
        </div>
      </summary>
      <div className="min-w-0 border-t border-white/10 px-4 py-4">{children}</div>
    </details>
  );
}
