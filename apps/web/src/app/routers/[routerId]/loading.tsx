import { RouteLoadingState } from "~/components/route-state";

export default function RouterDetailLoading() {
  return (
    <RouteLoadingState
      eyebrow="Роутер"
      title="Загружаем рабочую поверхность роутера"
      description="Открываем детальный маршрут без зависания на длинном объяснении загрузки."
      summary="Подтягиваем снимок, статус связи и рабочую поверхность конфигурации."
      details="Маршрут ждёт server-side editor surface. Если это занимает слишком долго, вернитесь в парк и откройте роутер повторно."
      checkpoints={[
        "читаем карточку роутера",
        "сверяем последний и свежий снимок",
        "готовим детальную рабочую поверхность и действия",
      ]}
      escapeHref="/fleet"
      escapeLabel="Вернуться в Парк"
      slowLoadLabel="Детальный экран отвечает медленно"
    />
  );
}
