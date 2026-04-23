import { RouteLoadingState } from "~/components/route-state";

export default function PublicInstallLoading() {
  return (
    <RouteLoadingState
      eyebrow="Публичная установка"
      title="Подготавливаем install surface"
      description="Готовим публичную one-click страницу и fallback-команду без operator-login."
      summary="Загружаем helper-aware install flow и публичные bootstrap assets."
      details="Страница поднимает public install shell, чтобы desktop helper мог подключиться к текущему bootstrap asset, а mobile сразу получил честный fallback."
      checkpoints={[
        "готовим публичную install-страницу",
        "собираем bootstrap-команду",
        "подключаем helper-aware статусы",
      ]}
      escapeHref="/enrollment"
      escapeLabel="Открыть Установку"
    />
  );
}
