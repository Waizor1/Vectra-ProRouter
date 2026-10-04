// The subscription's servers as a person chooses among them: the automatic
// one first (it picks the route by itself — through bridges in Russia to the
// freest exit abroad), then countries, then the variants a person rarely
// needs (a protocol, "direct", whitelists for mobile networks), folded away.
// Only the remarks are read; nothing is invented about a server.

import type { Entries, Entry, NodeRef, RouteView } from '../api/types';
import type { T } from '../i18n';
import { parseRemark } from './flags';
import { flagged } from './names';

export interface ServerGroups {
  /** The one to recommend: the panel's default, else the first automatic, else the first. */
  recommended: number | null;
  auto: Entry[];
  countries: Entry[];
  /** "🇩🇪 Германия · Hysteria2", "⚪ Белые списки · Уровень 1": a qualifier after " · ", or no flag. */
  other: Entry[];
}

const AUTO = /(^|[^\p{L}])(авто|auto)([^\p{L}]|$)|自动/iu;
const QUALIFIED = /\s·\s/;
const COUNTRY_FLAG = /^\p{Regional_Indicator}{2}$/u;

/** "Авто Самый стабильный" → "Авто" and "самый стабильный". */
const AUTO_LEAD = /^(авто|auto)(?![\p{L}])[\s·:—-]*(.*)$/iu;

/** How a server reads in a list. */
export interface Face {
  /** The automatic one: shown by what it does (a split of routes), not by the provider's flags. */
  auto: boolean;
  flag: string | null;
  title: string;
  sub: string | null;
}

export function face(remark: string | null | undefined, fallback: string): Face {
  const { flag, name } = parseRemark(remark);
  const m = AUTO_LEAD.exec(name);
  if (m) {
    const rest = m[2].trim();
    // "Самый стабильный" reads as "самый стабильный" under the title; "YouTube" and "VLESS" stay as written.
    const word = rest.split(/\s/)[0];
    const plain = word.length > 1 && word.slice(1) === word.slice(1).toLocaleLowerCase();
    return { auto: true, flag: null, title: m[1], sub: rest ? (plain ? rest.charAt(0).toLocaleLowerCase() + rest.slice(1) : rest) : null };
  }
  return { auto: false, flag, title: name || flag || fallback, sub: null };
}

export function groupServers(data: Entries | null): ServerGroups {
  const list = data?.entries ?? [];
  const auto: Entry[] = [];
  const countries: Entry[] = [];
  const other: Entry[] = [];
  for (const e of list) {
    const { flag, name } = parseRemark(e.remark);
    // "Automatic" is what the server is called, not who chose it: the panel's default may be a country.
    if (AUTO.test(name)) auto.push(e);
    else if (!QUALIFIED.test(name) && flag && COUNTRY_FLAG.test(flag)) countries.push(e);
    else other.push(e);
  }
  const panel = list.find((e) => e.index === data?.panelIndex);
  const recommended = panel?.index ?? auto[0]?.index ?? list[0]?.index ?? null;
  return { recommended, auto, countries, other };
}

/** A node as a person reads it: its country, and where it really exits when that is elsewhere. */
export function nodePlace(t: T, n: NodeRef): string {
  const name = n.country ? flagged(t.lang, n.country) : n.tag;
  return n.egress ? t('x.egress', { name, egress: flagged(t.lang, n.egress) }) : name;
}

/** Where the main traffic goes now: "🇵🇱 Польша"; null before the router has said. */
export function routeVia(t: T, r: RouteView | null | undefined): string | null {
  if (!r || !r.nodes.length) return null;
  return Array.from(new Set(r.nodes.map((n) => nodePlace(t, n)))).join(', ');
}

/**
 * The line under «Авто»: it picks by itself, and where it goes now — so «Авто»
 * and a country read as one choice, not two. A server chosen by name needs neither.
 */
export function autoLine(t: T, via: string | null): string {
  return via ? t('srv.auto.picks') + ' · ' + t('srv.auto.now', { via }) : t('srv.auto.picks');
}
