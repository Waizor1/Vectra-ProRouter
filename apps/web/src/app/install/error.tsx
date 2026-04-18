"use client";

export default function PublicInstallError({
  error,
}: {
  error: Error & { digest?: string };
}) {
  return (
    <section className="space-y-4">
      <div className="rounded-2xl border border-rose-400/25 bg-rose-500/10 px-5 py-5 text-rose-100">
        <p className="vectra-kicker text-rose-200">Публичная установка</p>
        <h1 className="mt-2 text-xl font-semibold text-white">
          Не удалось подготовить install surface
        </h1>
        <p className="mt-3 text-sm leading-6">
          Пока public one-click страница не поднялась, не продолжайте вслепую.
          Используйте только проверенный bootstrap asset после повторной загрузки.
        </p>
        <pre className="mt-4 overflow-x-auto rounded-md border border-rose-400/20 bg-black/20 p-4 text-xs leading-6 text-rose-50">
          <code>{error.message}</code>
        </pre>
      </div>
    </section>
  );
}
