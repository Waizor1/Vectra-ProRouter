import { RouteLoadingState } from "~/components/route-state";

export default function FleetLoading() {
  return (
    <RouteLoadingState
      eyebrow="Парк"
      title="Загружаем обзор парка"
      description="Открываем мониторинг по роутерам и сразу держим основную рабочую поверхность в фокусе."
      summary="Подтягиваем свежие снимки, тревоги и фильтры парка."
      details="Если срез отвечает медленно, безопаснее выйти в другой раздел и вернуться к парку повторно."
      checkpoints={[
        "считываем snapshot по парку",
        "собираем alerts и freshness",
        "готовим таблицу и карточки роутеров",
      ]}
      escapeHref="/updates"
      escapeLabel="Открыть Обновления"
    />
  );
}
