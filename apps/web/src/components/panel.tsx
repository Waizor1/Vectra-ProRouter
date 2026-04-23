import type { ReactNode } from "react";

export function Panel({
  eyebrow,
  title,
  children,
  aside,
  tone = "default",
  compact = false,
}: {
  eyebrow?: string;
  title: string;
  children: ReactNode;
  aside?: ReactNode;
  tone?: "default" | "hero" | "muted";
  compact?: boolean;
}) {
  const toneClassName =
    tone === "hero"
      ? "vectra-hero-panel"
      : tone === "muted"
        ? "vectra-subtle-panel"
        : "border border-white/10 bg-[rgba(16,20,27,0.88)] shadow-[var(--vectra-shadow-md)]";

  return (
    <section
      className={`min-w-0 rounded-2xl px-4 py-4 sm:px-5 ${compact ? "sm:py-4" : "sm:py-5"} ${toneClassName}`}
    >
      <div className="flex min-w-0 flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
        <div className="min-w-0">
          {eyebrow ? (
            <p className="vectra-kicker text-slate-500">
              {eyebrow}
            </p>
          ) : null}
          <h2 className="mt-1 text-base font-semibold tracking-[-0.02em] text-white sm:text-lg">
            {title}
          </h2>
        </div>
        {aside ? <div className="min-w-0 w-full xl:w-auto">{aside}</div> : null}
      </div>
      <div className={compact ? "mt-3" : "mt-4"}>{children}</div>
    </section>
  );
}
