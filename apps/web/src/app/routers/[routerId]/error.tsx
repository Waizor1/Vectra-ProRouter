"use client";

import { RouteErrorState } from "~/components/route-state";

export default function RouterDetailError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Роутер"
      title="Не удалось открыть карточку роутера"
      description="Детальный маршрут отделяет реальный сбой чтения от подтверждённого отсутствия роутера и больше не маскирует одно под другое."
      summary="Рабочая поверхность роутера сейчас недоступна."
      details="Повторите попытку. Если ошибка сохраняется, вернитесь в `Парк` и проверьте, существует ли роутер и доступны ли его снимки на сервере."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку роутера"
      actions={[
        { href: "/fleet", label: "Вернуться в Парк" },
        { href: "/rescue", label: "Открыть Восстановление" },
      ]}
    />
  );
}
