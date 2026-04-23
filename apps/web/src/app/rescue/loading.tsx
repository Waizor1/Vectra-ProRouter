import { RouteLoadingState } from "~/components/route-state";

export default function RescueLoading() {
  return (
    <RouteLoadingState
      eyebrow="Восстановление"
      title="Загружаем rescue-контур"
      description="Подготавливаем direct mode и открытые инциденты без длинного переходного экрана."
      summary="Считываем policy rescue и текущие аварийные случаи."
      details="Если rescue-контур отвечает медленно, можно выйти в парк и вернуться к инцидентам позже."
      checkpoints={[
        "читаем rescue policy",
        "подтягиваем direct-mode роутеры",
        "сверяем открытые инциденты",
      ]}
      escapeHref="/fleet"
      escapeLabel="Открыть Парк"
    />
  );
}
