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
      return "считано с роутера";
    case "authoritative":
      return "сохранено в панели";
    case "stale-authoritative":
      return "сохранено в панели, но уже может отставать";
    case "inventory-only":
      return "только краткий check-in";
    default:
      return "источник пока неясен";
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
          ? "Последняя полная сверка с роутером раньше совпадала, но сейчас панель показывает только последнюю известную картину. Считайте её ориентиром, а не подтверждённым текущим состоянием."
          : "Сейчас панель показывает последний известный check-in, а подробные PassWall-настройки берёт из того, что было сохранено в панели. На самом роутере это уже могло измениться.",
    };
  }

  if (args.directMode) {
    return {
      badge: "нужен разбор",
      badgeClassName: "border-amber-400/30 bg-amber-500/12 text-amber-100",
      title: "Связь жива, но контур нештатный",
      detail:
        "Панель получает свежие check-in, но роутер сейчас не в штатном прокси-режиме. Поэтому на расхождения между live-состоянием и сохранённой базой нужно смотреть особенно внимательно.",
    };
  }

  if (requiresReimport) {
    return {
      badge: digestMismatch ? "нужен re-import" : "deep config не подтверждён",
      badgeClassName: "border-amber-400/30 bg-amber-500/12 text-amber-100",
      title: digestMismatch
        ? "Панель видит, что настройки на роутере уже изменились"
        : "Есть свежий check-in, но подробная конфигурация ещё не перечитана",
      detail:
        "Панель уже видит текущее состояние сервиса, выбранную ноду и версии, но подробные разделы PassWall2 пока не перечитаны с роутера заново. Перед важным решением сначала выполните повторное чтение конфигурации.",
    };
  }

  if (mode === "authoritative") {
    return {
      badge: "эталон панели",
      badgeClassName: "border-sky-400/30 bg-sky-500/10 text-sky-100",
      title: "Подробные настройки сейчас берутся из панели",
      detail:
        "Это нормальный рабочий режим: панель опирается на сохранённый эталон и может с ним работать. Но это ещё не то же самое, что заново перечитанная конфигурация прямо с роутера.",
    };
  }

  if (mode === "inventory-only") {
    return {
      badge: "только snapshot",
      badgeClassName: "border-white/15 bg-white/5 text-slate-200",
      title: "Панель знает только краткое состояние роутера",
      detail:
        "Есть только check-in с основными признаками работы. Подробные разделы PassWall2 панель пока не может уверенно сравнивать.",
    };
  }

  return {
    badge: "live import",
    badgeClassName: "border-emerald-400/30 bg-emerald-500/12 text-emerald-100",
    title: "Подробные настройки подтверждены чтением с роутера",
    detail:
      "Панель сравнивает не только сохранённый эталон, но и последнее совпавшее чтение конфигурации с самого роутера. Это самый надёжный вариант для подробных разделов PassWall2.",
  };
}
