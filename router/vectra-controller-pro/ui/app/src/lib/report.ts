// The text an owner pastes into a message to support: what support asks first,
// in the reader's language, with the raw check ids, xray's and the router's
// numbers and, from Pro, every node with its tag and address for the engineer.
// Built only from `status`, `diagnostics` and `nodes`, which never carry
// credentials (see contract/README.md) — so the simple view may promise "no
// passwords or keys".

import type { Diagnostics, Node, Status } from '../api/types';
import { LOCALE, type Key, type T } from '../i18n';
import type { Fmt } from './format';
import { checkText, sortChecks, stateLabel } from './labels';
import { nodeName } from './names';
import { own } from './own';

const MARK: Record<string, string> = { fail: '✗', warn: '!', unknown: '?', ok: '✓' };

export function buildReport(t: T, f: Fmt, s: Status, d: Diagnostics | null, state: string, now = Date.now(), nodes?: Node[] | null): string {
  const r = s.router;
  const sub = s.subscription;
  const e = s.engine;
  const x = e.lastExit;
  const dp = s.dataplane;
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
    line(
      'rp.disk',
      join([
        t('r.overlay') + ' ' + t('r.free', { a: f.mib(r.overlayFreeMiB) }),
        t('r.tmp') + ' ' + t('r.free', { a: f.mib(r.tmpFreeMiB) }),
        r.load?.length ? t('r.load') + ' ' + r.load.map((v) => f.dec(v, 2)).join(' / ') : null,
        t('r.uptime') + ' ' + f.dur(r.uptimeSec),
      ]),
    ),
    'Xray: ' +
      join([
        stateLabel(t, e.state),
        e.uptimeSec !== null ? f.dur(e.uptimeSec) : null,
        e.rssMiB !== null ? t('x.memory') + ' ' + f.mib(e.rssMiB) + (e.memoryLimitMiB !== null ? ' (' + t('x.softLimit', { v: f.mib(e.memoryLimitMiB) }) + ')' : '') : null,
        s.api.reachable === null ? null : 'API ' + (s.api.listen ? s.api.listen + ' ' : '') + t(s.api.reachable ? 'x.apiOk' : 'x.apiDown'),
        t('x.restarts') + ' ' + f.num(e.restarts),
        t('x.lastExit') + ' ' + (x ? join([f.ago(x.at, now), x.code !== null ? t('x.code', { code: x.code }) : null, x.error]) : t('x.never')),
      ]),
    line(
      'dp.title',
      join([
        dp.loaded === null ? null : t(dp.loaded ? 'dp.on' : 'dp.off'),
        dp.killSwitch === null ? null : t('dp.ks') + ' ' + t(dp.killSwitch ? 'w.on' : 'w.off'),
        ...Object.keys(dp.counters || {}).map((k) => k + '=' + dp.counters![k]),
      ]),
    ),
  ];
  if (d) {
    const checks = sortChecks(d.checks);
    const bad = checks.filter((c) => c.status !== 'ok');
    const good = checks.length - bad.length;
    out.push(bad.length ? t('rp.checks') + ':' : line('rp.checks', t('rp.allOk')));
    for (const c of bad) out.push('  ' + (own(MARK, c.status) ?? '?') + ' ' + checkText(t, f, c) + ' [' + c.id + ']');
    if (bad.length && good) out.push('  ' + t.n('n.okMore', good));
  }
  if (nodes?.length) {
    out.push(t('rp.nodes') + ':');
    for (const n of nodes) {
      const at = n.address ? n.address + (n.port !== null ? ':' + n.port : '') : null;
      const how = [n.protocol, n.transport, n.security].filter((v, i, a) => !!v && a.indexOf(v) === i).join('/');
      const mark = n.alive ? '✓' : n.alive === false ? '✗' : '?';
      out.push('  ' + mark + ' ' + join([nodeName(t, n.tag, n.countryHint).name, n.tag, at, how, n.alive ? f.delay(n.delayMs, '') : t('nd.' + (n.alive === false ? 'dead' : 'unprobed') as Key)]));
    }
  }
  // Plain text for a chat or a ticket: without the joiner that holds "check-in" on one line.
  return out.join('\n').replace(/\u2060/g, '');
}
