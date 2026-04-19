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
      return "подключён";
    case "import_review":
      return "на проверке";
    case "out_of_sync":
      return "есть расхождение";
    case "awaiting_import":
      return "ждёт первый import";
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
      badge: trust.digestMismatch ? "Нужен re-import" : "Live config не подтверждён",
      title: trust.digestMismatch
        ? "Роутер и панель уже разошлись"
        : "Панель видит snapshot, но не deep config",
      summary:
        "Свежий check-in уже пришёл, но полный live PassWall state ещё не перечитан в панель. Selected node, версии и сервисы могут быть свежими, а ShuntRules и другие глубокие секции пока остаются от эталона панели.",
      steps: [
        "Перечитайте конфигурацию с роутера.",
        "Сравните новый import с эталоном панели.",
        "Только после этого решайте, принимать новый import или применять нужный черновик.",
      ],
      approveLabel: "Принять новый import",
      approveUnavailableLabel: "Сначала получить свежий import",
      reimportLabel: "Перечитать конфигурацию",
      cardActionLabel: "Проверить live-state",
      cardHint:
        "Approved-статус сохранён, но глубокая PassWall-конфигурация ещё не подтверждена live import-ом.",
      tone: "warning",
    };
  }

  switch (importState) {
    case "awaiting_import":
      return {
        badge: "Первый import",
        title: "Считать живую конфигурацию",
        summary:
          "Нужен первый import, чтобы панель увидела текущую конфигурацию роутера. Пока его нет, локальные LuCI-правки и реальные ShuntRules вообще не отражаются в панели.",
        steps: [
          "Считайте конфигурацию с роутера.",
          "Дождитесь статуса «на проверке».",
          "Если всё в порядке, примите import как эталон.",
        ],
        approveLabel: "Принять стартовую конфигурацию",
        approveUnavailableLabel: "Сначала получить import",
        reimportLabel: "Считать конфигурацию с роутера",
        cardActionLabel: "Завершить подключение",
        cardHint:
          "Сначала нужен первый import, иначе у роутера нет стартовой базы в панели.",
        tone: "warning",
      };
    case "import_review":
      return {
        badge: "Проверить import",
        title: "Подтвердите текущий import",
        summary:
          "Панель уже считала конфигурацию. Осталось подтвердить, что именно она станет базой этого роутера.",
        steps: [
          "Проверьте, что на роутере сейчас всё правильно.",
          "Примите import как эталон.",
          "После этого работайте с роутером в обычном режиме.",
        ],
        approveLabel: "Принять import как эталон",
        approveUnavailableLabel: "Жду импортированную ревизию",
        reimportLabel: "Перечитать конфигурацию",
        cardActionLabel: "Проверить import",
        cardHint:
          "Если текущее состояние правильное, примите его как базу для этого роутера.",
        tone: "warning",
      };
    case "out_of_sync":
      return {
        badge: "Есть drift",
        title: "Роутер и панель разошлись",
        summary:
          "Новый import уже расходится с текущим эталоном. Это означает, что live-конфигурация роутера ушла вперёд панели и нужно решить, какая база правильная.",
        steps: [
          "Сравните новое состояние с ожидаемым.",
          "Если import правильный, примите его как новый эталон.",
          "Если нет, перечитайте конфигурацию или примените нужный черновик.",
        ],
        approveLabel: "Принять новый import",
        approveUnavailableLabel: "Нет новой ревизии для принятия",
        reimportLabel: "Перечитать конфигурацию",
        cardActionLabel: "Проверить drift",
        cardHint:
          "Нужно решить, что считать правильной базой: новый import или текущий эталон.",
        tone: "warning",
      };
    case "approved":
      return {
        badge: "Готов",
        title: "Роутер подключён",
        summary:
          "Эталон подтверждён. Дальше это обычная локальная работа с роутером, но локальные LuCI-правки всё равно нужно перечитывать через re-import, если хотите увидеть их в панели.",
        steps: [
          "Правки и apply делайте на странице этого роутера.",
          "Если конфигурацию меняли прямо на роутере, перечитайте import.",
          "Массовые изменения запускаются из «Обновлений».",
        ],
        approveLabel: "Эталон подтверждён",
        approveUnavailableLabel: "Эталон уже подтверждён",
        reimportLabel: "Перечитать конфигурацию",
        cardActionLabel: "Открыть роутер",
        cardHint:
          "Локальные правки, apply и диагностика делаются на странице роутера.",
        tone: "good",
      };
    default:
      return {
        badge: "Состояние подключения",
        title: "Проверьте состояние import",
        summary:
          "Панель не смогла точно определить стадию подключения. Проверьте import вручную.",
        steps: [
          "Откройте страницу роутера.",
          "Проверьте import и связь контроллера.",
          "После этого решите, нужен ли reimport или apply.",
        ],
        approveLabel: "Принять текущую конфигурацию",
        approveUnavailableLabel: "Нет ревизии для принятия",
        reimportLabel: "Перечитать конфигурацию",
        cardActionLabel: "Открыть роутер",
        cardHint:
          "Стадия подключения не распознана автоматически. Лучше открыть router detail.",
        tone: "default",
      };
  }
}
