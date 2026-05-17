export const TONES = ["good", "warning", "critical", "info", "neutral"] as const;

export type Tone = (typeof TONES)[number];

export type ToneSet = {
  background: string;
  border: string;
  text: string;
  dot: string;
  ring: string;
};

export const toneClasses: Record<Tone, ToneSet> = {
  good: {
    background: "bg-[color:var(--tone-good)]/12",
    border: "border-[color:var(--tone-good)]/35",
    text: "text-[color:var(--tone-good)]",
    dot: "bg-[color:var(--tone-good)]",
    ring: "ring-[color:var(--tone-good)]/40",
  },
  warning: {
    background: "bg-[color:var(--tone-warning)]/12",
    border: "border-[color:var(--tone-warning)]/35",
    text: "text-[color:var(--tone-warning)]",
    dot: "bg-[color:var(--tone-warning)]",
    ring: "ring-[color:var(--tone-warning)]/40",
  },
  critical: {
    background: "bg-[color:var(--tone-critical)]/12",
    border: "border-[color:var(--tone-critical)]/35",
    text: "text-[color:var(--tone-critical)]",
    dot: "bg-[color:var(--tone-critical)]",
    ring: "ring-[color:var(--tone-critical)]/40",
  },
  info: {
    background: "bg-[color:var(--tone-info)]/12",
    border: "border-[color:var(--tone-info)]/35",
    text: "text-[color:var(--tone-info)]",
    dot: "bg-[color:var(--tone-info)]",
    ring: "ring-[color:var(--tone-info)]/40",
  },
  neutral: {
    background: "bg-white/5",
    border: "border-white/10",
    text: "text-slate-200",
    dot: "bg-slate-400",
    ring: "ring-white/15",
  },
};

export function toneClass(tone: Tone, slot: keyof ToneSet): string {
  return toneClasses[tone][slot];
}
