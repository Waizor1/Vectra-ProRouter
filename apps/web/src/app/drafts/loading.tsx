import { RouteLoadingState } from "~/components/route-state";

export default function DraftsLoading() {
  return (
    <RouteLoadingState
      eyebrow="Черновики"
      title="Загружаем JSON workspace"
      description="Открываем экспертную поверхность без лишнего переходного текста."
      summary="Подтягиваем список ревизий, выбранный роутер и preview-конфиг."
      details="Если рабочая поверхность не открывается быстро, вернитесь в карточку роутера или повторите запрос позже."
      checkpoints={[
        "читаем workspace по черновикам",
        "подтягиваем историю ревизий",
        "готовим preview для редактора",
      ]}
      escapeHref="/fleet"
      escapeLabel="Открыть Парк"
    />
  );
}
