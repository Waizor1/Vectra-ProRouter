import { RouteLoadingState } from "~/components/route-state";

export default function EnrollmentLoading() {
  return (
    <RouteLoadingState
      eyebrow="Установка"
      title="Подготавливаем bootstrap-материалы"
      description="Страница установки открывается как компактная рабочая поверхность, а не как длинный preamble."
      summary="Собираем bootstrap-команду, baseline и установочные материалы."
      details="Если команда или материалы не открываются быстро, можно перейти в парк и вернуться к установке позже."
      checkpoints={[
        "собираем bootstrap-команду",
        "подтягиваем install baseline",
        "готовим вспомогательные ссылки и шаги",
      ]}
      escapeHref="/fleet"
      escapeLabel="Открыть Парк"
    />
  );
}
