"use client";

import { RouteErrorState } from "~/components/route-state";

export default function RescueError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Восстановление"
      title="Не удалось открыть rescue-контур"
      description="Route-level rescue surface не смогла собрать policy или инциденты и поэтому показывает явное состояние сбоя."
      summary="Данные по direct mode и open incidents сейчас недоступны."
      details="Повторите попытку. Если ошибка не исчезает, переходите в `Парк` и сверяйте отдельные роутеры только как last-known state до восстановления rescue API."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку rescue"
      actions={[
        { href: "/fleet", label: "Открыть Парк" },
        { href: "/updates", label: "Открыть Обновления" },
      ]}
    />
  );
}
