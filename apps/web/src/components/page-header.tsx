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
    <header
      className={`rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 ${compact ? "py-3" : "py-3.5 sm:py-4"}`}
    >
      <div className="flex min-w-0 flex-col gap-2.5 xl:flex-row xl:items-start xl:justify-between">
        <div className="min-w-0">
          <p className="vectra-kicker text-[var(--vectra-accent)]">{eyebrow}</p>
          <h1
            className={`mt-1 font-semibold tracking-[-0.03em] text-white ${
              compact
                ? "text-xl sm:text-[1.65rem]"
                : "text-[1.65rem] sm:text-[2rem]"
            }`}
          >
            {title}
          </h1>
          {mobileCopy ? (
            <p
              className={`mt-1.5 max-w-3xl text-[13px] leading-5 text-slate-400 sm:hidden ${
                compact ? "text-[12px] leading-5" : ""
              }`}
            >
              {mobileCopy}
            </p>
          ) : null}
          {description ? (
            <p className="mt-1.5 max-sm:hidden max-w-3xl text-sm leading-6 text-slate-400 sm:block">
              {description}
            </p>
          ) : null}
        </div>
        {aside ? <div className="min-w-0 w-full xl:w-auto">{aside}</div> : null}
      </div>
    </header>
  );
}
