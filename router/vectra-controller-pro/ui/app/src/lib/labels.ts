// Codes from the router → words. Every lookup falls back to the raw code, so a
// value the UI has not learnt yet is shown as-is instead of breaking the screen.

import type { Key, T } from '../i18n';
import type { Check } from '../api/types';
import type { Fmt } from './format';
import { own } from './own';
import { tuneWords } from './tune';

const known = (t: T, k: string): k is Key => t.has(k);

export function stateLabel(t: T, state: string | null | undefined): string {
  if (!state) return t('st.unknown');
  const k = 'st.' + state;
  return known(t, k) ? t(k) : state;
}

export function probeSource(t: T, src: string | null): string {
  const k = 'p.src.' + src;
  return src && known(t, k) ? t(k) : src || t('na');
}

/**
 * The sentence for an action.code; an unknown code is returned raw. The simple
 * view has its own words for what its owner can do, and one plain sentence for
 * every failure it has no specific advice for.
 */
export function actionText(t: T, code: string, simple = false, ok = true): string {
  if (simple) {
    const s = 's.a.' + code;
    if (known(t, s)) return t(s);
    if (!ok) return t('s.a.fail');
  }
  const k = 'a.' + code;
  return known(t, k) ? t(k) : code;
}

// ── diagnostics ────────────────────────────────────────────────────────────

const RANK: Record<string, number> = { fail: 0, warn: 1, unknown: 2, ok: 3 };
export const rankOf = (s: string) => own(RANK, s) ?? 2;

export function sortChecks(checks: Check[]): Check[] {
  return checks
    .map((c, i) => [c, i] as const)
    .sort((a, b) => rankOf(a[0].status) - rankOf(b[0].status) || a[1] - b[1])
    .map(([c]) => c);
}

const s = (v: unknown): string | null => (typeof v === 'string' && v ? v : typeof v === 'number' && isFinite(v) ? String(v) : null);
const n = (v: unknown): number | null => (typeof v === 'number' && isFinite(v) ? v : null);

function list(v: unknown, max = 6): string {
  const items = Array.isArray(v) ? v.map(s).filter((x): x is string => x !== null) : [];
  if (!items.length) return '—';
  return items.slice(0, max).join(', ') + (items.length > max ? ' +' + (items.length - max) : '');
}

function rawValue(v: unknown): string {
  if (v === null || v === undefined) return '—';
  if (typeof v === 'number') return isFinite(v) ? String(v) : '—';
  if (typeof v === 'string' || typeof v === 'boolean') return String(v);
  if (Array.isArray(v)) return v.map(rawValue).join(', ');
  try {
    return JSON.stringify(v);
  } catch {
    return '—';
  }
}

/** Raw rendering for a check the UI has no sentence for: "some_check · a=1, b=x". */
export function rawCheck(c: Check): string {
  const keys = Object.keys(c.params || {});
  return keys.length ? c.id + ' · ' + keys.map((k) => k + '=' + rawValue(c.params[k])).join(', ') : c.id;
}

type Resolved = [suffix: string, params?: Record<string, string>, tail?: string];

function resolve(t: T, f: Fmt, id: string, p: Record<string, unknown>, good: boolean): Resolved {
  const g = good ? 'ok' : 'bad';
  switch (id) {
    case 'controller_running':
      return [g, { pid: s(p.pid) ?? '—' }];
    case 'xray_running':
      return good ? ['ok', { uptime: f.dur(n(p.uptimeSec)), pid: s(p.pid) ?? '—' }] : ['bad', { state: stateLabel(t, s(p.state)) }];
    case 'xray_api':
      return good ? ['ok', { listen: s(p.listen) ?? '—' }] : ['bad', { error: s(p.error) ?? t('na') }];
    case 'dataplane_loaded':
      return [g, { hits: f.num(n(p.tproxyHits)) }];
    case 'no_leak': {
      // vctl ≥ 0.4 baselines the counters at every xray start: only what
      // bypassed a RUNNING xray is a leak; ICMP and other non-TCP/UDP traffic,
      // never proxied by design, is counted apart. Older routers send the
      // running total only.
      const escaped = n(p.escaped);
      if (!good && escaped) return ['escaped', { count: t.n('n.packet', escaped) }];
      if (p.killSwitch === true) return good ? ['ks'] : ['drops', { count: t.n('n.packet', n(p.dropsSinceStart) ?? 0) }];
      const since = n(p.sinceStart);
      const total = n(p.wouldLeak);
      if (!good) return ['bad', { count: t.n('n.packet', since ?? total ?? 0) }];
      const restarts = since !== null && total !== null && total > since ? total - since : 0;
      return ['ok', {}, restarts ? t('d.no_leak.restarts', { count: t.n('n.packet', restarts) }) : ''];
    }
    case 'single_stack':
      return [g, { passwall: t(p.passwallRunning === true ? 'w.running' : 'w.stopped'), agent: t(p.agentEnabled === true ? 'w.on' : 'w.off') }];
    case 'panel_link':
      return n(p.lastCheckInAgoSec) === null ? ['never'] : [g, { ago: f.agoSec(n(p.lastCheckInAgoSec)) }];
    case 'subscription_ua': {
      // `reason` is a code (e.g. malformed_happ); a code without a sentence is shown raw.
      const reason = s(p.reason);
      return !good && reason && known(t, 'd.subscription_ua.' + reason) ? [reason] : [g, { reason: reason ?? t('na') }];
    }
    case 'memory': {
      const x = n(p.xrayMiB);
      return [g, { avail: f.mib(n(p.availableMiB)), total: f.mib(n(p.totalMiB)) }, x === null ? '' : 'xray ' + f.mib(x)];
    }
    case 'dead_nodes': {
      const tags = Array.isArray(p.tags) ? p.tags.length : 0;
      return [g, { count: t.n('n.node', n(p.count) ?? tags), tags: list(p.tags) }];
    }
    case 'balancer_fallback':
      // `mainDirect`: the catch-all traffic ends in a DIRECT outbound; `blocked`:
      // a chain ends with nothing to carry it; `main`: the catch-all traffic moved.
      if (good) return ['ok'];
      if (p.mainDirect === true) return ['direct', { list: list(p.balancers) }];
      if (p.blocked === true) return ['blocked', { list: list(Array.isArray(p.blockedBalancers) ? p.blockedBalancers : p.balancers) }];
      return [p.main === true ? 'main' : 'bad', { list: list(p.balancers) }];
    case 'pinned_node_dead':
      return [g, { node: s(p.node) ?? '—', balancer: s(p.balancer) ?? '—' }];
    case 'tune': {
      // warn: a small router without its compressed swap.
      if (!good) return ['bad'];
      if (p.enabled === false) return ['off'];
      const ids = Array.isArray(p.items) ? p.items.filter((x): x is string => typeof x === 'string') : [];
      const words = tuneWords(t, f, ids, n(p.zramMiB), true);
      // Chinese enumerates with its own comma.
      return words.length ? ['ok', { list: words.join(t.lang === 'zh' ? '、' : ', ') }] : ['none'];
    }
  }
  return [g];
}

// Checks whose title is already a word elsewhere in the UI.
const TITLE: Record<string, string> = { xray_running: 'Xray', dataplane_loaded: 'dp.title', panel_link: 'pl.title', memory: 'x.memory' };

/** The localized sentence for one check, params interpolated. Unknown ids fall back to raw. */
export function checkText(t: T, f: Fmt, c: Check): string {
  const base = 'd.' + c.id;
  if (!known(t, base + '.ok')) return rawCheck(c);
  if (c.id === 'balancer_fallback' && c.status === 'unknown' && c.params?.warming === true) return t('d.balancer_fallback.warming');
  if (c.status !== 'ok' && c.status !== 'warn' && c.status !== 'fail') {
    const k = own(TITLE, c.id) || base + '.t';
    return t('d.unknown', { title: known(t, k) ? t(k) : k });
  }
  const [suffix, params, tail] = resolve(t, f, c.id, c.params || {}, c.status === 'ok');
  const k = base + '.' + suffix;
  return known(t, k) ? t(k, params) + (tail ? ' · ' + tail : '') : rawCheck(c);
}
