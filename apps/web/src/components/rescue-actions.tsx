"use client";

import { useRouter } from "next/navigation";

import { api } from "~/trpc/react";

type RescueActionsProps = {
  routerId: string;
  needsRecoveryAction: boolean;
  directModeActive: boolean;
  routerReachable: boolean;
};

export function RescueActions({
  routerId,
  needsRecoveryAction,
  directModeActive,
  routerReachable,
}: RescueActionsProps) {
  const router = useRouter();
  const utils = api.useUtils();

  const reconnectMutation = api.rescue.triggerReconnect.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.rescue.directRouters.invalidate(),
        utils.rescue.openIncidents.invalidate(),
      ]);
      router.refresh();
    },
  });

  if (!needsRecoveryAction) {
    return null;
  }

  const disabled = reconnectMutation.isPending || !routerReachable;

  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
      <button
        type="button"
        className="w-full rounded-md bg-emerald-400 px-3 py-2 text-sm font-medium text-slate-950 transition hover:bg-emerald-300 disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
        disabled={disabled}
        onClick={() =>
          reconnectMutation.mutate({
            routerId,
            clearRescue: true,
          })
        }
        >
          {reconnectMutation.isPending
            ? directModeActive
              ? "Возвращаю прокси-режим..."
              : "Сбрасываю аварийный флаг..."
            : !routerReachable
              ? "Роутер офлайн"
              : directModeActive
              ? "Отключить аварийный режим"
              : "Сбросить аварийный флаг"}
      </button>
      <p className="text-sm leading-6 text-slate-400 sm:max-w-md sm:self-center">
        {routerReachable
          ? directModeActive
            ? "Запрашивает выход из локального direct/rescue режима и возврат к прокси-контуру."
            : "Очищает застрявший аварийный флаг и просит контроллер заново подтвердить нормальный режим."
          : "Роутер давно не выходил на связь. Сначала дождитесь нового check-in или восстановите контроллер."}
      </p>
    </div>
  );
}
