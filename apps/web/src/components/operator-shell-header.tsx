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
    <header className="rounded-2xl border border-white/10 bg-[rgba(9,12,18,0.92)] px-4 py-3 shadow-[var(--vectra-shadow-md)] backdrop-blur sm:px-5 sm:py-3">
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
          <div className="min-w-0 xl:max-w-[20rem]">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate text-base font-semibold tracking-[-0.03em] text-white sm:text-lg">
                  Панель оператора Vectra
                </p>
                <p className="mt-1 text-xs leading-5 text-slate-500">
                  {isLoginPage ? "Вход" : section.label}
                </p>
              </div>

              {!isLoginPage ? (
                <form action="/api/operator/logout" method="post" className="xl:hidden">
                  <button
                    type="submit"
                    className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
                  >
                    Выйти
                  </button>
                </form>
              ) : null}
            </div>
          </div>

          <div className="flex min-w-0 flex-col gap-3 xl:min-w-[760px] xl:flex-1 xl:items-end">
            <form onSubmit={handleSubmit} className="w-full">
              <label htmlFor="operator-command-surface" className="sr-only">
                Команда / поиск по парку
              </label>
              <div className="flex flex-col gap-2 md:flex-row">
                <input
                  id="operator-command-surface"
                  name="operator-command-surface"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="/fleet, /updates, AX3000T, direct mode, import review..."
                  className="vectra-field min-h-11 flex-1 px-4 py-2.5 text-sm text-white placeholder:text-slate-500"
                />
                <button
                  type="submit"
                  className="vectra-button-primary min-h-11 px-4 py-2.5 text-sm font-medium transition hover:bg-[rgba(99,185,255,0.22)] md:min-w-[8rem]"
                >
                  Открыть
                </button>
              </div>
            </form>
          </div>
        </div>

        {!isLoginPage ? (
          <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
            <div className="vectra-scrollbarless overflow-x-auto pb-1">
              <div className="flex min-w-max gap-2">
                {operatorShellTabs.map((tab) => {
                  const active =
                    pathname === tab.href ||
                    (tab.id === "fleet" && pathname.startsWith("/routers/"));

                  return (
                    <Link
                      key={tab.id}
                      href={tab.href}
                      className={`inline-flex items-center gap-2 rounded-full border px-3.5 py-2 text-sm font-medium transition ${
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

            <div className="hidden items-center gap-2 xl:flex">
              <form action="/api/operator/logout" method="post">
                <button
                  type="submit"
                  className="vectra-button-secondary px-3 py-2 text-sm font-medium tracking-[0.01em] transition hover:border-white/20 hover:text-white"
                >
                  Выйти
                </button>
              </form>
            </div>
          </div>
        ) : null}
      </div>
    </header>
  );
}
