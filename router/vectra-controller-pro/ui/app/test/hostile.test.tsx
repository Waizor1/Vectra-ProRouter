// Provider and router data name things freely: a balancer, node, level or
// counter called "__proto__", "constructor" or "toString" must stay a name.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { normalize } from '../src/api/normalize';
import type { CallFn, ReadData } from '../src/api/types';
import { TABS } from '../src/app/ctx';
import { makeT } from '../src/i18n';
import { fallbackPaths, reserveFor } from '../src/lib/balancing';
import { makeFmt } from '../src/lib/format';
import { checkText, sortChecks } from '../src/lib/labels';
import { matcherLabel, matcherLabels } from '../src/lib/matchers';
import { clone } from '../src/mock/scenarios';
import { FIXTURES } from '../src/mock/fixtures';
import { mount } from '../src/mount';

const NAMES = ['__proto__', 'constructor', 'toString'];

function hostileWorld(): ReadData {
  const w = clone(FIXTURES);
  const base = w.balancers.balancers[1];
  // BL-MAIN → __proto__ → constructor → toString → hasOwnProperty (a plain node).
  w.balancers.balancers[0].fallback = { tag: 'stage-x', balancer: '__proto__' };
  w.balancers.balancers.push(
    { ...base, tag: '__proto__', role: 'reserve', members: ['__proto__'], selected: [], pinned: null, fallback: { tag: 's', balancer: 'constructor' } },
    { ...base, tag: 'constructor', role: 'constructor', members: ['constructor'], selected: null, pinned: 'constructor', fallback: { tag: 's', balancer: 'toString' } },
    { ...base, tag: 'toString', role: 'reserve', members: ['toString'], selected: ['toString'], pinned: null, fallback: { tag: 'hasOwnProperty', balancer: null } },
  );
  w.balancers.balancers[0].matchers.push({ kind: 'constructor', network: null, sample: NAMES, total: 3 });
  for (const tag of NAMES) w.nodes.nodes.push({ ...w.nodes.nodes[0], tag, countryHint: null, balancers: [tag] });
  w.diagnostics.checks.push({ id: 'constructor', status: 'warn', params: {} }, { id: 'toString', status: 'constructor', params: {} });
  w.logs.lines.push({ time: null, level: 'constructor', source: '__proto__', message: 'toString' });
  // Own "__proto__" keys only exist the way real answers arrive: through JSON.
  const text = JSON.stringify(w)
    .replace('"counters":{', '"counters":{"__proto__":1,"constructor":2,')
    .replace('"pins":{', '"pins":{"__proto__":"x","constructor":"constructor",')
    .replace('{"id":"constructor","status":"warn","params":{}', '{"id":"constructor","status":"warn","params":{"__proto__":1}');
  return JSON.parse(text) as ReadData;
}

let cleanup: (() => void)[] = [];
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  localStorage.clear();
});

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

describe('prototype-named data', () => {
  it('builds the fallback chain and reserve map through the odd names', () => {
    const list = normalize('balancers', hostileWorld().balancers).balancers;
    const { main } = fallbackPaths(list);
    expect(main.map((s) => s.tag)).toEqual(['BL-MAIN', '__proto__', 'constructor', 'toString', 'hasOwnProperty']);
    const odd = fallbackPaths([list[list.length - 3], list[list.length - 2], list[list.length - 1]]);
    expect(odd.main.map((s) => s.tag)).toEqual(['__proto__', 'constructor', 'toString', 'hasOwnProperty']);
    expect(odd.main.map((s) => s.state)).toEqual(['skip', 'here', 'standby', 'standby']);
    expect(reserveFor(list).get('constructor')).toEqual(['__proto__']);
    expect(reserveFor(list).get('toString')).toEqual(['constructor']);
  });

  it('keeps data keys as data in normalized dictionaries', () => {
    const s = normalize('status', hostileWorld().status);
    expect(Object.keys(s.pins!)).toEqual(['__proto__', 'constructor', 'BL-MAIN']);
    expect(s.pins!['__proto__']).toBe('x');
    expect(Object.keys(s.dataplane.counters!)).toContain('__proto__');
  });

  it('shows odd matchers, check ids and statuses raw', () => {
    const t = makeT('en');
    for (const n of NAMES) expect(matcherLabel(t, n)).toEqual({ text: n, known: false });
    expect(matcherLabels(t, NAMES).map((l) => l.label.text)).toEqual(NAMES);
    const f = makeFmt(t);
    expect(checkText(t, f, { id: 'constructor', status: 'warn', params: {} })).toBe('constructor');
    expect(checkText(t, f, { id: 'toString', status: 'ok', params: {} })).toBe('toString');
    expect(sortChecks([{ id: 'a', status: 'constructor', params: {} }, { id: 'b', status: 'fail', params: {} }]).map((c) => c.id)).toEqual(['b', 'a']);
  });

  it('renders every tab without throwing', async () => {
    const world = hostileWorld();
    const call: CallFn = (m) => Promise.resolve(m in world ? JSON.parse(JSON.stringify(world[m as keyof ReadData])) : { ok: true, code: 'constructor', detail: null });
    const errors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    localStorage.setItem('vectra.ui.mode', 'pro');
    const host = document.createElement('div');
    document.body.appendChild(host);
    cleanup.push(mount(host, { call, lang: 'en' }), () => host.remove());
    const root = host.shadowRoot!;
    await settle();
    for (const tab of TABS) {
      root.querySelector<HTMLElement>('#vx-tab-' + tab)!.click();
      await settle();
      expect(root.querySelector('.err'), tab).toBeNull();
      expect(root.textContent, tab).not.toMatch(/undefined|NaN|\[object Object\]|function /);
    }
    root.querySelector<HTMLElement>('#vx-tab-balancing')!.click();
    await settle();
    const text = root.textContent ?? '';
    for (const n of NAMES) expect(text).toContain(n);
    // The simple view reads the same answers, pins named "__proto__" included.
    root.querySelector<HTMLElement>('[role="radio"]:not([aria-checked="true"])')!.click();
    await settle();
    expect(root.querySelector('.sv')).not.toBeNull();
    expect(root.textContent).not.toMatch(/undefined|NaN|\[object Object\]|function /);
    expect(errors).not.toHaveBeenCalled();
  });
});
