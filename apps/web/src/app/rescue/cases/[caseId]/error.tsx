"use client";

import { RouteErrorState } from "~/components/route-state";

export default function RescueCaseError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Rescue cockpit"
      title="Rescue cockpit не открылся"
      description="Не удалось прочитать case или его job history."
      summary="Guided recovery surface сейчас недоступен."
      details="Можно повторить загрузку или вернуться в общий rescue surface."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку case"
      actions={[{ href: "/rescue", label: "Все rescue cases" }]}
    />
  );
}
