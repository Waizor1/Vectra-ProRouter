import Link from "next/link";
import {
  ArrowRight,
  Boxes,
  CircuitBoard,
  PackageOpen,
  Shield,
  ShieldCheck,
  Wrench,
} from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { ToneBadge } from "~/components/vectra/tone-badge";
import type { Tone } from "~/lib/tone";

interface Artifact {
  id: string;
  name: string;
  type: string;
  channel: string;
  version: string;
}

interface FirmwareManifest {
  channel: string;
  version: string;
  boardName: string;
  layoutFamily: string;
}

export interface UpdatesV2Props {
  artifacts: Artifact[];
  manifests: FirmwareManifest[];
}

function describeArtifactScope(name: string): string {
  if (name.includes("vectra-controller")) {
    return "vectra-controller-agent + luci-app";
  }
  if (name.includes("passwall")) {
    return "PassWall2 и пакетный канал";
  }
  return "Версионированный канал артефактов";
}

function channelTone(channel: string): Tone {
  switch (channel) {
    case "stable":
      return "good";
    case "beta":
      return "warning";
    case "guarded":
      return "info";
    default:
      return "neutral";
  }
}

function formatChannelLabel(value: string): string {
  switch (value) {
    case "stable":
      return "stable";
    case "beta":
      return "beta";
    case "guarded":
      return "guarded";
    default:
      return value;
  }
}

function formatArtifactType(value: string): string {
  switch (value) {
    case "controller":
      return "контроллер";
    case "passwall_package":
      return "пакет PassWall2";
    case "passwall_bundle":
      return "набор PassWall2";
    case "firmware":
      return "прошивка";
    default:
      return value;
  }
}

export function UpdatesV2({ artifacts, manifests }: UpdatesV2Props) {
  const latestController = artifacts.find((a) => a.type === "controller");
  const latestPasswall =
    artifacts.find((a) => a.type === "passwall_bundle") ??
    artifacts.find((a) => a.type === "passwall_package");
  const latestFirmware = manifests[0];

  return (
    <section className="mx-auto w-full max-w-6xl space-y-4">
      <header className="space-y-1">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
          Обновления
        </p>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          Updates
        </h1>
        <p className="text-sm text-muted-foreground">
          Что катим, на кого, что получится. Раскатка — шаг за шагом.
        </p>
      </header>

      <div className="grid gap-3 lg:grid-cols-3">
        <ReleaseTrackCard
          icon={CircuitBoard}
          lane="Controller"
          version={latestController?.version ?? "не опубликовано"}
          channel={latestController?.channel ?? "stable"}
          scope={
            latestController
              ? describeArtifactScope(latestController.name)
              : "Ожидается первый артефакт"
          }
          ctaHref="/updates?lane=controller"
        />
        <ReleaseTrackCard
          icon={Boxes}
          lane="PassWall2"
          version={latestPasswall?.version ?? "не опубликовано"}
          channel={latestPasswall?.channel ?? "stable"}
          scope={
            latestPasswall
              ? describeArtifactScope(latestPasswall.name)
              : "Ожидается первый артефакт"
          }
          ctaHref="/updates?lane=passwall"
        />
        <ReleaseTrackCard
          icon={ShieldCheck}
          lane="Firmware"
          version={latestFirmware?.version ?? "нет манифеста"}
          channel={latestFirmware?.channel ?? "guarded"}
          scope={
            latestFirmware
              ? `${latestFirmware.boardName} · ${latestFirmware.layoutFamily}`
              : "Защищённый guarded-канал"
          }
          ctaHref="/updates?lane=firmware"
        />
      </div>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <PackageOpen className="h-4 w-4" strokeWidth={1.75} />
            Wizard раскатки
          </CardTitle>
          <CardDescription>
            Один линейный поток: что катим → на кого → preview → запуск.
            Включается, когда выбран release-track выше или конкретный артефакт.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <WizardOutline />
          <Separator className="my-4" />
          <p className="text-xs text-muted-foreground">
            В переходный период мутации раскатки выполняются через расширенный
            режим. После Phase 6 wizard заменит legacy-workspace целиком.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button asChild size="sm">
              <Link href="/updates?ui=v1">
                Расширенный режим
                <ArrowRight className="ml-1 h-3 w-3" />
              </Link>
            </Button>
            <Button asChild size="sm" variant="outline">
              <Link href="/drafts">JSON эксперт</Link>
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Wrench className="h-4 w-4" strokeWidth={1.75} />
            Последние артефакты
          </CardTitle>
          <CardDescription>
            Опубликованные пакеты в проде. Полный каталог в Settings → Артефакты.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          {artifacts.length > 0 ? (
            artifacts.slice(0, 6).map((a) => (
              <div
                key={a.id}
                className="flex flex-col gap-2 rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm md:flex-row md:items-center md:justify-between"
              >
                <div className="min-w-0">
                  <p className="truncate font-medium text-foreground">
                    {a.name}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {formatArtifactType(a.type)}
                    <span className="mx-1.5">·</span>
                    <ToneBadge tone={channelTone(a.channel)} dot>
                      {formatChannelLabel(a.channel)}
                    </ToneBadge>
                  </p>
                </div>
                <code className="rounded bg-muted/40 px-1.5 py-0.5 font-mono text-xs text-foreground">
                  {a.version}
                </code>
              </div>
            ))
          ) : (
            <p className="rounded-md border border-dashed border-border/40 px-3 py-6 text-center text-sm text-muted-foreground">
              Артефакты пока не опубликованы.
            </p>
          )}
        </CardContent>
      </Card>
    </section>
  );
}

function ReleaseTrackCard({
  icon: Icon,
  lane,
  version,
  channel,
  scope,
  ctaHref,
}: {
  icon: typeof CircuitBoard;
  lane: string;
  version: string;
  channel: string;
  scope: string;
  ctaHref: string;
}) {
  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-base">
            <Icon className="h-4 w-4" strokeWidth={1.75} />
            {lane}
          </CardTitle>
          <ToneBadge tone={channelTone(channel)} dot>
            {formatChannelLabel(channel)}
          </ToneBadge>
        </div>
      </CardHeader>
      <CardContent className="flex-1 space-y-2">
        <code className="block truncate rounded bg-muted/40 px-1.5 py-0.5 font-mono text-xs text-foreground">
          {version}
        </code>
        <p className="text-xs text-muted-foreground">{scope}</p>
      </CardContent>
      <CardContent className="pt-0">
        <Button asChild size="sm" variant="outline" className="w-full">
          <Link href={ctaHref}>
            Раскатать
            <ArrowRight className="ml-1.5 h-3 w-3" />
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}

function WizardOutline() {
  const steps = [
    { n: 1, title: "Что катим", desc: "Пакет, controller, прошивка, шаблон" },
    { n: 2, title: "На кого", desc: "Один, группа, весь парк" },
    { n: 3, title: "Preview", desc: "Diff и список таргетов" },
    { n: 4, title: "Запуск", desc: "Confirm → live monitor" },
  ];
  return (
    <ol className="grid gap-2 md:grid-cols-4">
      {steps.map((s) => (
        <li
          key={s.n}
          className="rounded-md border border-border/40 bg-background/40 px-3 py-2.5"
        >
          <div className="flex items-center gap-2">
            <span className="grid h-5 w-5 place-items-center rounded-full bg-primary/20 font-mono text-xs font-bold text-primary">
              {s.n}
            </span>
            <Shield className="h-3 w-3 text-muted-foreground" />
          </div>
          <p className="mt-1.5 text-sm font-medium text-foreground">
            {s.title}
          </p>
          <p className="text-xs text-muted-foreground">{s.desc}</p>
        </li>
      ))}
    </ol>
  );
}
