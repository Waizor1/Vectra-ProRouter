import Link from "next/link";

type OperatorWorkflowScreen =
  | "fleet"
  | "router"
  | "drafts"
  | "rescue"
  | "updates"
  | "enrollment";

type WorkflowItem = {
  id: OperatorWorkflowScreen;
  label: string;
  href: string | null;
  badge: string;
  title: string;
  description: string;
  boundary: string;
};

const workflowItems: readonly WorkflowItem[] = [
  {
    id: "fleet",
    label: "Парк",
    href: "/fleet",
    badge: "Старт",
    title: "Мониторинг и алерты",
    description: "Ищите нужный роутер и проблемные устройства.",
    boundary: "Новые роутеры после первого check-in появляются здесь.",
  },
  {
    id: "router",
    label: "Роутер",
    href: null,
    badge: "Один роутер",
    title: "Локальная работа",
    description: "Import, apply, recovery, Watch Logs и терминал.",
    boundary: "Все действия касаются только текущего router ID.",
  },
  {
    id: "drafts",
    label: "Черновики",
    href: "/drafts",
    badge: "Резерв",
    title: "Экспертный JSON",
    description: "Резервный путь для нестандартных правок.",
    boundary: "Используйте только если обычной формы не хватает.",
  },
  {
    id: "rescue",
    label: "Восстановление",
    href: "/rescue",
    badge: "Сбой",
    title: "Direct mode и reconnect",
    description: "Инциденты, direct mode и возврат в proxy.",
    boundary: "Это аварийный раздел, не место для обычной настройки роутера.",
  },
  {
    id: "updates",
    label: "Обновления",
    href: "/updates",
    badge: "Весь парк",
    title: "Глобальный baseline",
    description: "Общий эталон и массовые действия по выбранным роутерам.",
    boundary: "Если менять нужно сразу нескольким роутерам, идите сюда.",
  },
  {
    id: "enrollment",
    label: "Установка",
    href: "/enrollment",
    badge: "Новый роутер",
    title: "Первичное подключение",
    description: "Bootstrap для controller, PassWall2 и первого baseline.",
    boundary: "Здесь только первичное подключение, не массовая рассылка.",
  },
] as const;

export function buildOperatorWorkflowMapItems(current: OperatorWorkflowScreen) {
  return workflowItems.map((item) => ({
    ...item,
    active: item.id === current,
    emphasized: item.id === "updates",
  }));
}

export function OperatorWorkflowMap({
  current,
  compact = false,
}: {
  current: OperatorWorkflowScreen;
  compact?: boolean;
}) {
  const items = buildOperatorWorkflowMapItems(current);
  const currentItem = items.find((item) => item.active) ?? workflowItems[0]!;
  const summary = compact ? currentItem.boundary : currentItem.description;

  return (
    <section className="rounded-md border border-white/10 bg-[var(--vectra-panel)] px-3 py-3 shadow-[0_16px_48px_rgba(0,0,0,0.25)] sm:px-4 sm:py-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <p className="vectra-kicker text-[var(--vectra-accent)]">
          Навигация оператора
        </p>
        <p className="text-[12px] leading-5 text-slate-400 sm:text-xs">
          Новый роутер: <strong>Установка</strong> · Один роутер:{" "}
          <strong>его страница</strong> · Несколько роутеров:{" "}
          <strong>Обновления</strong>
        </p>
      </div>

      <div className="mt-3 overflow-x-auto pb-1">
        <div className="flex min-w-max gap-2">
          {items.map((item) => {
            const cardClassName = `group rounded-md border px-3 py-3 transition ${
              item.active
                ? "border-[var(--vectra-accent)] bg-[rgba(33,70,104,0.34)]"
                : item.emphasized
                  ? "border-sky-400/25 bg-[rgba(20,33,48,0.78)] hover:border-sky-300/40"
                  : "border-white/10 bg-[var(--vectra-panel-soft)] hover:border-white/20"
            } ${compact ? "w-[12.5rem]" : "w-[14rem]"}`;

            const cardContent = (
              <>
                <div className="flex items-center justify-between gap-3">
                  <p
                    className={`vectra-kicker ${
                      item.active
                        ? "text-sky-100"
                        : item.emphasized
                          ? "text-sky-200"
                          : "text-slate-500"
                    }`}
                  >
                    {item.badge}
                  </p>
                  {item.active ? (
                    <span className="rounded-full border border-white/15 bg-white/10 px-2 py-1 text-[10px] font-semibold tracking-[0.1em] text-white uppercase">
                      сейчас
                    </span>
                  ) : null}
                </div>
                <p className="mt-2 text-sm font-semibold text-white sm:text-base">
                  {item.label}
                </p>
                <p className="mt-1 text-xs leading-5 text-slate-300">
                  {item.title}
                </p>
              </>
            );

            if (!item.href || item.active) {
              return (
                <div key={item.id} className={cardClassName}>
                  {cardContent}
                </div>
              );
            }

            return (
              <Link key={item.id} href={item.href} className={cardClassName}>
                {cardContent}
              </Link>
            );
          })}
        </div>
      </div>

      <p className="mt-3 text-sm leading-6 text-slate-300">{summary}</p>
    </section>
  );
}
