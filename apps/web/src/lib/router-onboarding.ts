export type RouterImportState =
  | "awaiting_import"
  | "import_review"
  | "approved"
  | "out_of_sync";

export type RouterOnboardingTrust = {
  liveConfigAvailable?: boolean | null;
  requiresReimport?: boolean | null;
  digestMismatch?: boolean | null;
  configSourceMode?: string | null;
};

export type RouterOnboardingDescriptor = {
  badge: string;
  title: string;
  summary: string;
  steps: string[];
  approveLabel: string;
  approveUnavailableLabel: string;
  reimportLabel: string;
  cardActionLabel: string;
  cardHint: string;
  tone: "good" | "warning" | "default";
};

export function formatRouterImportStateLabel(importState: string) {
  switch (importState) {
    case "approved":
      return "в работе";
    case "import_review":
      return "проверить базу";
    case "out_of_sync":
      return "есть расхождение";
    case "awaiting_import":
      return "ждёт первое чтение";
    default:
      return importState;
  }
}

export function isRouterOnboardingPending(
  importState: string,
  trust?: RouterOnboardingTrust | null,
) {
  return (
    importState === "awaiting_import" ||
    importState === "import_review" ||
    importState === "out_of_sync" ||
    Boolean(trust?.requiresReimport)
  );
}

export function describeRouterOnboarding(
  importState: string,
  trust?: RouterOnboardingTrust | null,
): RouterOnboardingDescriptor {
  if (importState === "approved" && trust?.requiresReimport) {
    return {
      badge: trust.digestMismatch ? "Нужна сверка" : "Проверяем настройки",
      title: trust.digestMismatch
        ? "Настройки на роутере изменились вне панели"
        : "Панель видит роутер, но ждёт подробные настройки",
      summary:
        "Связь с роутером есть, но подробные разделы PassWall2 ещё обновляются. В обычном сценарии панель сама догонит состояние; вручную нажимайте обновление только если меняли настройки через LuCI/SSH или видите реальный конфликт.",
      steps: [
        "Обновите данные с роутера, если состояние не догналось само.",
        "Сравните изменения только если панель покажет реальный конфликт.",
        "Если конфликт подтвердился, выберите: принять состояние роутера или применить черновик панели.",
      ],
      approveLabel: "Принять состояние роутера",
      approveUnavailableLabel: "Сначала обновить данные",
      reimportLabel: "Обновить данные с роутера",
      cardActionLabel: "Проверить состояние",
      cardHint: "Подключение сохранено; панель ждёт подробную сверку настроек.",
      tone: "warning",
    };
  }

  switch (importState) {
    case "awaiting_import":
      return {
        badge: "Первое чтение",
        title: "Считать текущие настройки",
        summary:
          "Нужно один раз считать текущую конфигурацию, чтобы панель получила стартовую базу роутера. После этого обычные правки идут прямо из панели без повторного ручного импорта.",
        steps: [
          "Считайте настройки с роутера.",
          "Проверьте стартовую базу.",
          "Если всё в порядке, примите её и работайте с роутером из панели.",
        ],
        approveLabel: "Принять стартовую базу",
        approveUnavailableLabel: "Сначала считать настройки",
        reimportLabel: "Считать настройки с роутера",
        cardActionLabel: "Завершить подключение",
        cardHint:
          "Сначала нужна стартовая база, иначе панель не знает текущие PassWall-настройки.",
        tone: "warning",
      };
    case "import_review":
      return {
        badge: "Проверить базу",
        title: "Подтвердите стартовые настройки",
        summary:
          "Панель уже считала настройки. Осталось подтвердить, что именно это состояние станет базой для дальнейшей работы.",
        steps: [
          "Проверьте, что на роутере сейчас всё правильно.",
          "Примите это состояние как стартовую базу.",
          "После этого работайте с роутером в обычном режиме.",
        ],
        approveLabel: "Принять как базу",
        approveUnavailableLabel: "Жду считанную ревизию",
        reimportLabel: "Обновить данные с роутера",
        cardActionLabel: "Проверить базу",
        cardHint:
          "Если текущее состояние правильное, примите его как базу для дальнейших правок.",
        tone: "warning",
      };
    case "out_of_sync":
      return {
        badge: "Есть расхождение",
        title: "Настройки на роутере и в панели различаются",
        summary:
          "Панель увидела отличие между текущим состоянием роутера и сохранённой базой. Такое бывает после правок через LuCI/SSH или после внешнего обновления.",
        steps: [
          "Сравните новое состояние с ожидаемым.",
          "Если состояние роутера правильное, примите его как новую базу.",
          "Если нет, примените нужный черновик панели.",
        ],
        approveLabel: "Принять состояние роутера",
        approveUnavailableLabel: "Нет новой ревизии для принятия",
        reimportLabel: "Обновить данные с роутера",
        cardActionLabel: "Проверить расхождение",
        cardHint:
          "Нужно решить, что считать правильной базой: роутер или черновик панели.",
        tone: "warning",
      };
    case "approved":
      return {
        badge: "Готов",
        title: "Роутер подключён",
        summary:
          "Роутер в обычном рабочем режиме. Меняйте настройки в панели и применяйте их на роутер; ручная сверка нужна только после правок вне панели.",
        steps: [
          "Правки и применение делайте на странице этого роутера.",
          "Если конфигурацию меняли прямо на роутере, обновите данные с роутера.",
          "Массовые изменения запускаются из «Обновлений».",
        ],
        approveLabel: "База подтверждена",
        approveUnavailableLabel: "База уже подтверждена",
        reimportLabel: "Обновить данные с роутера",
        cardActionLabel: "Открыть роутер",
        cardHint:
          "Локальные правки, применение и диагностика делаются на странице роутера.",
        tone: "good",
      };
    default:
      return {
        badge: "Состояние подключения",
        title: "Проверьте состояние подключения",
        summary:
          "Панель не смогла точно определить стадию подключения. Проверьте состояние роутера вручную.",
        steps: [
          "Откройте страницу роутера.",
          "Проверьте связь контроллера и подробные настройки.",
          "После этого решите, нужно ли обновить данные или применить черновик.",
        ],
        approveLabel: "Принять текущую конфигурацию",
        approveUnavailableLabel: "Нет ревизии для принятия",
        reimportLabel: "Обновить данные с роутера",
        cardActionLabel: "Открыть роутер",
        cardHint:
          "Стадия подключения не распознана автоматически. Лучше открыть router detail.",
        tone: "default",
      };
  }
}
