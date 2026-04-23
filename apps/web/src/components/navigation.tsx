"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { operatorShellTabs } from "~/components/router-console";
import { TabBar } from "~/components/tab-bar";

export function Navigation() {
  const pathname = usePathname();
  const items = operatorShellTabs.map((item) => ({
    ...item,
    active:
      pathname === item.href ||
      (item.href === "/fleet" && pathname.startsWith("/routers/")),
  }));

  return (
    <div className="flex w-full flex-col gap-2 lg:w-auto lg:items-end">
      <div className="grid grid-cols-2 gap-1.5 sm:hidden">
        {items.map((item, index) => {
          const className = `inline-flex min-h-10 items-center justify-center rounded-md border px-3 py-2 text-center text-[13px] font-medium transition ${
            item.active
              ? "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel)] text-white"
              : "border-white/8 bg-[var(--vectra-panel-soft)] text-slate-300 hover:border-white/15 hover:text-white"
          } ${index === items.length - 1 ? "col-span-2" : ""}`;

          return (
            <Link key={item.id} href={item.href} className={className}>
              {item.label}
            </Link>
          );
        })}
      </div>

      <div className="max-sm:hidden sm:block">
        <TabBar items={items} ariaLabel="Разделы панели оператора" />
      </div>

      <form action="/api/operator/logout" method="post" className="sm:self-end">
        <button
          type="submit"
          className="w-full rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium tracking-[0.01em] text-slate-300 transition hover:border-white/20 hover:text-white sm:w-auto"
        >
          Выйти
        </button>
      </form>
    </div>
  );
}
