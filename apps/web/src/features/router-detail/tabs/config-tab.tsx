"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { AlertOctagon, Info, RotateCcw, Save, Send } from "lucide-react";
import { toast } from "sonner";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { ConfigEditor } from "~/features/config-editor";
import type { RouterDetailEditorSurface } from "~/features/router-detail";
import { api } from "~/trpc/react";

export interface ConfigTabProps {
  routerId: string;
  initialSurface: RouterDetailEditorSurface;
}

function surfaceRevisionKey(surface: RouterDetailEditorSurface): string {
  return (
    surface.workspaceRevisionId ??
    surface.latestDraftId ??
    surface.activeRevisionId ??
    surface.importedRevisionId ??
    "live"
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}

export function ConfigTab({ routerId, initialSurface }: ConfigTabProps) {
  const router = useRouter();
  const utils = api.useUtils();

  const surfaceQuery = api.draft.editorSurface.useQuery(
    { routerId },
    { initialData: initialSurface, refetchOnWindowFocus: false },
  );
  const surface = surfaceQuery.data ?? initialSurface;

  const [config, setConfig] = useState<PasswallDesiredConfig>(() =>
    passwallDesiredConfigSchema.parse(initialSurface.draftConfig),
  );
  const [loadedKey, setLoadedKey] = useState(() =>
    surfaceRevisionKey(initialSurface),
  );

  // Reload local config only when the server-side revision changes (after a
  // save/apply or a fresh live import) so in-progress edits are not clobbered.
  useEffect(() => {
    const key = surfaceRevisionKey(surface);
    if (key === loadedKey) {
      return;
    }
    setConfig(passwallDesiredConfigSchema.parse(surface.draftConfig));
    setLoadedKey(key);
  }, [surface, loadedKey]);

  const dirty = useMemo(
    () => JSON.stringify(config) !== JSON.stringify(surface.draftConfig),
    [config, surface.draftConfig],
  );

  const saveMutation = api.draft.save.useMutation();
  const applyMutation = api.draft.queueApply.useMutation();
  const busy = saveMutation.isPending || applyMutation.isPending;

  const summary = surface.routerRuntimeSummary;
  const applyAllowed =
    summary.destructiveActionsAllowed && summary.importState === "approved";

  const invalidate = () =>
    Promise.all([
      utils.draft.editorSurface.invalidate({ routerId }),
      utils.draft.list.invalidate(),
      utils.fleet.byId.invalidate({ routerId }),
      utils.fleet.monitoring.invalidate(),
    ]);

  const handleSave = async () => {
    try {
      await saveMutation.mutateAsync({ routerId, config });
      await invalidate();
      router.refresh();
      toast.success("Черновик сохранён");
    } catch (error) {
      toast.error("Не удалось сохранить черновик", {
        description: errorMessage(error),
      });
    }
  };

  const handleSaveAndApply = async () => {
    try {
      const revision = await saveMutation.mutateAsync({ routerId, config });
      if (!revision?.id) {
        throw new Error("Сервер не вернул ревизию для применения.");
      }
      await applyMutation.mutateAsync({
        routerId,
        desiredRevisionId: revision.id,
      });
      await invalidate();
      router.refresh();
      toast.success("Конфигурация поставлена в очередь на применение");
    } catch (error) {
      toast.error("Не удалось применить конфигурацию", {
        description: errorMessage(error),
      });
    }
  };

  const handleReset = () => {
    setConfig(passwallDesiredConfigSchema.parse(surface.draftConfig));
  };

  const { router: routerChanges, panel: panelChanges } =
    surface.unconfirmedChanges;

  return (
    <div className="space-y-4">
      {routerChanges.status !== "none" ? (
        <UnconfirmedNotice
          tone="warning"
          title={routerChanges.title}
          summary={routerChanges.summary}
          changeCount={routerChanges.changeCount}
        />
      ) : null}
      {panelChanges.status !== "none" ? (
        <UnconfirmedNotice
          tone="info"
          title={panelChanges.title}
          summary={panelChanges.summary}
          changeCount={panelChanges.changeCount}
        />
      ) : null}

      <ConfigEditor config={config} onChange={setConfig} disabled={busy} />

      <Card className="sticky bottom-4 border-border/60 shadow-lg">
        <CardContent className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-sm text-muted-foreground">
            {dirty
              ? "Есть несохранённые изменения."
              : "Изменений нет."}
            {!applyAllowed ? (
              <span className="block text-xs">
                Применение доступно только на pilot/certified роутере с
                подтверждённым import (сейчас: {summary.importState}).
              </span>
            ) : null}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="ghost"
              onClick={handleReset}
              disabled={!dirty || busy}
            >
              <RotateCcw className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
              Сбросить
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={handleSave}
              disabled={!dirty || busy}
            >
              <Save className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
              Сохранить черновик
            </Button>
            <Button
              size="sm"
              onClick={handleSaveAndApply}
              disabled={!dirty || busy || !applyAllowed}
            >
              <Send className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
              Сохранить и применить
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function UnconfirmedNotice({
  tone,
  title,
  summary,
  changeCount,
}: {
  tone: "warning" | "info";
  title: string;
  summary: string;
  changeCount: number;
}) {
  const Icon = tone === "warning" ? AlertOctagon : Info;
  const accent =
    tone === "warning"
      ? "border-amber-500/30 bg-amber-500/[0.04]"
      : "border-sky-500/30 bg-sky-500/[0.04]";
  const iconColor = tone === "warning" ? "text-amber-300" : "text-sky-300";

  return (
    <Card className={accent}>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Icon className={`h-4 w-4 ${iconColor}`} strokeWidth={1.75} />
          {title}
          {changeCount > 0 ? (
            <span className="text-xs font-normal text-muted-foreground">
              · {changeCount} полей
            </span>
          ) : null}
        </CardTitle>
        <CardDescription>{summary}</CardDescription>
      </CardHeader>
    </Card>
  );
}
