export function StatusTile({
  label,
  value,
  tone = "default",
  hint,
}: {
  label: string;
  value: string;
  tone?: "default" | "good" | "warning" | "danger";
  hint?: string;
}) {
  const toneClassName =
    tone === "good"
      ? "text-emerald-300"
      : tone === "warning"
        ? "text-amber-200"
        : tone === "danger"
          ? "text-rose-200"
      : "text-white";

  return (
    <article className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-3 sm:p-4">
      <p className="vectra-kicker text-slate-500">
        {label}
      </p>
      <p
        className={`mt-2 text-base font-semibold tracking-[-0.01em] sm:text-lg ${toneClassName}`}
      >
        {value}
      </p>
      {hint ? (
        <p className="mt-2 text-xs leading-5 text-slate-400 sm:leading-6">
          {hint}
        </p>
      ) : null}
    </article>
  );
}
