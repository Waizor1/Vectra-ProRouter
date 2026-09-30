// What a person types into "My sites" → the entry the router keeps. People
// paste whole links, ports, "www.", Cyrillic domains and IP ranges. The router
// is authoritative (internal/sites); this mirrors it, so the list on screen,
// its duplicates and its errors are what the router will keep. Both sides are
// held to ../../../contract/sites-vector.json. One difference, on purpose: a
// single label ("сбербанк") is refused here as a mistyped site, though the
// router would take it as a whole top-level domain.

export type Site = { value: string; kind: 'domain' | 'ip' | 'service' } | { error: 'empty' | 'invalid' };

const INVALID: Site = { error: 'invalid' };

/**
 * Lowercase as the router's Go does (simple case mapping): JavaScript turns
 * "İ" into "i̇" (two characters) and a word-final "Σ" into "ς", Go into "i"
 * and "σ" — the same letters must name the same domain on both sides.
 */
const lower = (s: string) => s.replace(/\u0130/g, 'i').replace(/\u03a3/g, '\u03c3').toLowerCase();

/** What an East Asian keyboard types for ASCII and for a dot → what a browser sends. */
const foldWidth = (s: string) =>
  s.replace(/[。．｡]/g, '.').replace(/[！-～]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0xfee0));

// ── addresses ────────────────────────────────────────────────────────────────

/** An address as 8 groups of 16 bits (IPv4 in the last two), and which family. */
interface Addr {
  v6: boolean;
  g: number[];
}

function v4(s: string): number[] | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s);
  if (!m) return null;
  const b = m.slice(1).map(Number);
  // No leading zeros ("01.2.3.4"), as Go's netip.
  return m.slice(1).every((x, i) => b[i] <= 255 && String(b[i]) === x) ? b : null;
}

function v6(s: string): number[] | null {
  if (!/^[0-9a-f:.]+$/.test(s) || s.indexOf(':') < 0) return null;
  const halves = s.split('::');
  if (halves.length > 2) return null;
  const groups = (h: string, tail: boolean): number[] | null => {
    if (h === '') return [];
    const out: number[] = [];
    const parts = h.split(':');
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i];
      if (tail && i === parts.length - 1 && p.indexOf('.') >= 0) {
        const b = v4(p);
        if (!b) return null;
        out.push((b[0] << 8) | b[1], (b[2] << 8) | b[3]);
      } else if (/^[0-9a-f]{1,4}$/.test(p)) out.push(parseInt(p, 16));
      else return null;
    }
    return out;
  };
  if (halves.length === 1) {
    const g = groups(s, true);
    return g && g.length === 8 ? g : null;
  }
  const a = groups(halves[0], false);
  const b = groups(halves[1], true);
  if (!a || !b || a.length + b.length > 7) return null;
  return a.concat(new Array<number>(8 - a.length - b.length).fill(0), b);
}

function parseAddr(s: string): Addr | null {
  const b = v4(s);
  if (b) return { v6: false, g: [0, 0, 0, 0, 0, 0xffff, (b[0] << 8) | b[1], (b[2] << 8) | b[3]] };
  const g = v6(s);
  return g ? { v6: true, g } : null;
}

const is4in6 = (a: Addr) => a.v6 && a.g.slice(0, 5).every((x) => x === 0) && a.g[5] === 0xffff;
const unmap = (a: Addr): Addr => (is4in6(a) ? { v6: false, g: a.g } : a);

/** As Go's netip prints it: dotted IPv4; IPv6 in RFC 5952 form. */
function show(a: Addr): string {
  const g = a.g;
  if (!a.v6) return [g[6] >> 8, g[6] & 255, g[7] >> 8, g[7] & 255].join('.');
  if (is4in6(a)) return '::ffff:' + show({ v6: false, g });
  let at = -1;
  let len = 0;
  for (let i = 0; i < 8; ) {
    if (g[i] !== 0) {
      i++;
      continue;
    }
    let j = i;
    while (j < 8 && g[j] === 0) j++;
    if (j - i > len && j - i >= 2) [at, len] = [i, j - i];
    i = j;
  }
  const h = g.map((x) => x.toString(16));
  return at < 0 ? h.join(':') : h.slice(0, at).join(':') + '::' + h.slice(at + len).join(':');
}

/** A CIDR: masked; a full-length one is its address. */
function prefix(a: Addr, bits: string): Site {
  if (!/^\d{1,3}$/.test(bits)) return INVALID;
  let n = +bits;
  if (n > (a.v6 ? 128 : 32)) return INVALID;
  if (is4in6(a) && n >= 96) [a, n] = [unmap(a), n - 96];
  // The prefix in the 128 bits the groups hold: IPv4 is the last 32 of them.
  const bits128 = (a.v6 ? 0 : 96) + n;
  const g = a.g.map((x, i) => {
    const keep = Math.max(0, Math.min(16, bits128 - i * 16));
    return x & ((0xffff << (16 - keep)) & 0xffff);
  });
  const m = { v6: a.v6, g };
  return { value: n === (a.v6 ? 128 : 32) ? show(m) : show(m) + '/' + n, kind: 'ip' };
}

// ── names ────────────────────────────────────────────────────────────────────

// RFC 3492 punycode: decoding for an "xn--" label a person pasted, encoding to
// know a label's length as it travels (63 at most).
const BASE = 36;
const TMIN = 1;
const TMAX = 26;
function adapt(delta: number, points: number, first: boolean): number {
  delta = first ? Math.floor(delta / 700) : delta >> 1;
  delta += Math.floor(delta / points);
  let k = 0;
  for (; delta > ((BASE - TMIN) * TMAX) >> 1; k += BASE) delta = Math.floor(delta / (BASE - TMIN));
  return k + Math.floor(((BASE - TMIN + 1) * delta) / (delta + 38));
}
const threshold = (k: number, bias: number) => (k <= bias ? TMIN : k >= bias + TMAX ? TMAX : k - bias);
const digit = (d: number) => String.fromCharCode(d < 26 ? 97 + d : 22 + d);

function punyEncode(input: string): string {
  const cps = Array.from(input, (ch) => ch.codePointAt(0)!);
  const basic = cps.filter((c) => c < 0x80);
  let out = String.fromCharCode(...basic) + (basic.length ? '-' : '');
  let n = 128;
  let delta = 0;
  let bias = 72;
  for (let h = basic.length; h < cps.length; ) {
    const m = Math.min(...cps.filter((c) => c >= n));
    delta += (m - n) * (h + 1);
    n = m;
    for (const c of cps) {
      if (c < n) delta++;
      if (c !== n) continue;
      let q = delta;
      for (let k = BASE; ; k += BASE) {
        const t = threshold(k, bias);
        if (q < t) break;
        out += digit(t + ((q - t) % (BASE - t)));
        q = Math.floor((q - t) / (BASE - t));
      }
      out += digit(q);
      bias = adapt(delta, h + 1, h === basic.length);
      delta = 0;
      h++;
    }
    delta++;
    n++;
  }
  return out;
}

function punyDecode(s: string): string | null {
  const out: number[] = [];
  const dash = s.lastIndexOf('-');
  for (let j = 0; j < Math.max(dash, 0); j++) out.push(s.charCodeAt(j));
  let n = 128;
  let bias = 72;
  let i = 0;
  for (let at = dash > 0 ? dash + 1 : 0; at < s.length; ) {
    const old = i;
    for (let w = 1, k = BASE; ; k += BASE) {
      if (at >= s.length) return null;
      const c = s.charCodeAt(at++);
      const d = c >= 48 && c <= 57 ? c - 22 : c >= 97 && c <= 122 ? c - 97 : BASE;
      if (d >= BASE) return null;
      i += d * w;
      const t = threshold(k, bias);
      if (d < t) break;
      w *= BASE - t;
      if (w > 0x7fffffff) return null;
    }
    bias = adapt(i - old, out.length + 1, old === 0);
    n += Math.floor(i / (out.length + 1));
    i %= out.length + 1;
    if (n > 0x10ffff) return null;
    out.splice(i++, 0, n);
  }
  return out.length ? String.fromCodePoint(...out) : null;
}

/** A label as a person reads it, or null: letters, digits, marks (not first) and inner hyphens. */
function label(l: string): string | null {
  if (!l) return null;
  if (/^[\x20-\x7e]+$/.test(l)) {
    if (l.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(l)) return null;
    if (!l.startsWith('xn--')) return l;
    const u = punyDecode(l.slice(4));
    return u && !/^[\x00-\x7f]*$/.test(u) && u === u.toLowerCase() && label(u) === u ? u : null;
  }
  if (l.startsWith('xn--')) return null;
  // Decimal digits only (Go's unicode.IsDigit): "½" and "Ⅻ" are numbers, not digits.
  if (!/^[\p{L}\p{Nd}][\p{L}\p{Nd}\p{M}]*(?:-+[\p{L}\p{Nd}\p{M}]+)*$/u.test(l)) return null;
  return 4 + punyEncode(l).length <= 63 ? l : null;
}

/** "*." or "." first, then "www." — unless that leaves one label: "www.com" is a site of its own. */
function trimWildcard(host: string): string {
  if (host.startsWith('*.')) host = host.slice(2);
  else if (host.startsWith('.')) host = host.slice(1);
  const rest = host.startsWith('www.') ? host.slice(4) : host;
  return rest !== host && rest.indexOf('.') >= 0 ? rest : host;
}

function domain(host: string): Site {
  if (!host) return INVALID;
  const labels = host.split('.').map(label);
  // At least two labels here (see the top); and "1.2.3" is a mistyped address, not a name.
  if (labels.length < 2 || labels.some((l) => l === null) || /^\d+$/.test(labels[labels.length - 1]!)) return INVALID;
  // 253 characters as the name travels: in punycode.
  const ascii = labels.map((l) => (/^[\x00-\x7f]*$/.test(l!) ? l! : 'xn--' + punyEncode(l!))).join('.');
  return ascii.length > 253 ? INVALID : { value: labels.join('.'), kind: 'domain' };
}

// ── the entry ────────────────────────────────────────────────────────────────

const isPort = (p: string) => p === '' || (/^\d{1,5}$/.test(p) && +p >= 1 && +p <= 65535);

/** Drops a port: "host:443", "[v6]:443", "[v6]". More than one colon and no brackets is IPv6. */
function stripPort(s: string): string | null {
  if (s.startsWith('[')) {
    const end = s.indexOf(']');
    if (end < 0) return null;
    const host = s.slice(1, end);
    const rest = s.slice(end + 1);
    if (rest && !(rest[0] === ':' && isPort(rest.slice(1)))) return null;
    return v6(host) ? host : null;
  }
  const parts = s.split(':');
  if (parts.length !== 2) return s;
  return isPort(parts[1]) ? parts[0] : null;
}

export function normalizeSite(input: string): Site {
  let s = input.trim();
  if (!s) return { error: 'empty' };
  if (s.length > 2048 || /[\s\p{Cc}]/u.test(s)) return INVALID;
  s = lower(foldWidth(s));
  // «+ сервис»: a category of the router's geo file (sites.parseService).
  if (s.startsWith('geosite:')) {
    const name = s.slice(8);
    return /^[a-z0-9!@._-]{1,64}$/.test(name) && !name.includes('..') ? { value: s, kind: 'service' } : INVALID;
  }
  let url = false;
  const scheme = s.indexOf('://');
  if (scheme >= 0) {
    if (!/^[a-z][a-z0-9+.-]*$/.test(s.slice(0, scheme))) return INVALID;
    s = s.slice(scheme + 3);
    url = true;
  } else if (s.startsWith('//')) {
    s = s.slice(2);
    url = true;
  }
  if (!url) {
    // Not a URL: after an address, "/" starts a prefix length, never a path.
    const slash = s.indexOf('/');
    if (slash >= 0) {
      const a = parseAddr(s.slice(0, slash).replace(/^\[(.*)\]$/, '$1'));
      if (a) return prefix(a, s.slice(slash + 1));
    }
  }
  const cut = s.search(/[/?#]/);
  if (cut >= 0) s = s.slice(0, cut);
  s = s.slice(s.lastIndexOf('@') + 1);
  const host = stripPort(s);
  if (host === null) return INVALID;
  const a = parseAddr(host);
  if (a) return { value: show(unmap(a)), kind: 'ip' };
  return domain(trimWildcard(host.replace(/\.$/, '')));
}
