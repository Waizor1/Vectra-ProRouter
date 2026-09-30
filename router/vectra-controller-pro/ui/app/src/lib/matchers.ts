// Raw xray routing matchers → names a person recognises. Brand names are the
// same in every language; the rest go through i18n. Anything unknown is shown
// raw (minus the noisy "domain:" prefix), with the exact matcher in the title.

import type { Key, T } from '../i18n';
import { own } from './own';

/** In the order a route that carries several of them is named: "Telegram и TikTok". */
export const BRANDS: Record<string, string> = {
  'geosite:telegram': 'Telegram',
  'geoip:telegram': 'Telegram',
  'geosite:tiktok': 'TikTok',
  'geosite:youtube': 'YouTube',
  'geosite:google': 'Google',
  'geosite:meta': 'Instagram · Facebook',
  'geosite:instagram': 'Instagram',
  'geosite:facebook': 'Facebook',
  'geosite:whatsapp': 'WhatsApp',
  'geosite:discord': 'Discord',
  'geosite:twitter': 'X',
  'geosite:netflix': 'Netflix',
  'geosite:openai': 'ChatGPT',
};

const WORDS: Record<string, Key> = {
  'geosite:category-gov-ru': 'm.gov',
  'geosite:category-ru': 'm.ruSites',
  'geosite:ru': 'm.ruSites',
  'domain:ru': 'm.ru',
  'domain:xn--p1ai': 'm.rf',
  'domain:рф': 'm.rf',
  'domain:by': 'm.by',
  'geoip:ru': 'm.ruIp',
  'geoip:private': 'm.private',
};

/** Matchers that make a route "Russian sites" when it is named. */
export const RU_SITES = ['geosite:category-gov-ru', 'geosite:category-ru', 'geosite:ru', 'domain:ru', 'domain:xn--p1ai', 'domain:рф', 'geoip:ru'];

// RFC 1918 / loopback / link-local / ULA ranges → "local network".
const PRIVATE = /^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|f[cd][0-9a-f]{0,2}:|fe80:|::1\/)/i;

export interface MatcherLabel {
  text: string;
  /** True when the label is a recognised service or category, not a raw matcher. */
  known: boolean;
}

export function matcherLabel(t: T, raw: string): MatcherLabel {
  const key = raw.trim();
  const lower = key.toLowerCase();
  const brand = own(BRANDS, lower);
  if (brand) return { text: brand, known: true };
  const word = own(WORDS, lower);
  if (word) return { text: t(word), known: true };
  if (PRIVATE.test(key)) return { text: t('m.private'), known: true };
  const m = /^(domain|full):(.+)$/i.exec(key);
  return { text: m ? m[2] : key, known: false };
}

/** Labels for one rule's sample, recognised ones first, duplicates dropped. */
export function matcherLabels(t: T, sample: string[]): { label: MatcherLabel; raw: string }[] {
  const seen = new Set<string>();
  const out: { label: MatcherLabel; raw: string }[] = [];
  for (const raw of sample) {
    const label = matcherLabel(t, raw);
    if (seen.has(label.text)) continue;
    seen.add(label.text);
    out.push({ label, raw });
  }
  return out.sort((a, b) => Number(b.label.known) - Number(a.label.known));
}
