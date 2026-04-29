import { RouteLoadingState } from "~/components/route-state";

export default function RescueCaseLoading() {
  return (
    <RouteLoadingState
      eyebrow="Rescue cockpit"
      title="Открываю rescue cockpit"
      description="Подгружаю case, evidence bundle и последние job results."
      summary="Готовим guided recovery surface."
      details="Если загрузка затянулась, можно вернуться в общий rescue surface и открыть case повторно."
      checkpoints={[
        "Читаем rescue case",
        "Поднимаем compact evidence",
        "Собираем job progress",
      ]}
      escapeHref="/rescue"
      escapeLabel="Все rescue cases"
    />
  );
}
