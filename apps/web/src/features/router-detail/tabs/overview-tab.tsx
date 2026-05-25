"use client";

import { useCallback, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Activity,
  AlertOctagon,
  CheckCircle2,
  Clock,
  RefreshCw,
  ScrollText,
} from "lucide-react";
import { toast } from "sonner";

import { api } from "~/trpc/react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { EmptyState } from "~/components/vectra/empty-state";
import { ToneBadge } from "~/components/vectra/tone-badge";
import { describeRouterMemory } from "~/lib/router-memory";
import type { Tone } from "~/lib/tone";

import type { RouterDetailEditorSurface } from "~/features/router-detail";

export interface OverviewTabProps {
  routerId: string;
  surface: RouterDetailEditorSurface;
  routerReachable: boolean;
  directModeActive: boolean;
  needsRecoveryAction: boolean;
}

type EventItem = {
  id: string;
  label: string;
  summary: string;
  reportedAt: Date | null;
  createdAt: Date;
  tone: Tone;
  badgeLabel: string;
};

function memoryLevelToTone(
  level: ReturnType<typeof describeRouterMemory>["level"],
): Tone {
  switch (level) {
    case "good":
      return "good";
    case "warning":
      return "warning";
    case "critical":
      return "critical";
    case "unknown":
      return "neutral";
  }
}

function formatRelative(value: Date | null | undefined): string {
  if (!value) {
    return "нет данных";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }
  const diffMs = Date.now() - date.getTime();
  if (diffMs < 0) {
    return "только что";
  }
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) {
    return `${seconds} с назад`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes} мин назад`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} ч назад`;
  }
  const days = Math.floor(hours / 24);
  return `${days} д назад`;
}

function formatDateTime(value: Date | null | undefined): string {
  if (!value) {
    return "нет данных";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function eventTone(
  item: RouterDetailEditorSurface["managementTaskLog"][number],
): { tone: Tone; label: string } {
  if (item.deliveryBlocked) {
    return { tone: "warning", label: "блокируется" };
  }
  if (
    ["queued", "delivered", "running"].includes(item.jobState) ||
    item.resultStatus === "accepted"
  ) {
    return { tone: "info", label: "в процессе" };
  }
  if (item.resultStatus === "failure" || item.jobState === "failed") {
    return { tone: "critical", label: "ошибка" };
  }
  if (item.resultStatus === null) {
    return { tone: "neutral", label: "без подтверждения" };
  }
  return { tone: "good", label: "успешно" };
}

export function OverviewTab({
  routerId,
  surface,
  routerReachable,
  directModeActive,
  needsRecoveryAction,
}: OverviewTabProps) {
  const [rebootOpen, setRebootOpen] = useState(false);
  const [reimportOpen, setReimportOpen] = useState(false);

  const router = useRouter();
  const utils = api.useUtils();
  const rebootMutation = api.update.queueRouterReboot.useMutation();
  const reimportMutation = api.fleet.requestReimport.useMutation();

  const summary = surface.routerRuntimeSummary;
  const inventory = surface.inventory;
  const memoryStatus = describeRouterMemory(inventory?.resources ?? null);
  const memoryTone = memoryLevelToTone(memoryStatus.level);

  const recentEvents: EventItem[] = surface.managementTaskLog
    .slice(0, 3)
    .map((item) => {
      const { tone, label } = eventTone(item);
      return {
        id: item.jobId,
        label: item.label,
        summary: item.summary,
        reportedAt: item.reportedAt,
        createdAt: item.createdAt,
        tone,
        badgeLabel: label,
      };
    });

  const handleRebootConfirm = useCallback(async () => {
    setRebootOpen(false);
    try {
      await rebootMutation.mutateAsync({ routerId });
      await utils.draft.editorSurface.invalidate({ routerId });
      router.refresh();
      toast.success("Перезагрузка поставлена в очередь");
    } catch (error) {
      toast.error("Не удалось перезагрузить роутер", {
        description: error instanceof Error ? error.message : undefined,
      });
    }
  }, [routerId, rebootMutation, utils, router]);

  const handleReimportConfirm = useCallback(async () => {
    setReimportOpen(false);
    try {
      await reimportMutation.mutateAsync({ routerId });
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
      ]);
      router.refresh();
      toast.success("Принудительный re-import поставлен в очередь");
    } catch (error) {
      toast.error("Не удалось запустить re-import", {
        description: error instanceof Error ? error.message : undefined,
      });
    }
  }, [routerId, reimportMutation, utils, router]);

  const passwallEnabled = summary.passwallEnabled === true;
  const passwallTone: Tone =
    summary.passwallEnabled === null
      ? "neutral"
      : passwallEnabled
        ? "good"
        : "warning";
  const passwallLabel =
    summary.passwallEnabled === null
      ? "PassWall ?"
      : passwallEnabled
        ? "PassWall on"
        : "PassWall off";

  return (
    <div className="space-y-4">
      {directModeActive || needsRecoveryAction ? (
        <Card className="border-amber-500/30 bg-amber-500/[0.04]">
          <CardContent className="flex items-start gap-3 px-4 py-3 text-sm">
            <AlertOctagon
              className="mt-0.5 h-4 w-4 shrink-0 text-amber-300"
              strokeWidth={1.75}
            />
            <div className="space-y-1">
              <p className="font-medium text-foreground">
                Нужно внимание оператора
              </p>
              <p className="text-muted-foreground">
                {directModeActive
                  ? "Роутер сейчас в direct mode и работает мимо proxy."
                  : "У роутера зафиксированы признаки rescue/recovery."}{" "}
                Действия и подробности — в Rescue.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <Activity className="h-4 w-4" strokeWidth={1.75} />
              Здоровье
            </CardTitle>
            <CardDescription>
              Что контроллер сейчас видит по последнему check-in.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <HealthRow
              label="RAM"
              value={memoryStatus.summary}
              tone={memoryTone}
              badgeLabel={memoryStatus.label}
              hint={memoryStatus.detail}
            />
            <Separator />
            <HealthRow
              label="Доступность"
              value={routerReachable ? "online" : "контроллер не видит"}
              tone={routerReachable ? "good" : "critical"}
              badgeLabel={routerReachable ? "reachable" : "unreachable"}
              hint={`Последний check-in: ${formatDateTime(summary.lastSeenAt)}`}
            />
            <Separator />
            <HealthRow
              label="Контроллер"
              value={summary.controllerVersion ?? "версия неизвестна"}
              tone={summary.controllerVersion ? "good" : "neutral"}
              badgeLabel={summary.supportTitle ?? "support ?"}
              hint={summary.supportReason ?? undefined}
            />
            <Separator />
            <HealthRow
              label="Proxy"
              value={
                summary.selectedNodeLabel ?? "узел не выбран"
              }
              tone={passwallTone}
              badgeLabel={passwallLabel}
              hint={
                summary.pendingChanges
                  ? `В очереди задач: ${summary.pendingChanges}`
                  : undefined
              }
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <ScrollText className="h-4 w-4" strokeWidth={1.75} />
              Последние события
            </CardTitle>
            <CardDescription>
              Panel-issued задачи controller / PassWall / reboot.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 px-3 pb-3 sm:px-6">
            {recentEvents.length > 0 ? (
              recentEvents.map((event) => (
                <div
                  key={event.id}
                  className="flex items-start gap-3 rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm"
                >
                  <Clock
                    className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
                    strokeWidth={1.75}
                  />
                  <div className="flex-1 min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="truncate font-medium text-foreground">
                        {event.label}
                      </p>
                      <ToneBadge tone={event.tone}>
                        {event.badgeLabel}
                      </ToneBadge>
                    </div>
                    <p className="line-clamp-2 text-xs text-muted-foreground">
                      {event.summary}
                    </p>
                    <p className="text-[11px] uppercase tracking-wider text-muted-foreground">
                      {formatRelative(event.reportedAt ?? event.createdAt)}
                    </p>
                  </div>
                </div>
              ))
            ) : (
              <EmptyState
                icon={CheckCircle2}
                title="Действий пока не было"
                description="В окне истории нет panel-issued задач для этого роутера."
              />
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">Действия</CardTitle>
          <CardDescription>
            Перезагрузка и принудительный re-import. Тяжёлые операции — через
            расширенный режим.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 sm:flex-row">
          <Button
            size="lg"
            variant="default"
            onClick={() => setRebootOpen(true)}
            disabled={!summary.destructiveActionsAllowed}
            className="sm:flex-1"
          >
            <RefreshCw className="mr-2 h-4 w-4" strokeWidth={1.75} />
            Перезагрузить роутер
          </Button>
          <Button
            size="lg"
            variant="outline"
            onClick={() => setReimportOpen(true)}
            className="sm:flex-1"
          >
            <RefreshCw className="mr-2 h-4 w-4" strokeWidth={1.75} />
            Принудительный re-import
          </Button>
        </CardContent>
      </Card>

      <AlertDialog open={rebootOpen} onOpenChange={setRebootOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Перезагрузить роутер?</AlertDialogTitle>
            <AlertDialogDescription>
              Роутер уйдёт в reboot и пропадёт из контроллера на 1–2 минуты.
              Если активна сессия пользователя, она прервётся. PassWall и
              controller-agent поднимутся автоматически после перезагрузки.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Отмена</AlertDialogCancel>
            <AlertDialogAction onClick={handleRebootConfirm}>
              Перезагрузить
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={reimportOpen} onOpenChange={setReimportOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Запустить принудительный re-import?</AlertDialogTitle>
            <AlertDialogDescription>
              Контроллер заново подтянет текущую UCI-конфигурацию с роутера и
              сделает её authoritative-снимком в панели. Полезно, если
              authoritative-снимок отстал от того, что реально на устройстве.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Отмена</AlertDialogCancel>
            <AlertDialogAction onClick={handleReimportConfirm}>
              Запустить re-import
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function HealthRow({
  label,
  value,
  tone,
  badgeLabel,
  hint,
}: {
  label: string;
  value: string;
  tone: Tone;
  badgeLabel: string;
  hint?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
      <div className="space-y-0.5">
        <p className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </p>
        <p className="text-sm text-foreground">{value}</p>
        {hint ? (
          <p className="text-xs text-muted-foreground">{hint}</p>
        ) : null}
      </div>
      <ToneBadge tone={tone} dot>
        {badgeLabel}
      </ToneBadge>
    </div>
  );
}
