import { RouteLoadingState } from "~/components/route-state";

export default function UpdatesLoading() {
  return (
    <RouteLoadingState
      eyebrow="Обновления"
      title="Загружаем baseline и каналы выпусков"
      description="Готовим рабочую поверхность для baseline и рассылки так, чтобы оператор видел явный переход между состояниями."
      summary="Подтягиваем глобальный шаблон, каналы артефактов и историю рассылки."
      details="Маршрут ждёт серверные данные по baseline, firmware manifest и свежим артефактам. До завершения загрузки страница остаётся в явном состоянии загрузки."
      checkpoints={[
        "читаем рабочую поверхность глобального шаблона",
        "сверяем каналы артефактов",
        "готовим историю и цели рассылки",
      ]}
    />
  );
}
