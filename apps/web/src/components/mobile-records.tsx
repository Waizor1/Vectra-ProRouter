import type { ReactNode } from "react";

type MobileCardTone = "default" | "accent" | "good" | "warning" | "danger";

const toneClassNameMap: Record<MobileCardTone, string> = {
  default: "border-white/10 bg-[var(--vectra-panel-soft)]",
  accent:
    "border-[var(--vectra-line-strong)] bg-[rgba(31,44,62,0.72)] shadow-[0_0_0_1px_rgba(138,170,204,0.12)_inset]",
  good: "border-emerald-400/20 bg-[rgba(10,38,28,0.6)]",
  warning: "border-amber-400/20 bg-[rgba(69,44,10,0.48)]",
  danger: "border-rose-400/20 bg-[rgba(54,18,22,0.56)]",
};

export function MobileCardList({
  title,
  hint,
  children,
}: {
  title?: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-3 lg:hidden">
      {title || hint ? (
        <div className="flex items-center justify-between gap-3 px-1 text-[11px] leading-5 text-slate-500">
          <span>{title ?? "Карточки"}</span>
          {hint ? (
            <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-0.5 text-slate-400">
              {hint}
            </span>
          ) : null}
        </div>
      ) : null}
      <div className="space-y-3">{children}</div>
    </div>
  );
}

export function MobileCard({
  children,
  tone = "default",
  className = "",
}: {
  children: ReactNode;
  tone?: MobileCardTone;
  className?: string;
}) {
  return (
    <article
      className={`rounded-2xl border px-4 py-4 ${toneClassNameMap[tone]} ${className}`}
    >
      {children}
    </article>
  );
}

export function MobileCardGrid({
  children,
  columns = 2,
}: {
  children: ReactNode;
  columns?: 1 | 2;
}) {
  return (
    <div
      className={`grid gap-2 ${columns === 2 ? "sm:grid-cols-2" : ""}`}
    >
      {children}
    </div>
  );
}

export function MobileCardField({
  label,
  value,
  detail,
  mono = false,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="rounded-xl border border-white/10 bg-black/10 px-3 py-3">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <div
        className={`mt-1 text-sm text-slate-100 ${mono ? "font-[family:var(--font-plex-mono)]" : ""}`}
      >
        {value}
      </div>
      {detail ? (
        <div className="mt-1 text-xs leading-5 text-slate-400">{detail}</div>
      ) : null}
    </div>
  );
}
