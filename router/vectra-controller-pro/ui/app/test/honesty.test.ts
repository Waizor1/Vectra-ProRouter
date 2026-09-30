// The UI must never tell the router's owner something false. Each case here is
// a router state an independent review found the UI would have misreported.
import { describe, expect, it, vi } from 'vitest';
import { normalize } from '../src/api/normalize';
import { createStore } from '../src/api/store';
import type { Balancer, Check, Diagnostics, Status } from '../src/api/types';
import { makeT } from '../src/i18n';
import { carries, chainFrom, fallbackPaths, routeState, type Alive, type RouteEnv } from '../src/lib/balancing';
import { makeFmt } from '../src/lib/format';
import { health, simpleVerdict } from '../src/lib/health';
import { checkText } from '../src/lib/labels';
import { FIXTURES } from '../src/mock/fixtures';
import { clone } from '../src/mock/scenarios';

const status = (): Status => clone(FIXTURES.status);
function diags(...checks: Check[]): Diagnostics {
  const d = clone(FIXTURES.diagnostics);
  const ids = new Set(checks.map((c) => c.id));
  d.checks = d.checks.filter((c) => !ids.has(c.id)).concat(checks);
  return d;
}
const bf = (status: string, params: Record<string, unknown>): Check => ({ id: 'balancer_fallback', status, params });

describe('the simple verdict says only what it can back', () => {
  it('says "checking" until the diagnostics arrive, and makes no claim about servers when they cannot be read', () => {
    expect(simpleVerdict(status(), null).kind).toBe('checking');
    const v = simpleVerdict(status(), null, true);
    expect(v).toMatchObject({ kind: 'ok', lines: ['s.ok.d0'] });
  });

  it('says "servers respond" only when the router walked the chains and found them carried', () => {
    expect(simpleVerdict(status(), diags(bf('ok', { balancers: [], main: false, blocked: false }))).lines).toEqual(['s.ok.d']);
    const d = diags();
    d.checks = d.checks.filter((c) => c.id !== 'balancer_fallback');
    expect(simpleVerdict(status(), d).lines).toEqual(['s.ok.d0']);
  });

  it('reports traffic bypassing a running xray (the flushed-route failure) — or cut by the kill switch', () => {
    const leak = (killSwitch: boolean): Check => ({ id: 'no_leak', status: 'fail', params: { wouldLeak: 900, sinceStart: 900, escaped: 900, killSwitch } });
    expect(simpleVerdict(status(), diags(leak(false)))).toMatchObject({ kind: 'bypass', tone: 'fail' });
    expect(simpleVerdict(status(), diags(leak(true)))).toMatchObject({ kind: 'cut', tone: 'fail' });
    // A handful of packets is a Pro warning, not "the VPN does not work".
    expect(simpleVerdict(status(), diags({ id: 'no_leak', status: 'warn', params: { sinceStart: 3 } })).kind).toBe('ok');
  });

  it('treats a dead pin on the main balancer as an outage, elsewhere as "some sites"', () => {
    const pin = (main: boolean): Check => ({ id: 'pinned_node_dead', status: main ? 'fail' : 'warn', params: { balancer: 'BL-X', node: 'n', main } });
    expect(simpleVerdict(status(), diags(pin(true)))).toMatchObject({ kind: 'pinDown', act: 'unpin', tone: 'fail' });
    expect(simpleVerdict(status(), diags(pin(false)))).toMatchObject({ kind: 'partial', act: 'unpin' });
  });

  it('does not guess while the observatory warms up after an xray start', () => {
    expect(simpleVerdict(status(), diags(bf('unknown', { balancers: [], warming: true }))).kind).toBe('connecting');
  });

  it('says the internet runs without the VPN when the main chain ends in DIRECT', () => {
    expect(simpleVerdict(status(), diags(bf('fail', { balancers: ['BL-MAIN'], main: true, mainDirect: true, blocked: false }))).kind).toBe('direct');
  });

  it('says "no route" only for the main chain; a blocked routed chain is "some sites"', () => {
    const both = bf('fail', { balancers: ['BL-MAIN', 'BL-X'], main: true, mainBlocked: false, blocked: true, blockedBalancers: ['BL-X'] });
    expect(simpleVerdict(status(), diags(both)).kind).toBe('partial');
    const main = bf('fail', { balancers: ['BL-MAIN'], main: true, mainBlocked: true, blocked: true, blockedBalancers: ['BL-MAIN'] });
    expect(simpleVerdict(status(), diags(main)).kind).toBe('noroute');
    // vctl 0.3 had no mainBlocked: fail + main + blocked still reads as no route.
    expect(simpleVerdict(status(), diags(bf('fail', { balancers: ['BL-MAIN'], main: true, blocked: true }))).kind).toBe('noroute');
  });
});

describe('the fallback chain knows how each strategy behaves', () => {
  const base = clone(FIXTURES.balancers.balancers[0]);
  const bal = (over: Partial<Balancer>): Balancer => ({ ...base, pinned: null, fallback: null, ...over });

  it('reads a leastPing balancer with no live node ([""] from xray) as carrying nothing', () => {
    const b = normalize('balancers', { balancers: [{ ...base, strategy: 'leastPing', pinned: null, selected: [''] }] }).balancers[0];
    expect(b.selected).toEqual([]);
    expect(carries(b)).toBe(false);
  });

  it('judges random/roundRobin by their members as xray does: a never-probed node counts alive, all dead passes on', () => {
    const dead: Alive = () => false;
    const oneDead: Alive = (tag) => (tag === 'bridge-de5' ? false : null);
    // The API lists every member as selected whatever its health: that is not evidence.
    const r = bal({ strategy: 'random', members: ['bridge-de5', 'bridge-nl5'], selected: ['bridge-de5', 'bridge-nl5'] });
    expect(carries(r)).toBeNull();
    expect(carries(r, oneDead)).toBe(true);
    expect(carries(r, dead)).toBe(false);
    const next = bal({ tag: 'NEXT', role: 'reserve', members: ['x'], selected: ['x'] });
    const chain = chainFrom({ ...r, fallback: { tag: 'to-next', balancer: 'NEXT' } }, new Map([['NEXT', next]]), undefined, dead);
    expect(chain.map((s) => s.state)).toEqual(['skip', 'here']);
    // No fallback at all: xray hands the traffic to its default outbound, not to nothing.
    const alone = chainFrom(r, new Map(), undefined, dead);
    expect(alone.map((s) => [s.state, s.toDefault])).toEqual([['skip', true]]);
  });
});

describe('a route says where its traffic lands, as the router judges it (internal/uiapi walkChain)', () => {
  const list = normalize('balancers', FIXTURES.balancers).balancers;
  const index = new Map(list.map((b) => [b.tag, b] as const));
  const nodes = new Set(FIXTURES.nodes.nodes.map((n) => n.tag));
  const main = list.find((b) => b.tag === 'BL-MAIN')!;
  const env = (alive: Alive = () => true, def: string | null = 'bridge-pl5', idx = index): RouteEnv => ({ alive, isNode: (tag) => nodes.has(tag), def, index: idx });
  const route = (b: Balancer, e: RouteEnv = env()) => routeState(chainFrom(b, e.index, undefined, e.alive), e);
  const endsIn = (tag: string): Balancer => ({ ...main, pinned: null, selected: [], fallback: { tag, balancer: null } });
  const alone: Balancer = { ...main, pinned: null, selected: [], fallback: null };

  it('is ok when its own nodes carry it, on a backup when a fallback does', () => {
    expect(route({ ...main, pinned: null })).toBe('ok');
    expect(route({ ...main, pinned: null, selected: [] })).toBe('backup');
  });

  it('is down when it is pinned to a dead node: xray sends it to the pin whatever its health', () => {
    expect(route(main)).toBe('ok');
    expect(route(main, env((tag) => tag !== 'bridge-nl5'))).toBe('down');
  });

  it('ends: a node carries it unless seen dead, freedom goes without the VPN, a blackhole drops it, anything else cannot be told', () => {
    expect(route(endsIn('bridge-de5'))).toBe('backup');
    expect(route(endsIn('bridge-de5'), env((tag) => tag !== 'bridge-de5'))).toBe('down');
    expect(route(endsIn('DIRECT'))).toBe('direct');
    expect(route(endsIn('vctl-direct'))).toBe('direct');
    expect(route(endsIn('BLOCK'))).toBe('down');
    // Not a proxy node and no known name: a loopback, dns — the router cannot tell either.
    expect(route(endsIn('stage-main'))).toBe('unknown');
    expect(route(endsIn('my-freedom'))).toBe('unknown');
  });

  it('with nothing left on the chain, goes where xray’s default outbound sends it', () => {
    const steps = chainFrom(alone, index, undefined, () => true);
    expect(steps.map((s) => [s.state, s.toDefault])).toEqual([['skip', true]]);
    expect(routeState(steps, env())).toBe('backup'); // bridge-pl5: a node, alive
    expect(routeState(steps, env((tag) => tag !== 'bridge-pl5'))).toBe('down');
    expect(routeState(steps, env(undefined, 'DIRECT'))).toBe('direct');
    expect(routeState(steps, env(undefined, null))).toBe('down'); // no default outbound at all
  });

  it('random/roundRobin: unknown until a member is seen alive; stuck on dead members without a fallback', () => {
    const r: Balancer = { ...alone, strategy: 'roundrobin', members: ['bridge-de5', 'bridge-nl5'], selected: ['bridge-de5', 'bridge-nl5'] };
    expect(route(r, env(() => null))).toBe('unknown'); // xray sends it there, but nobody knows yet whether they work
    expect(route(r, env((tag) => (tag === 'bridge-de5' ? true : null)))).toBe('ok');
    expect(route(r, env(() => false))).toBe('down'); // no fallback: they keep sending to their dead members
    expect(route({ ...r, members: [] })).toBe('backup'); // no member at all: passed on to the default outbound
  });

  it('a routed chain goes on along the main path where it joins it', () => {
    // BL-RU's only node is dead and BL-MAIN has none working either: the main path's backup carries it.
    const dead: Alive = (tag) => (tag === 'bridge-ru-tcp' || main.members.indexOf(tag) >= 0 ? false : true);
    const lost = list.map((b) => (b.tag === 'BL-MAIN' ? { ...b, pinned: null, selected: [] } : b));
    const idx = new Map(lost.map((b) => [b.tag, b] as const));
    const { routed } = fallbackPaths(lost, dead);
    const ru = routed[0];
    expect(ru.map((s) => [s.tag, s.state])).toEqual([
      ['BL-RU', 'here'],
      ['BL-MAIN', 'standby'],
    ]);
    const e = env(dead, 'bridge-pl5', idx);
    // xray's leastLoad still lists its target: the router judges by the selection, and so does the UI.
    expect(routeState(ru, e)).toBe('ok');
    const noRu = lost.map((b) => (b.tag === 'BL-RU' ? { ...b, selected: [] } : b));
    const ru2 = fallbackPaths(noRu, dead).routed[0];
    expect(ru2.map((s) => [s.tag, s.state])).toEqual([
      ['BL-RU', 'skip'],
      ['BL-MAIN', 'skip'],
    ]);
    expect(routeState(ru2, env(dead, 'bridge-pl5', new Map(noRu.map((b) => [b.tag, b] as const))))).toBe('backup');
  });

  it('cannot tell when xray’s API could not be asked — a pin then may be only what the router will re-apply', () => {
    expect(route({ ...main, pinned: null, selected: null })).toBe('unknown');
    expect(route({ ...main, selected: null })).toBe('unknown');
  });
});

describe('Pro sentences for the new diagnostics', () => {
  const t = makeT('ru');
  const f = makeFmt(t);
  it('names the broken route, the kill switch drops, DIRECT and the warm-up', () => {
    expect(checkText(t, f, { id: 'no_leak', status: 'fail', params: { escaped: 12, sinceStart: 12, killSwitch: false } })).toBe(
      'Перехваченный трафик уходит мимо прокси: 12 пакетов — сломан маршрут к xray',
    );
    expect(checkText(t, f, { id: 'no_leak', status: 'fail', params: { killSwitch: true, dropsSinceStart: 51 } })).toBe('Kill switch отбросил при работающем xray: 51 пакет');
    expect(checkText(t, f, { id: 'no_leak', status: 'warn', params: { sinceStart: 3, killSwitch: false } })).toBe('Утечки при работающем xray: 3 пакета');
    expect(checkText(t, f, bf('fail', { balancers: ['BL-MAIN'], main: true, mainDirect: true }))).toContain('DIRECT');
    expect(checkText(t, f, bf('fail', { balancers: ['BL-MAIN', 'BL-X'], blocked: true, blockedBalancers: ['BL-X'] }))).toBe(
      'Трафику BL-X некуда идти: нет ни рабочих узлов, ни резерва',
    );
    expect(checkText(t, f, bf('unknown', { warming: true }))).toBe('Узлы ещё проверяются после запуска xray');
  });
});

describe('a read the router refused', () => {
  it('is an error, never empty data marked fresh', async () => {
    const store = createStore(vi.fn(() => Promise.resolve({ ok: false, code: 'locked', detail: null })));
    await store.fetch('nodes');
    expect(store.get('nodes').data).toBeNull();
    expect(store.get('nodes').error?.kind).toBe('locked');
    store.dispose();
  });
});

describe('Vectra switched off is not Vectra broken', () => {
  const offWith = (power: Status['power']): Status => ({ ...status(), power, controller: { running: false, pid: null, uptimeSec: null } });

  it('says off, where the internet goes, and offers the way back — whatever the checks say', () => {
    const failing = diags({ id: 'controller_running', status: 'fail', params: {} }, { id: 'single_stack', status: 'fail', params: { passwallRunning: true } });
    for (const [holder, line] of [
      ['passwall2', 's.pw.via.passwall2'],
      ['agent', 's.pw.via.agent'],
      ['direct', 's.pw.via.direct'],
      ['sing-box', 's.pw.via.other'], // a holder this UI does not know yet
      [null, 's.pw.via.other'],
    ] as const) {
      const s = offWith({ enabled: false, running: false, holder, handBack: null });
      expect(simpleVerdict(s, failing), String(holder)).toMatchObject({ kind: 'poweroff', tone: 'mute', title: 's.pw.off.t', lines: [line], act: 'power' });
      expect(health(s, failing).level).toBe('off');
    }
  });

  it('switched off but still running is being turned off', () => {
    const s = { ...status(), power: { enabled: false, running: true, holder: 'vectra', handBack: 'passwall2' } };
    expect(simpleVerdict(s, diags())).toMatchObject({ kind: 'switching', title: 's.pw.stopping.t', act: null });
  });

  it('reads a router older than the switch as it always did', () => {
    const s = { ...status(), power: { enabled: null, running: null, holder: null, handBack: null } };
    expect(simpleVerdict(s, diags()).kind).toBe('ok');
    expect(health(s, diags()).level).not.toBe('off');
    // And a switched-on Vectra whose controller is down is still a failure.
    const down = { ...status(), power: { enabled: true, running: false, holder: 'passwall2', handBack: 'passwall2' }, controller: { running: false, pid: null, uptimeSec: null } };
    expect(simpleVerdict(down, diags()).kind).toBe('off');
    expect(health(down, diags()).level).toBe('fail');
  });
});
