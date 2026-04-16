"use client";

import { RouteErrorState } from "~/components/route-state";

export default function EnrollmentError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Установка"
      title="Не удалось подготовить install route"
      description="Bootstrap-поверхность не смогла собрать нужные материалы и показывает явный операторский error state."
      summary="Команда подключения или install helper сейчас недоступны."
      details="Повторите попытку. Если страница продолжает падать, не запускайте неподтверждённую установку вслепую — сначала восстановите генерацию bootstrap-материалов."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку установки"
      actions={[
        { href: "/fleet", label: "Открыть Парк" },
        { href: "/updates", label: "Открыть Обновления" },
      ]}
    />
  );
}
