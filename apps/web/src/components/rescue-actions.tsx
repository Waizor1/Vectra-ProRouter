"use client";

import Link from "next/link";
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

  const activeCase = api.rescue.activeCaseForRouter.useQuery(
    { routerId },
    { refetchInterval: 10000 },
  );

  const reconnectMutation = api.rescue.triggerReconnect.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.rescue.directRouters.invalidate(),
        utils.rescue.openIncidents.invalidate(),
        utils.rescue.activeCaseForRouter.invalidate({ routerId }),
      ]);
      router.refresh();
    },
  });

  const rescueCaseLink = activeCase.data ? (
    <Link
      href={`/rescue/cases/${activeCase.data.id}`}
      className="vectra-button-secondary w-full px-3 py-2 text-center text-sm font-medium transition hover:border-white/20 hover:text-white sm:w-auto"
    >
      Открыть rescue cockpit
    </Link>
  ) : null;

  if (!needsRecoveryAction && !rescueCaseLink) {
    return null;
  }

  const disabled = reconnectMutation.isPending || !routerReachable;

  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
      {rescueCaseLink}
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
