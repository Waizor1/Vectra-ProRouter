// The port-forwarding catalogue: what a person may want to open, with the
// ports its makers name (docs/superpowers/specs/2026-10-06-port-presets-games-research.md,
// official sources). The router keeps only the tag (`preset`); a preset of
// several rules becomes several router rules with the same tag and device,
// and the list shows them as one.

import type { Proto } from './portForwards';
import DATA from './presets.txt?raw';

export const CATS = ['games', 'consoles', 'remote', 'media', 'home', 'voice', 'vpn'] as const;
export type Cat = (typeof CATS)[number];

export interface PresetRule {
  port: string;
  /** null: the makers name the port, not the protocol — the person picks it, nothing is assumed. */
  proto: Proto | null;
}

export interface Preset {
  /** The tag the router keeps: 1–24 of a-z, 0-9, "-". */
  id: string;
  cat: Cat;
  /** The program's own name; null: said in the UI's words (`pf.pre.<id>`). */
  name: string | null;
  rules: PresetRule[];
  /** Consoles want their own traffic past the VPN (an open NAT). */
  direct: boolean;
}

// presets.txt, a preset a line: id|category|name|rules — the category by its
// first letter ("n": own VPN server); name empty: the id capitalised; "~": the
// UI's words. A rule is the port and t (TCP), u (UDP), b (both) or ? (the
// makers do not say). The build packs it with the CSS and the strings.
const PROTO: Record<string, Proto | null> = { t: 'tcp', u: 'udp', b: 'both', '?': null };

export const PRESETS: readonly Preset[] = DATA.trim().split('\n').map((row) => {
  const [id, c, name, rules] = row.split('|');
  const cat = CATS.find((x) => (x === 'vpn' ? 'n' : x[0]) === c)!;
  return {
    id,
    cat,
    name: name === '~' ? null : name || id[0].toUpperCase() + id.slice(1),
    rules: rules.split(',').map((r) => ({ port: r.slice(0, -1), proto: PROTO[r.slice(-1)] })),
    direct: cat === 'consoles',
  };
});

/** The wizard's first screen: the few most people come for; the rest are under «All presets». */
export const POPULAR = ['minecraft-java', 'playstation', 'xbox', 'rdp', 'plex', 'transmission'];

/** A preset the UI knows, by the tag the router kept; undefined for none or a newer one. */
export const presetOf = (id: string | null): Preset | undefined => (id ? PRESETS.find((p) => p.id === id) : undefined);
