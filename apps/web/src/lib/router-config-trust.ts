type RouterConfigTrustLike = {
  liveConfigAvailable?: boolean | null;
  requiresReimport?: boolean | null;
  digestMismatch?: boolean | null;
  configSourceMode?: string | null;
};

export type ConfigTrustDescription = {
  badge: string;
  badgeClassName: string;
  title: string;
  detail: string;
};

export function formatConfigSourceModeLabel(mode: string | null | undefined) {
  switch (mode) {
    case "live-import":
      return "live import";
    case "authoritative":
      return "эталон панели";
    case "stale-authoritative":
      return "эталон панели (stale)";
    case "inventory-only":
      return "только snapshot";
    default:
      return "источник не определён";
  }
}

export function describeConfigTrustState(args: {
  trust?: RouterConfigTrustLike | null;
  offline?: boolean;
  directMode?: boolean;
}): ConfigTrustDescription {
  const trust = args.trust;
  const requiresReimport = Boolean(trust?.requiresReimport);
  const digestMismatch = Boolean(trust?.digestMismatch);
  const mode = trust?.configSourceMode ?? "inventory-only";

  if (args.offline) {
    return {
      badge: "последний снимок",
      badgeClassName: "border-rose-400/30 bg-rose-500/12 text-rose-100",
      title: "Связи сейчас нет",
      detail:
        mode === "live-import"
          ? "Последний полный import совпадал с последним известным snapshot, но сам snapshot уже устарел. Используйте его как последнюю известную картину, не как live-state."
          : "Карточка показывает последний известный snapshot, а глубокая PassWall-конфигурация ниже остаётся эталоном панели. Текущее состояние роутера могло уже уйти вперёд.",
    };
  }

  if (args.directMode) {
    return {
      badge: "нужен разбор",
      badgeClassName: "border-amber-400/30 bg-amber-500/12 text-amber-100",
      title: "Связь жива, но контур нештатный",
      detail:
        "Панель получает свежие check-in, но роутер сейчас не в штатном прокси-режиме и требует внимания оператора.",
    };
  }

  if (requiresReimport) {
    return {
      badge: digestMismatch ? "нужен re-import" : "deep config не подтверждён",
      badgeClassName: "border-amber-400/30 bg-amber-500/12 text-amber-100",
      title: digestMismatch
        ? "Панель отстаёт от live-конфига"
        : "Есть свежий snapshot, но нет matching live import",
      detail:
        "Selected node, версии и сервисы уже пришли со свежего snapshot, но ShuntRules, Nodes, Subscriptions и Rule Manage пока не подтверждены live import-ом. Перед серьёзными решениями сначала перечитайте конфигурацию с роутера.",
    };
  }

  if (mode === "authoritative") {
    return {
      badge: "эталон панели",
      badgeClassName: "border-sky-400/30 bg-sky-500/10 text-sky-100",
      title: "Глубокая конфигурация идёт из панели",
      detail:
        "Для этого роутера панель сейчас опирается на authoritative baseline. Это нормальный рабочий источник, но он ещё не подтверждён свежим full import-ом с роутера.",
    };
  }

  if (mode === "inventory-only") {
    return {
      badge: "только snapshot",
      badgeClassName: "border-white/15 bg-white/5 text-slate-200",
      title: "Есть только сводочный snapshot",
      detail:
        "Панель знает summary из heartbeat, но у неё ещё нет подтверждённого полного PassWall baseline для глубоких секций.",
    };
  }

  return {
    badge: "live import",
    badgeClassName: "border-emerald-400/30 bg-emerald-500/12 text-emerald-100",
    title: "Глубокая конфигурация подтверждена live import-ом",
    detail:
      "ShuntRules, Nodes, Subscriptions и Rule Manage опираются на последний matching import с роутера, а не только на эталон панели.",
  };
}
