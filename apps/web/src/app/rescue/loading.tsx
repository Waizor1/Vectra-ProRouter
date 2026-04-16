import { RouteLoadingState } from "~/components/route-state";

export default function RescueLoading() {
  return (
    <RouteLoadingState
      eyebrow="Восстановление"
      title="Загружаем rescue-контур"
      description="Подготавливаем policy, список direct-mode роутеров и инциденты с явным состоянием загрузки маршрута."
      summary="Считываем policy rescue и текущие аварийные случаи."
      details="Страница дождётся порогов rescue, активных direct-mode роутеров и открытых инцидентов, а затем покажет полный рабочий контур без немого перехода."
      checkpoints={[
        "читаем rescue policy",
        "подтягиваем direct-mode роутеры",
        "сверяем открытые инциденты",
      ]}
    />
  );
}
