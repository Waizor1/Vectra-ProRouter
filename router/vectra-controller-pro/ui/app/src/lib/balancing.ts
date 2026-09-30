// The fallback graph: each balancer may fall back to another balancer or to a
// plain node. The Routes tab draws each route as a chain of stops and marks
// where its traffic actually is.
// Tags come from the provider, so every lookup keyed by them is a Map: a
// balancer called "__proto__" or "constructor" is just a name.

import type { Balancer } from '../api/types';
import { kindOf } from './names';

/** here: carries the traffic · skip: none working, passes it on · standby: never reached · unknown: cannot tell. */
export type StepState = 'here' | 'skip' | 'standby' | 'unknown';

export interface Step {
  kind: 'balancer' | 'node' | 'cycle' | 'missing';
  tag: string;
  bal: Balancer | null;
  state: StepState;
  /** The station where a routed chain joins the main path. */
  join: boolean;
  /** A balancer with no working node and no fallback: xray hands its traffic to the default outbound. */
  toDefault?: boolean;
}

const MAX_STEPS = 32;

/** Node liveness from the observatory: true, false, or null = not probed yet. */
export type Alive = (tag: string) => boolean | null;

const STRATEGIES = ['leastLoad', 'leastPing', 'random', 'roundRobin'];

/** xray's strategy, whatever case the config spells it in (the router reads it so); none at all is random. */
export function strategyOf(b: Balancer): string {
  const s = b.strategy.toLowerCase();
  return s ? (STRATEGIES.find((x) => x.toLowerCase() === s) ?? b.strategy) : 'random';
}

/**
 * random and roundRobin: xray's API lists every member as selected whatever
 * its health, but the balancer itself drops members the observatory saw dead
 * (a member never probed counts as alive) and passes the traffic on when none
 * is left (xray v26.3.27, app/router/balancing.go, strategy_random.go).
 */
export const byMembers = (b: Balancer) => {
  const s = strategyOf(b);
  return s === 'random' || s === 'roundRobin';
};

/**
 * Does this balancer carry traffic itself right now? null = cannot tell.
 * leastLoad/leastPing: what xray's strategy selects (only live nodes).
 * random/roundRobin: whether any member is not known dead.
 */
export function carries(b: Balancer, alive?: Alive): boolean | null {
  if (b.pinned) return true;
  if (byMembers(b)) {
    if (!b.members.length) return false;
    if (!alive) return null;
    return b.members.some((m) => alive(m) !== false);
  }
  if (b.selected === null) return null;
  return b.selected.length > 0;
}

export function chainFrom(start: Balancer, index: Map<string, Balancer>, joinAt?: Set<string>, alive?: Alive): Step[] {
  const steps: Step[] = [];
  const seen = new Set<string>();
  let cur: Balancer | undefined = start;
  while (cur && steps.length < MAX_STEPS) {
    if (seen.has(cur.tag)) {
      steps.push({ kind: 'cycle', tag: cur.tag, bal: null, state: 'standby', join: false });
      break;
    }
    seen.add(cur.tag);
    const join = !!joinAt && steps.length > 0 && joinAt.has(cur.tag);
    steps.push({ kind: 'balancer', tag: cur.tag, bal: cur, state: 'standby', join });
    const fb: Balancer['fallback'] = cur.fallback;
    if (join || !fb) break;
    if (fb.balancer) {
      cur = index.get(fb.balancer);
      if (!cur) steps.push({ kind: 'missing', tag: fb.balancer, bal: null, state: 'standby', join: false });
    } else {
      steps.push({ kind: 'node', tag: fb.tag, bal: null, state: 'standby', join: false });
      cur = undefined;
    }
  }

  // Traffic stops at the first station that has something to carry it.
  let settled: 'here' | 'unknown' | null = null;
  for (const st of steps) {
    if (settled) {
      st.state = settled === 'here' ? 'standby' : 'unknown';
      continue;
    }
    if (st.kind === 'node') {
      st.state = 'here';
      settled = 'here';
    } else if (st.bal) {
      const c = carries(st.bal, alive);
      st.state = c === null ? 'unknown' : c ? 'here' : 'skip';
      if (c !== false) settled = c === null ? 'unknown' : 'here';
    }
  }
  // Nothing carried it and there is no fallback: xray's dispatcher sends the
  // traffic to its default handler, the config's first outbound.
  const last = steps[steps.length - 1];
  if (last && last.kind === 'balancer' && last.state === 'skip' && !last.bal?.fallback) last.toDefault = true;
  return steps;
}

export interface Paths {
  main: Step[];
  routed: Step[][];
}

export function fallbackPaths(list: Balancer[], alive?: Alive): Paths {
  const index = new Map(list.map((b) => [b.tag, b] as const));
  const targets = new Set(list.map((b) => b.fallback?.balancer));
  const start = list.find((b) => b.role === 'main') || list.find((b) => !targets.has(b.tag) && b.role !== 'unused') || list[0];
  if (!start) return { main: [], routed: [] };
  const main = chainFrom(start, index, undefined, alive);
  const onMain = new Set(main.filter((s) => s.kind === 'balancer').map((s) => s.tag));
  // Every routed balancer is a route of its own — also one the main path falls
  // back to: the traffic its rules name starts at it.
  const routed = list.filter((b) => b.role === 'routed' && b !== start).map((b) => chainFrom(b, index, onMain, alive));
  return { main, routed };
}

/**
 * Where a route's traffic lands, as the route's state. `ok`: its own balancer
 * carries it · `backup`: a later balancer, a plain node or xray's default
 * outbound does · `direct`: it ends in freedom, out WITHOUT the VPN · `down`:
 * nothing takes it (a dead node, a blackhole, a loop, members stuck dead, or a
 * pin on a node the observatory saw dead — xray sends traffic to the pin
 * whatever its health) · `unknown`: the router could not tell.
 */
export type RouteState = 'ok' | 'backup' | 'direct' | 'down' | 'unknown';

/** What a route's end is judged by. */
export interface RouteEnv {
  alive: Alive;
  /** The proxy outbounds (nodes[]): only such an outbound carries traffic on its own. */
  isNode: (tag: string) => boolean;
  /** xray's default outbound, the config's first: it gets what a chain passes on with nothing left. */
  def: string | null;
  /** Every balancer by tag, to follow a routed chain on along the main path it joins. */
  index: Map<string, Balancer>;
}

/**
 * Where a plain outbound sends what it is handed, as the router judges it
 * (internal/uiapi outboundEnd): a proxy node carries it unless the observatory
 * saw it dead; freedom sends it out without the VPN; a blackhole, or no
 * default outbound at all, drops it; anything else — a loopback into rules
 * the router does not follow, dns — cannot be told.
 */
function outboundEnd(tag: string | null, env: RouteEnv): RouteState {
  if (tag === null) return 'down';
  if (env.isNode(tag)) return env.alive(tag) === false ? 'down' : 'backup';
  const k = kindOf(tag);
  return k === 'direct' ? 'direct' : k === 'block' ? 'down' : 'unknown';
}

/** Follows a route's traffic the way the router does (internal/uiapi walkChain). */
export function routeState(steps: Step[], env: RouteEnv): RouteState {
  // A routed chain that reaches the main path goes on the way the main path does from there.
  const last = steps[steps.length - 1];
  if (last?.join && last.bal && last.state === 'skip') steps = steps.slice(0, -1).concat(chainFrom(last.bal, env.index, undefined, env.alive));
  const at = steps.find((s) => s.state === 'here');
  if (at?.kind === 'node') return outboundEnd(at.tag, env);
  if (at?.bal) {
    const b = at.bal;
    // xray's API was not asked: a pin shown may be only what the router will re-apply.
    if (b.selected === null) return 'unknown';
    if (b.pinned && env.alive(b.pinned) === false) return 'down';
    // random/roundRobin send traffic to members never probed too; whether those work, nobody knows yet.
    if (!b.pinned && byMembers(b) && !b.members.some((m) => env.alive(m) === true)) return 'unknown';
    return at === steps[0] ? 'ok' : 'backup';
  }
  const end = steps[steps.length - 1];
  if (!end || end.state === 'unknown' || end.kind === 'missing') return 'unknown';
  // Nothing on the chain takes it: xray's default outbound gets it — except from
  // random/roundRobin without a fallback, which keep sending to their dead members.
  if (end.toDefault && end.bal && (!byMembers(end.bal) || !end.bal.members.length)) return outboundEnd(env.def, env);
  return 'down';
}

/** For each balancer: the balancers that fall back to it. */
export function reserveFor(list: Balancer[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const b of list) {
    const to = b.fallback?.balancer;
    if (to && to !== b.tag) out.set(to, (out.get(to) || []).concat(b.tag));
  }
  return out;
}
