export function DisabledFeatureNotice({ text }: { text: string }) {
  return (
    <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-xs leading-6 text-slate-400">
      {text}
    </div>
  );
}
