// Human formatting for numbers the router reports. Every formatter takes null
// ("the router could not measure it") and renders it as a word, never as 0,
// "NaN" or "undefined".

import { LOCALE, type Key, type T } from '../i18n';

export type Tone = 'good' | 'ok' | 'slow';

export interface Fmt {
  num(n: number | null | undefined, fallback?: string): string;
  dec(n: number | null | undefined, digits?: number, fallback?: string): string;
  bytes(n: number | null | undefined, fallback?: string): string;
  mib(n: number | null | undefined, fallback?: string): string;
  /** Duration in seconds → "10 ч 41 мин" (two largest units). */
  dur(sec: number | null | undefined, fallback?: string): string;
  /** RFC 3339 time → "5 мин назад". */
  ago(iso: string | null | undefined, now?: number, fallback?: string): string;
  /** Seconds elapsed → "5 мин назад". */
  agoSec(sec: number | null | undefined, fallback?: string): string;
  delay(ms: number | null | undefined, fallback?: string): string;
  /** Local wall-clock HH:MM:SS. */
  clock(t: number | string | null | undefined, fallback?: string): string;
}

const ok = (n: unknown): n is number => typeof n === 'number' && isFinite(n);

export function delayTone(ms: number | null | undefined): Tone | null {
  if (!ok(ms) || ms < 0) return null;
  return ms < 120 ? 'good' : ms < 250 ? 'ok' : 'slow';
}

export function parseTime(iso: string | null | undefined): number | null {
  if (typeof iso !== 'string' || !iso) return null;
  const t = Date.parse(iso);
  return isNaN(t) ? null : t;
}

const pad = (n: number) => (n < 10 ? '0' : '') + n;

export function makeFmt(t: T): Fmt {
  const loc = LOCALE[t.lang];
  const na = t('na');
  const nf0 = new Intl.NumberFormat(loc, { maximumFractionDigits: 0 });
  const nf1 = new Intl.NumberFormat(loc, { maximumFractionDigits: 1 });
  const unit = (k: Key, n: number, f = nf0) => t(k, { n: f.format(n) });

  const dur = (sec: number | null | undefined, fallback = na): string => {
    if (!ok(sec) || sec < 0) return fallback;
    const s = Math.floor(sec);
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    const r = s % 60;
    const parts: [Key, number][] = d ? [['u.d', d], ['u.h', h]] : h ? [['u.h', h], ['u.m', m]] : m ? [['u.m', m], ['u.s', r]] : [['u.s', r]];
    return parts
      .filter(([, v], i) => i === 0 || v > 0)
      .map(([k, v]) => unit(k, v))
      .join(' ');
  };

  const agoSec = (sec: number | null | undefined, fallback = na): string => {
    if (!ok(sec)) return fallback;
    if (sec < 10) return t('justNow');
    const s = Math.floor(sec);
    const d =
      s < 60 ? unit('u.s', s) : s < 3600 ? unit('u.m', Math.floor(s / 60)) : s < 86400 ? unit('u.h', Math.floor(s / 3600)) : unit('u.d', Math.floor(s / 86400));
    return t('ago', { d });
  };

  return {
    num: (n, fallback = na) => (ok(n) ? nf0.format(n) : fallback),
    dec: (n, digits = 1, fallback = na) => (ok(n) ? new Intl.NumberFormat(loc, { maximumFractionDigits: digits }).format(n) : fallback),
    bytes(n, fallback = na) {
      if (!ok(n) || n < 0) return fallback;
      const units: Key[] = ['u.B', 'u.KB', 'u.MB', 'u.GB', 'u.TB'];
      let v = n;
      let i = 0;
      while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
      }
      return unit(units[i], v, i && v < 10 ? nf1 : nf0);
    },
    mib: (n, fallback = na) => (ok(n) && n >= 0 ? unit('u.MB', n, n < 10 || n % 1 ? nf1 : nf0) : fallback),
    dur,
    ago(iso, now = Date.now(), fallback = na) {
      const at = parseTime(iso);
      return at === null ? fallback : agoSec(Math.max(0, (now - at) / 1000), fallback);
    },
    agoSec,
    delay: (ms, fallback = na) => (ok(ms) && ms >= 0 ? unit('u.ms', Math.round(ms)) : fallback),
    clock(v, fallback = na) {
      const at = typeof v === 'number' ? v : parseTime(v);
      if (at === null || !isFinite(at)) return fallback;
      const d = new Date(at);
      return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    },
  };
}
