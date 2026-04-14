"use client";

import { useState } from "react";

export function CopyTextButton({
  text,
  label = "Копировать",
  copiedLabel = "Скопировано",
}: {
  text: string;
  label?: string;
  copiedLabel?: string;
}) {
  const [copied, setCopied] = useState(false);

  return (
    <button
      type="button"
      className="w-full rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium tracking-[0.01em] text-slate-300 transition hover:border-white/20 hover:text-white sm:w-auto"
      onClick={async () => {
        await navigator.clipboard.writeText(text);
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1600);
      }}
    >
      {copied ? copiedLabel : label}
    </button>
  );
}
