// Provider tags and routing rules → words a person understands.
//
// Providers name their outbounds by convention: "bridge-pl5" is a bridge (in
// through Russia, out in Poland), "hy2-de5" Hysteria2 to Germany,
// "whitelist-lv3-2" a whitelist node of level 3 (for mobile internet that lets
// only approved sites through). A tag that follows no convention is shown as it
// is: a name is never invented. Balancers are named by what the routing rules
// pointing at them carry. The UI keeps the tag next to every name: support
// needs it.

import type { Balancer } from '../api/types';
import { LOCALE, type Lang, type T } from '../i18n';
import { isoFlag } from './flags';
import { BRANDS, RU_SITES } from './matchers';
import { own } from './own';

export type Kind = 'bridge' | 'hy2' | 'wl' | 'direct' | 'block' | 'other';

// The router's own table (internal/uiapi, countryHint): a token that is also a
// word or a tag fragment ("in", "it", "lv" for level) is left out on purpose —
// a wrong flag is worse than none.
const CC: Record<string, string> = {
  pl: 'PL', de: 'DE', fin: 'FI', fi: 'FI', fr: 'FR', nl: 'NL', us: 'US', usa: 'US', tr: 'TR', ae: 'AE', uae: 'AE', ru: 'RU', by: 'BY',
  kz: 'KZ', uk: 'GB', gb: 'GB', se: 'SE', jp: 'JP', sg: 'SG', ge: 'GE', es: 'ES', cz: 'CZ', ee: 'EE', hk: 'HK', kr: 'KR',
};

/** The country a tag names, derived exactly as the router derives nodes[].countryHint: "bridge-fin5" → "FI". */
export function tagCountry(tag: string): string | null {
  for (const tok of tag.toLowerCase().split('-')) {
    const cc = own(CC, tok.replace(/\d+$/, ''));
    if (cc) return cc;
  }
  return null;
}

/** "🇩🇪" → "DE"; anything but exactly one country flag ("🇷🇺🇪🇺", "⚪") → null. */
export function flagCountry(flag: string | null | undefined): string | null {
  const cps = Array.from(flag || '').map((c) => c.codePointAt(0)! - 0x1f1e6);
  return cps.length === 2 && cps.every((x) => x >= 0 && x < 26) ? String.fromCharCode(65 + cps[0], 65 + cps[1]) : null;
}

const regions: Partial<Record<Lang, Intl.DisplayNames | null>> = {};

/** "DE" → "🇩🇪 Германия": the flag kept with its name (a no-break space), so a line never ends on a flag. */
export function flagged(lang: Lang, cc: string): string {
  return [isoFlag(cc), countryName(lang, cc)].filter(Boolean).join('\u00a0');
}

/** "DE" → "Германия" / "Germany" / "德国"; the code itself in a browser that has no country names. */
export function countryName(lang: Lang, cc: string): string {
  let d = regions[lang];
  if (d === undefined) {
    try {
      d = new Intl.DisplayNames([LOCALE[lang]], { type: 'region', style: 'short' });
    } catch {
      d = null;
    }
    regions[lang] = d;
  }
  try {
    return (d && d.of(cc)) || cc;
  } catch {
    return cc;
  }
}

/** "A, B и C" / "A, B, and C" / "A、B和C". */
export function joinList(lang: Lang, items: string[]): string {
  try {
    return new Intl.ListFormat(LOCALE[lang], { type: 'conjunction' }).format(items);
  } catch {
    return items.join(', ');
  }
}

export function kindOf(tag: string): Kind {
  const low = tag.toLowerCase();
  const head = low.split('-')[0];
  if (head === 'bridge') return 'bridge';
  if (head === 'hy2' || head === 'hysteria2' || head === 'hysteria') return 'hy2';
  if (head === 'whitelist' || head === 'wl') return 'wl';
  if (low === 'direct' || low === 'freedom' || low === 'vctl-direct') return 'direct';
  if (low === 'block' || low === 'blackhole') return 'block';
  return 'other';
}

/** The whitelist level a tag names: "whitelist-lv3-2" → 3. */
export const levelOf = (tag: string): number | null => {
  const m = /-lv(\d+)/i.exec(tag);
  return m ? +m[1] : null;
};

export interface Named {
  kind: Kind;
  /** What comes before the arrow: "Мост", "Резерв" (a Hysteria2 node); null when nothing does. */
  pre: string | null;
  flag: string | null;
  /** Inside a list already grouped by kind: "Польша", "Уровень 3"; the tag when nothing is known. */
  short: string;
  /** The whole name as plain text, without the flag: "Мост → Польша", "Белый список, уровень 3". */
  name: string;
  /** The words say more than the tag does. */
  known: boolean;
}

/** A node's name. `hint` is the router's countryHint, for a node that is in nodes[]. */
export function nodeName(t: T, tag: string, hint?: string | null): Named {
  const kind = kindOf(tag);
  let pre: string | null = null;
  let cc: string | null = null;
  let short = tag;
  let name = tag;
  if (kind === 'wl') {
    // "lv" is the level here, not Latvia: no flag, ever.
    const lv = levelOf(tag);
    short = lv === null ? t('ng.wl') : t('nm.lv', { n: lv });
    name = lv === null ? t('nm.wl0') : t('nm.wl', { n: lv });
  } else if (kind === 'direct' || kind === 'block') {
    short = name = t(kind === 'direct' ? 'nm.direct' : 'nm.block');
  } else {
    pre = kind === 'bridge' ? t('nm.bridge') : kind === 'hy2' ? t('nm.hy2') : null;
    cc = hint || tagCountry(tag);
    if (cc) {
      short = countryName(t.lang, cc);
      name = pre ? pre + ' → ' + short : short;
    } else if (pre) {
      short = name = pre;
    }
  }
  return { kind, pre, flag: isoFlag(cc), short, name, known: kind !== 'other' || cc !== null };
}

export interface RouteName {
  name: string;
  /** false: nothing told what it carries, so the name is the tag. */
  known: boolean;
}

/**
 * A balancer's name, from what the routing rules that point at it carry: the
 * catch-all → "Основной трафик", geosite:telegram + tiktok → "Telegram и
 * TikTok", geoip:ru / domain:ru → "Российские сайты". A balancer reached only
 * as another's fallback is named by its nodes: whitelist levels, a backup
 * channel (Hysteria2 nodes), or just a backup. Nothing tells → the tag.
 */
export function routeName(t: T, b: Balancer): RouteName {
  if (b.role === 'main' || b.matchers.some((m) => m.kind === 'all')) return { name: t('rt.main'), known: true };
  const raws = new Set<string>();
  for (const m of b.matchers) for (const s of m.sample) raws.add(s.trim().toLowerCase());
  const parts = RU_SITES.some((x) => raws.has(x)) ? [t('m.ruSites')] : [];
  for (const k in BRANDS) if (raws.has(k) && parts.indexOf(BRANDS[k]) < 0) parts.push(BRANDS[k]);
  if (parts.length) return { name: joinList(t.lang, parts.slice(0, 3)), known: true };
  if (b.role === 'reserve') {
    const tags = b.members.length ? b.members : b.selector;
    const all = (k: Kind) => tags.length > 0 && tags.every((x) => kindOf(x) === k);
    if (all('wl')) {
      const lv = tags.map(levelOf).filter((x, i, a) => a.indexOf(x) === i);
      return { name: lv.length === 1 && lv[0] !== null ? t('rt.wl', { n: lv[0] }) : t('ng.wl'), known: true };
    }
    return { name: t(all('hy2') ? 'rt.hy2' : 'rt.reserve'), known: true };
  }
  return { name: b.tag, known: false };
}
