// Flags live in provider data: a remark starts with one or more flag emoji
// ("🇩🇪 Германия", "🇷🇺🇪🇺 Авто…") or another emoji ("⚪ Белые списки"), and a
// node carries an ISO country hint derived from its tag.

// A leading run of regional-indicator pairs and/or pictographic emoji
// (with optional VS16 and ZWJ sequences), then whitespace.
const LEAD = /^((?:\p{Regional_Indicator}{2}|\p{Extended_Pictographic}️?(?:‍\p{Extended_Pictographic}️?)*)+)\s*/u;

export interface Remark {
  /** The leading emoji run, e.g. "🇩🇪" or "🇷🇺🇪🇺"; null when the remark has none. */
  flag: string | null;
  /** The remark without its leading emoji. */
  name: string;
}

export function parseRemark(remark: string | null | undefined): Remark {
  const s = (remark ?? '').trim();
  const m = LEAD.exec(s);
  if (!m) return { flag: null, name: s };
  return { flag: m[1], name: s.slice(m[0].length).trim() };
}

/** "DE" → "🇩🇪". Anything that is not a two-letter code → null. */
export function isoFlag(code: string | null | undefined): string | null {
  if (typeof code !== 'string' || !/^[A-Za-z]{2}$/.test(code)) return null;
  const up = code.toUpperCase();
  const base = 0x1f1e6 - 65;
  return String.fromCodePoint(base + up.charCodeAt(0), base + up.charCodeAt(1));
}
