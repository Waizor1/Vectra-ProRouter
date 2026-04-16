"use client";

import { RouteErrorState } from "~/components/route-state";

export default function FleetError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Парк"
      title="Не удалось открыть обзор парка"
      description="Мониторинговый маршрут ответил с ошибкой, поэтому оператор видит явный сбой вместо пустого или generic-экрана."
      summary="Срез по роутерам сейчас не прочитался."
      details="Повторите запрос. Если сбой сохранится, перейдите в другой операторский маршрут и проверьте API/базу данных до следующей попытки."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку парка"
      actions={[
        { href: "/updates", label: "Открыть Обновления" },
        { href: "/rescue", label: "Открыть Восстановление" },
      ]}
    />
  );
}
