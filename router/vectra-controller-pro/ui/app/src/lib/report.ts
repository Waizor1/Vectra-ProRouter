// The text an owner pastes into a message to support: what support asks first,
// in the reader's language, with the raw check ids for the engineer. Built only
// from `status` and `diagnostics`, which never carry credentials (see
// contract/README.md) — so the simple view may promise "no passwords or keys".

import type { Diagnostics, Status } from '../api/types';
import { LOCALE, type Key, type T } from '../i18n';
import type { Fmt } from './format';
import { checkText, sortChecks } from './labels';
import { own } from './own';

const MARK: Record<string, string> = { fail: '✗', warn: '!', unknown: '?', ok: '✓' };

export function buildReport(t: T, f: Fmt, s: Status, d: Diagnostics | null, state: string, now = Date.now()): string {
  const r = s.router;
  const sub = s.subscription;
  const join = (xs: (string | null | undefined)[]) => xs.filter((x): x is string => !!x).join(' · ') || t('na');
  const line = (k: Key, v: string) => t(k) + ': ' + v;
  let when: string;
  try {
    when = new Intl.DateTimeFormat(LOCALE[t.lang], { dateStyle: 'short', timeStyle: 'medium' }).format(now);
  } catch {
    when = new Date(now).toString();
  }
  const src = sub.source === 'local' ? t('src.local') : sub.source === 'panel' ? t('src.panel') : null;
  const out = [
    t('rp.title'),
    line('rp.time', when + ' (' + new Date(now).toISOString().replace(/\.\d+Z$/, 'Z') + ')'),
    line('rp.state', state),
    line('rp.location', sub.entryRemark ? sub.entryRemark + (src ? ' (' + src + ')' : '') : t('hero.noLocation')),
    line('rp.router', join([r.model, r.release, r.hostname])),
    line('rp.id', s.controlPlane.routerId || t('na')),
    line('rp.version', join([s.version && 'Vectra ' + s.version, s.engine.xrayVersion && 'xray ' + s.engine.xrayVersion])),
    line('rp.panel', f.ago(s.controlPlane.lastCheckIn, now, t('pl.never'))),
    line('rp.memory', r.memAvailableMiB !== null && r.memTotalMiB !== null ? t('r.ramFree', { a: f.mib(r.memAvailableMiB), b: f.mib(r.memTotalMiB) }) : t('na')),
  ];
  if (d) {
    const checks = sortChecks(d.checks);
    const bad = checks.filter((c) => c.status !== 'ok');
    const good = checks.length - bad.length;
    out.push(bad.length ? t('rp.checks') + ':' : line('rp.checks', t('rp.allOk')));
    for (const c of bad) out.push('  ' + (own(MARK, c.status) ?? '?') + ' ' + checkText(t, f, c) + ' [' + c.id + ']');
    if (bad.length && good) out.push('  ' + t.n('n.okMore', good));
  }
  // Plain text for a chat or a ticket: without the joiner that holds "check-in" on one line.
  return out.join('\n').replace(/\u2060/g, '');
}
