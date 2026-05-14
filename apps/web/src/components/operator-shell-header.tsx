"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useMemo, useState, type FormEvent } from "react";

import {
  buildFleetSearchHref,
  getOperatorShellSectionForPath,
  operatorShellTabs,
  resolveOperatorCommand,
} from "~/components/router-console";

export function OperatorShellHeader() {
  const pathname = usePathname();
  const router = useRouter();
  const [query, setQuery] = useState("");

  const section = useMemo(
    () => getOperatorShellSectionForPath(pathname),
    [pathname],
  );
  const isLoginPage = pathname === "/login";

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const directTarget = resolveOperatorCommand(query);
    if (directTarget) {
      router.push(directTarget);
      return;
    }

    router.push(buildFleetSearchHref(query));
  };

  return (
    <header className="rounded-2xl border border-white/10 bg-[rgba(9,12,18,0.9)] px-4 py-3 shadow-[var(--vectra-shadow-md)] backdrop-blur sm:px-5">
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
          <div className="flex min-w-0 items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-[11px] font-medium tracking-[0.14em] text-slate-500 uppercase">
                Vectra Operator
              </p>
              <div className="mt-1 flex flex-wrap items-center gap-2">
                <p className="truncate text-base font-semibold tracking-[-0.03em] text-white sm:text-lg">
                  Панель оператора
                </p>
                <span className="rounded-full border border-white/10 bg-white/[0.04] px-2.5 py-1 text-[11px] text-slate-300">
                  {isLoginPage ? "Вход" : section.label}
                </span>
              </div>
            </div>

            {!isLoginPage ? (
              <form
                action="/api/operator/logout"
                method="post"
                className="xl:hidden"
              >
                <button
                  type="submit"
                  className="vectra-button-secondary min-h-11 px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
                >
                  Выйти
                </button>
              </form>
            ) : null}
          </div>

          {!isLoginPage ? (
            <div className="w-full xl:flex-1">
              <div className="grid grid-cols-2 gap-1.5 lg:hidden">
                {operatorShellTabs.map((tab) => {
                  const active =
                    pathname === tab.href ||
                    (tab.id === "fleet" && pathname.startsWith("/routers/"));
                  const isLastTab = tab.id === operatorShellTabs.at(-1)?.id;

                  return (
                    <Link
                      key={tab.id}
                      href={tab.href}
                      className={`inline-flex min-h-11 items-center justify-center rounded-xl border px-3 py-2 text-center text-[13px] font-medium transition ${
                        active
                          ? "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel-strong)] text-white"
                          : "border-white/10 bg-[var(--vectra-panel-soft)] text-slate-300 hover:border-white/20 hover:text-white"
                      } ${isLastTab ? "col-span-2" : ""}`}
                    >
                      <span>{tab.label}</span>
                    </Link>
                  );
                })}
              </div>

              <div className="vectra-scrollbarless hidden overflow-x-auto pb-1 lg:block xl:pb-0">
                <div className="flex min-w-max gap-2 xl:justify-center">
                  {operatorShellTabs.map((tab) => {
                    const active =
                      pathname === tab.href ||
                      (tab.id === "fleet" && pathname.startsWith("/routers/"));

                    return (
                      <Link
                        key={tab.id}
                        href={tab.href}
                        className={`inline-flex min-h-11 items-center gap-2 rounded-full border px-3.5 py-2 text-sm font-medium transition ${
                          active
                            ? "border-[var(--vectra-line-strong)] bg-[var(--vectra-panel-strong)] text-white"
                            : "border-white/10 bg-[var(--vectra-panel-soft)] text-slate-300 hover:border-white/20 hover:text-white"
                        }`}
                      >
                        <span>{tab.label}</span>
                      </Link>
                    );
                  })}
                </div>
              </div>
            </div>
          ) : null}

          {!isLoginPage ? (
            <div className="hidden items-center gap-2 xl:flex">
              <form action="/api/operator/logout" method="post">
                <button
                  type="submit"
                  className="vectra-button-secondary min-h-11 px-3 py-2 text-sm font-medium tracking-[0.01em] transition hover:border-white/20 hover:text-white"
                >
                  Выйти
                </button>
              </form>
            </div>
          ) : null}
        </div>

        {!isLoginPage ? (
          <form onSubmit={handleSubmit} className="w-full">
            <label htmlFor="operator-command-surface" className="sr-only">
              Команда / поиск по парку
            </label>
            <div className="flex min-w-0 flex-col gap-2 lg:flex-row lg:items-center">
              <div className="rounded-2xl border border-white/10 bg-[rgba(255,255,255,0.03)] px-3 py-2 text-[11px] tracking-[0.12em] text-slate-500 uppercase lg:shrink-0">
                Команда / поиск
              </div>
              <input
                id="operator-command-surface"
                name="operator-command-surface"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="/fleet, AX3000T, direct mode, import review..."
                className="vectra-field min-h-11 min-w-0 flex-1 border-white/8 bg-[rgba(255,255,255,0.03)] px-3 py-2 text-sm text-white placeholder:text-slate-500"
              />
              <button
                type="submit"
                className="vectra-button-secondary min-h-11 w-full px-4 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white lg:w-auto lg:min-w-[7.5rem]"
              >
                Открыть
              </button>
            </div>
          </form>
        ) : null}
      </div>
    </header>
  );
}
