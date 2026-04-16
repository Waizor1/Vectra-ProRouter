"use client";

import { RouteErrorState } from "~/components/route-state";

export default function UpdatesError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Обновления"
      title="Не удалось открыть release workspace"
      description="Страница baseline/rollout не смогла собрать нужные серверные данные и показывает операторский error state вместо частично пустого экрана."
      summary="Каналы выпусков или глобальный template сейчас недоступны."
      details="Повторите попытку. Если ошибка повторяется, проверьте состояние API, PostgreSQL и свежесть metadata sync перед новой рассылкой."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку обновлений"
      actions={[
        { href: "/fleet", label: "Вернуться в Парк" },
        { href: "/enrollment", label: "Открыть Установку" },
      ]}
    />
  );
}
