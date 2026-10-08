import { own } from '../lib/own';
import { S, type Key } from './strings';

export type { Key };
export type Lang = 'ru' | 'en' | 'zh';
export type Params = Record<string, string | number>;

export const LANGS: readonly Lang[] = ['ru', 'en', 'zh'];
export const LANG_LABEL: Record<Lang, string> = { ru: 'RU', en: 'EN', zh: '中文' };
export const LOCALE: Record<Lang, string> = { ru: 'ru-RU', en: 'en-GB', zh: 'zh-CN' };
const IDX: Record<Lang, 0 | 1 | 2> = { ru: 0, en: 1, zh: 2 };
// CLDR plural categories in the order the '|'-separated forms are written.
const PLURAL_ORDER: Record<Lang, string[]> = { ru: ['one', 'few', 'many'], en: ['one', 'other'], zh: ['other'] };

/** 'ru', 'ru-RU', 'zh_Hans', 'zh-cn' → a supported language, or null. */
export function normLang(x: unknown): Lang | null {
  if (typeof x !== 'string') return null;
  const s = x.trim().toLowerCase();
  for (const l of LANGS) if (s === l || s.startsWith(l + '-') || s.startsWith(l + '_')) return l;
  return null;
}

/** Stored choice → the host page's language → the browser's → English. */
export function pickLang(stored: unknown, mountLang: unknown, navLang: unknown): Lang {
  return normLang(stored) ?? normLang(mountLang) ?? normLang(navLang) ?? 'en';
}

export function interpolate(s: string, p?: Params): string {
  if (!p) return s;
  return s.replace(/\{(\w+)\}/g, (_, k: string) => {
    const v = p[k];
    return v === undefined || v === null || (typeof v === 'number' && !isFinite(v)) ? '—' : String(v);
  });
}

const rulesCache: Partial<Record<Lang, Intl.PluralRules | null>> = {};

function ruFallback(n: number): string {
  const i = Math.abs(Math.trunc(n));
  if (i !== n) return 'other';
  const m10 = i % 10;
  const m100 = i % 100;
  if (m10 === 1 && m100 !== 11) return 'one';
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return 'few';
  return 'many';
}

export function pluralIndex(lang: Lang, n: number): number {
  let rules = rulesCache[lang];
  if (rules === undefined) {
    try {
      rules = new Intl.PluralRules(LOCALE[lang]);
    } catch {
      rules = null;
    }
    rulesCache[lang] = rules;
  }
  const cat = rules ? rules.select(n) : lang === 'ru' ? ruFallback(n) : lang === 'en' && n === 1 ? 'one' : 'other';
  const order = PLURAL_ORDER[lang];
  const i = order.indexOf(cat);
  // Russian fractions ("1,5 узла") read like "few"; en/zh fall to their last form.
  return i >= 0 ? i : lang === 'ru' ? 1 : order.length - 1;
}

/**
 * A hyphen next to a part of one or two letters holds the line together:
 * "Wi-Fi", "QR-код", "что-то" never break into "Wi-" / "Fi". The word joiner
 * (U+2060) is invisible and needs no glyph. Only the table's own words:
 * values put into a sentence (a network's name) stay as they are.
 */
export const glue = (s: string): string =>
  s.replace(/(^|[^\p{L}])(\p{L}{1,2})-(?=\p{L})/gu, '$1$2-\u2060').replace(/(\p{L})-(\p{L}{1,2})(?!\p{L})/gu, '$1-\u2060$2');

export interface T {
  (key: Key, p?: Params): string;
  /** Plural sentence; {n} gets the locale-formatted count. */
  n(key: Key, count: number, p?: Params): string;
  has(key: string): key is Key;
  lang: Lang;
}

export function makeT(lang: Lang): T {
  const nf = new Intl.NumberFormat(LOCALE[lang]);
  const glued = new Map<string, string>();
  const text = (key: string): string | undefined => {
    let s = glued.get(key);
    if (s === undefined) {
      const row = own<readonly string[]>(S, key);
      if (!row) return undefined;
      glued.set(key, (s = glue(row[IDX[lang]])));
    }
    return s;
  };
  const t = ((key: Key, p?: Params) => {
    const s = text(key);
    return s !== undefined ? interpolate(s, p) : String(key);
  }) as T;
  t.n = (key, count, p) => {
    const s = text(key);
    if (s === undefined) return String(key);
    const forms = s.split('|');
    const form = forms[Math.min(pluralIndex(lang, count), forms.length - 1)];
    return interpolate(form, { ...p, n: nf.format(count) });
  };
  t.has = (key: string): key is Key => own(S, key) !== undefined;
  t.lang = lang;
  return t;
}

/** t with default parameters ({brand} on every sentence); a caller's own win. */
export function withParams(t: T, defaults: Params): T {
  const wrapped = ((key: Key, p?: Params) => t(key, { ...defaults, ...p })) as T;
  wrapped.n = (key, count, p) => t.n(key, count, { ...defaults, ...p });
  wrapped.has = t.has;
  wrapped.lang = t.lang;
  return wrapped;
}
