import { GlobalTemplateRolloutWorkspace } from "~/components/global-template-rollout-workspace";
import { OperatorWorkflowMap } from "~/components/operator-workflow-map";
import { Panel } from "~/components/panel";
import { PageHeader } from "~/components/page-header";
import { api } from "~/trpc/server";

function describeArtifactScope(name: string) {
  if (name.includes("vectra-controller")) {
    return "vectra-controller-agent и luci-app-vectra-controller";
  }
  if (name.includes("passwall")) {
    return "PassWall2 и пакетный канал";
  }
  return "Версионированный канал артефактов";
}

function formatChannelLabel(value: string) {
  switch (value) {
    case "stable":
      return "стабильный канал";
    case "beta":
      return "тестовый канал";
    case "guarded":
      return "защищённый канал";
    default:
      return value;
  }
}

function formatArtifactType(value: string) {
  switch (value) {
    case "controller":
      return "контроллер";
    case "passwall_package":
      return "пакеты PassWall2";
    case "passwall_bundle":
      return "набор пакетов PassWall2";
    case "firmware":
      return "прошивка";
    default:
      return value;
  }
}

function buildReleaseTracks(
  artifacts: Awaited<ReturnType<typeof api.update.artifacts>>,
  manifests: Awaited<ReturnType<typeof api.update.firmwareMatrix>>,
) {
  const latestController = artifacts.find(
    (artifact) => artifact.type === "controller",
  );
  const latestPasswall = artifacts.find((artifact) =>
    ["passwall_package", "passwall_bundle"].includes(artifact.type),
  );
  const latestFirmware = manifests[0];

  return [
    {
      lane: "Контроллер",
      channel: latestController?.channel ?? "stable",
      version: latestController?.version ?? "не опубликовано",
      scope: latestController
        ? describeArtifactScope(latestController.name)
        : "Ожидается первый артефакт подписанной ленты",
    },
    {
      lane: "PassWall2",
      channel: latestPasswall?.channel ?? "stable",
      version: latestPasswall?.version ?? "не опубликовано",
      scope: latestPasswall
        ? describeArtifactScope(latestPasswall.name)
        : "Ожидается первый артефакт пакетного канала",
    },
    {
      lane: "Прошивка",
      channel: latestFirmware?.channel ?? "guarded",
      version: latestFirmware?.version ?? "нет манифеста",
      scope: latestFirmware
        ? `${latestFirmware.boardName} · ${latestFirmware.layoutFamily}`
        : "Доступен только защищённый сервис манифестов",
    },
  ];
}

export default async function UpdatesPage() {
  const [artifacts, manifests, globalTemplateWorkspace] = await Promise.all([
    api.update.artifacts(),
    api.update.firmwareMatrix(),
    api.update.globalTemplateWorkspace(),
  ]);
  const releaseTracks = buildReleaseTracks(artifacts, manifests);

  return (
    <section className="space-y-4">
      <PageHeader
        eyebrow="Центр обновлений"
        title="Глобальный baseline и массовая рассылка по парку"
        description="Если нужно менять сразу несколько роутеров, делайте это здесь: общий эталон, подготовка черновиков и массовый apply."
        mobileDescription="Общий baseline и массовая рассылка."
      />

      <OperatorWorkflowMap current="updates" compact />

      <GlobalTemplateRolloutWorkspace
        initialWorkspace={globalTemplateWorkspace}
      />

      <div className="grid gap-4 md:grid-cols-3">
        {releaseTracks.map((track) => (
          <Panel
            key={track.lane}
            eyebrow={formatChannelLabel(track.channel)}
            title={track.lane}
          >
            <p className="text-2xl font-semibold text-white">{track.version}</p>
            <p className="mt-2 text-sm leading-6 text-slate-300">
              {track.scope}
            </p>
          </Panel>
        ))}
      </div>

      <Panel eyebrow="Прошивки" title="Прошивки идут отдельно">
        <div className="grid gap-4 md:grid-cols-2">
          <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-300">
            Подбирать прошивку нужно по плате, архитектуре и типу разметки.
            Stock-layout и ubootmod считаются разными устройствами.
          </div>
          <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm leading-7 text-slate-300">
            Сначала всегда делайте проверку через <code>sysupgrade -T</code>{" "}
            или <code>ubus validate_firmware_image</code>. Массового rollout
            прошивок здесь пока нет.
          </div>
        </div>
      </Panel>

      <Panel eyebrow="Артефакты" title="Опубликованные артефакты">
        <div className="space-y-3">
          {artifacts.length > 0 ? (
            artifacts.slice(0, 6).map((artifact) => (
              <div
                key={artifact.id}
                className="flex flex-col gap-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4 text-sm text-slate-200 md:flex-row md:items-center md:justify-between"
              >
                <div>
                  <p className="font-semibold text-white">{artifact.name}</p>
                  <p className="mt-1 text-slate-400">
                    Тип: {formatArtifactType(artifact.type)} · Канал:{" "}
                    {formatChannelLabel(artifact.channel)}
                  </p>
                </div>
                <div className="font-[family:var(--font-plex-mono)] text-slate-300">
                  {artifact.version}
                </div>
              </div>
            ))
          ) : (
            <div className="rounded-md border border-dashed border-white/12 bg-white/[0.03] p-4 text-sm leading-7 text-slate-300">
              Артефакты пока не опубликованы. После публикации они появятся
              здесь.
            </div>
          )}
        </div>
      </Panel>
    </section>
  );
}
