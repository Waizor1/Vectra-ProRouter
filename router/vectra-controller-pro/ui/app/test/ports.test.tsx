// Port forwarding (Pro): the contract's answer, the form's checks, every
// refusal in short words, and the screen — the whole list goes back on every
// change.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import portForwardsJson from '../../contract/port_forwards.json';
import { normalize } from '../src/api/normalize';
import type { CallFn, PortForwards } from '../src/api/types';
import { LANGS, makeT } from '../src/i18n';
import { S } from '../src/i18n/strings';
import { checkDraft, draftOf, emptyDraft, FAIL, failKey, ipv4, landed, overlaps, parsePorts, portLabel, presetOf, PRESETS, wireOf, wireOfDraft, type Draft } from '../src/lib/portForwards';
import { createMock, type MockOptions } from '../src/mock/transport';
import { mount } from '../src/mount';

const PF: PortForwards = normalize('port_forwards', portForwardsJson);
const draft = (over: Partial<Draft> = {}): Draft => ({ ...emptyDraft(), destIp: '192.168.1.64', port: '32400', ...over });

describe('the contract answer', () => {
  it('normalizes the fixture unchanged', () => {
    expect(PF).toEqual(portForwardsJson);
  });

  it('turns a missing or odd answer into empty lists and nulls, never undefined', () => {
    const none = { rules: [], devices: [], cgnat: null, directActive: null, max: null };
    expect(normalize('port_forwards', {})).toEqual(none);
    expect(normalize('port_forwards', { rules: 'x', cgnat: 'yes', directActive: 'no' })).toEqual(none);
    expect(normalize('port_forwards', { directActive: false }).directActive).toBe(false);
  });

  it('keeps a rule without an id or a preset (the whole list goes back: a dropped rule would be deleted)', () => {
    const out = normalize('port_forwards', { rules: [{ destIp: '192.168.1.9', port: '27015', proto: 'udp', enabled: true }] });
    expect(out.rules).toHaveLength(1);
    expect(out.rules[0].id).toBeNull();
    expect(wireOf(out.rules[0])).toEqual({ preset: null, destIp: '192.168.1.9', port: '27015', proto: 'udp', direct: false, enabled: true });
  });

  it('drops a device without an address: nothing to point a rule at', () => {
    expect(normalize('port_forwards', { devices: [{ name: 'tv' }, { ip: '192.168.1.5' }] }).devices).toEqual([{ name: null, ip: '192.168.1.5' }]);
  });
});

describe('presets', () => {
  it('fill the port, the protocol and the VPN choice: consoles go around the VPN', () => {
    expect(PRESETS.map((p) => [p.id, p.port, p.proto, p.direct])).toEqual([
      ['minecraft', '25565', 'tcp', false],
      ['playstation', '3478-3480', 'both', true],
      ['xbox', '3074', 'both', true],
      ['rdp', '3389', 'tcp', false],
      ['plex', '32400', 'tcp', false],
      ['torrent', '51413', 'both', false],
    ]);
    expect(emptyDraft(presetOf('xbox'))).toEqual({ preset: 'xbox', destIp: '', port: '3074', proto: 'both', direct: true, enabled: true });
    for (const p of PRESETS) expect(parsePorts(p.port), p.id).not.toBeNull();
  });

  it('find a tag the UI knows; a newer or missing one is none', () => {
    expect(presetOf('plex')?.port).toBe('32400');
    expect(presetOf('valheim')).toBeUndefined();
    expect(presetOf(null)).toBeUndefined();
  });

  it('have a name in every language', () => {
    for (const lang of LANGS) {
      const t = makeT(lang);
      for (const k of ['pf.pre.rdp', 'pf.pre.torrent', 'pf.pre.custom'] as const) expect(t(k)).not.toBe(k);
    }
  });
});

describe('ports and addresses', () => {
  it('reads a port or a range, forgiving dashes and spaces', () => {
    expect(parsePorts('25565')).toEqual({ lo: 25565, hi: 25565 });
    expect(parsePorts(' 3478 - 3480 ')).toEqual({ lo: 3478, hi: 3480 });
    expect(parsePorts('3478–3480')).toEqual({ lo: 3478, hi: 3480 });
    for (const bad of ['', '0', '65536', '80-79', 'abc', '1-2-3', '-5', '99999']) expect(parsePorts(bad), bad).toBeNull();
    expect(portLabel('3478-3480')).toBe('3478–3480');
  });

  it('finds clashes only where a protocol and a port meet', () => {
    expect(overlaps({ proto: 'tcp', port: '3479' }, { proto: 'both', port: '3478-3480' })).toBe(true);
    expect(overlaps({ proto: 'udp', port: '25565' }, { proto: 'tcp', port: '25565' })).toBe(false);
    expect(overlaps({ proto: 'tcp', port: '3481' }, { proto: 'both', port: '3478-3480' })).toBe(false);
  });

  it('reads IPv4 strictly', () => {
    expect(ipv4('192.168.1.50')).toBe(0xc0a80132);
    for (const bad of ['192.168.1', '192.168.1.256', '192.168.01.5', 'fe80::1', '']) expect(ipv4(bad), bad).toBeNull();
  });
});

describe('the form’s checks', () => {
  it('has sensible defaults for one’s own port: TCP, through the VPN, on', () => {
    expect(emptyDraft()).toEqual({ preset: null, destIp: '', port: '', proto: 'tcp', direct: false, enabled: true });
  });

  it('passes a good rule', () => {
    expect(checkDraft(draft(), PF.rules, -1)).toEqual({});
  });

  it('names each field’s mistake', () => {
    expect(checkDraft(draft({ destIp: '' }), PF.rules, -1).destIp).toBe('pf.e.device');
    expect(checkDraft(draft({ destIp: '192.168.1' }), PF.rules, -1).destIp).toBe('pf.e.ip');
    expect(checkDraft(draft({ port: '' }), PF.rules, -1).port).toBe('w.req');
    expect(checkDraft(draft({ port: '70000' }), PF.rules, -1).port).toBe('pf.e.port');
  });

  it('refuses a port an enabled rule already takes, but not the rule being edited, nor a rule switched off', () => {
    expect(checkDraft(draft({ port: '25565' }), PF.rules, -1).port).toBe('a.port_conflict');
    expect(checkDraft(draft({ port: '25565', proto: 'udp' }), PF.rules, -1).port).toBeUndefined();
    expect(checkDraft(draftOf(PF.rules[0]), PF.rules, 0)).toEqual({});
    expect(checkDraft(draft({ port: '25565', enabled: false }), PF.rules, -1)).toEqual({});
  });
});

describe('the wire', () => {
  it('sends the form canonical, with its preset', () => {
    expect(wireOfDraft(draft({ destIp: ' 192.168.1.64 ', port: '3478 – 3480', proto: 'both', direct: true, preset: 'playstation' }), null)).toEqual({
      preset: 'playstation',
      destIp: '192.168.1.64',
      port: '3478-3480',
      proto: 'both',
      direct: true,
      enabled: true,
    });
    expect(wireOfDraft(draftOf(PF.rules[1]), PF.rules[1].id)).toMatchObject({ id: 'b7d204aa', preset: 'playstation' });
  });

  it('sends saved rules back unchanged, with their ids and presets', () => {
    expect(PF.rules.map(wireOf)).toEqual([
      { id: '3fa1c09e', preset: 'minecraft', destIp: '192.168.1.50', port: '25565', proto: 'tcp', direct: false, enabled: true },
      { id: 'b7d204aa', preset: 'playstation', destIp: '192.168.1.60', port: '3478-3480', proto: 'both', direct: true, enabled: true },
    ]);
  });

  it('knows a pending save has landed by the rules, ids aside', () => {
    const sent = PF.rules.map(wireOf).map(({ id: _id, ...r }) => r);
    expect(landed(sent, PF)).toBe(true);
    expect(landed(sent.slice(1), PF)).toBe(false);
    expect(landed([{ ...sent[0], enabled: false }, sent[1]], PF)).toBe(false);
    expect(landed(sent, null)).toBe(false);
  });
});

describe('the router’s refusals', () => {
  it('has short words for every code set_port_forwards refuses with, in every language', () => {
    const FAIL_CODES = Object.keys(FAIL);
    expect(FAIL_CODES.sort()).toEqual(['apply_failed', 'busy', 'dest_is_router', 'dest_not_lan', 'failed', 'invalid_params', 'locked', 'port_conflict', 'too_many']);
    for (const lang of LANGS) {
      const t = makeT(lang);
      for (const code of FAIL_CODES) {
        const key = failKey(code)!;
        expect(key, code).not.toBeNull();
        expect(t(key), `${lang}/${code}`).not.toBe(key);
        expect(t(key), `${lang}/${code}`).not.toMatch(/\{\w+\}/);
      }
      expect(t('a.port_forwards_set')).not.toBe('a.port_forwards_set');
      expect(t('a.pending')).not.toBe('a.pending');
    }
  });

  it('leaves a code it does not know to the generic words', () => {
    expect(failKey('teleported')).toBeNull();
    expect(failKey('__proto__')).toBeNull();
    expect(failKey('pending')).toBeNull();
  });

  it('writes every port-forwarding sentence in all three languages', () => {
    const keys = Object.keys(S).filter((k) => k.startsWith('pf.') || k === 'tab.ports');
    expect(keys.length).toBeGreaterThan(30);
    for (const k of keys) for (const s of S[k as keyof typeof S]) expect(s.trim(), k).not.toBe('');
  });
});

// ── the screen ──────────────────────────────────────────────────────────────

let cleanup: (() => void)[] = [];
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('vectra.ui.mode', 'pro');
  localStorage.setItem('vectra.ui.tab', 'ports');
});
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  document.body.innerHTML = '';
});

async function settle(rounds = 10) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(opts: MockOptions & { lang?: string; call?: CallFn } = {}) {
  localStorage.setItem('vectra.ui.lang', opts.lang ?? 'en');
  const mock = createMock({ latencyMs: 0, live: false, applyMs: 10, ...opts });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const base = opts.call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: (m, p) => (calls.push([m, p]), base(m, p)), lang: 'en' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  const button = (label: string) => all('button').find((b) => b.textContent?.trim() === label);
  const sent = (m: string) => calls.filter(([x]) => x === m).map(([, p]) => p);
  const type = (sel: string, v: string) => {
    const el = $<HTMLInputElement>(sel)!;
    el.value = v;
    el.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const pick = (ip: string) => {
    const el = $<HTMLSelectElement>('#vx-pf-dev')!;
    el.value = ip;
    el.dispatchEvent(new Event('change', { bubbles: true }));
  };
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  return { root, $, all, button, sent, type, pick, confirm };
}

describe('the port forwarding tab', () => {
  const open = (app: ReturnType<typeof start>) => app.all('.pf-add')[0].click();
  const tile = (app: ReturnType<typeof start>, name: string) => app.all('.pf-tile').find((b) => b.querySelector('.pf-t')?.textContent === name)!.click();
  const device = (app: ReturnType<typeof start>, name: string) => app.all('.pf-devs .pf-ch').find((b) => b.querySelector('.pf-t')?.textContent === name)!.click();
  const title = (app: ReturnType<typeof start>) => app.$('.pf-sheet h2')?.textContent;

  it('lists each rule by what it opens, the device under it, and a switch', async () => {
    const app = start();
    await settle();
    const rows = app.all('.pf-r');
    expect(rows.map((r) => [r.querySelector('.pf-main .pf-t')?.textContent, r.querySelector('.pf-main .pf-s')?.textContent])).toEqual([
      ['Minecraft', 'gaming-pc'],
      ['PlayStation', '192.168.1.60 · around VPN'],
    ]);
    expect(rows.map((r) => r.querySelector('[role="switch"]')?.getAttribute('aria-checked'))).toEqual(['true', 'true']);
    // No section title repeating the tab, no warning on a white IP.
    expect(app.$('.pf h3')).toBeNull();
    expect(app.$('.pf .note')).toBeNull();
  });

  it('names a rule without a known preset by its port', async () => {
    const mock = createMock({ latencyMs: 0, live: false });
    cleanup.push(() => mock.dispose());
    const app = start({
      call: (m, p) =>
        m === 'port_forwards'
          ? Promise.resolve({ rules: [{ id: 'aa', preset: 'valheim', destIp: '192.168.1.50', deviceName: 'gaming-pc', port: '2456-2458', proto: 'udp', direct: false, enabled: true }], devices: [], cgnat: false, max: 32 })
          : mock.call(m, p),
    });
    await settle();
    expect(app.$('.pf-main .pf-t')?.textContent).toBe('Port 2456–2458');
  });

  it('says once that «around VPN» is not in effect, only when the router says so', async () => {
    const mock = createMock({ latencyMs: 0, live: false });
    cleanup.push(() => mock.dispose());
    let active: boolean | null = false;
    const app = start({
      lang: 'ru',
      call: (m, p) => (m === 'port_forwards' ? (mock.call(m, p) as Promise<Record<string, unknown>>).then((x) => ({ ...x, directActive: active })) : mock.call(m, p)),
    });
    await settle();
    expect(app.all('.pf .hint').map((h) => h.textContent)).toEqual(['Режим «мимо VPN» сейчас не действует — устройства идут через VPN.']);
    cleanup.forEach((fn) => fn());
    cleanup = [];
    document.body.innerHTML = '';
    active = null;
    const again = start({ call: (m, p) => (m === 'port_forwards' ? (mock.call(m, p) as Promise<Record<string, unknown>>).then((x) => ({ ...x, directActive: active })) : mock.call(m, p)) });
    await settle();
    expect(again.all('.pf .hint')).toEqual([]);
  });

  it('behind the provider’s CGNAT: one warning line, then a friendly empty state with the way to start', async () => {
    const app = start({ scenario: 'cgnat', lang: 'ru' });
    await settle();
    expect(app.$('.pf .note')?.textContent).toBe('Провайдер не дал белый IP — снаружи порты не откроются.');
    expect(app.$('.pf-none p')?.textContent).toBe('Откройте порт для игры, сервера или удалённого доступа к компьютеру.');
    expect(app.$('.pf-none .pf-add')?.textContent).toBe('Открыть порт');
  });

  it('switches a rule off by sending the whole list', async () => {
    const app = start();
    await settle();
    app.all('.pf-sw')[1].click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([
      {
        rules: [
          { id: '3fa1c09e', preset: 'minecraft', destIp: '192.168.1.50', port: '25565', proto: 'tcp', direct: false, enabled: true },
          { id: 'b7d204aa', preset: 'playstation', destIp: '192.168.1.60', port: '3478-3480', proto: 'both', direct: true, enabled: false },
        ],
      },
    ]);
    expect(app.all('.pf-r')[1].classList.contains('off')).toBe(true);
  });

  it('adds a rule as a short wizard: what, which device, the VPN — the preset fills the rest', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    expect(title(app)).toBe('What to open?');
    expect(app.all('.pf-tile .pf-t').map((x) => x.textContent)).toEqual(['Minecraft', 'PlayStation', 'Xbox', 'Remote Desktop', 'Plex', 'Torrent', 'Custom port']);
    tile(app, 'Xbox');
    await settle();
    expect(title(app)).toBe('For which device?');
    expect(app.all('.pf-devs .pf-t').map((x) => x.textContent)).toEqual(['gaming-pc', 'MacBook-Air', 'synology-nas', '192.168.1.77']);
    device(app, 'gaming-pc');
    await settle();
    expect(title(app)).toBe('Xbox');
    // The port stays one tap away, not a field in the way; a console goes around the VPN.
    expect(app.$('#vx-pf-port')).toBeNull();
    expect(app.$('.pf-line-s')?.textContent).toBe('Port 3074 · TCP+UDP' + 'change');
    expect(app.$('.pf-opt[aria-checked="true"] .pf-t')?.textContent).toBe('Around VPN');
    app.button('Save')!.click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([{ rules: [{ preset: 'xbox', destIp: '192.168.1.50', port: '3074', proto: 'both', direct: true, enabled: true }] }]);
    expect(app.$('.pf-sheet')).toBeNull();
    expect(app.$('.pf-main .pf-t')?.textContent).toBe('Xbox');
  });

  it('goes back a step, and lets the preset’s port be changed', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    tile(app, 'Minecraft');
    await settle();
    app.$<HTMLButtonElement>('.pf-sheet-h button[aria-label="Back"]')!.click();
    await settle();
    expect(title(app)).toBe('What to open?');
    tile(app, 'Minecraft');
    await settle();
    device(app, 'MacBook-Air');
    await settle();
    expect(app.$('.pf-opt[aria-checked="true"] .pf-t')?.textContent).toBe('Through VPN');
    app.$<HTMLButtonElement>('.pf-line-s')!.click();
    await settle();
    app.type('#vx-pf-port', '25566');
    await settle();
    app.button('Save')!.click();
    await settle(20);
    expect(app.sent('set_port_forwards')[0]).toEqual({ rules: [{ preset: 'minecraft', destIp: '192.168.1.64', port: '25566', proto: 'tcp', direct: false, enabled: true }] });
  });

  it('takes one’s own port and a typed address', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    tile(app, 'Custom port');
    await settle();
    expect(title(app)).toBe('Which port?');
    app.button('Next')!.click();
    await settle();
    expect(app.$('#vx-pf-port-h')?.textContent).toBe('Fill in this field.');
    app.type('#vx-pf-port', '8080');
    app.button('UDP')!.click();
    await settle();
    app.button('Next')!.click();
    await settle();
    app.button('Another IP…')!.click();
    await settle();
    app.type('#vx-pf-ip', '192.168.1.99');
    await settle();
    app.button('Next')!.click();
    await settle();
    expect(title(app)).toBe('Port 8080');
    app.button('Save')!.click();
    await settle(20);
    expect(app.sent('set_port_forwards')[0]).toEqual({ rules: [{ preset: null, destIp: '192.168.1.99', port: '8080', proto: 'udp', direct: false, enabled: true }] });
  });

  it('checks the port before it asks the router', async () => {
    const app = start();
    await settle();
    open(app);
    await settle();
    tile(app, 'Minecraft');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    app.button('Save')!.click();
    await settle();
    // Minecraft's port is taken by the saved Minecraft rule: said where the port is.
    expect(app.$('#vx-pf-port-h')?.textContent).toBe('This port is already taken.');
    expect(app.sent('set_port_forwards')).toEqual([]);
  });

  it('shows the router’s refusal beside what it is about, and keeps the sheet', async () => {
    const app = start({ pfFail: 'dest_not_lan' });
    await settle();
    open(app);
    await settle();
    tile(app, 'Plex');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    app.button('Save')!.click();
    await settle(20);
    expect(app.$('.pf-sheet')).not.toBeNull();
    expect(app.$('.pf-sum .fld-err')?.textContent).toBe('This address is not on the home network.');
    // Said once, on the sheet: no toast repeats it.
    expect(app.$('.toast')).toBeNull();
  });

  it('says a refusal about no field under the form', async () => {
    const app = start({ pfFail: 'too_many' });
    await settle();
    app.all('.pf-main')[0].click();
    await settle();
    app.button('Save')!.click();
    await settle(20);
    expect(app.$('.pf-sheet .note')?.textContent).toContain('Too many rules.');
  });

  it('opens a saved rule on its summary, and removes it after asking', async () => {
    const app = start();
    await settle();
    app.all('.pf-main')[0].click();
    await settle();
    expect(title(app)).toBe('Minecraft');
    expect(app.all('.pf-line .pf-t').map((x) => x.textContent)).toEqual(['gaming-pc']);
    app.button('Remove')!.click();
    await settle();
    expect(app.$('[role="alertdialog"] h2')?.textContent).toBe('Remove the rule?');
    app.confirm();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([
      { rules: [{ id: 'b7d204aa', preset: 'playstation', destIp: '192.168.1.60', port: '3478-3480', proto: 'both', direct: true, enabled: true }] },
    ]);
    expect(app.$('.pf-sheet')).toBeNull();
    expect(app.all('.pf-r')).toHaveLength(1);
  });

  it('waits for a pending save to show in the list', async () => {
    const app = start({ pfPending: true });
    await settle();
    app.all('.pf-sw')[0].click();
    await settle(10);
    await new Promise((r) => setTimeout(r, 2300));
    await settle(20);
    expect(app.all('.pf-sw')[0].getAttribute('aria-checked')).toBe('false');
    expect(app.all('.toast p').map((p) => p.textContent)).toContain('Port forwarding saved');
  });

  it('on a router without port forwarding, says it needs an update', async () => {
    const mock = createMock({ latencyMs: 0, live: false });
    cleanup.push(() => mock.dispose());
    const app = start({
      call: (m, p) => (m === 'port_forwards' ? Promise.reject(new Error('RPC call to vectra/port_forwards failed with ubus code 3: Method not found')) : mock.call(m, p)),
    });
    await settle();
    expect(app.$('[role="tabpanel"] .note')?.textContent).toBe('Vectra on the router needs an update for this.');
    expect(app.$('[role="tabpanel"] .err')).toBeNull();
  });

  it('is refused under the operator’s lock, as the router does', async () => {
    const mock = createMock({ latencyMs: 0, live: false, locked: true });
    cleanup.push(() => mock.dispose());
    expect(await mock.call('port_forwards', {})).toEqual({ ok: false, code: 'locked', detail: null });
    expect(await mock.call('set_port_forwards', { rules: [] })).toEqual({ ok: false, code: 'locked', detail: null });
  });

  it('renders the list and every wizard step in every language without a stray null', async () => {
    for (const lang of LANGS) {
      const app = start({ lang });
      await settle();
      const texts = [app.root.textContent ?? ''];
      open(app);
      await settle();
      texts.push(app.root.textContent ?? '');
      app.all('.pf-tile')[0].click();
      await settle();
      texts.push(app.root.textContent ?? '');
      app.all('.pf-devs .pf-ch')[1].click();
      await settle();
      texts.push(app.root.textContent ?? '');
      for (const x of texts) expect(x, lang).not.toMatch(/undefined|NaN|\bnull\b|pf\.\w/);
      cleanup.forEach((fn) => fn());
      cleanup = [];
      document.body.innerHTML = '';
    }
  });
});
