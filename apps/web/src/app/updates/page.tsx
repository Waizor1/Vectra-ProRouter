import { UpdatesWorkspaceClientBoundary } from "~/components/updates-workspace-client-boundary";
import { PageHeader } from "~/components/page-header";
import { UpdatesV2 } from "~/features/updates/updates-v2";
import { isUiV2 } from "~/lib/feature-flag";
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
  const latestPasswall =
    artifacts.find((artifact) => artifact.type === "passwall_bundle") ??
    artifacts.find((artifact) => artifact.type === "passwall_package");
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

export default async function UpdatesPage({
  searchParams,
}: {
  searchParams: Promise<{ ui?: string }>;
}) {
  const [artifacts, manifests, globalTemplateWorkspace, v2, params] =
    await Promise.all([
      api.update.artifacts(),
      api.update.firmwareMatrix(),
      api.update.globalTemplateWorkspace(),
      isUiV2(),
      searchParams,
    ]);
  const releaseTracks = buildReleaseTracks(artifacts, manifests);

  if (v2 && params.ui !== "v1") {
    return <UpdatesV2 artifacts={artifacts} manifests={manifests} />;
  }

  return (
    <section className="space-y-4">
      <PageHeader
        eyebrow="Обновления"
        title="Профили, группы и контроль обновлений"
        description="Baseline, группы и version-control собраны в один рабочий экран. Справочные треки и артефакты вынесены ниже."
        mobileDescription="Baseline, группы и version-control."
        compact
      />

      <UpdatesWorkspaceClientBoundary
        initialGlobalTemplateWorkspace={globalTemplateWorkspace}
      />

      <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)]">
        <summary className="cursor-pointer list-none px-4 py-3 sm:px-5">
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="vectra-kicker text-slate-500">Справка</p>
              <p className="mt-1 text-sm font-medium text-white">
                Опубликованные треки, guarded-прошивки и последние артефакты
              </p>
            </div>
            <span className="text-xs text-slate-400">раскрыть</span>
          </div>
        </summary>

        <div className="space-y-4 border-t border-white/10 px-4 py-4 sm:px-5">
          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.9fr)]">
            <div className="space-y-3">
              {releaseTracks.map((track) => (
                <div
                  key={track.lane}
                  className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p className="vectra-kicker text-slate-500">
                        {formatChannelLabel(track.channel)}
                      </p>
                      <p className="mt-2 text-sm font-semibold text-white sm:text-base">
                        {track.lane}
                      </p>
                    </div>
                    <p className="font-[family:var(--font-plex-mono)] text-sm text-slate-200">
                      {track.version}
                    </p>
                  </div>
                  <p className="mt-2 text-sm leading-6 text-slate-400">{track.scope}</p>
                </div>
              ))}
            </div>

            <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm leading-6 text-slate-300">
              Прошивки остаются отдельным guarded-путём: сначала проверка через{" "}
              <code>sysupgrade -T</code> или <code>ubus validate_firmware_image</code>,
              затем точечное действие по совместимой плате и layout. Этот блок
              остаётся справкой, а не основной зоной массовой рассылки.
            </div>
          </div>

          <div className="space-y-2">
            {artifacts.length > 0 ? (
              artifacts.slice(0, 6).map((artifact) => (
                <div
                  key={artifact.id}
                  className="flex flex-col gap-2 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3 text-sm text-slate-200 md:flex-row md:items-center md:justify-between"
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
                Артефакты пока не опубликованы. После первой публикации они появятся здесь как короткий справочный список.
              </div>
            )}
          </div>
        </div>
      </details>
    </section>
  );
}
