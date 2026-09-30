// The Routes tab: every route (a balancer and its fallback chain) as a card
// that says what it carries, where that traffic goes now and where it goes
// next when nothing there works — in words, with the tags kept for support.

import type { ComponentChildren } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import type { Balancer, Node, Probe } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key, T } from '../i18n';
import { byMembers, fallbackPaths, routeState, strategyOf, type Alive, type RouteEnv, type RouteState, type Step, type StepState } from '../lib/balancing';
import { delayTone } from '../lib/format';
import { probeSource, stateLabel } from '../lib/labels';
import { matcherLabel, matcherLabels } from '../lib/matchers';
import { kindOf, nodeName, routeName } from '../lib/names';
import { Badge, Button, Card, Empty, ErrorBox, NodeDot, Note, Seg, Skeleton, type Opt, type Tone } from '../ui/kit';
import { around, Nm, SLOT } from './Nodes';

const STATE: Record<StepState, [Key, Tone]> = { here: ['rt.here', 'info'], skip: ['rt.skip', 'fail'], standby: ['rt.standby', 'mute'], unknown: ['na', 'mute'] };
const ROUTE: Record<RouteState, [Key, Tone]> = { ok: ['rt.ok', 'ok'], backup: ['rt.backup', 'warn'], direct: ['nm.direct', 'fail'], down: ['rt.down', 'fail'], unknown: ['na', 'mute'] };
const PRESETS = [60, 300, 600, 1800];

interface Ctx {
  nodes: Map<string, Node>;
  alive: Alive;
  /** How a route's end is judged: the nodes, xray's default outbound, every balancer. */
  env: RouteEnv;
  /** The main route's name, for a routed chain that joins it. */
  main: string;
  /** xray runs: without it no route carries anything, whatever a pin says. */
  live: boolean;
}

const tagsOf = (s: Step) => (s.kind === 'node' ? [s.tag] : s.bal ? (s.bal.members.length ? s.bal.members : s.bal.selector) : []);
const isWl = (s: Step) => !s.join && tagsOf(s).length > 0 && tagsOf(s).every((x) => kindOf(x) === 'wl');

/** The stops of a route: its own balancer, then each fallback — whitelist levels in a row read as one stop. */
function stops(steps: Step[]): Step[][] {
  const out: Step[][] = [];
  steps.forEach((s, i) => {
    const last = out[out.length - 1];
    if (i > 1 && isWl(s) && isWl(last[last.length - 1])) last.push(s);
    else out.push([s]);
  });
  return out;
}

/** Where a stop's traffic is: at its first step that has something to carry it. */
const stateOf = (stop: Step[]): StepState => stop.find((s) => s.state !== 'skip')?.state ?? 'skip';

/** How a balancer picks among its nodes, in words. leastLoad keeps up to `expected` of them. */
function how(t: T, b: Balancer): string {
  const s = strategyOf(b);
  const keep = Math.min(b.expected ?? 0, b.members.length);
  if (s === 'leastLoad' && keep > 1) return t('sg.keep', { n: keep });
  return t.has('sg.' + s) ? t(('sg.' + s) as Key) : s;
}

function ProbeCard({ probe }: { probe: Probe }) {
  const { t, f, run, pending } = useApp();
  const cur = probe.intervalSec;
  const [draft, setDraft] = useState(cur);
  useEffect(() => setDraft(cur), [cur]);
  const prov = probe.providerIntervalSec;
  const opts: Opt<number>[] = PRESETS.map((s) => ({ value: s, label: f.dur(s) }));
  if (prov && PRESETS.indexOf(prov) < 0) opts.push({ value: prov, label: t('p.provider', { d: f.dur(prov) }) });
  // 0 returns to the router default. Both paths restart xray, so both confirm first.
  const apply = (seconds: number) =>
    run('set_probe_interval', { seconds }, {
      key: seconds ? 'probe' : 'probe0',
      confirm: { title: seconds ? t('p.q', { d: f.dur(seconds) }) : t('p.resetQ'), body: t('restartWarn'), ok: t(seconds ? 'p.apply' : 'p.reset') },
      landed: (s) => (seconds ? s.probe.intervalSec === seconds : s.probe.source !== 'local'),
    });
  const host = probe.destination?.replace(/^\w+:\/\/|\/.*$/g, '');
  return (
    <Card id="vx-c-probe" class="probe" title={t('p.title')}>
      <p>{t('p.now', { v: cur === null ? t('na') : t('p.every', { d: f.dur(cur) }), src: probeSource(t, probe.source) })}</p>
      <div class="row">
        <Seg label={t('p.title')} value={draft} options={opts} onChange={setDraft} disabled={!!pending} />
      </div>
      <div class="row">
        <Button kind="p" small busy={pending === 'probe'} disabled={!!pending || draft === null || draft === cur} onClick={() => draft && apply(draft)}>
          {t('p.apply')}
        </Button>
        {probe.source === 'local' ? (
          <Button kind="g" small busy={pending === 'probe0'} disabled={!!pending} onClick={() => apply(0)}>
            {t('p.reset')}
          </Button>
        ) : null}
      </div>
      <p class="hint">
        {t('p.meta', { s: f.num(probe.sampling), t: f.dur(probe.timeoutSec) })}
        {host ? ' · ' + host : ''}
      </p>
    </Card>
  );
}

/**
 * What a route carries, in one line: the services and categories its rules
 * name; where they name none, the first entry of each rule (so a rule on a
 * port or a protocol still shows); then how many more.
 */
function Carries({ b }: { b: Balancer }) {
  const { t } = useApp();
  const raws = b.matchers.reduce<string[]>((a, m) => a.concat(m.sample), []);
  const known = matcherLabels(t, raws).filter((l) => l.label.known);
  const shown = known.length ? known : matcherLabels(t, b.matchers.map((m) => m.sample[0]).filter(Boolean));
  const texts = new Set(shown.map((l) => l.label.text));
  const rest = b.matchers.reduce((n, m) => n + m.total, 0) - raws.filter((r) => texts.has(matcherLabel(t, r).text)).length;
  return (
    <p class="chips">
      {b.matchers.some((m) => m.kind === 'all') ? <span class="chip ck">{t('rt.all')}</span> : null}
      {shown.map(({ label, raw }) => (
        <span key={raw} class={label.known ? 'chip ck' : 'chip'} title={raw}>
          {label.text}
        </span>
      ))}
      {rest > 0 ? <span class="chip">{t('rt.more', { n: rest })}</span> : null}
    </p>
  );
}

/** A balancer's nodes, each with its health, the strategy's pick, and a pin. */
function Members({ b, ctx, short }: { b: Balancer; ctx: Ctx; short: boolean }) {
  const { t, f, run, pending } = useApp();
  const bal = routeName(t, b).name;
  return (
    <ul class="mems">
      {b.members.map((m) => {
        const n = ctx.nodes.get(m);
        const nm = nodeName(t, m, n?.countryHint);
        const alive = n ? n.alive : null;
        // A pin overrides the strategy: its pick is shown, but only as the strategy's.
        // random/roundRobin name no pick: the API lists every member whatever its health.
        const pick = !byMembers(b) && !!b.selected && b.selected.indexOf(m) >= 0 && b.pinned !== m;
        const on = b.pinned ? b.pinned === m : pick;
        const tone = delayTone(n?.delayMs);
        const aria = t('b.pinAria', { node: nm.name, tag: m, bal });
        return (
          <li key={m} class={'mem' + (on ? ' on' : '')}>
            <NodeDot alive={alive} label={t(alive ? 'nd.alive' : alive === false ? 'nd.dead' : 'nd.unprobed')} />
            <span class="nn">
              <span class="clip">
                <Nm n={nm} short={short} />
              </span>
              {nm.known ? <code class="tg">{m}</code> : null}
            </span>
            <span class={'dly' + (tone ? ' dl-' + tone : ' mu')}>{f.delay(n?.delayMs, '—')}</span>
            <span class="mem-b">
              {pick ? <Badge tone={b.pinned ? 'mute' : 'info'}>{t(b.pinned ? 'b.byStrategy' : 'b.selected')}</Badge> : null}
              {b.pinned === m ? (
                <Badge tone="warm" icon="pin">
                  {t('b.pinned')}
                </Badge>
              ) : (
                <Button
                  kind="g"
                  small
                  class="pin"
                  aria-label={aria}
                  title={aria}
                  busy={pending === 'pin:' + b.tag + m}
                  disabled={!!pending}
                  onClick={() =>
                    run('pin_balancer', { balancer: b.tag, node: m }, {
                      key: 'pin:' + b.tag + m,
                      confirm: { title: t('b.pinQ', { node: nm.name }), body: t('b.pinBody', { bal }), ok: t('b.pin') },
                      landed: (s) => s.pins?.[b.tag] === m,
                    })
                  }
                >
                  {t('b.pin')}
                </Button>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** A pin in words, with the one-click way back to the automatic choice; and what a dead pin means. */
function PinNote({ b, ctx }: { b: Balancer; ctx: Ctx }) {
  const { t, run, pending } = useApp();
  return b.pinned ? (
    <>
      <Note
        tone="warm"
        action={
          <Button
            kind="g"
            small
            icon="pin"
            busy={pending === 'unpin:' + b.tag}
            disabled={!!pending}
            onClick={() => run('unpin_balancer', { balancer: b.tag }, { key: 'unpin:' + b.tag, landed: (st) => !st.pins?.[b.tag] })}
          >
            {t('b.unpin')}
          </Button>
        }
      >
        {t('rt.pinNote', { node: nodeName(t, b.pinned, ctx.nodes.get(b.pinned)?.countryHint).name })}
      </Note>
      {ctx.alive(b.pinned) === false ? <Note tone="fail">{t('b.pinDead')}</Note> : null}
    </>
  ) : null;
}

/** Where a step with nothing left sends its traffic: xray's default outbound (random/roundRobin with members keep them). */
const DefaultNote = ({ s, ctx }: { s: Step; ctx: Ctx }) => {
  const { t } = useApp();
  const d = ctx.env.def;
  return s.toDefault && s.bal && (!byMembers(s.bal) || !s.bal.members.length) ? (
    <Note tone="fail">{t('rt.default', { name: d ? nodeName(t, d, ctx.nodes.get(d)?.countryHint).name : t('na') })}</Note>
  ) : null;
};

/** A stop's title line: what it is, a hint, and where the traffic is. */
type Head = (title: ComponentChildren, hint?: ComponentChildren) => ComponentChildren;

/** One balancer on a route: how it picks, from which countries, what it picked now, which nodes are down, its pin. */
function BalancerStop({ s, first, ctx, head }: { s: Step; first: boolean; ctx: Ctx; head: Head }) {
  const { t } = useApp();
  const b = s.bal!;
  const names = b.members.map((m) => nodeName(t, m, ctx.nodes.get(m)?.countryHint));
  const kinds = names.map((n) => n.kind).filter((k, i, a) => a.indexOf(k) === i);
  // Nodes of one kind, under a title that names it, read by country: "8 мостов" → "🇵🇱 Польша".
  const short = kinds.length === 1 && kinds[0] !== 'other';
  const count = t.n(kinds.length === 1 && kinds[0] === 'bridge' ? 'n.bridge' : 'n.node', b.members.length);
  const flags = names.filter((n, i) => n.flag && names.findIndex((x) => x.flag === n.flag) === i);
  const nameOf = (tag: string) => names[b.members.indexOf(tag)] ?? nodeName(t, tag);
  // One node is the whole story: "Российские сайты → Мост → 🇷🇺 Россия".
  const one = first && b.members.length === 1 ? names[0] : null;
  const many = b.members.length > 1;
  // random/roundRobin: the API lists every member as selected whatever its health — that names no pick.
  const picks = many && !b.pinned && ctx.live && s.state === 'here' && b.selected && !byMembers(b) ? b.selected : [];
  const down = b.members.filter((m) => ctx.alive(m) === false);
  const list = (tags: string[]) =>
    tags.map((m, i) => (
      <span key={m}>
        {i ? t('rt.sep') : null}
        <Nm n={nameOf(m)} short={short} />
      </span>
    ));
  // The route's own stop says what its nodes are; a fallback's stop is named like a route, with its tag.
  const title = one ? <Nm n={one} /> : !first ? routeName(t, b).name : b.members.length ? count : t('b.noMembers', { sel: b.selector.join(', ') || '—' });
  const hint = one ? (
    <code class="tg">{b.members[0]}</code>
  ) : first ? null : (
    <>
      {b.members.length ? count + ' ' : null}
      <code class="tg">{b.tag}</code>
    </>
  );
  return (
    <>
      {head(title, hint)}
      {many && !b.pinned ? <p class="hint">{how(t, b)}</p> : null}
      {flags.length > 1 || (flags.length && !first) ? (
        <p class="ctry">
          {flags.slice(0, 5).map((n) => (
            <span key={n.flag}>
              <span class="fl fl-i" aria-hidden="true">
                {n.flag}
              </span>
              {n.short}
            </span>
          ))}
          {flags.length > 5 ? <span>+{flags.length - 5}</span> : null}
        </p>
      ) : null}
      {picks.length ? <p class="now">{around(t('rt.now', { x: SLOT }), list(picks))}</p> : null}
      {down.length ? <p class="down">{around(t.n('n.down', down.length, { list: SLOT }), list(down))}</p> : null}
      <PinNote b={b} ctx={ctx} />
      <DefaultNote s={s} ctx={ctx} />
      {b.members.length ? (
        <details class="more">
          <summary>
            {t('tab.nodes')} · {b.members.length}
          </summary>
          <p class="hint">{t('rt.pinHint')}</p>
          <Members b={b} ctx={ctx} short={short} />
        </details>
      ) : null}
    </>
  );
}

/** One stop on a route's chain, with the rail dot that shows where the traffic is. */
function Stop({ stop, first, ctx, unused }: { stop: Step[]; first: boolean; ctx: Ctx; unused?: boolean }) {
  const { t } = useApp();
  const s = stop[0];
  const st = ctx.live ? stateOf(stop) : 'unknown';
  const [word, tone] = STATE[st];
  const node = (tag: string) => nodeName(t, tag, ctx.nodes.get(tag)?.countryHint);
  const head: Head = (title, hint) => (
    <p class="stop-t">
      <b>{title}</b>
      {hint ? <span class="hint">{hint}</span> : null}
      {s.join || unused ? null : <Badge tone={tone}>{t(word)}</Badge>}
    </p>
  );
  let body: ComponentChildren;
  if (s.join) {
    body = head(t('rt.join', { name: ctx.main }));
  } else if (s.kind === 'cycle' || s.kind === 'missing') {
    body = head(<span class="mono">{s.tag}</span>, t(s.kind === 'cycle' ? 'b.cycle' : 'a.unknown_balancer'));
  } else if (stop.length > 1 || (!first && isWl(s))) {
    // Whitelist levels: one stop, a line per level, each level's nodes behind its own disclosure.
    body = (
      <>
        {head(t('ng.wl'))}
        <p class="hint">{t('ng.wl.d')}</p>
        <ul class="lvls">
          {stop.map((x) => (
            <li key={x.kind + x.tag} class={ctx.live ? x.state : undefined}>
              {x.bal ? routeName(t, x.bal).name : <Nm n={node(x.tag)} />}
              <code class="tg">{x.tag}</code>
              <span class="hint">
                {x.bal ? t.n('n.node', x.bal.members.length) : t('b.nodeEnd')}
                {ctx.live && (x.state === 'here' || x.state === 'skip') ? ' · ' + t(STATE[x.state][0]) : ''}
              </span>
            </li>
          ))}
        </ul>
        {stop.map((x) => (x.bal ? <PinNote key={x.tag} b={x.bal} ctx={ctx} /> : null))}
        {stop.map((x) => <DefaultNote key={x.tag} s={x} ctx={ctx} />)}
        {stop.map((x) =>
          x.bal && x.bal.members.length ? (
            <details key={x.tag} class="more">
              <summary>
                {routeName(t, x.bal).name} · {x.bal.members.length}
              </summary>
              <Members b={x.bal} ctx={ctx} short />
            </details>
          ) : null,
        )}
      </>
    );
  } else if (s.kind === 'node') {
    body = head(
      <Nm n={node(s.tag)} />,
      <>
        <code class="tg">{s.tag}</code> {t('b.nodeEnd') + (ctx.alive(s.tag) === false ? ' · ' + t('nd.dead') : '')}
      </>,
    );
  } else {
    body = <BalancerStop s={s} first={first} ctx={ctx} head={head} />;
  }
  return (
    <li class={'stop ' + (unused ? 'standby' : st)}>
      <i aria-hidden="true" />
      <div>
        {first ? null : <span class="k then">{t('rt.then')}</span>}
        {body}
      </div>
    </li>
  );
}

/** A route: what it carries, then its chain of stops. */
function Route({ steps, ctx, id, unused }: { steps: Step[]; ctx: Ctx; id: string; unused?: boolean }) {
  const { t } = useApp();
  const b = steps[0].bal!;
  const rn = routeName(t, b);
  const [label, tone]: [Key, Tone] = unused ? ['role.unused', 'mute'] : ROUTE[ctx.live ? routeState(steps, ctx.env) : 'unknown'];
  return (
    <article class={'card rt' + (tone === 'fail' ? ' bad' : '')} aria-labelledby={id}>
      <header class="rt-h">
        <h3 id={id}>{rn.name}</h3>
        {rn.known ? <code class="tg">{b.tag}</code> : null}
        <Badge tone={tone}>{t(label)}</Badge>
      </header>
      {b.matchers.length ? <Carries b={b} /> : null}
      <ol class="chain">
        {stops(steps).map((stop, i) => (
          <Stop key={stop[0].kind + stop[0].tag + stop[0].join} stop={stop} first={!i} ctx={ctx} unused={unused} />
        ))}
      </ol>
    </article>
  );
}

export function Balancing() {
  const { t, refresh } = useApp();
  const br = useRes('balancers');
  const nodesRes = useRes('nodes');
  const engine = useRes('status').data?.engine.state;
  const data = br.data;
  if (!data) return br.error ? <ErrorBox t={t} err={br.error} onRetry={refresh} /> : <Skeleton />;
  const nodes = new Map((nodesRes.data?.nodes || []).map((n) => [n.tag, n] as const));
  const alive: Alive = (tag) => nodes.get(tag)?.alive ?? null;
  const list = data.balancers;
  const paths = fallbackPaths(list, alive);
  const routes = paths.main.length ? [paths.main, ...paths.routed] : [];
  // A balancer on no route — no rule and no fallback on a route leads to it —
  // carries nothing, whatever its role; it still gets a card: Pro shows everything.
  const shown = new Set(routes.reduce<string[]>((a, r) => a.concat(r.filter((s) => s.bal).map((s) => s.tag)), []));
  const idle = list.filter((b) => !shown.has(b.tag));
  // A router with no subscription has no routes to judge; one whose xray is down has none that carry.
  const live = !engine || engine === 'running' || !list.length;
  const env: RouteEnv = { alive, isNode: (tag) => nodes.has(tag), def: data.defaultOutbound, index: new Map(list.map((b) => [b.tag, b] as const)) };
  const ctx: Ctx = { nodes, alive, env, main: paths.main[0]?.bal ? routeName(t, paths.main[0].bal).name : '', live };
  return (
    <div class="stack">
      <p class="lede">{t('rt.intro')}</p>
      {live ? null : <Note tone="fail">{t('d.xray_running.bad', { state: stateLabel(t, engine) })}</Note>}
      {data.apiReachable === false && list.length ? <Note tone="warn">{t('b.apiDown')}</Note> : null}
      {list.length ? null : <Empty icon="split">{t('b.none')}</Empty>}
      <div class="bals">
        {routes.map((r, i) => (
          <Route key={r[0].tag} steps={r} ctx={ctx} id={'vx-rt-' + i} />
        ))}
        {idle.map((b, i) => (
          <Route key={b.tag} steps={[{ kind: 'balancer', tag: b.tag, bal: b, state: 'standby', join: false }]} ctx={ctx} id={'vx-rt-u' + i} unused />
        ))}
        <ProbeCard probe={data.probe} />
      </div>
    </div>
  );
}
