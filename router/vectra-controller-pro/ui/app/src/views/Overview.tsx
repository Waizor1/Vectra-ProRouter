// The Pro overview: the verdict with the server in use and the way to change
// it, where the traffic goes when that is news, and the checks. Everything an
// engineer needs beyond that (xray's numbers, the counters, the router's disk)
// goes into the support report, not on the screen.

import { useEffect } from 'preact/hooks';
import type { Check, Diagnostics, Status } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import { powerOpts, powerParams } from '../app/power';
import type { Key, T } from '../i18n';
import { parseRemark } from '../lib/flags';
import { hasSubscription, health, powerSwitching, simpleVerdict, viaKey, type Health, type SimpleKind } from '../lib/health';
import { checkText, sortChecks, stateLabel } from '../lib/labels';
import { nodeName, routeName } from '../lib/names';
import { autoLine, face, routeVia } from '../lib/servers';
import { buildReport } from '../lib/report';
import { copyText } from '../lib/storage';
import { Icon, type IconName } from '../ui/icons';
import { Button, Card, Empty, ErrorBox, Note, Skeleton, StatusIcon, checkTone, type Tone } from '../ui/kit';
import { PowerActs } from './PowerActs';

const LEVEL: Record<Health['level'], [Tone, IconName]> = {
  ok: ['ok', 'ok'],
  warn: ['warn', 'warn'],
  fail: ['fail', 'fail'],
  setup: ['info', 'info'],
  unknown: ['info', 'unknown'],
  off: ['mute', 'power'],
};
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

/**
 * Where the LAN's traffic goes, from the same evidence as the owner's verdict.
 * Said only when the verdict leaves it open: "everything works" already says
 * the traffic goes through the VPN, and off or not set up there is no VPN.
 */
function traffic(s: Status, d: Diagnostics | null, failed: boolean, level: Health['level']): [Key, Tone] | null {
  if (level === 'ok' || level === 'off' || level === 'setup' || !hasSubscription(s)) return null;
  const v = simpleVerdict(s, d, failed).kind;
  const dp = s.dataplane;
  // With xray down, the kill switch drops what xray cannot take; without it, that goes direct.
  if (v === 'down') return [dp.loaded !== false && dp.killSwitch ? 'ov.tr.cut' : 'ov.tr.bad', 'fail'];
  // Traffic that escaped the interception went past the VPN, whatever the kill switch.
  const leak = d?.checks.find((c) => c.id === 'no_leak');
  if (v === 'cut' && typeof leak?.params.escaped === 'number' && leak.params.escaped > 0) return ['ov.tr.bad', 'fail'];
  return TRAFFIC[v] ?? null;
}

/** A check with its node and route tags put in words: the list reads names, the report keeps the tags. */
function named(t: T, c: Check, routes: Map<string, string>): Check {
  const p = c.params;
  const node = (x: unknown) => (typeof x === 'string' ? nodeName(t, x).name : x);
  const route = (x: unknown) => (typeof x === 'string' ? (routes.get(x) ?? x) : x);
  const each = (v: unknown, fn: (x: unknown) => unknown) => (Array.isArray(v) ? v.map(fn) : v);
  if (c.id === 'dead_nodes') return { ...c, params: { ...p, tags: each(p.tags, node) } };
  if (c.id === 'pinned_node_dead') return { ...c, params: { ...p, node: node(p.node), balancer: route(p.balancer) } };
  if (c.id === 'balancer_fallback') return { ...c, params: { ...p, balancers: each(p.balancers, route), blockedBalancers: each(p.blockedBalancers, route) } };
  return c;
}

/** `fix`: the verdict's own fix is a restart (xray is down), so the restart leads, not the server. */
function Hero({ s, hl, line, flow, fix }: { s: Status; hl: Health; line: string; flow: [Key, Tone] | null; fix: boolean }) {
  const { t, goTab, run, pending } = useApp();
  const sub = s.subscription;
  const loc = parseRemark(sub.entryRemark);
  // Named as the simple view names it: "Авто", that it picks and where it goes now, and who chose it.
  const fc = face(sub.entryRemark, t('l.nothing'));
  const local = sub.source === 'local';
  const how = [fc.auto ? autoLine(t, routeVia(t, s.route)) : fc.sub, local || sub.source === 'panel' ? t(local ? 's.loc.mine' : 's.loc.default') : null].filter(Boolean).join(' · ');
  const lv = hl.level;
  const pw = s.power;
  // Being turned on or off (from here, or seen still running while switched off).
  const sw =
    pending === 'power:on' || pending === 'power:off' ? powerSwitching(pending === 'power:on') : pw.enabled === false && pw.running === true ? powerSwitching(false) : null;
  const power = (on: boolean) => run('set_power', powerParams(s, on), powerOpts(t, s, on));
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
          {sw ? <p>{t(sw.lines[0])}</p> : line ? <p>{line}</p> : null}
          {flow && !sw ? (
            <p class="fv">
              <span class="k">{t('ov.traffic')}</span>
              <b class={'tx-' + flow[1]}>{t(flow[0])}</b>
            </p>
          ) : null}
        </div>
        {/* The buttons are the card's own row, not the text column's: a phone gives them its whole width. */}
        {lv === 'off' && !sw ? (
          <div class="row">
            <Button kind="p" icon="power" busy={pending === 'power:on'} disabled={!!pending} onClick={() => power(true)}>
              {t('s.pw.on')}
            </Button>
          </div>
        ) : null}
        <PowerActs
          primary={fix}
          // Nothing to restart until the router has its settings, or while Vectra is off.
          restart={
            s.engine.state === 'idle' || lv === 'off'
              ? undefined
              : () =>
                  void run('restart_xray', {}, {
                    key: 'restart',
                    confirm: { title: t('x.restartQ'), body: t('restartWarn'), ok: t('x.restart') },
                    // An answer of `pending` lands when xray runs again under a new pid.
                    landed: ({ engine: e }, before) => {
                      const b = before?.engine;
                      return e.state === 'running' && (e.pid !== null ? e.pid !== b?.pid : e.uptimeSec !== null && b?.uptimeSec != null && e.uptimeSec < b.uptimeSec);
                    },
                  })
          }
          off={pw.enabled === true || pw.running === true ? () => void power(false) : undefined}
        />
      </div>
      {/* Switched off, no server is in use. */}
      {lv === 'off' ? null : (
        <div class="loc">
          <span class="flag" aria-hidden="true">
            {fc.auto ? <Icon name="split" size={22} /> : loc.flag || <Icon name="globe" size={22} />}
          </span>
          <div title={sub.entryRemark ?? undefined}>
            <span class="k">{t('hero.location')}</span>
            <b>{fc.title}</b>
            {how ? <span class="hint">{how}</span> : null}
          </div>
          <Button kind={fix ? undefined : 'p'} icon="arrow" onClick={() => goTab('locations')}>
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

export function Overview() {
  const { t, f, refresh, toast, root, store } = useApp();
  const s = useRes('status').data;
  const dg = useRes('diagnostics');
  const bals = useRes('balancers').data;
  const d = dg.data;
  const checks = d ? sortChecks(d.checks) : [];
  // A check that names routes reads them by name: the routes are read once for it.
  const wantsRoutes = checks.some((c) => c.status !== 'ok' && (c.id === 'balancer_fallback' || c.id === 'pinned_node_dead'));
  useEffect(() => {
    if (wantsRoutes) void store.fetch('balancers', 60_000);
  }, [wantsRoutes]);
  if (!s) return <Skeleton />;
  const routes = new Map((bals?.balancers || []).map((b) => [b.tag, routeName(t, b).name] as const));
  const hl = health(s, d);
  const lv = hl.level;
  const words = (c: Check) => checkText(t, f, named(t, c, routes));
  // One honest sentence under the verdict: the first thing that is wrong, or why all is well.
  const line =
    lv === 'off'
      ? t(viaKey(s.power.holder))
      : lv === 'ok'
        ? t('hero.okSub', { uptime: f.dur(s.engine.uptimeSec) })
        : lv === 'setup'
          ? t('hero.setupSub')
          : lv === 'fail'
            ? hl.fails.length
              ? words(hl.fails[0])
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
  // What needs attention stays in view; the passed checks fold away under a count.
  const bad = checks.filter((c) => c.status !== 'ok');
  const good = checks.filter((c) => c.status === 'ok');
  const item = (c: Check) => (
    <li key={c.id} class={'chk ' + c.status}>
      <StatusIcon status={c.status} />
      <span>{words(c)}</span>
      <span class={'chk-s t-' + checkTone(c.status)}>{t.has('d.s.' + c.status) ? t(('d.s.' + c.status) as Key) : c.status}</span>
    </li>
  );
  // The report names every node with its tag and address: read them for it (Pro reads `nodes`; the simple view does not).
  const report = async () => {
    await store.fetch('nodes', 30_000);
    const ok = await copyText(buildReport(t, f, s, d, t(('hero.' + lv) as Key), Date.now(), store.get('nodes').data?.nodes), root);
    toast(ok ? 'ok' : 'fail', t(ok ? 's.copied' : 'j.copyFail'));
  };
  return (
    <div class="stack">
      <Hero s={s} hl={hl} line={line} flow={traffic(s, d, !!dg.error && !d, lv)} fix={simpleVerdict(s, d, !!dg.error && !d).act === 'restart'} />
      <Card
        id="vx-c-diag"
        title={t('d.title')}
        aside={
          <Button small icon="copy" onClick={report}>
            {t('s.help.support.btn')}
          </Button>
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
    </div>
  );
}
