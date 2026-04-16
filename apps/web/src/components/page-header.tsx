export function PageHeader({
  eyebrow,
  title,
  description,
  mobileDescription,
  compact = false,
  aside,
}: {
  eyebrow: string;
  title: string;
  description?: string;
  mobileDescription?: string;
  compact?: boolean;
  aside?: React.ReactNode;
}) {
  const mobileCopy = mobileDescription ?? description;

  return (
    <header className={`rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 ${compact ? "py-3.5" : "py-4 sm:py-5"}`}>
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <p className="vectra-kicker text-[var(--vectra-accent)]">
            {eyebrow}
          </p>
          <h1
            className={`mt-1.5 font-semibold tracking-[-0.03em] text-white ${
              compact
                ? "text-lg sm:text-xl"
                : "text-2xl sm:text-3xl md:text-[2.4rem]"
            }`}
          >
            {title}
          </h1>
          {mobileCopy ? (
            <p
              className={`mt-1.5 max-w-3xl text-[13px] leading-5 text-slate-400 sm:hidden ${
                compact ? "text-xs leading-5" : ""
              }`}
            >
              {mobileCopy}
            </p>
          ) : null}
          {description ? (
            <p className="mt-2 hidden max-w-3xl text-sm leading-6 text-slate-400 sm:block">
              {description}
            </p>
          ) : null}
        </div>
        {aside ? <div className="w-full lg:w-auto">{aside}</div> : null}
      </div>
    </header>
  );
}
