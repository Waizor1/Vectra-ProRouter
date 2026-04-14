export type StatItem = {
  label: string;
  value: string;
  tone?: "default" | "good" | "warning";
};

const toneMap = {
  default: "text-white",
  good: "text-emerald-200",
  warning: "text-amber-200",
} as const;

export function StatGrid({ items }: { items: StatItem[] }) {
  return (
    <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
      {items.map((item) => (
        <article
          key={item.label}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] p-4"
        >
          <p className="vectra-kicker text-slate-500">
            {item.label}
          </p>
          <p
            className={`mt-2 text-2xl font-semibold tracking-[-0.02em] ${toneMap[item.tone ?? "default"]}`}
          >
            {item.value}
          </p>
        </article>
      ))}
    </div>
  );
}
