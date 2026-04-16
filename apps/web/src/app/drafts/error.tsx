"use client";

import { RouteErrorState } from "~/components/route-state";

export default function DraftsError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <RouteErrorState
      eyebrow="Черновики"
      title="Не удалось открыть экспертный режим"
      description="JSON workspace ответил с ошибкой, поэтому страница явно сообщает о сбое вместо generic fallback."
      summary="История ревизий или редактор сейчас не прочитались."
      details="Повторите попытку. Если ошибка сохраняется, откройте нужный роутер через `Парк` и проверьте, не исчез ли сам объект или его ревизии."
      errorMessage={error.message}
      onRetry={reset}
      retryLabel="Повторить загрузку черновиков"
      actions={[
        { href: "/fleet", label: "Открыть Парк" },
        { href: "/updates", label: "Открыть Обновления" },
      ]}
    />
  );
}
