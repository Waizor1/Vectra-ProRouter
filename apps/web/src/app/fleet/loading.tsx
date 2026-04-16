import { RouteLoadingState } from "~/components/route-state";

export default function FleetLoading() {
  return (
    <RouteLoadingState
      eyebrow="Парк"
      title="Загружаем обзор парка"
      description="Открываем мониторинг по роутерам и подготавливаем рабочую поверхность без молчаливого перехода."
      summary="Подтягиваем свежие снимки, тревоги и фильтры парка."
      details="Как только сервер вернёт текущий срез, страница покажет состояние роутеров, последние известные инциденты и активные фильтры без скрытого промежуточного состояния."
      checkpoints={[
        "считываем snapshot по парку",
        "собираем alerts и freshness",
        "готовим таблицу и карточки роутеров",
      ]}
    />
  );
}
