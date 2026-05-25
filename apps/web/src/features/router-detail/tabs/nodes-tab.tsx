"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { RotateCcw, Save, Send } from "lucide-react";
import { toast } from "sonner";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import type { RouterDetailEditorSurface } from "~/features/router-detail";
import { api } from "~/trpc/react";

import { NodeListSection } from "./nodes/node-list-section";
import { ShuntBindingsSection } from "./nodes/shunt-bindings-section";
import { SubscriptionSection } from "./nodes/subscription-section";

export interface NodesTabProps {
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

export function NodesTab({ routerId, initialSurface }: NodesTabProps) {
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

  const editableNodeIds = useMemo(
    () =>
      surface.subscriptionRuntime?.editableNodeIds ??
      config.nodes.map((node) => node.id),
    [surface.subscriptionRuntime, config.nodes],
  );

  const saveMutation = api.draft.save.useMutation();
  const applyMutation = api.draft.queueApply.useMutation();
  const refreshMutation = api.update.queueSubscriptionsRefresh.useMutation();
  const inspectMutation = api.update.queueSubscriptionsInspect.useMutation();
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
      toast.success("Узлы поставлены в очередь на применение");
    } catch (error) {
      toast.error("Не удалось применить", { description: errorMessage(error) });
    }
  };

  const handleReset = () => {
    setConfig(passwallDesiredConfigSchema.parse(surface.draftConfig));
  };

  const handleRefresh = async () => {
    try {
      await refreshMutation.mutateAsync({ routerId });
      await invalidate();
      router.refresh();
      toast.success("Обновление подписок поставлено в очередь");
    } catch (error) {
      toast.error("Не удалось обновить подписки", {
        description: errorMessage(error),
      });
    }
  };

  const handleInspect = async () => {
    try {
      await inspectMutation.mutateAsync({ routerId });
      await invalidate();
      router.refresh();
      toast.success("Проверка подписок поставлена в очередь");
    } catch (error) {
      toast.error("Не удалось проверить подписки", {
        description: errorMessage(error),
      });
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-4 xl:grid xl:grid-cols-2">
        <NodeListSection
          config={config}
          onChange={setConfig}
          editableNodeIds={editableNodeIds}
          disabled={busy}
        />
        <SubscriptionSection
          config={config}
          onChange={setConfig}
          disabled={busy}
          onRefresh={handleRefresh}
          onInspect={handleInspect}
          refreshing={refreshMutation.isPending}
          inspecting={inspectMutation.isPending}
        />
      </div>

      <ShuntBindingsSection
        config={config}
        onChange={setConfig}
        disabled={busy}
      />

      <Card className="sticky bottom-4 border-border/60 shadow-lg">
        <CardContent className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-sm text-muted-foreground">
            {dirty ? "Есть несохранённые изменения." : "Изменений нет."}
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
