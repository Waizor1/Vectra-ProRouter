import { RouteLoadingState } from "~/components/route-state";

export default function UpdatesLoading() {
  return (
    <RouteLoadingState
      eyebrow="Обновления"
      title="Загружаем рабочие поверхности обновлений"
      description="Готовим baseline, профили и version-control как один рабочий контур."
      summary="Подтягиваем глобальный шаблон, каналы артефактов и историю рассылки."
      details="Если загрузка тянется, можно безопасно перейти в парк или повторить открытие этого экрана."
      checkpoints={[
        "читаем рабочую поверхность глобального шаблона",
        "сверяем каналы артефактов",
        "готовим историю и цели рассылки",
      ]}
      escapeHref="/fleet"
      escapeLabel="Открыть Парк"
    />
  );
}
