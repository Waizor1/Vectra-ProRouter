// «+ сервис» (spec decision 4): the router offers the categories of its geo
// file; a chosen one goes into «Мои сайты» as "geosite:<id>". The UI names
// the ones people know, finds them by what people call them, and puts the
// popular ones first. A category it does not know is shown by its id — a
// newer geo file may bring one.

import type { Key } from '../i18n';

export const SERVICE_PREFIX = 'geosite:';

interface Known {
  /** A brand, the same in every language. */
  brand?: string;
  /** A kind of site, said in the reader's language. */
  key?: Key;
  /** What people type looking for it, besides its name and id. */
  aliases: string[];
}

const KNOWN: Record<string, Known> = {
  discord: { brand: 'Discord', aliases: ['дискорд'] },
  meta: { brand: 'Instagram, Facebook, WhatsApp', aliases: ['instagram', 'инстаграм', 'инста', 'facebook', 'фейсбук', 'whatsapp', 'ватсап', 'вотсап', 'meta', 'мета'] },
  twitter: { brand: 'X (Twitter)', aliases: ['x.com', 'твиттер', 'твитер'] },
  youtube: { brand: 'YouTube', aliases: ['ютуб', 'ютьюб'] },
  tiktok: { brand: 'TikTok', aliases: ['тикток', 'тик ток'] },
  telegram: { brand: 'Telegram', aliases: ['телеграм', 'телега'] },
  'google-ai': { brand: 'Gemini (Google AI)', aliases: ['джемини', 'гемини', 'bard'] },
  'google-meet': { brand: 'Google Meet', aliases: ['гугл мит'] },
  'google-play': { brand: 'Google Play', aliases: ['play market', 'плей маркет', 'гугл плей'] },
  roblox: { brand: 'Roblox', aliases: ['роблокс'] },
  hdrezka: { brand: 'HDRezka', aliases: ['rezka', 'резка'] },
  geoblock: { key: 'svc.cat.geoblock', aliases: [] },
  'russia-outside': { key: 'svc.cat.russia-outside', aliases: [] },
  'russia-inside': { key: 'svc.cat.russia-inside', aliases: [] },
  news: { key: 'svc.cat.news', aliases: [] },
  anime: { key: 'svc.cat.anime', aliases: [] },
  porn: { key: 'svc.cat.porn', aliases: ['18+'] },
  'category-ru': { key: 'svc.cat.category-ru', aliases: [] },
  'category-gov-ru': { key: 'svc.cat.category-gov-ru', aliases: ['госуслуги'] },
  'ukraine-inside': { key: 'svc.cat.ukraine-inside', aliases: [] },
  block: { key: 'svc.cat.block', aliases: ['ркн'] },
  cloudflare: { brand: 'Cloudflare', aliases: [] },
  cloudfront: { brand: 'Amazon CloudFront', aliases: ['aws'] },
  digitalocean: { brand: 'DigitalOcean', aliases: [] },
  hetzner: { brand: 'Hetzner', aliases: [] },
  ovh: { brand: 'OVH', aliases: [] },
};

// The order people look for them in: the popular first, the hosters last,
// then what the UI does not know, by id.
const ORDER = Object.keys(KNOWN);

/** The service an entry of «Мои сайты» names, or null for a site. */
export function serviceOf(entry: string): string | null {
  return entry.startsWith(SERVICE_PREFIX) ? entry.slice(SERVICE_PREFIX.length) : null;
}

/** A service's name for the reader. */
export function serviceName(t: (k: Key) => string, id: string): string {
  const k = KNOWN[id];
  if (k?.brand) return k.brand;
  if (k?.key) return t(k.key);
  return id.toUpperCase();
}

/** The catalog's services matching what was typed, in the order people look for them; none of `taken`. */
export function findServices(t: (k: Key) => string, catalog: readonly string[], query: string, taken: readonly string[]): string[] {
  const q = query.trim().toLowerCase();
  const rank = (id: string) => {
    const i = ORDER.indexOf(id);
    return i < 0 ? ORDER.length : i;
  };
  return catalog
    .filter((id) => taken.indexOf(id) < 0)
    .filter((id) => {
      if (!q) return true;
      const k = KNOWN[id];
      return id.includes(q) || serviceName(t, id).toLowerCase().includes(q) || !!k?.aliases.some((a) => a.includes(q));
    })
    .sort((a, b) => rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0));
}
