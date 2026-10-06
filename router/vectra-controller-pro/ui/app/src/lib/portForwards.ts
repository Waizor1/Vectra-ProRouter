// Port forwarding: a rule is a device, a port (or a range; the same outside
// and inside), a protocol, whether the device's own traffic goes around the
// VPN, and on/off. What the screen checks before it asks the router, and how
// a rule goes over the wire. The router is authoritative (contract:
// port_forwards, set_port_forwards).

import type { PortForward, PortForwardIn, PortForwards } from '../api/types';
import type { Key } from '../i18n';
import { presetOf, type Preset } from './presets';

export const PF_MAX = 32;
export const PROTOS = ['tcp', 'udp', 'both'] as const;
export type Proto = (typeof PROTOS)[number];

// ── ports ───────────────────────────────────────────────────────────────────

export interface Span {
  lo: number;
  hi: number;
}

/** "25565" or "3478-3480" (an en dash, a colon or spaces are forgiven); null unless a port or range of 1–65535. */
export function parsePorts(s: string): Span | null {
  const v = s.replace(/\s+/g, '').replace(/[–—:]/g, '-');
  const m = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(v);
  if (!m) return null;
  const lo = +m[1];
  const hi = m[2] === undefined ? lo : +m[2];
  return lo >= 1 && hi <= 65535 && lo <= hi ? { lo, hi } : null;
}

/** How a port or range goes on the wire: "80", "3478-3480". */
export const portText = (p: Span): string => (p.lo === p.hi ? String(p.lo) : p.lo + '-' + p.hi);

/** A port or range as people read it: "3478–3480". */
export const portLabel = (s: string | null): string => (s ? s.replace(/-/g, '–') : '—');

const protoSet = (p: string | null): string[] => (p === 'both' ? ['tcp', 'udp'] : p ? [p] : []);

/** Two rules take the same port: a protocol in common and ranges that meet. */
export function overlaps(a: { proto: string | null; port: string | null }, b: { proto: string | null; port: string | null }): boolean {
  const pa = parsePorts(a.port ?? '');
  const pb = parsePorts(b.port ?? '');
  if (!pa || !pb) return false;
  return protoSet(a.proto).some((x) => protoSet(b.proto).indexOf(x) >= 0) && pa.lo <= pb.hi && pb.lo <= pa.hi;
}

/** A dotted IPv4 address as a number; null for anything else (leading zeros included). */
export function ipv4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim());
  if (!m) return null;
  let n = 0;
  for (let i = 1; i <= 4; i++) {
    const part = m[i];
    if ((part.length > 1 && part[0] === '0') || +part > 255) return null;
    n = n * 256 + +part;
  }
  return n;
}

// ── the list: a preset's rules for one device are one line ─────────────────

/**
 * The rules as lines, in the list's order: each line the indexes of its rules.
 * The rules of a preset the UI knows, for one device, alike in «past the VPN»
 * and on/off, are one line (they were made together; one switch and one form
 * can stand for them all). Any other rule — one's own port, a tag this UI does
 * not know, a rule changed on its own in Connect or LuCI — is a line of its own.
 */
export function groups(rules: PortForward[]): number[][] {
  const out: number[][] = [];
  const at: Record<string, number[]> = {};
  rules.forEach((r, i) => {
    const key = presetOf(r.preset) ? [r.preset, r.destIp, r.direct === true, r.enabled !== false].join(' ') : '';
    if (key && at[key]) at[key].push(i);
    else out.push((at[key || i] = [i]));
  });
  return out;
}

// ── the form ────────────────────────────────────────────────────────────────

export interface DraftRule {
  port: string;
  /** null until the person picks it, where the program's makers do not say. */
  proto: Proto | null;
}

export interface Draft {
  /** The catalogue's tag; null: the owner's own port. */
  preset: string | null;
  destIp: string;
  rules: DraftRule[];
  direct: boolean;
  enabled: boolean;
}

export const emptyDraft = (p?: Preset): Draft => ({
  preset: p ? p.id : null,
  destIp: '',
  rules: p ? p.rules.map((r) => ({ ...r })) : [{ port: '', proto: 'tcp' }],
  direct: p ? p.direct : false,
  enabled: true,
});

const protoOf = (p: string): Proto => ((PROTOS as readonly string[]).indexOf(p) >= 0 ? (p as Proto) : 'tcp');

/** A line of saved rules as the form: theirs, not the catalogue's. */
export const draftOf = (all: PortForward[], at: number[]): Draft => {
  const r = all[at[0]];
  return {
    preset: r.preset,
    destIp: r.destIp,
    rules: at.map((i) => ({ port: all[i].port, proto: protoOf(all[i].proto) })),
    direct: r.direct === true,
    enabled: r.enabled !== false,
  };
};

export type Field = 'destIp' | 'port';

export interface Errors {
  destIp?: Key;
  /** Beside each rule's port, by its place in the form. */
  port: (Key | undefined)[];
  /** A rule still waits for its protocol to be picked. */
  proto: boolean;
}

/**
 * The form's mistakes, beside their fields. `at`: the saved rules the form
 * replaces (none for a new line), so they do not clash with themselves. Rules
 * made elsewhere (LuCI) the screen does not see: the router names that clash.
 */
export function checkDraft(d: Draft, rules: PortForward[], at: number[]): Errors {
  const e: Errors = { port: [], proto: d.rules.some((r) => !r.proto) };
  if (!d.destIp.trim()) e.destIp = 'pf.e.device';
  else if (ipv4(d.destIp) === null) e.destIp = 'pf.e.ip';
  const others: { proto: string | null; port: string | null }[] = d.enabled ? rules.filter((r, i) => at.indexOf(i) < 0 && r.enabled !== false) : [];
  d.rules.forEach((r, i) => {
    const port = parsePorts(r.port);
    const mine = { proto: r.proto, port: port && portText(port) };
    e.port[i] = !r.port.trim() ? 'w.req' : !port ? 'pf.e.port' : others.some((x) => overlaps(mine, x)) ? 'a.port_conflict' : undefined;
    // The form's own rules must not take one port twice either.
    if (d.enabled) others.push(mine);
  });
  return e;
}

/** Nothing stands in the way of saving. */
export const clean = (e: Errors): boolean => !e.destIp && !e.proto && e.port.every((x) => !x);

// ── the wire ────────────────────────────────────────────────────────────────

/** A saved rule as it goes back: unchanged, its id kept. */
export function wireOf(r: PortForward): PortForwardIn {
  const out: PortForwardIn = { preset: r.preset, destIp: r.destIp, port: r.port, proto: r.proto, direct: r.direct === true, enabled: r.enabled !== false };
  if (r.id) out.id = r.id;
  return out;
}

/**
 * The whole list with one line replaced by the form (or the form added at
 * the end): its rules take the saved ids in order; a rule the form no longer
 * has goes. Only for a form with every protocol picked.
 */
export function withDraft(all: PortForward[], at: number[], d: Draft): PortForwardIn[] {
  const mine = d.rules.map((r, k): PortForwardIn => {
    const port = parsePorts(r.port);
    const out: PortForwardIn = { preset: d.preset, destIp: d.destIp.trim(), port: port ? portText(port) : r.port.trim(), proto: r.proto!, direct: d.direct, enabled: d.enabled };
    const id = k < at.length ? all[at[k]].id : null;
    if (id) out.id = id;
    return out;
  });
  const out: PortForwardIn[] = [];
  all.forEach((r, i) => (i === at[0] ? out.push(...mine) : at.indexOf(i) < 0 && out.push(wireOf(r))));
  return at.length ? out : out.concat(mine);
}

/** The whole list without a line. */
export const without = (all: PortForward[], at: number[]): PortForwardIn[] => all.filter((_, i) => at.indexOf(i) < 0).map(wireOf);

/** The whole list with a line switched on or off. */
export const switched = (all: PortForward[], at: number[], on: boolean): PortForwardIn[] =>
  all.map((r, i) => (at.indexOf(i) < 0 ? wireOf(r) : { ...wireOf(r), enabled: on }));

const sig = (r: PortForwardIn) => [r.preset, r.destIp, r.port, r.proto, r.direct, r.enabled].join('\u0001');

/**
 * Where the rules a form was opened on are in the list now (it is read again
 * every few seconds; Connect or LuCI may have changed it meanwhile): each by
 * its id and unchanged (one without an id: an unchanged one not taken yet).
 * null when any is gone or changed — the form must not be saved over it.
 */
export function locate(all: PortForward[], were: PortForward[]): number[] | null {
  const out: number[] = [];
  for (const w of were) {
    const want = sig(wireOf(w));
    const i = all.findIndex((r, k) => out.indexOf(k) < 0 && (r.id ?? null) === (w.id ?? null) && sig(wireOf(r)) === want);
    if (i < 0) return null;
    out.push(i);
  }
  return out;
}

/** The router shows the list that was sent (ids aside: a new rule gets its id there): a `pending` save has landed. */
export function landed(sent: PortForwardIn[], now: PortForwards | null): boolean {
  if (!now || now.rules.length !== sent.length) return false;
  const got = now.rules.map((r) => sig(wireOf(r))).sort();
  const want = sent.map(sig).sort();
  return got.every((x, i) => x === want[i]);
}

// ── the router's answers ────────────────────────────────────────────────────

// The router's own codes (a.<code>); only `invalid_params` gets words about a rule here.
export const FAIL: Partial<Record<string, Key>> = {
  invalid_params: 'pf.e.invalid_params',
  too_many: 'a.too_many',
  dest_not_lan: 'a.dest_not_lan',
  dest_is_router: 'a.dest_is_router',
  port_conflict: 'a.port_conflict',
  busy: 'a.busy',
  apply_failed: 'a.failed',
  locked: 'a.locked',
  failed: 'a.failed',
};

/** The sentence for a refused `set_port_forwards`; null for a code without one here (the generic words apply). */
export const failKey = (code: string): Key | null => (Object.prototype.hasOwnProperty.call(FAIL, code) ? FAIL[code]! : null);

/** Which field a refusal is about; the rest are said under the form. */
export const FIELD_OF: Partial<Record<string, Field>> = { port_conflict: 'port', dest_not_lan: 'destIp', dest_is_router: 'destIp' };
