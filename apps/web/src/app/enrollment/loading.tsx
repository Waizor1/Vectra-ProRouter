import { RouteLoadingState } from "~/components/route-state";

export default function EnrollmentLoading() {
  return (
    <RouteLoadingState
      eyebrow="Установка"
      title="Подготавливаем bootstrap-материалы"
      description="Страница установки остаётся явной даже во время перехода, чтобы оператор не гадал, загрузилась ли команда и baseline."
      summary="Собираем bootstrap-команду, baseline и установочные материалы."
      details="Маршрут готовит операторские команды, ссылки на bootstrap-скрипты и пояснения по install path. До завершения чтения состояние остаётся прозрачным и предсказуемым."
      checkpoints={[
        "собираем bootstrap-команду",
        "подтягиваем install baseline",
        "готовим вспомогательные ссылки и шаги",
      ]}
    />
  );
}
