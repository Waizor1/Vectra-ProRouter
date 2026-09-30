// The UI against ../contract: every code the router may send has words, every
// fixture survives normalization unchanged, and unknown codes degrade to raw.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { describeError } from '../src/api/errors';
import { normAction, normalize } from '../src/api/normalize';
import type { Check, ReadMethod } from '../src/api/types';
import { LANGS, makeT } from '../src/i18n';
import { fallbackPaths, reserveFor } from '../src/lib/balancing';
import { makeFmt } from '../src/lib/format';
import { health } from '../src/lib/health';
import { actionText, checkText, sortChecks } from '../src/lib/labels';
import { matcherLabel } from '../src/lib/matchers';
import { CONTRACT_SETUP, CONTRACT_WAN_CHECK, CONTRACT_WIFI_SCAN, FIXTURES } from '../src/mock/fixtures';
import { buildWorld } from '../src/mock/scenarios';
import { createMock } from '../src/mock/transport';
import { wifiEsc } from '../src/views/Setup';

const README = readFileSync(resolve(__dirname, '../../contract/README.md'), 'utf8');

/** Diagnostic ids from the README's Diagnostics table. */
function diagnosticIds(): string[] {
  const table = README.split('## Diagnostics')[1].split('\n## ')[0];
  return [...table.matchAll(/^\| `(\w+)` \|/gm)].map((m) => m[1]);
}

/** Every action.code named in the README's Enumerations bullet (explanations in parentheses are prose). */
function actionCodes(): string[] {
  const bullet = README.split('- `action.code` on success')[1].split(/\n- |\n\n/)[0].replace(/\([^)]*\)/g, '');
  return [...bullet.matchAll(/`([a-z_]+)`/g)].map((m) => m[1]);
}

describe('contract coverage', () => {
  const ids = diagnosticIds();
  const codes = actionCodes();

  it('reads the ids and codes from the README', () => {
    expect(ids).toContain('controller_running');
    expect(ids).toContain('pinned_node_dead');
    expect(ids.length).toBeGreaterThanOrEqual(12);
    expect(codes).toContain('entry_selected');
    expect(codes).toContain('pending');
    expect(codes).toContain('internal');
    expect(codes.length).toBeGreaterThanOrEqual(17);
  });

  for (const lang of LANGS) {
    const t = makeT(lang);
    const f = makeFmt(t);

    it(`has a sentence for every diagnostic id and status (${lang})`, () => {
      const fixture = new Map(FIXTURES.diagnostics.checks.map((c) => [c.id, c.params]));
      for (const id of ids) {
        for (const status of ['ok', 'warn', 'fail', 'unknown']) {
          const text = checkText(t, f, { id, status, params: fixture.get(id) ?? {} });
          expect(text, `${id}/${status}`).not.toMatch(new RegExp('^' + id));
          expect(text, `${id}/${status}`).not.toMatch(/undefined|NaN|\bnull\b/);
        }
      }
    });

    it(`has a sentence for every action code (${lang})`, () => {
      for (const code of codes) expect(actionText(t, code), code).not.toBe(code);
    });
  }

  it('shows an unknown diagnostic id raw, with its params, without throwing', () => {
    const t = makeT('ru');
    const text = checkText(t, makeFmt(t), { id: 'brand_new_check', status: 'warn', params: { a: 1, b: 'x', c: null, d: [1, 2] } });
    expect(text).toBe('brand_new_check · a=1, b=x, c=—, d=1, 2');
    expect(checkText(t, makeFmt(t), { id: 'bare', status: 'ok', params: {} })).toBe('bare');
  });

  it('shows an unknown action code raw', () => {
    expect(actionText(makeT('zh'), 'teleported')).toBe('teleported');
  });

  it('explains subscription_ua malformed_happ in every language, and a new reason raw', () => {
    for (const lang of LANGS) {
      const t = makeT(lang);
      expect(checkText(t, makeFmt(t), { id: 'subscription_ua', status: 'fail', params: { reason: 'malformed_happ' } })).toMatch(/Happ/);
    }
    const t = makeT('en');
    expect(checkText(t, makeFmt(t), { id: 'subscription_ua', status: 'fail', params: { reason: 'something_new' } })).toContain('something_new');
  });

  it('orders checks fail, warn, unknown, ok and keeps the router order inside a status', () => {
    const c = (id: string, status: string): Check => ({ id, status, params: {} });
    const sorted = sortChecks([c('a', 'ok'), c('b', 'warn'), c('c', 'fail'), c('d', 'unknown'), c('e', 'warn')]);
    expect(sorted.map((x) => x.id)).toEqual(['c', 'b', 'e', 'd', 'a']);
  });
});

describe('normalization', () => {
  const methods = Object.keys(FIXTURES) as ReadMethod[];

  it('returns every fixture unchanged (the spec covers every field)', () => {
    for (const m of methods) expect(normalize(m, FIXTURES[m]), m).toEqual(FIXTURES[m]);
    // The wizard's answers as the router writes them in contract/.
    expect(normalize('setup', CONTRACT_SETUP)).toEqual(CONTRACT_SETUP);
    expect(normalize('wan_check', CONTRACT_WAN_CHECK)).toEqual(CONTRACT_WAN_CHECK);
    expect(normalize('wifi_scan', CONTRACT_WIFI_SCAN)).toEqual(CONTRACT_WIFI_SCAN);
  });

  it('turns an empty answer into nulls and empty lists, never undefined', () => {
    for (const m of methods) {
      const out = JSON.stringify(normalize(m, {}), (_, v) => (v === undefined ? '__undefined__' : v));
      expect(out, m).not.toContain('__undefined__');
    }
    const s = normalize('status', 42);
    expect(s.engine.state).toBeNull();
    expect(s.router.load).toBeNull();
    expect(normalize('balancers', { balancers: [{ role: 'main' }, { tag: 'BL' }] }).balancers.map((b) => b.tag)).toEqual(['BL']);
  });

  it('keeps a balancer whose fallback is malformed, with fallback null', () => {
    const out = normalize('balancers', { balancers: [{ tag: 'X', fallback: { balancer: 'Y' } }] });
    expect(out.balancers[0].fallback).toBeNull();
  });

  it('normalizes action answers', () => {
    expect(normAction({ ok: true, code: 'pending', detail: null })).toEqual({ ok: true, code: 'pending', detail: null });
    expect(normAction({})).toEqual({ ok: false, code: 'internal', detail: null });
  });
});

describe('transport errors', () => {
  it('classifies LuCI rejections', () => {
    expect(describeError(new Error('RPC call to vectra/status failed with ubus code 4: Object not found')).kind).toBe('not_found');
    expect(describeError(new Error('RPC call to vectra/status failed with error -32002: Access denied')).kind).toBe('access');
    expect(describeError(new Error('RPC call to vectra/logs failed with ubus code 7: Request timed out')).kind).toBe('timeout');
    expect(describeError(new Error('RPC call to vectra/x failed with ubus code 3: Method not found')).kind).toBe('method');
    expect(describeError(new Error('RPC call to vectra/status failed with HTTP error 0: ?')).kind).toBe('network');
    // LuCI 24.10's own wordings (luci.js / rpc.js).
    expect(describeError(new Error('RPC call to vectra/status failed with ubus code 4: Resource not found')).kind).toBe('not_found');
    expect(describeError(new Error('XHR request aborted by browser')).kind).toBe('network');
    expect(describeError(new Error('XHR request timed out')).kind).toBe('timeout');
    expect(describeError(new Error('RPC call to vectra/status failed with ubus code 10: Connection lost')).kind).toBe('network');
    // vctl exited without printing JSON: not a timeout.
    expect(describeError(new Error('RPC call to vectra/status failed with ubus code 5: No data received')).kind).toBe('other');
    expect(describeError('boom')).toEqual({ kind: 'other', raw: 'boom' });
  });
});

describe('verdict', () => {
  it('reads each scenario honestly', () => {
    const verdict = (name: 'healthy' | 'degraded' | 'empty') => {
      const w = buildWorld(name)!;
      return health(normalize('status', w.status), normalize('diagnostics', w.diagnostics)).level;
    };
    expect(verdict('healthy')).toBe('warn'); // the fixtures carry four warnings
    expect(verdict('degraded')).toBe('fail');
    expect(verdict('empty')).toBe('setup');
    expect(health(null, null).level).toBe('unknown');
  });
});

describe('fallback chain', () => {
  const list = normalize('balancers', FIXTURES.balancers).balancers;

  it('follows fallback links from the main balancer to the final node', () => {
    const { main, routed } = fallbackPaths(list);
    expect(main.map((s) => s.tag)).toEqual(['BL-MAIN', 'BL-MAIN-BACKUP', 'BL-WL-LV1', 'BL-WL-LV2', 'BL-WL-LV3', 'whitelist-lv3']);
    expect(main.map((s) => s.state)).toEqual(['here', 'standby', 'standby', 'standby', 'standby', 'standby']);
    expect(main[main.length - 1].kind).toBe('node');
    expect(routed.map((r) => r.map((s) => s.tag))).toEqual([
      ['BL-RU', 'BL-MAIN'],
      ['BL-TK', 'BL-MAIN'],
    ]);
    expect(routed[0][1].join).toBe(true);
  });

  it('skips empty balancers and stops where traffic lands', () => {
    const empty = list.map((b) => (b.tag === 'BL-MAIN' || b.tag === 'BL-MAIN-BACKUP' ? { ...b, selected: [], pinned: null } : b));
    expect(fallbackPaths(empty).main.map((s) => s.state).slice(0, 4)).toEqual(['skip', 'skip', 'skip', 'here']);
  });

  it('marks an unknown selection and never loops forever', () => {
    const cyc = [
      { ...list[0], tag: 'A', role: 'main', selected: null, pinned: null, fallback: { tag: 'x', balancer: 'B' } },
      { ...list[0], tag: 'B', role: 'reserve', fallback: { tag: 'y', balancer: 'A' } },
    ];
    const { main } = fallbackPaths(cyc);
    expect(main.map((s) => s.kind)).toEqual(['balancer', 'balancer', 'cycle']);
    expect(main[0].state).toBe('unknown');
  });

  it('gives a routed balancer its own route, also when the main path falls back to it', () => {
    const viaRu = list.map((b) => (b.tag === 'BL-MAIN' ? { ...b, fallback: { tag: 'stage-ru', balancer: 'BL-RU' } } : b));
    const { main, routed } = fallbackPaths(viaRu);
    expect(main.map((s) => s.tag).slice(0, 2)).toEqual(['BL-MAIN', 'BL-RU']);
    expect(routed.map((r) => r.map((s) => s.tag))).toEqual([
      ['BL-RU', 'BL-MAIN'],
      ['BL-TK', 'BL-MAIN'],
    ]);
  });

  it('knows which balancers use each reserve', () => {
    expect(reserveFor(list).get('BL-MAIN')).toEqual(['BL-RU', 'BL-TK']);
    expect(reserveFor(list).get('BL-WL-LV1')).toEqual(['BL-MAIN-BACKUP']);
  });
});

describe('matchers', () => {
  const t = makeT('en');
  it('names well-known matchers and leaves the rest raw', () => {
    expect(matcherLabel(t, 'geosite:telegram')).toEqual({ text: 'Telegram', known: true });
    expect(matcherLabel(t, 'geoip:ru').text).toBe('Russian IPs');
    expect(matcherLabel(t, 'domain:ru').text).toBe('.ru domains');
    expect(matcherLabel(t, '192.168.0.0/16').text).toBe('Local network');
    expect(matcherLabel(t, 'fc00::/7').text).toBe('Local network');
    expect(matcherLabel(t, 'domain:canva.com')).toEqual({ text: 'canva.com', known: false });
    expect(matcherLabel(t, 'port:443')).toEqual({ text: 'port:443', known: false });
  });
});

describe('the mock router refuses what the router refuses', () => {
  // The contract, "Changes": an open network is never left on the air by the wizard's calls.
  it('tunes an open network that is off and leaves it off; one change at a time; names an open network only with a key', async () => {
    const mock = createMock({ scenario: 'unboxed', latencyMs: 0, live: false, applyMs: 0 });
    const tuned = (await mock.call('optimize_wifi', {})) as { ok: boolean; code: string; detail: string | null };
    expect(tuned).toMatchObject({ ok: true, code: 'wifi_optimized' });
    expect(tuned.detail).toContain('radio0: open network left disabled; give it a key');
    // Until the router has seen the Wi-Fi come back, it holds its lock.
    expect(await mock.call('set_wifi', { radios: { radio0: { ssid: 'Home', key: 'a-long-key' } } })).toMatchObject({ ok: false, code: 'busy' });
    await new Promise((r) => setTimeout(r, 5));
    const named = (await mock.call('set_wifi', { radios: { radio0: { ssid: 'Home' } } })) as { ok: boolean; code: string; detail: string | null };
    expect(named).toMatchObject({ ok: false, code: 'invalid_params', detail: 'radios.radio0.key: the network is open — give it a key' });
    mock.dispose();
  });
});

describe('the Wi-Fi QR', () => {
  it('escapes a backslash as well as ; , : and " (the WIFI: format)', () => {
    expect(wifiEsc('a\\b;c,d:e"f')).toBe('a\\\\b\\;c\\,d\\:e\\"f');
    expect(wifiEsc('plain-Key_123')).toBe('plain-Key_123');
  });
});
