// Port forwarding: a rule is a device, a port (or a range; the same outside
// and inside), a protocol, whether the device's own traffic goes around the
// VPN, and on/off. What the screen checks before it asks the router, and how
// a rule goes over the wire. The router is authoritative (contract:
// port_forwards, set_port_forwards).

import type { PortForward, PortForwardIn, PortForwards } from '../api/types';
import type { Key } from '../i18n';

export const PF_MAX = 32;
export const PROTOS = ['tcp', 'udp', 'both'] as const;
export type Proto = (typeof PROTOS)[number];

// ── presets: the UI's catalogue; the router keeps only the tag ──────────────

export interface Preset {
  id: string;
  /** The program's own name; null: said in the UI's words (`pf.pre.<id>`). */
  name: string | null;
  port: string;
  proto: Proto;
  /** Consoles want their own traffic past the VPN (an open NAT). */
  direct: boolean;
}

export const PRESETS: readonly Preset[] = [
  { id: 'minecraft', name: 'Minecraft', port: '25565', proto: 'tcp', direct: false },
  { id: 'playstation', name: 'PlayStation', port: '3478-3480', proto: 'both', direct: true },
  { id: 'xbox', name: 'Xbox', port: '3074', proto: 'both', direct: true },
  { id: 'rdp', name: null, port: '3389', proto: 'tcp', direct: false },
  { id: 'plex', name: 'Plex', port: '32400', proto: 'tcp', direct: false },
  { id: 'torrent', name: null, port: '51413', proto: 'both', direct: false },
];

/** A preset the UI knows, by the tag the router kept; undefined for none or a newer one. */
export const presetOf = (id: string | null): Preset | undefined => (id ? PRESETS.find((p) => p.id === id) : undefined);

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

// ── the form ────────────────────────────────────────────────────────────────

export interface Draft {
  /** The catalogue's tag; null: the owner's own port. */
  preset: string | null;
  destIp: string;
  port: string;
  proto: Proto;
  direct: boolean;
  enabled: boolean;
}

export const emptyDraft = (p?: Preset): Draft => ({
  preset: p ? p.id : null,
  destIp: '',
  port: p ? p.port : '',
  proto: p ? p.proto : 'tcp',
  direct: p ? p.direct : false,
  enabled: true,
});

export const draftOf = (r: PortForward): Draft => ({
  preset: r.preset,
  destIp: r.destIp,
  port: r.port,
  proto: (PROTOS as readonly string[]).indexOf(r.proto) >= 0 ? (r.proto as Proto) : 'tcp',
  direct: r.direct === true,
  enabled: r.enabled !== false,
});

export type Field = 'destIp' | 'port';

/**
 * The form's mistakes, beside their fields. `index`: the rule being edited
 * (-1 for a new one), so it does not clash with itself. Rules made elsewhere
 * (LuCI) the screen does not see: the router names that clash itself.
 */
export function checkDraft(d: Draft, rules: PortForward[], index: number): Partial<Record<Field, Key>> {
  const e: Partial<Record<Field, Key>> = {};
  if (!d.destIp.trim()) e.destIp = 'pf.e.device';
  else if (ipv4(d.destIp) === null) e.destIp = 'pf.e.ip';
  const port = parsePorts(d.port);
  if (!d.port.trim()) e.port = 'w.req';
  else if (!port) e.port = 'pf.e.port';
  else if (d.enabled && rules.some((r, i) => i !== index && r.enabled !== false && overlaps({ proto: d.proto, port: portText(port) }, r))) e.port = 'a.port_conflict';
  return e;
}

// ── the wire ────────────────────────────────────────────────────────────────

/** A saved rule as it goes back: unchanged, its id kept. */
export function wireOf(r: PortForward): PortForwardIn {
  const out: PortForwardIn = { preset: r.preset, destIp: r.destIp, port: r.port, proto: r.proto, direct: r.direct === true, enabled: r.enabled !== false };
  if (r.id) out.id = r.id;
  return out;
}

/** The form as a rule; `id` when it replaces a saved one. */
export function wireOfDraft(d: Draft, id: string | null): PortForwardIn {
  const port = parsePorts(d.port);
  const out: PortForwardIn = { preset: d.preset, destIp: d.destIp.trim(), port: port ? portText(port) : d.port.trim(), proto: d.proto, direct: d.direct, enabled: d.enabled };
  if (id) out.id = id;
  return out;
}

const sig = (r: PortForwardIn) => [r.preset, r.destIp, r.port, r.proto, r.direct, r.enabled].join('\u0001');

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
