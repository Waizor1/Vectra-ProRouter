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
    <div className="vectra-scrollbarless overflow-x-auto pb-1">
      <nav
        aria-label={ariaLabel}
        className={`flex min-w-max snap-x snap-mandatory items-center gap-1 pr-1 ${
          variant === "primary" ? "border-b border-white/12 pb-2" : "rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] p-1"
        }`}
      >
        {items.map((item) => {
          const baseClassName =
            variant === "primary"
              ? "inline-flex min-h-10 snap-start items-center gap-2 rounded-xl border px-3 py-2 text-[12px] font-medium whitespace-nowrap tracking-[0.01em] transition sm:rounded-t-xl sm:border-b-0 sm:px-3.5 sm:text-[13px]"
              : "inline-flex min-h-9 snap-start items-center gap-2 rounded-xl border px-3 py-1.5 text-[12px] font-medium whitespace-nowrap tracking-[0.01em] transition sm:px-3.5 sm:text-[13px]";

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
