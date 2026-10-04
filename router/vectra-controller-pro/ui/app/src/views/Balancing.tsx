// The Routes tab: every route in use as a card — what it carries, its nodes
// (alive, how fast, the one to pin) and, folded away, where it goes next when
// none of them works. Names only: the tags live in the support report.

import type { Balancer, Node } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key, T } from '../i18n';
import { byMembers, fallbackPaths, routeState, type Alive, type RouteEnv, type RouteState, type Step } from '../lib/balancing';
import { delayTone } from '../lib/format';
import { stateLabel } from '../lib/labels';
import { matcherLabel, matcherLabels } from '../lib/matchers';
import { kindOf, nodeName, routeName } from '../lib/names';
import { Badge, Button, Empty, ErrorBox, NodeDot, Note, Skeleton, type Tone } from '../ui/kit';
import { Nm } from '../ui/words';

const ROUTE: Record<RouteState, [Key, Tone]> = { ok: ['rt.ok', 'ok'], backup: ['rt.backup', 'warn'], direct: ['nm.direct', 'fail'], down: ['rt.down', 'fail'], unknown: ['na', 'mute'] };

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
/** A whitelist level: the provider ships several in a row, read as one stop. */
const isWl = (s: Step) => !s.join && tagsOf(s).length > 0 && tagsOf(s).every((x) => kindOf(x) === 'wl');

/** Where a route goes when none of its nodes works, as names: whitelist levels in a row read as one. */
function nextNames(t: T, steps: Step[], ctx: Ctx): string[] {
  const out: string[] = [];
  let wl = false;
  for (const s of steps.slice(1)) {
    if (s.kind === 'cycle' || s.kind === 'missing') continue;
    const level = isWl(s);
    if (level && wl) continue;
    wl = level;
    out.push(
      s.join
        ? t('rt.join', { name: ctx.main })
        : level
          ? t('ng.wl')
          : s.bal
            ? routeName(t, s.bal).name
            : nodeName(t, s.tag, ctx.nodes.get(s.tag)?.countryHint).name,
    );
  }
  return out;
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
        <span key={raw} class={label.known ? 'chip ck' : 'chip'}>
          {label.text}
        </span>
      ))}
      {rest > 0 ? <span class="chip">{t('rt.more', { n: rest })}</span> : null}
    </p>
  );
}

/** A balancer's nodes: alive, how fast, the one in use, and the pin. */
/** `picks`: mark the nodes the balancer uses now (a backup's picks mean nothing until the traffic is there). */
function Members({ b, ctx, route, picks }: { b: Balancer; ctx: Ctx; route: string; picks: boolean }) {
  const { t, f, run, pending } = useApp();
  if (!b.members.length) return <p class="hint">{t('rt.noNodes')}</p>;
  const names = b.members.map((m) => nodeName(t, m, ctx.nodes.get(m)?.countryHint));
  const kinds = names.map((n) => n.kind).filter((k, i, a) => a.indexOf(k) === i);
  // Nodes of one kind read by country: "🇵🇱 Польша", not "Мост → 🇵🇱 Польша" ten times.
  const short = kinds.length === 1 && kinds[0] !== 'other';
  const unpin = () => run('unpin_balancer', { balancer: b.tag }, { key: 'unpin:' + b.tag, landed: (st) => !st.pins?.[b.tag] });
  return (
    <>
      <ul class="mems">
        {b.members.map((m, i) => {
          const n = ctx.nodes.get(m);
          const nm = names[i];
          const alive = n ? n.alive : null;
          // random/roundRobin name no pick: the API lists every member whatever its health.
          const on = b.pinned ? b.pinned === m : picks && !byMembers(b) && !!b.selected && b.selected.indexOf(m) >= 0;
          const tone = delayTone(n?.delayMs);
          const aria = t('b.pinAria', { node: nm.name, bal: route });
          return (
            <li key={m} class={'mem' + (on ? ' on' : '') + (b.pinned === m ? ' pinned' : '')}>
              <NodeDot alive={alive} label={t(alive ? 'nd.alive' : alive === false ? 'nd.dead' : 'nd.unprobed')} />
              <span class="nn">
                <span class="clip" title={nm.name}>
                  <Nm n={nm} short={short} />
                </span>
              </span>
              <span class={'dly' + (tone ? ' dl-' + tone : ' mu')}>{f.delay(n?.delayMs, '—')}</span>
              <span class="mem-b">
                {b.pinned === m ? (
                  <>
                    <Badge tone="warm" icon="pin">
                      {t('b.pinned')}
                    </Badge>
                    <Button key="unpin" kind="g" small icon="pin" class="unpin" title={t('b.unpin')} busy={pending === 'unpin:' + b.tag} disabled={!!pending} onClick={unpin}>
                      {/* A phone keeps the icon (in the pin's colour): the delays stay in one column. */}
                      <span class="pin-t">{t('b.unpin')}</span>
                    </Button>
                  </>
                ) : (
                  <>
                    {on ? (
                      // A tick as in the servers' list; a phone keeps the tick alone, the name of the node needs the room.
                      <Badge tone="info" icon="ok">
                        <span class="bd-t">{t('b.selected')}</span>
                      </Badge>
                    ) : null}
                    <Button
                      key="pin"
                      kind="g"
                      small
                      icon="pin"
                      class="pin"
                      aria-label={aria}
                      title={aria}
                      busy={pending === 'pin:' + b.tag + m}
                      disabled={!!pending}
                      onClick={() =>
                        run('pin_balancer', { balancer: b.tag, node: m }, {
                          key: 'pin:' + b.tag + m,
                          confirm: { title: t('b.pinQ', { node: nm.name }), body: t('b.pinBody', { bal: route }), ok: t('b.pin') },
                          landed: (s) => s.pins?.[b.tag] === m,
                        })
                      }
                    >
                      {/* A phone keeps the pin's icon: the node's name needs the room. */}
                      <span class="pin-t">{t('b.pin')}</span>
                    </Button>
                  </>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {b.pinned && ctx.alive(b.pinned) === false ? <Note tone="fail">{t('b.pinDead')}</Note> : null}
    </>
  );
}

/** A route: its name and state, what it carries, its nodes, and where it goes next. */
function Route({ steps, ctx, id }: { steps: Step[]; ctx: Ctx; id: string }) {
  const { t } = useApp();
  const b = steps[0].bal!;
  const name = routeName(t, b).name;
  const [label, tone] = ROUTE[ctx.live ? routeState(steps, ctx.env) : 'unknown'];
  const next = nextNames(t, steps, ctx);
  // The backups a person can pin in: the chain's own balancers, not the route it joins.
  const backups = steps.slice(1).filter((s) => s.kind === 'balancer' && !s.join && s.bal && s.bal.members.length);
  // Open where the traffic is now, or where a pin was left.
  const open = backups.some((s) => (ctx.live && s.state === 'here') || !!s.bal!.pinned);
  const then = next.length ? t('rt.next', { list: next.join(t('rt.sep')) }) : null;
  return (
    <article class={'card rt' + (tone === 'fail' ? ' bad' : '')} aria-labelledby={id}>
      <header class="rt-h">
        <h3 id={id}>{name}</h3>
        <Badge tone={tone}>{t(label)}</Badge>
      </header>
      {b.matchers.length ? <Carries b={b} /> : null}
      <Members b={b} ctx={ctx} route={name} picks={ctx.live} />
      {then && backups.length ? (
        <details class="more" open={open}>
          <summary>{then}</summary>
          <div class="bk">
            {backups.map((s) => {
              const rn = routeName(t, s.bal!).name;
              return (
                <section key={s.tag} aria-label={rn}>
                  <h4 class="k">{rn}</h4>
                  <Members b={s.bal!} ctx={ctx} route={rn} picks={ctx.live && s.state === 'here'} />
                </section>
              );
            })}
          </div>
        </details>
      ) : then ? (
        <p class="hint">{then}</p>
      ) : null}
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
  // Only the routes traffic can take: a balancer no rule and no fallback leads to carries nothing.
  const routes = paths.main.length ? [paths.main, ...paths.routed] : [];
  // A router with no subscription has no routes to judge; one whose xray is down has none that carry.
  const live = !engine || engine === 'running' || !list.length;
  const env: RouteEnv = { alive, isNode: (tag) => nodes.has(tag), def: data.defaultOutbound, index: new Map(list.map((b) => [b.tag, b] as const)) };
  const ctx: Ctx = { nodes, alive, env, main: paths.main[0]?.bal ? routeName(t, paths.main[0].bal).name : '', live };
  return (
    <div class="stack">
      {routes.length ? <p class="lede">{t('rt.pinHint')}</p> : null}
      {live ? null : <Note tone="fail">{t('d.xray_running.bad', { state: stateLabel(t, engine) })}</Note>}
      {data.apiReachable === false && list.length ? <Note tone="warn">{t('b.apiDown')}</Note> : null}
      {routes.length ? null : <Empty icon="split">{t('b.none')}</Empty>}
      <div class="bals">
        {routes.map((r, i) => (
          <Route key={r[0].tag} steps={r} ctx={ctx} id={'vx-rt-' + i} />
        ))}
      </div>
    </div>
  );
}
