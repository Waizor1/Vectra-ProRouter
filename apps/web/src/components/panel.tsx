import type { ReactNode } from "react";

export function Panel({
  eyebrow,
  title,
  children,
  aside,
}: {
  eyebrow?: string;
  title: string;
  children: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <section className="rounded-md border border-white/10 bg-[var(--vectra-panel)] px-3 py-3 shadow-[0_16px_48px_rgba(0,0,0,0.25)] sm:px-4 sm:py-4">
      <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0">
          {eyebrow ? (
            <p className="vectra-kicker text-[var(--vectra-accent)]">
              {eyebrow}
            </p>
          ) : null}
          <h2 className="mt-1 text-lg font-semibold tracking-[-0.01em] text-white sm:text-xl">
            {title}
          </h2>
        </div>
        {aside ? <div className="w-full md:w-auto">{aside}</div> : null}
      </div>
      <div className="mt-3 sm:mt-4">{children}</div>
    </section>
  );
}
