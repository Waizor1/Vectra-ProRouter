"use client";

import Link from "next/link";

export type TabBarItem = {
  id: string;
  label: string;
  href?: string;
  active?: boolean;
  disabled?: boolean;
  onSelect?: () => void;
  badge?: string;
};

export function TabBar({
  items,
  ariaLabel,
  variant = "primary",
}: {
  items: readonly TabBarItem[];
  ariaLabel: string;
  variant?: "primary" | "secondary";
}) {
  return (
    <div className="vectra-scrollbarless min-w-0 pb-1 lg:overflow-x-auto">
      <nav
        aria-label={ariaLabel}
        className={`flex w-full flex-wrap items-center gap-1 pr-1 lg:min-w-max lg:flex-nowrap lg:snap-x lg:snap-mandatory ${
          variant === "primary"
            ? "border-b border-white/12 pb-2"
            : "rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] p-1"
        }`}
      >
        {items.map((item) => {
          const baseClassName =
            variant === "primary"
              ? "inline-flex min-h-10 min-w-0 max-w-full items-center justify-center gap-2 rounded-xl border px-3 py-2 text-center text-[12px] font-medium tracking-[0.01em] whitespace-normal transition lg:snap-start lg:whitespace-nowrap sm:rounded-t-xl sm:border-b-0 sm:px-3.5 sm:text-[13px]"
              : "inline-flex min-h-9 min-w-0 max-w-full items-center justify-center gap-2 rounded-xl border px-3 py-1.5 text-center text-[12px] font-medium tracking-[0.01em] whitespace-normal transition lg:snap-start lg:whitespace-nowrap sm:px-3.5 sm:text-[13px]";

          const stateClassName = item.disabled
            ? "cursor-not-allowed border-white/8 bg-white/[0.03] text-slate-500"
            : item.active
              ? variant === "primary"
                ? "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel)] text-white"
                : "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel-soft)] text-white"
              : "border-white/8 bg-[var(--vectra-panel-soft)] text-slate-300 hover:border-white/15 hover:text-white";

          const content = (
            <>
              <span>{item.label}</span>
              {item.badge ? (
                <span className="vectra-chip hidden rounded-sm border border-white/10 px-1.5 py-0.5 text-slate-300 sm:inline-flex">
                  {item.badge}
                </span>
              ) : null}
            </>
          );

          if (item.href && !item.disabled) {
            return (
              <Link
                key={item.id}
                href={item.href}
                className={`${baseClassName} ${stateClassName}`}
              >
                {content}
              </Link>
            );
          }

          return (
            <button
              key={item.id}
              type="button"
              disabled={item.disabled}
              onClick={item.onSelect}
              className={`${baseClassName} ${stateClassName}`}
              aria-pressed={item.active}
            >
              {content}
            </button>
          );
        })}
      </nav>
    </div>
  );
}
