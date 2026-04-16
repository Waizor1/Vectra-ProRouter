export function StatusTile({
  label,
  value,
  tone = "default",
  hint,
  compact = false,
  emphasis = false,
}: {
  label: string;
  value: string;
  tone?: "default" | "good" | "warning" | "danger";
  hint?: string;
  compact?: boolean;
  emphasis?: boolean;
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
    <article
      className={`rounded-2xl border ${emphasis ? "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel-strong)]" : "border-white/10 bg-[var(--vectra-panel-soft)]"} ${
        compact ? "p-2.5 sm:p-3" : "p-3 sm:p-4"
      }`}
    >
      <p className="vectra-kicker text-slate-500">
        {label}
      </p>
      <p
        className={`mt-1.5 font-semibold tracking-[-0.01em] ${toneClassName} ${
          compact ? "text-sm sm:text-[15px]" : "text-sm sm:text-base xl:text-lg"
        }`}
      >
        {value}
      </p>
      {hint ? (
        <p
          className={`mt-1 text-[11px] text-slate-400 ${
            compact ? "leading-4.5 sm:text-[11px] sm:leading-5" : "leading-5 sm:text-xs sm:leading-6"
          }`}
        >
          {hint}
        </p>
      ) : null}
    </article>
  );
}
