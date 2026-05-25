"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import {
  Boxes,
  Cpu,
  Globe2,
  Power,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Separator } from "~/components/ui/separator";
import type { RouterDetailEditorSurface } from "~/features/router-detail";
import { api } from "~/trpc/react";

export interface UpdatesTabProps {
  routerId: string;
  initialSurface: RouterDetailEditorSurface;
}

type Channel = "stable" | "beta";

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}

export function UpdatesTab({ routerId, initialSurface }: UpdatesTabProps) {
  const router = useRouter();
  const utils = api.useUtils();

  const surfaceQuery = api.draft.editorSurface.useQuery(
    { routerId },
    { initialData: initialSurface, refetchOnWindowFocus: false },
  );
  const surface = surfaceQuery.data ?? initialSurface;
  const inventory = surface.inventory;
  const summary = surface.routerRuntimeSummary;
  const updatesAllowed = summary.updateActionsAllowed;

  const [controllerChannel, setControllerChannel] = useState<Channel>("stable");
  const [passwallChannel, setPasswallChannel] = useState<Channel>("stable");
  const [rebootOpen, setRebootOpen] = useState(false);

  const controllerMutation = api.update.queueControllerUpdate.useMutation();
  const passwallMutation = api.update.queuePasswallPackageUpdate.useMutation();
  const rulesMutation = api.update.queueRulesRefresh.useMutation();
  const clearIpsetsMutation = api.update.queuePasswallClearIpsets.useMutation();
  const rebootMutation = api.update.queueRouterReboot.useMutation();

  const busy =
    controllerMutation.isPending ||
    passwallMutation.isPending ||
    rulesMutation.isPending ||
    clearIpsetsMutation.isPending ||
    rebootMutation.isPending;

  const refresh = async () => {
    await Promise.all([
      utils.draft.editorSurface.invalidate({ routerId }),
      utils.fleet.byId.invalidate({ routerId }),
      utils.fleet.monitoring.invalidate(),
    ]);
    router.refresh();
  };

  const run = async (
    fn: () => Promise<unknown>,
    success: string,
    failure: string,
  ) => {
    try {
      await fn();
      await refresh();
      toast.success(success);
    } catch (error) {
      toast.error(failure, { description: errorMessage(error) });
    }
  };

  const binaryEntries = Object.entries(inventory.binaryVersions ?? {}).filter(
    ([, value]) => Boolean(value),
  );
  const rules = inventory.rulesAssets;

  return (
    <div className="flex flex-col gap-4 xl:grid xl:grid-cols-2">
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Cpu className="h-4 w-4" strokeWidth={1.75} />
            Контроллер
          </CardTitle>
          <CardDescription>
            Vectra controller-agent: {inventory.controllerVersion ?? "версия неизвестна"}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-end gap-2">
          <ChannelSelect
            value={controllerChannel}
            onChange={setControllerChannel}
            disabled={busy}
          />
          <Button
            size="sm"
            onClick={() =>
              run(
                () =>
                  controllerMutation.mutateAsync({
                    routerId,
                    channel: controllerChannel,
                  }),
                "Обновление контроллера поставлено в очередь",
                "Не удалось обновить контроллер",
              )
            }
            disabled={busy || !updatesAllowed}
          >
            <RefreshCw className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
            Обновить контроллер
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Boxes className="h-4 w-4" strokeWidth={1.75} />
            PassWall
          </CardTitle>
          <CardDescription>
            luci-app-passwall2: {inventory.passwallVersion ?? "версия неизвестна"}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {binaryEntries.length > 0 ? (
            <div className="grid gap-x-6 gap-y-1 text-xs text-muted-foreground sm:grid-cols-2">
              {binaryEntries.map(([name, version]) => (
                <div key={name} className="flex justify-between gap-2">
                  <span>{name}</span>
                  <span className="text-foreground">{version}</span>
                </div>
              ))}
            </div>
          ) : null}
          <div className="flex flex-wrap items-end gap-2">
            <ChannelSelect
              value={passwallChannel}
              onChange={setPasswallChannel}
              disabled={busy}
            />
            <Button
              size="sm"
              onClick={() =>
                run(
                  () =>
                    passwallMutation.mutateAsync({
                      routerId,
                      artifactChannel: passwallChannel,
                    }),
                  "Обновление пакетов PassWall поставлено в очередь",
                  "Не удалось обновить пакеты PassWall",
                )
              }
              disabled={busy || !updatesAllowed}
            >
              <RefreshCw className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
              Обновить пакеты
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Globe2 className="h-4 w-4" strokeWidth={1.75} />
            Гео-правила
          </CardTitle>
          <CardDescription>geoip.dat и geosite.dat для шунтирования.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-x-6 gap-y-1 text-xs text-muted-foreground sm:grid-cols-2">
            <div className="flex justify-between gap-2">
              <span>geoip</span>
              <span className="text-foreground">
                {rules?.geoipVersion ?? "—"}
              </span>
            </div>
            <div className="flex justify-between gap-2">
              <span>geosite</span>
              <span className="text-foreground">
                {rules?.geositeVersion ?? "—"}
              </span>
            </div>
          </div>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              run(
                () => rulesMutation.mutateAsync({ routerId }),
                "Обновление гео-правил поставлено в очередь",
                "Не удалось обновить гео-правила",
              )
            }
            disabled={busy || !updatesAllowed}
          >
            <RefreshCw className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
            Обновить гео-правила
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Power className="h-4 w-4" strokeWidth={1.75} />
            Обслуживание
          </CardTitle>
          <CardDescription>
            Сервисные операции на роутере. Применяются с осторожностью.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Button
            size="sm"
            variant="outline"
            className="w-full justify-start"
            onClick={() =>
              run(
                () => clearIpsetsMutation.mutateAsync({ routerId }),
                "Очистка IPSet поставлена в очередь",
                "Не удалось очистить IPSet",
              )
            }
            disabled={busy || !updatesAllowed}
          >
            <Trash2 className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
            Очистить IPSet PassWall
          </Button>
          <Separator />
          <Button
            size="sm"
            variant="destructive"
            className="w-full justify-start"
            onClick={() => setRebootOpen(true)}
            disabled={busy || !summary.destructiveActionsAllowed}
          >
            <Power className="mr-1.5 h-3.5 w-3.5" strokeWidth={1.75} />
            Перезагрузить роутер
          </Button>
        </CardContent>
      </Card>

      <AlertDialog open={rebootOpen} onOpenChange={setRebootOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Перезагрузить роутер?</AlertDialogTitle>
            <AlertDialogDescription>
              Роутер уйдёт в reboot и пропадёт из контроллера на 1–2 минуты.
              PassWall и controller-agent поднимутся автоматически.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Отмена</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setRebootOpen(false);
                void run(
                  () => rebootMutation.mutateAsync({ routerId }),
                  "Перезагрузка поставлена в очередь",
                  "Не удалось перезагрузить роутер",
                );
              }}
            >
              Перезагрузить
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function ChannelSelect({
  value,
  onChange,
  disabled,
}: {
  value: Channel;
  onChange: (value: Channel) => void;
  disabled?: boolean;
}) {
  return (
    <Select
      value={value}
      onValueChange={(next) => onChange(next as Channel)}
      disabled={disabled}
    >
      <SelectTrigger className="h-9 w-32">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="stable">stable</SelectItem>
        <SelectItem value="beta">beta</SelectItem>
      </SelectContent>
    </Select>
  );
}
