"use client";

import { Activity, HeartPulse, ScrollText, ShieldAlert } from "lucide-react";

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
import { api } from "~/trpc/react";

export interface DiagnosticsTabProps {
  routerId: string;
  initialSurface: RouterDetailEditorSurface;
}

function serviceTone(status: string | null | undefined): Tone {
  if (!status) {
    return "neutral";
  }
  const normalized = status.toLowerCase();
  if (["running", "active", "ok", "up"].includes(normalized)) {
    return "good";
  }
  if (["stopped", "inactive", "down", "failed", "error"].includes(normalized)) {
    return "critical";
  }
  return "warning";
}

function severityTone(severity: string): Tone {
  switch (severity) {
    case "critical":
      return "critical";
    case "warning":
      return "warning";
    default:
      return "info";
  }
}

function formatDateTime(value: string | Date | null | undefined): string {
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
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function DiagnosticsTab({ routerId, initialSurface }: DiagnosticsTabProps) {
  const surfaceQuery = api.draft.editorSurface.useQuery(
    { routerId },
    { initialData: initialSurface, refetchOnWindowFocus: false },
  );
  const surface = surfaceQuery.data ?? initialSurface;
  const inventory = surface.inventory;

  const memory = describeRouterMemory(inventory.resources ?? null);
  const services: Array<{ label: string; status: string | null | undefined }> =
    [
      { label: "Controller", status: inventory.serviceHealth?.controller },
      { label: "PassWall", status: inventory.serviceHealth?.passwall },
      { label: "dnsmasq", status: inventory.serviceHealth?.dnsmasq },
    ];
  const safetyEvents = inventory.safetyEvents ?? [];
  const taskLog = surface.managementTaskLog;

  return (
    <div className="flex flex-col gap-4 xl:grid xl:grid-cols-2 xl:items-start">
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <HeartPulse className="h-4 w-4" strokeWidth={1.75} />
            Состояние сервисов
          </CardTitle>
          <CardDescription>
            Что контроллер видит по последнему check-in.
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-y divide-border/40 py-0">
          {services.map((service) => (
            <div
              key={service.label}
              className="flex items-center justify-between gap-3 py-2.5 text-sm"
            >
              <span className="text-foreground">{service.label}</span>
              <ToneBadge tone={serviceTone(service.status)} dot>
                {service.status ?? "нет данных"}
              </ToneBadge>
            </div>
          ))}
          <div className="flex items-center justify-between gap-3 py-2.5 text-sm">
            <div>
              <span className="text-foreground">RAM</span>
              <p className="text-xs text-muted-foreground">{memory.summary}</p>
            </div>
            <ToneBadge
              tone={
                memory.level === "good"
                  ? "good"
                  : memory.level === "warning"
                    ? "warning"
                    : memory.level === "critical"
                      ? "critical"
                      : "neutral"
              }
              dot
            >
              {memory.label}
            </ToneBadge>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldAlert className="h-4 w-4" strokeWidth={1.75} />
            События безопасности
          </CardTitle>
          <CardDescription>
            Safety-события controller-agent на роутере.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 px-3 pb-3 sm:px-6">
          {safetyEvents.length === 0 ? (
            <EmptyState
              icon={ShieldAlert}
              title="Событий нет"
              description="Контроллер не зафиксировал safety-событий."
            />
          ) : (
            safetyEvents.map((event, index) => (
              <div
                key={`${event.type}-${event.observedAt}-${index}`}
                className="rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <ToneBadge tone={severityTone(event.severity)}>
                    {event.severity}
                  </ToneBadge>
                  <span className="font-medium text-foreground">
                    {event.type}
                  </span>
                  {event.component ? (
                    <span className="text-xs text-muted-foreground">
                      · {event.component}
                    </span>
                  ) : null}
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {event.message}
                </p>
                <p className="mt-1 text-[11px] uppercase tracking-wider text-muted-foreground">
                  {formatDateTime(event.observedAt)}
                </p>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      <Card className="xl:col-span-2">
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <ScrollText className="h-4 w-4" strokeWidth={1.75} />
            Журнал задач
          </CardTitle>
          <CardDescription>
            Panel-issued задачи controller / PassWall / reboot и их статус.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 px-3 pb-3 sm:px-6">
          {taskLog.length === 0 ? (
            <EmptyState
              icon={Activity}
              title="Задач пока не было"
              description="В окне истории нет panel-issued задач для этого роутера."
            />
          ) : (
            taskLog.map((item) => {
              const tone: Tone = item.deliveryBlocked
                ? "warning"
                : item.resultStatus === "failure" || item.jobState === "failed"
                  ? "critical"
                  : ["queued", "delivered", "running"].includes(item.jobState)
                    ? "info"
                    : item.resultStatus === null
                      ? "neutral"
                      : "good";
              return (
                <div
                  key={item.jobId}
                  className="flex items-start justify-between gap-3 rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm"
                >
                  <div className="min-w-0 space-y-1">
                    <p className="truncate font-medium text-foreground">
                      {item.label}
                    </p>
                    <p className="line-clamp-2 text-xs text-muted-foreground">
                      {item.summary}
                    </p>
                    <p className="text-[11px] uppercase tracking-wider text-muted-foreground">
                      {formatDateTime(item.reportedAt ?? item.createdAt)}
                    </p>
                  </div>
                  <ToneBadge tone={tone}>{item.jobState}</ToneBadge>
                </div>
              );
            })
          )}
        </CardContent>
      </Card>
    </div>
  );
}
