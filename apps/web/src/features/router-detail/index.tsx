"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useMemo } from "react";
import {
  ArrowLeft,
  Activity,
  Boxes,
  Code2,
  RefreshCw,
  ScrollText,
  Wrench,
} from "lucide-react";

import { Button } from "~/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "~/components/ui/tabs";
import { ToneBadge } from "~/components/vectra/tone-badge";
import { StaleBadge } from "~/components/vectra/stale-badge";
import type { Tone } from "~/lib/tone";
import type { RouterOutputs } from "~/trpc/react";

import { OverviewTab } from "~/features/router-detail/tabs/overview-tab";
import { ConfigTab } from "~/features/router-detail/tabs/config-tab";
import { NodesTab } from "~/features/router-detail/tabs/nodes-tab";
import { UpdatesTab } from "~/features/router-detail/tabs/updates-tab";
import { DiagnosticsTab } from "~/features/router-detail/tabs/diagnostics-tab";
import { JsonTab } from "~/features/router-detail/tabs/json-tab";

export type RouterDetailEditorSurface =
  RouterOutputs["draft"]["editorSurface"];

const TAB_IDS = [
  "overview",
  "config",
  "nodes",
  "updates",
  "diagnostics",
  "json",
] as const;

export type RouterDetailTabId = (typeof TAB_IDS)[number];

const TABS: ReadonlyArray<{
  id: RouterDetailTabId;
  label: string;
  icon: typeof Activity;
}> = [
  { id: "overview", label: "Обзор", icon: Activity },
  { id: "config", label: "Конфигурация", icon: Wrench },
  { id: "nodes", label: "Узлы", icon: Boxes },
  { id: "updates", label: "Обновления", icon: RefreshCw },
  { id: "diagnostics", label: "Диагностика", icon: ScrollText },
  { id: "json", label: "JSON эксперт", icon: Code2 },
];

function normalizeTab(value: string | null | undefined): RouterDetailTabId {
  return (TAB_IDS as readonly string[]).includes(value ?? "")
    ? (value as RouterDetailTabId)
    : "overview";
}

function statusToTone(
  status: RouterDetailEditorSurface["routerRuntimeSummary"]["status"],
): Tone {
  switch (status) {
    case "active":
      return "good";
    case "pending":
      return "info";
    case "direct":
    case "rescue":
      return "warning";
    case "offline":
    case "disabled":
      return "critical";
    default:
      return "neutral";
  }
}

function statusLabel(
  status: RouterDetailEditorSurface["routerRuntimeSummary"]["status"],
): string {
  switch (status) {
    case "active":
      return "active";
    case "pending":
      return "pending";
    case "offline":
      return "offline";
    case "direct":
      return "direct mode";
    case "rescue":
      return "rescue";
    case "disabled":
      return "disabled";
    default:
      return String(status);
  }
}

export interface RouterDetailV2Props {
  routerId: string;
  initialSurface: RouterDetailEditorSurface;
  routerReachable: boolean;
  directModeActive: boolean;
  needsRecoveryAction: boolean;
}

export function RouterDetailV2({
  routerId,
  initialSurface,
  routerReachable,
  directModeActive,
  needsRecoveryAction,
}: RouterDetailV2Props) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const activeTab = normalizeTab(searchParams?.get("tab"));

  const summary = initialSurface.routerRuntimeSummary;
  const tone = statusToTone(summary.status);
  const label = statusLabel(summary.status);
  const lastSeenMs = useMemo<number | null>(() => {
    if (!summary.lastSeenAt) {
      return null;
    }
    const date =
      summary.lastSeenAt instanceof Date
        ? summary.lastSeenAt
        : new Date(summary.lastSeenAt);
    if (Number.isNaN(date.getTime())) {
      return null;
    }
    return Date.now() - date.getTime();
  }, [summary.lastSeenAt]);

  const onTabChange = useCallback(
    (next: string) => {
      const params = new URLSearchParams(searchParams?.toString() ?? "");
      const nextId = normalizeTab(next);
      if (nextId === "overview") {
        params.delete("tab");
      } else {
        params.set("tab", nextId);
      }
      const query = params.toString();
      router.replace(query ? `?${query}` : `?`, { scroll: false });
    },
    [router, searchParams],
  );

  return (
    <section className="mx-auto w-full max-w-6xl space-y-4">
      <div className="space-y-2">
        <Link
          href="/fleet"
          className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="h-3 w-3" strokeWidth={1.75} />
          Fleet
        </Link>
        <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="space-y-1">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
              Роутер
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-2xl font-semibold tracking-tight text-foreground">
                {summary.name}
              </h1>
              <ToneBadge tone={tone} dot>
                {label}
              </ToneBadge>
              <StaleBadge sinceMs={lastSeenMs} prefix="last seen" />
            </div>
            <p className="text-sm text-muted-foreground">
              {summary.hostname ?? summary.deviceIdentifier}
              {summary.boardName ? ` · ${summary.boardName}` : ""}
            </p>
          </div>
          <Button asChild variant="outline" size="sm">
            <Link href={`/routers/${routerId}?ui=v1`}>
              Расширенный режим (v1)
            </Link>
          </Button>
        </header>
      </div>

      <Tabs value={activeTab} onValueChange={onTabChange} className="space-y-4">
        <TabsList className="flex h-auto w-full flex-wrap justify-start gap-1 bg-muted/40 p-1">
          {TABS.map((tab) => {
            const Icon = tab.icon;
            return (
              <TabsTrigger
                key={tab.id}
                value={tab.id}
                className="inline-flex items-center gap-1.5"
              >
                <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
                {tab.label}
              </TabsTrigger>
            );
          })}
        </TabsList>

        <TabsContent value="overview" className="space-y-4">
          <OverviewTab
            routerId={routerId}
            surface={initialSurface}
            routerReachable={routerReachable}
            directModeActive={directModeActive}
            needsRecoveryAction={needsRecoveryAction}
          />
        </TabsContent>
        <TabsContent value="config">
          <ConfigTab routerId={routerId} />
        </TabsContent>
        <TabsContent value="nodes">
          <NodesTab routerId={routerId} />
        </TabsContent>
        <TabsContent value="updates">
          <UpdatesTab routerId={routerId} />
        </TabsContent>
        <TabsContent value="diagnostics">
          <DiagnosticsTab routerId={routerId} />
        </TabsContent>
        <TabsContent value="json">
          <JsonTab routerId={routerId} />
        </TabsContent>
      </Tabs>
    </section>
  );
}
