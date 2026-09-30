import type { ComponentChildren } from 'preact';
import type { Check, Diagnostics, Status } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import { powerOpts } from '../app/power';
import type { Key } from '../i18n';
import { parseRemark } from '../lib/flags';
import { hasSubscription, health, memTone, powerSwitching, simpleVerdict, viaKey, type Health, type SimpleKind } from '../lib/health';
import { own } from '../lib/own';
import { checkText, sortChecks, stateLabel } from '../lib/labels';
import { buildReport } from '../lib/report';
import { copyText } from '../lib/storage';
import { Icon, type IconName } from '../ui/icons';
import { Badge, Button, Card, Empty, ErrorBox, KV, Meter, Note, Skeleton, StatusIcon, checkTone, type Row, type Tone } from '../ui/kit';
import { around, NodeList, SLOT } from './Nodes';

const LEVEL: Record<Health['level'], [Tone, IconName]> = {
  ok: ['ok', 'ok'],
  warn: ['warn', 'warn'],
  fail: ['fail', 'fail'],
  setup: ['info', 'info'],
  unknown: ['info', 'unknown'],
  off: ['mute', 'power'],
};
const DOWN = ['backoff', 'exited', 'stopped', 'failed'];
// The owner's verdict (lib/health.ts) → where the LAN's traffic goes, in one line.
const TRAFFIC: Partial<Record<SimpleKind, [Key, Tone]>> = {
  ok: ['ov.tr.ok', 'ok'],
  partial: ['ov.tr.ok', 'warn'],
  reserve: ['ov.tr.backup', 'warn'],
  direct: ['ov.tr.bad', 'fail'],
  bypass: ['ov.tr.bad', 'fail'],
  cut: ['ov.tr.cut', 'fail'],
  noroute: ['ov.tr.none', 'fail'],
  pinDown: ['ov.tr.none', 'fail'],
};
// With Vectra off, where the traffic goes instead.
const OFF_TRAFFIC: Record<string, [Key, Tone]> = { passwall2: ['ov.tr.passwall2', 'info'], agent: ['ov.tr.agent', 'info'], direct: ['ov.tr.direct', 'warn'] };

export function engineTone(state: string | null): Tone {
  return state === 'running' ? 'ok' : DOWN.indexOf(state!) >= 0 ? 'fail' : state === 'starting' || state === 'reloading' ? 'warn' : state === 'idle' ? 'info' : 'mute';
}

function Hero({ s, hl }: { s: Status; hl: Health }) {
  const { t, f, goTab, run, pending } = useApp();
  const sub = s.subscription;
  const loc = parseRemark(sub.entryRemark);
  const lv = hl.level;
  const pw = s.power;
  // Being turned on or off (from here, or seen still running while switched off).
  const sw =
    pending === 'power:on' || pending === 'power:off' ? powerSwitching(pending === 'power:on') : pw.enabled === false && pw.running === true ? powerSwitching(false) : null;
  // One honest sentence under the verdict: the first thing that is wrong, or why all is well.
  const line = sw
    ? t(sw.lines[0])
    : lv === 'off'
      ? t(viaKey(pw.holder))
      : lv === 'ok'
        ? t('hero.okSub', { uptime: f.dur(s.engine.uptimeSec) })
        : lv === 'setup'
          ? t('hero.setupSub')
          : lv === 'fail'
            ? hl.fails.length
              ? checkText(t, f, hl.fails[0])
              : s.controller.running === false
                ? t('d.controller_running.bad')
                : s.dataplane.loaded === false
                  ? t('d.dataplane_loaded.bad')
                  : t('d.xray_running.bad', { state: stateLabel(t, s.engine.state) })
            : lv === 'warn'
              ? hl.warns.length
                ? t.n('n.warnSub', hl.warns.length)
                : s.api.reachable === false
                  ? t('b.apiDown')
                  : 'Xray: ' + stateLabel(t, s.engine.state)
              : '';
  const power = (on: boolean) => run('set_power', { on }, powerOpts(t, s, on));
  return (
    <section class={'hero t-' + LEVEL[lv][0]} aria-labelledby="vx-verdict">
      <div class="hero-m">
        <span class="beacon" aria-hidden="true">
          <Icon name={LEVEL[lv][1]} size={22} />
        </span>
        <div>
          <h2 id="vx-verdict" class="verdict">
            {sw ? t(sw.title) : t(('hero.' + lv) as Key)}
          </h2>
          {line ? <p>{line}</p> : null}
          <div class="row">
            {lv === 'off' && !sw ? (
              <Button kind="p" small icon="power" busy={pending === 'power:on'} disabled={!!pending} onClick={() => power(true)}>
                {t('s.pw.on')}
              </Button>
            ) : null}
            {/* Nothing to restart until the router has its settings, or while Vectra is off. */}
            {s.engine.state === 'idle' || lv === 'off' ? null : (
              <Button
                kind="g"
                small
                icon="refresh"
                busy={pending === 'restart'}
                disabled={!!pending}
                onClick={() =>
                  run('restart_xray', {}, {
                    key: 'restart',
                    confirm: { title: t('x.restartQ'), body: t('restartWarn'), ok: t('x.restart') },
                    // An answer of `pending` lands when xray runs again under a new pid.
                    landed: ({ engine: e }, before) => {
                      const b = before?.engine;
                      return e.state === 'running' && (e.pid !== null ? e.pid !== b?.pid : e.uptimeSec !== null && b?.uptimeSec != null && e.uptimeSec < b.uptimeSec);
                    },
                  })
                }
              >
                {t('x.restart')}
              </Button>
            )}
            {pw.enabled === true || pw.running === true ? (
              <Button kind="g" small icon="power" busy={pending === 'power:off'} disabled={!!pending} onClick={() => power(false)}>
                {t('s.pw.offBtn')}
              </Button>
            ) : null}
          </div>
        </div>
      </div>
      {/* Switched off, no server is in use. */}
      {lv === 'off' ? null : (
        <div class="loc">
          <span class="flag" aria-hidden="true">
            {loc.flag || <Icon name="globe" size={22} />}
          </span>
          <div>
            <span class="k">{t('hero.location')}</span>
            <b>{loc.name || loc.flag || t('l.nothing')}</b>
            {sub.source === 'panel' || sub.source === 'local' ? <span class="hint">{t(sub.source === 'local' ? 'l.local' : 'l.panel')}</span> : null}
          </div>
          <Button kind="p" icon="arrow" onClick={() => goTab('locations')}>
            {t('hero.change')}
          </Button>
        </div>
      )}
      {sub.overrideStale && lv !== 'off' ? (
        <div class="hero-n">
          <Note tone="warn">{t('src.stale')}</Note>
        </div>
      ) : null}
    </section>
  );
}

/**
 * The key facts in plain words: is the VPN up, where does the traffic go, is
 * the router in touch with Vectra, does it have the memory it needs. Each is
 * read from the same evidence as the verdict above it.
 */
function Facts({ s, d, failed }: { s: Status; d: Diagnostics | null; failed: boolean }) {
  const { t, f } = useApp();
  const e = s.engine;
  const dp = s.dataplane;
  const cp = s.controlPlane;
  const ks = dp.killSwitch;
  const free = s.router.memAvailableMiB;
  const total = s.router.memTotalMiB;
  const v = simpleVerdict(s, d, failed).kind;
  const leak = d?.checks.find((c) => c.id === 'no_leak');
  const off = s.power.enabled === false;
  // Switched off, the traffic goes where the router says. Without a subscription
  // nothing is intercepted, by design. With xray down, the kill switch drops what
  // xray cannot take; without it, that goes direct. Traffic that escaped the
  // interception went past the VPN, whatever the kill switch.
  const traffic: [Key, Tone] | null = off
    ? (own(OFF_TRAFFIC, s.power.holder ?? '') ?? null)
    : !hasSubscription(s)
      ? ['ov.tr.bad', 'mute']
      : v === 'down'
        ? [dp.loaded !== false && ks ? 'ov.tr.cut' : 'ov.tr.bad', 'fail']
        : v === 'cut' && typeof leak?.params.escaped === 'number' && leak.params.escaped > 0
          ? ['ov.tr.bad', 'fail']
          : (TRAFFIC[v] ?? null);
  const mem: Tone = memTone(free, total);
  const facts: [ComponentChildren, Tone, string, string | null][] = [
    off
      ? ['VPN', 'mute', t('ov.off'), null]
      : ['VPN', engineTone(e.state), stateLabel(t, e.state), e.state === 'running' && e.uptimeSec !== null ? t('ov.up', { d: f.dur(e.uptimeSec) }) : null],
    [t('col.traffic'), traffic ? traffic[1] : 'mute', traffic ? t(traffic[0]) : t('na'), off || ks === null || !hasSubscription(s) ? null : t(ks ? 'dp.ksOnHint' : 'dp.ksOffHint')],
    [
      t('pl.title'),
      cp.reachable === null ? 'mute' : cp.reachable ? 'ok' : 'warn',
      cp.reachable === null ? t('na') : t(cp.reachable ? 'pl.on' : 'pl.off'),
      cp.lastCheckIn ? t('ov.seen', { ago: f.ago(cp.lastCheckIn) }) : null,
    ],
    [
      t('r.title'),
      mem,
      free === null ? t('na') : t(mem === 'ok' ? 'ov.mem.ok' : 'ov.mem.low'),
      free !== null && total !== null ? t('r.ramFree', { a: f.mib(free), b: f.mib(total) }) : null,
    ],
  ];
  return (
    <ul class="facts">
      {facts.map(([k, tone, v, sub], i) => (
        <li key={i} class={'card fact t-' + tone}>
          <span class="k">{k}</span>
          <b>
            <i aria-hidden="true" />
            {v}
          </b>
          {sub ? <span class="hint">{sub}</span> : null}
        </li>
      ))}
    </ul>
  );
}

const badge = (v: boolean | null, t: (k: Key) => string, on: Key, off: Key, offTone: Tone) =>
  v === null ? <Badge>{t('na')}</Badge> : <Badge tone={v ? 'ok' : offTone}>{t(v ? on : off)}</Badge>;

function XrayCard({ s }: { s: Status }) {
  const { t, f } = useApp();
  const e = s.engine;
  const x = e.lastExit;
  const why = x ? [x.code !== null ? t('x.code', { code: x.code }) : '', x.error].filter(Boolean).join(' · ') : '';
  // GOMEMLIMIT is a target for Go's garbage collector, not a ceiling: xray
  // sits near it by design, so it is a note, not a gauge that looks full.
  const limit = e.memoryLimitMiB !== null ? <span class="hint">{t('x.softLimit', { v: f.mib(e.memoryLimitMiB) })}</span> : null;
  const api = s.api.reachable;
  const rows: Row[] = [
    [t('x.version'), e.xrayVersion ?? t('na')],
    [t('x.uptime'), f.dur(e.uptimeSec)],
    [t('x.memory'), f.mib(e.rssMiB), limit],
    ['API', api === null ? t('na') : <span class={api ? 'tx-ok' : 'tx-fail'}>{t(api ? 'x.apiOk' : 'x.apiDown')}</span>],
    [t('x.restarts'), f.num(e.restarts)],
    [t('x.lastExit'), x ? f.ago(x.at) : t('x.never'), why ? <span class="hint">{why}</span> : null],
  ];
  return (
    <Card id="vx-c-x" class="a-x" title="Xray" aside={<Badge tone={engineTone(e.state)}>{stateLabel(t, e.state)}</Badge>}>
      <KV rows={rows} />
    </Card>
  );
}

const COUNTERS = ['vctl_tproxy_hits', 'vctl_egress_exempt', 'vctl_output_marked', 'vctl_would_leak', 'vctl_killswitch_drops', 'vctl_local_guard'];

function DataplaneCard({ s }: { s: Status }) {
  const { t, f } = useApp();
  const dp = s.dataplane;
  const c = dp.counters || {};
  const ks = dp.killSwitch;
  const rows: Row[] = [[t('dp.ks'), ks === null ? t('na') : t(ks ? 'w.on' : 'w.off'), ks === null ? null : <span class="hint">{t(ks ? 'dp.ksOnHint' : 'dp.ksOffHint')}</span>]];
  // Known counters first in a fixed order, anything new after them under its raw name.
  for (const k of COUNTERS.filter((k) => k in c).concat(Object.keys(c).filter((k) => COUNTERS.indexOf(k) < 0))) {
    rows.push([t.has('k.' + k) ? t(('k.' + k) as Key) : <code>{k}</code>, <span class={k === 'vctl_would_leak' && c[k] > 0 ? 'tx-warn' : undefined}>{f.num(c[k])}</span>]);
  }
  return (
    <Card id="vx-c-d" class="a-d" title={t('dp.title')} aside={badge(dp.loaded, t, 'dp.on', 'dp.off', hasSubscription(s) ? 'fail' : 'mute')}>
      <KV rows={rows} />
      {rows.length > 1 ? null : <p class="hint">{t('dp.none')}</p>}
    </Card>
  );
}

function PanelCard({ s }: { s: Status }) {
  const { t, f } = useApp();
  const cp = s.controlPlane;
  return (
    <Card id="vx-c-p" class="a-p" title={t('pl.title')} aside={badge(cp.reachable, t, 'pl.on', 'pl.off', 'warn')}>
      <KV
        rows={[
          [t('pl.last'), f.ago(cp.lastCheckIn, Date.now(), t('pl.never'))],
          [
            t('pl.id'),
            cp.routerId ? (
              <code class="clip" title={cp.routerId}>
                {cp.routerId}
              </code>
            ) : (
              t('na')
            ),
          ],
        ]}
      />
    </Card>
  );
}

function RouterCard({ s }: { s: Status }) {
  const { t, f } = useApp();
  const r = s.router;
  const free = r.memAvailableMiB;
  const total = r.memTotalMiB;
  const ram = free !== null && total !== null ? t('r.ramFree', { a: f.mib(free), b: f.mib(total) }) : t('na');
  const tone: Tone = memTone(free, total);
  const space = (v: number | null, low: number) => (v === null ? t('na') : <span class={v < low ? 'tx-warn' : undefined}>{t('r.free', { a: f.mib(v) })}</span>);
  return (
    <Card id="vx-c-r" class="a-r" title={t('r.title')}>
      <KV
        rows={[
          [t('r.model'), r.model ?? t('na')],
          [t('r.release'), r.release ?? t('na')],
          [
            t('x.memory'),
            <span class={tone === 'ok' ? undefined : 'tx-' + tone}>{ram}</span>,
            <Meter value={free !== null && total !== null ? total - free : null} max={total} label={ram} tone={tone} />,
          ],
          [t('r.overlay'), space(r.overlayFreeMiB, 8)],
          [t('r.tmp'), space(r.tmpFreeMiB, 16)],
          [t('r.load'), r.load?.length ? r.load.map((v) => f.dec(v, 2)).join(' · ') : t('na')],
          [t('r.uptime'), f.dur(r.uptimeSec)],
        ]}
      />
    </Card>
  );
}

export function Overview() {
  const { t, f, refresh, toast, root } = useApp();
  const s = useRes('status').data;
  const dg = useRes('diagnostics');
  const d = dg.data;
  if (!s) return <Skeleton />;
  const checks = d ? sortChecks(d.checks) : [];
  // What needs attention stays in view; the passed checks fold away under a count.
  const bad = checks.filter((c) => c.status !== 'ok');
  const good = checks.filter((c) => c.status === 'ok');
  // Nodes that do not answer are named in words, their tags kept beside them.
  const dead = (c: Check) => {
    const tags = Array.isArray(c.params.tags) ? c.params.tags.filter((x): x is string => typeof x === 'string') : [];
    const n = typeof c.params.count === 'number' ? c.params.count : tags.length;
    return c.id === 'dead_nodes' && c.status !== 'ok' && tags.length
      ? around(
          t('d.dead_nodes.bad', { count: t.n('n.node', n), tags: SLOT }),
          <>
            <NodeList t={t} tags={tags.slice(0, 6)} />
            {tags.length > 6 ? ' +' + (tags.length - 6) : null}
          </>,
        )
      : null;
  };
  const item = (c: Check) => (
    <li key={c.id} class={'chk ' + c.status}>
      <StatusIcon status={c.status} />
      <span>{dead(c) || checkText(t, f, c)}</span>
      <span class={'chk-s t-' + checkTone(c.status)}>{t.has('d.s.' + c.status) ? t(('d.s.' + c.status) as Key) : c.status}</span>
    </li>
  );
  const report = async () => {
    const ok = await copyText(buildReport(t, f, s, d, t(('hero.' + health(s, d).level) as Key)), root);
    toast(ok ? 'ok' : 'fail', t(ok ? 's.copied' : 'j.copyFail'));
  };
  return (
    <div class="stack">
      <Hero s={s} hl={health(s, d)} />
      <Facts s={s} d={d} failed={!!dg.error && !d} />
      <Card
        id="vx-c-diag"
        title={t('d.title')}
        aside={
          <span class="row">
            {d?.checkedAt ? <span class="hint">{t('d.checked', { when: f.ago(d.checkedAt) })}</span> : null}
            <Button small icon="copy" onClick={report}>
              {t('s.help.support.btn')}
            </Button>
          </span>
        }
      >
        {s.power.enabled === false ? (
          // The checks judge what Vectra runs; switched off, it runs nothing.
          <Empty>{t('ov.checksOff')}</Empty>
        ) : !d ? (
          dg.error ? (
            <ErrorBox t={t} err={dg.error} onRetry={refresh} />
          ) : (
            <Skeleton />
          )
        ) : !checks.length ? (
          <Empty>{t('d.none')}</Empty>
        ) : bad.length && good.length ? (
          <>
            <ul class="checks">{bad.map(item)}</ul>
            <details class="more">
              <summary>{t.n('n.okMore', good.length)}</summary>
              <ul class="checks">{good.map(item)}</ul>
            </details>
          </>
        ) : (
          <ul class="checks">{checks.map(item)}</ul>
        )}
      </Card>
      <details class="more tech">
        <summary>{t('ov.tech')}</summary>
        <div class="grid g4">
          <XrayCard s={s} />
          <DataplaneCard s={s} />
          <PanelCard s={s} />
          <RouterCard s={s} />
        </div>
      </details>
    </div>
  );
}
