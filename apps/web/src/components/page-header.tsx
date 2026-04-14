export function PageHeader({
  eyebrow,
  title,
  description,
  mobileDescription,
}: {
  eyebrow: string;
  title: string;
  description?: string;
  mobileDescription?: string;
}) {
  const mobileCopy = mobileDescription ?? description;

  return (
    <header className="rounded-md border border-white/10 bg-[var(--vectra-panel)] px-3 py-3 shadow-[0_18px_60px_rgba(0,0,0,0.28)] sm:px-4 sm:py-4">
      <p className="vectra-kicker text-[var(--vectra-accent)]">
        {eyebrow}
      </p>
      <h1 className="mt-2 text-xl font-semibold tracking-[-0.02em] text-white sm:text-2xl md:text-3xl">
        {title}
      </h1>
      {mobileCopy ? (
        <p className="mt-2 max-w-3xl text-[13px] leading-5 text-slate-300 sm:hidden">
          {mobileCopy}
        </p>
      ) : null}
      {description ? (
        <p className="mt-2 hidden max-w-3xl text-sm leading-7 text-slate-300 sm:block">
          {description}
        </p>
      ) : null}
    </header>
  );
}
