import Link from "next/link";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import {
  getOperatorCookieName,
  verifyOperatorSession,
} from "~/server/operator-session";
import { isUiV2 } from "~/lib/feature-flag";
import { LoginV2 } from "~/features/auth/login-v2";

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string }>;
}) {
  const [cookieStore, params, v2] = await Promise.all([
    cookies(),
    searchParams,
    isUiV2(),
  ]);
  const session = await verifyOperatorSession(
    cookieStore.get(getOperatorCookieName())?.value,
  );

  if (session) {
    redirect("/fleet");
  }

  const hasError = params.error === "1";

  if (v2) {
    return <LoginV2 hasError={hasError} />;
  }

  return (
    <section className="mx-auto flex min-h-[70vh] w-full max-w-xl items-center">
      <div className="w-full rounded-[2.5rem] border border-white/10 bg-[linear-gradient(135deg,rgba(12,18,31,0.98),rgba(20,29,49,0.95),rgba(43,78,59,0.42))] p-5 shadow-[0_30px_90px_rgba(0,0,0,0.45)] sm:p-8">
        <p className="vectra-kicker text-slate-500">Вход оператора</p>
        <h1 className="mt-3 text-3xl font-semibold tracking-[-0.03em] text-white sm:text-4xl">
          Панель Vectra
        </h1>
        <p className="mt-4 text-sm leading-7 text-slate-300 sm:text-base sm:leading-8">
          Панель рассчитана только на наших операторов. Войдите под
          операторскими учётными данными, чтобы управлять сертифицированными
          роутерами.
        </p>

        <form
          action="/api/operator/login"
          method="post"
          className="mt-8 space-y-5"
        >
          <label className="block">
            <span className="text-sm font-medium text-slate-200">Логин</span>
            <input
              name="username"
              type="text"
              autoComplete="username"
              className="mt-2 w-full rounded-2xl border border-white/10 bg-[rgba(8,12,20,0.82)] px-4 py-3 text-sm text-white transition outline-none focus:border-[var(--vectra-accent)]/60"
              placeholder="operator"
            />
          </label>

          <label className="block">
            <span className="text-sm font-medium text-slate-200">Пароль</span>
            <input
              name="password"
              type="password"
              autoComplete="current-password"
              className="mt-2 w-full rounded-2xl border border-white/10 bg-[rgba(8,12,20,0.82)] px-4 py-3 text-sm text-white transition outline-none focus:border-[var(--vectra-accent)]/60"
              placeholder="Введите пароль оператора"
            />
          </label>

          {hasError ? (
            <div className="rounded-2xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-100">
              Неверный логин или пароль.
            </div>
          ) : null}

          <button
            type="submit"
            className="w-full rounded-full bg-[var(--vectra-accent)] px-5 py-3 text-sm font-semibold text-slate-950 transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)]"
          >
            Войти
          </button>
        </form>

        <p className="mt-6 text-sm leading-7 text-slate-400">
          После входа основная работа ведётся на экране роутера: текущая
          конфигурация, черновик, предпросмотр применения, история изменений,
          обновления и rescue.
        </p>

        <div className="mt-6 text-sm text-slate-500">
          <Link
            href="/install"
            className="inline-flex min-h-11 items-center underline decoration-white/20 underline-offset-4 hover:text-slate-300"
          >
            Перейти к публичной установке
          </Link>
        </div>
      </div>
    </section>
  );
}
