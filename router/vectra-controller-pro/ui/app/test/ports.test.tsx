// Port forwarding (Pro): the contract's answer, the form's checks, every
// refusal in short words, and the screen — the whole list goes back on every
// change.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import portForwardsJson from '../../contract/port_forwards.json';
import { normalize } from '../src/api/normalize';
import type { CallFn, PortForward, PortForwardIn, PortForwards } from '../src/api/types';
import { LANGS, makeT, type Key } from '../src/i18n';
import { S } from '../src/i18n/strings';
import {
  checkDraft,
  clean,
  draftOf,
  emptyDraft,
  FAIL,
  failKey,
  groups,
  ipv4,
  landed,
  overlaps,
  parsePorts,
  portLabel,
  portText,
  PROTOS,
  switched,
  wireOf,
  withDraft,
  without,
  type Draft,
} from '../src/lib/portForwards';
import { CATS, POPULAR, presetOf, PRESETS } from '../src/lib/presets';
import { createMock, type MockOptions } from '../src/mock/transport';
import { mount } from '../src/mount';

const PF: PortForwards = normalize('port_forwards', portForwardsJson);
const draft = (over: Partial<Draft> = {}): Draft => ({ ...emptyDraft(), destIp: '192.168.1.64', rules: [{ port: '32400', proto: 'tcp' }], ...over });
const rule = (over: Partial<PortForward>): PortForward => ({ id: null, preset: null, destIp: '192.168.1.50', deviceName: null, port: '1', proto: 'tcp', direct: false, enabled: true, ...over });

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

  it('tags the fixture’s rules with ids the catalogue knows', () => {
    for (const r of PF.rules) expect(presetOf(r.preset), String(r.preset)).toBeDefined();
  });
});

describe('the preset catalogue', () => {
  // The research the catalogue is built from: every ```json block of the spec.
  const doc = readFileSync(resolve(__dirname, '../../../docs/superpowers/specs/2026-10-06-port-presets-games-research.md'), 'utf8');
  const research = Array.from(doc.matchAll(/```json\n([\s\S]*?)```/g)).flatMap(
    (m) => JSON.parse(m[1]) as { id: string; rules: { port: string; proto: string }[]; note?: string }[],
  );

  it('has every preset of the research, with its rules; where the makers do not say the protocol, none is assumed', () => {
    expect(research.length).toBe(35);
    expect(PRESETS.map((p) => p.id).sort()).toEqual(research.map((r) => r.id).sort());
    for (const r of research) {
      const unstated = /protocol unstated/.test(r.note ?? '');
      expect(presetOf(r.id)!.rules, r.id).toEqual(r.rules.map((x) => ({ port: x.port, proto: unstated ? null : x.proto })));
    }
    expect(PRESETS.filter((p) => p.rules.some((r) => !r.proto)).map((p) => p.id)).toEqual(['minecraft-java', 'minecraft-bedrock', 'valheim', 'enshrouded']);
  });

  it('has tags the router keeps: unique, 1–24 of a-z, 0-9, "-"', () => {
    const ids = PRESETS.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const id of ids) expect(id).toMatch(/^[a-z0-9-]{1,24}$/);
  });

  it('has ports and protocols the router takes, written as the router writes them, never twice in one preset', () => {
    for (const p of PRESETS) {
      expect(p.rules.length, p.id).toBeGreaterThan(0);
      for (const r of p.rules) {
        const span = parsePorts(r.port);
        expect(span, p.id).not.toBeNull();
        expect(portText(span!), p.id).toBe(r.port);
        if (r.proto !== null) expect(PROTOS, p.id).toContain(r.proto);
      }
      for (let i = 0; i < p.rules.length; i++) for (let k = i + 1; k < p.rules.length; k++) expect(overlaps(p.rules[i], p.rules[k]), p.id).toBe(false);
    }
  });

  it('files every preset under a category, and every category has some; consoles alone go around the VPN', () => {
    for (const c of CATS) expect(PRESETS.filter((p) => p.cat === c).length, c).toBeGreaterThan(0);
    for (const p of PRESETS) expect(CATS, p.id).toContain(p.cat);
    expect(PRESETS.filter((p) => p.direct).map((p) => p.id)).toEqual(['playstation', 'xbox']);
    expect(PRESETS.filter((p) => p.cat === 'consoles').map((p) => p.id)).toEqual(['playstation', 'xbox']);
  });

  it('puts a short set of known presets on the first screen', () => {
    expect(POPULAR.length).toBeGreaterThanOrEqual(6);
    expect(POPULAR.length).toBeLessThanOrEqual(8);
    for (const id of POPULAR) expect(presetOf(id), id).toBeDefined();
  });

  it('has a name and a category in every language', () => {
    for (const lang of LANGS) {
      const t = makeT(lang);
      for (const p of PRESETS) {
        if (p.name === null) expect(t(('pf.pre.' + p.id) as Key), `${lang}/${p.id}`).not.toBe('pf.pre.' + p.id);
        else expect(p.name.trim(), p.id).not.toBe('');
      }
      for (const k of ['pf.cat.all', ...CATS.map((c) => 'pf.cat.' + c), 'pf.pre.custom', 'pf.q.all', 'pf.all.d', 'pf.find', 'pf.none', 'pf.proto.q', 'pf.line.ports'])
        expect(t(k as Key), `${lang}/${k}`).not.toBe(k);
    }
    expect(PRESETS.filter((p) => p.name === null).map((p) => p.id)).toEqual(['rdp', 'transmission', 'web-server']);
    expect(presetOf('rust')?.name).toBe('Rust');
    expect(presetOf('ark-survival-evolved')?.name).toBe('ARK: Survival Evolved');
  });

  it('finds a tag the UI knows; a newer or missing one is none', () => {
    expect(presetOf('plex')?.rules).toEqual([{ port: '32400', proto: 'tcp' }]);
    expect(presetOf('minecraft')).toBeUndefined();
    expect(presetOf('quake3')).toBeUndefined();
    expect(presetOf(null)).toBeUndefined();
  });
});

describe('the list’s lines', () => {
  it('makes one line of a known preset’s rules for one device; anything else stands alone', () => {
    const rules = [
      rule({ preset: 'rust', port: '28015', proto: 'udp' }),
      rule({ preset: null, port: '8080' }),
      rule({ preset: 'rust', port: '28017', proto: 'udp' }),
      rule({ preset: 'rust', destIp: '192.168.1.64', port: '28015', proto: 'udp' }),
      rule({ preset: 'quake3', port: '27960' }),
      rule({ preset: 'quake3', port: '27961' }),
      rule({ preset: null, port: '8081' }),
    ];
    expect(groups(rules)).toEqual([[0, 2], [1], [3], [4], [5], [6]]);
    expect(groups(PF.rules)).toEqual([[0], [1]]);
    expect(groups([])).toEqual([]);
  });

  it('switches and removes a line as a whole', () => {
    const rules = [rule({ id: 'aaaaaaaa', preset: 'rust', port: '28015', proto: 'udp' }), rule({ id: 'bbbbbbbb', port: '80' }), rule({ id: 'cccccccc', preset: 'rust', port: '28017', proto: 'udp' })];
    expect(switched(rules, [0, 2], false).map((r) => [r.id, r.enabled])).toEqual([
      ['aaaaaaaa', false],
      ['bbbbbbbb', true],
      ['cccccccc', false],
    ]);
    expect(without(rules, [0, 2]).map((r) => r.id)).toEqual(['bbbbbbbb']);
  });
});

describe('a preset as a form', () => {
  it('fills every rule, the device left to pick; consoles go around the VPN', () => {
    expect(emptyDraft(presetOf('xbox'))).toEqual({
      preset: 'xbox',
      destIp: '',
      rules: [
        { port: '3074', proto: 'both' },
        { port: '9002', proto: 'udp' },
      ],
      direct: true,
      enabled: true,
    });
    expect(emptyDraft(presetOf('rust')).direct).toBe(false);
    // The catalogue is not the form's to change.
    const d = emptyDraft(presetOf('rust'));
    d.rules[0].port = '1';
    expect(presetOf('rust')!.rules[0].port).toBe('28015');
  });

  it('leaves an unstated protocol unpicked, and will not save until it is', () => {
    const d = { ...emptyDraft(presetOf('minecraft-java')), destIp: '192.168.1.64' };
    expect(d.rules).toEqual([{ port: '25565', proto: null }]);
    const e = checkDraft(d, [], []);
    expect(e.proto).toBe(true);
    expect(clean(e)).toBe(false);
    expect(clean(checkDraft({ ...d, rules: [{ port: '25565', proto: 'udp' }] }, [], []))).toBe(true);
  });

  it('reads a saved line as the form: its own ports, not the catalogue’s', () => {
    const rules = [rule({ preset: 'rust', port: '28015', proto: 'udp', enabled: false }), rule({ preset: 'rust', port: '28099', proto: 'udp', direct: true })];
    expect(draftOf(rules, [0, 1])).toEqual({
      preset: 'rust',
      destIp: '192.168.1.50',
      rules: [
        { port: '28015', proto: 'udp' },
        { port: '28099', proto: 'udp' },
      ],
      direct: false,
      enabled: true,
    });
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
    expect(emptyDraft()).toEqual({ preset: null, destIp: '', rules: [{ port: '', proto: 'tcp' }], direct: false, enabled: true });
  });

  it('passes a good rule', () => {
    expect(clean(checkDraft(draft(), PF.rules, []))).toBe(true);
  });

  it('names each field’s mistake', () => {
    expect(checkDraft(draft({ destIp: '' }), PF.rules, []).destIp).toBe('pf.e.device');
    expect(checkDraft(draft({ destIp: '192.168.1' }), PF.rules, []).destIp).toBe('pf.e.ip');
    expect(checkDraft(draft({ rules: [{ port: '', proto: 'tcp' }] }), PF.rules, []).port).toEqual(['w.req']);
    expect(checkDraft(draft({ rules: [{ port: '80', proto: 'tcp' }, { port: '70000', proto: 'tcp' }] }), PF.rules, []).port).toEqual([undefined, 'pf.e.port']);
  });

  it('refuses a port an enabled rule already takes, but not the line being edited, nor a rule switched off', () => {
    expect(checkDraft(draft({ rules: [{ port: '25565', proto: 'tcp' }] }), PF.rules, []).port).toEqual(['a.port_conflict']);
    expect(checkDraft(draft({ rules: [{ port: '25565', proto: 'udp' }] }), PF.rules, []).port).toEqual([undefined]);
    expect(clean(checkDraft(draftOf(PF.rules, [0]), PF.rules, [0]))).toBe(true);
    expect(clean(checkDraft(draft({ rules: [{ port: '25565', proto: 'tcp' }], enabled: false }), PF.rules, []))).toBe(true);
  });

  it('checks every rule of a preset, against the list and against each other, before it asks the router', () => {
    // PlayStation for another device: the saved PlayStation (3478–3480, both) takes all of its ports.
    expect(checkDraft({ ...emptyDraft(presetOf('playstation')), destIp: '192.168.1.64' }, PF.rules, []).port).toEqual(['a.port_conflict', 'a.port_conflict']);
    expect(checkDraft(draft({ rules: [{ port: '7000-7010', proto: 'udp' }, { port: '7005', proto: 'both' }] }), [], []).port).toEqual([undefined, 'a.port_conflict']);
  });
});

describe('the wire', () => {
  it('adds a preset of several rules as several rules: one tag, one device, canonical ports', () => {
    const d = { ...emptyDraft(presetOf('rust')), destIp: ' 192.168.1.64 ' };
    d.rules[1].port = '28017 – 28018';
    expect(withDraft(PF.rules, [], d)).toEqual([
      ...PF.rules.map(wireOf),
      { preset: 'rust', destIp: '192.168.1.64', port: '28015', proto: 'udp', direct: false, enabled: true },
      { preset: 'rust', destIp: '192.168.1.64', port: '28017-28018', proto: 'udp', direct: false, enabled: true },
    ]);
  });

  it('replaces a line in place: its rules keep their ids in order, a rule the form dropped goes', () => {
    const rules = [
      rule({ id: 'aaaaaaaa', preset: 'rust', port: '28015', proto: 'udp' }),
      rule({ id: 'bbbbbbbb', port: '80' }),
      rule({ id: 'cccccccc', preset: 'rust', port: '28017', proto: 'udp' }),
    ];
    const two = { ...draftOf(rules, [0, 2]), direct: true };
    expect(withDraft(rules, [0, 2], two).map((r) => [r.id, r.port, r.direct])).toEqual([
      ['aaaaaaaa', '28015', true],
      ['cccccccc', '28017', true],
      ['bbbbbbbb', '80', false],
    ]);
    const one = { ...emptyDraft(presetOf('plex')), destIp: '192.168.1.50' };
    expect(withDraft(rules, [0, 2], one)).toEqual([
      { id: 'aaaaaaaa', preset: 'plex', destIp: '192.168.1.50', port: '32400', proto: 'tcp', direct: false, enabled: true },
      wireOf(rules[1]),
    ]);
    const three = { ...emptyDraft(presetOf('hikvision')), destIp: '192.168.1.50' };
    expect(withDraft([rules[1]], [0], three).map((r) => [r.id ?? null, r.port])).toEqual([
      ['bbbbbbbb', '80'],
      [null, '8000'],
      [null, '554'],
    ]);
  });

  it('sends saved rules back unchanged, with their ids and presets', () => {
    expect(PF.rules.map(wireOf)).toEqual([
      { id: '3fa1c09e', preset: 'minecraft-java', destIp: '192.168.1.50', port: '25565', proto: 'tcp', direct: false, enabled: true },
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
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  return { root, $, all, button, sent, type, confirm };
}

type App = ReturnType<typeof start>;

/** A router whose list starts as `rules`: the mock judges each save, this list keeps it (ids kept, new ones made). */
function withList(rules: PortForward[], more: MockOptions = {}) {
  const mock = createMock({ latencyMs: 0, live: false, applyMs: 10, ...more });
  cleanup.push(() => mock.dispose());
  let list = rules;
  let made = 0;
  const call: CallFn = async (m, p) => {
    if (m === 'port_forwards') return { ...((await mock.call(m, p)) as PortForwards), rules: list };
    const out = await mock.call(m, p);
    if (m === 'set_port_forwards' && (out as { ok?: boolean }).ok) {
      list = (p!.rules as PortForwardIn[]).map((r) => ({ ...r, id: r.id ?? 'new' + made++, deviceName: list.find((x) => x.destIp === r.destIp)?.deviceName ?? null }));
    }
    return out;
  };
  return start({ ...more, call });
}

const RUST = [
  rule({ id: 'aaaaaaaa', preset: 'rust', port: '28015', proto: 'udp', deviceName: 'gaming-pc' }),
  rule({ id: 'bbbbbbbb', preset: null, port: '8080', destIp: '192.168.1.64', deviceName: 'MacBook-Air' }),
  rule({ id: 'cccccccc', preset: 'rust', port: '28017', proto: 'udp', deviceName: 'gaming-pc' }),
];

describe('the port forwarding tab', () => {
  const open = (app: App) => app.all('.pf-add')[0].click();
  const tile = (app: App, name: string) => app.all('.pf-tile').find((b) => b.querySelector('.pf-t')?.textContent === name)!.click();
  const choice = (app: App, name: string) => app.all('.pf-body .pf-ch').find((b) => b.querySelector('.pf-t')?.textContent === name)!.click();
  const device = (app: App, name: string) => app.all('.pf-devs .pf-ch').find((b) => b.querySelector('.pf-t')?.textContent === name)!.click();
  const title = (app: App) => app.$('.pf-sheet h2')?.textContent;
  const back = (app: App) => app.$<HTMLButtonElement>('.pf-sheet-h button[aria-label="Back"]')!.click();
  const listed = (app: App) => app.all('.pf-body .pf-sec .pf-ch .pf-t').map((x) => x.textContent);
  const save = (app: App) => app.$<HTMLButtonElement>('.pf-sheet-a .bp')!;
  const iconOfRow = (row: HTMLElement) => row.querySelector('.pf-ic path')?.getAttribute('d');

  it('lists each rule by what it opens, the device under it, and a switch', async () => {
    const app = start();
    await settle();
    const rows = app.all('.pf-r');
    expect(rows.map((r) => [r.querySelector('.pf-main .pf-t')?.textContent, r.querySelector('.pf-main .pf-s')?.textContent])).toEqual([
      ['Minecraft Java', 'gaming-pc'],
      ['PlayStation', '192.168.1.60 · around VPN'],
    ]);
    expect(rows.map((r) => r.querySelector('[role="switch"]')?.getAttribute('aria-checked'))).toEqual(['true', 'true']);
    // No section title repeating the tab, no warning on a white IP.
    expect(app.$('.pf h3')).toBeNull();
    expect(app.$('.pf .note')).toBeNull();
  });

  it('names a rule with a tag this UI does not know by its port, with the generic icon', async () => {
    const app = withList([
      rule({ id: 'aaaaaaaa', preset: 'seven-days', port: '26900-26902', proto: 'udp', deviceName: 'gaming-pc' }),
      rule({ id: 'bbbbbbbb', preset: 'seven-days', port: '26903', proto: 'udp', deviceName: 'gaming-pc' }),
      rule({ id: 'cccccccc', preset: null, port: '8080', deviceName: 'gaming-pc' }),
    ]);
    await settle();
    const rows = app.all('.pf-r');
    expect(rows.map((r) => r.querySelector('.pf-main .pf-t')?.textContent)).toEqual(['Port 26900–26902', 'Port 26903', 'Port 8080']);
    expect(iconOfRow(rows[0])).toBe(iconOfRow(rows[2]));
    // Opened, it is still that rule, its tag kept.
    rows[0].querySelector<HTMLElement>('.pf-main')!.click();
    await settle();
    expect(title(app)).toBe('Port 26900–26902');
    app.button('Save')!.click();
    await settle(20);
    expect((app.sent('set_port_forwards').at(-1) as { rules: unknown[] }).rules[0]).toEqual({ id: 'aaaaaaaa', preset: 'seven-days', destIp: '192.168.1.50', port: '26900-26902', proto: 'udp', direct: false, enabled: true });
  });

  it('shows a preset’s rules for one device as one line, switched and removed together', async () => {
    const app = withList(RUST);
    await settle();
    expect(app.all('.pf-r .pf-main .pf-t').map((x) => x.textContent)).toEqual(['Rust', 'Port 8080']);
    app.all('.pf-sw')[0].click();
    await settle(20);
    expect((app.sent('set_port_forwards').at(-1) as { rules: { id: string; enabled: boolean }[] }).rules.map((r) => [r.id, r.enabled])).toEqual([
      ['aaaaaaaa', false],
      ['bbbbbbbb', true],
      ['cccccccc', false],
    ]);
    expect(app.all('.pf-r')[0].classList.contains('off')).toBe(true);
    app.all('.pf-main')[0].click();
    await settle();
    app.button('Remove')!.click();
    await settle();
    app.confirm();
    await settle(20);
    expect((app.sent('set_port_forwards').at(-1) as { rules: { id: string }[] }).rules.map((r) => r.id)).toEqual(['bbbbbbbb']);
    expect(app.all('.pf-r .pf-main .pf-t').map((x) => x.textContent)).toEqual(['Port 8080']);
  });

  it('edits a line as a whole: its ports, one row each, and keeps the ids', async () => {
    const app = withList(RUST);
    await settle();
    app.all('.pf-main')[0].click();
    await settle();
    expect(title(app)).toBe('Rust');
    expect(app.$('.pf-line-s')?.textContent).toBe('Ports 28015 · UDP, 28017 · UDP' + 'change');
    app.$<HTMLButtonElement>('.pf-line-s')!.click();
    await settle();
    expect(app.all('.pf-pr label').map((l) => l.textContent)).toEqual(['Port 1', 'Port 2']);
    app.type('#vx-pf-port-1', '28018');
    await settle();
    app.button('Save')!.click();
    await settle(20);
    expect((app.sent('set_port_forwards').at(-1) as { rules: unknown[] }).rules).toEqual([
      { id: 'aaaaaaaa', preset: 'rust', destIp: '192.168.1.50', port: '28015', proto: 'udp', direct: false, enabled: true },
      { id: 'cccccccc', preset: 'rust', destIp: '192.168.1.50', port: '28018', proto: 'udp', direct: false, enabled: true },
      { id: 'bbbbbbbb', preset: null, destIp: '192.168.1.64', port: '8080', proto: 'tcp', direct: false, enabled: true },
    ]);
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
          { id: '3fa1c09e', preset: 'minecraft-java', destIp: '192.168.1.50', port: '25565', proto: 'tcp', direct: false, enabled: true },
          { id: 'b7d204aa', preset: 'playstation', destIp: '192.168.1.60', port: '3478-3480', proto: 'both', direct: true, enabled: false },
        ],
      },
    ]);
    expect(app.all('.pf-r')[1].classList.contains('off')).toBe(true);
  });

  it('adds a preset of several rules as a short wizard: what, which device, the VPN — the preset fills the rest', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    expect(title(app)).toBe('What to open?');
    expect(app.all('.pf-tile .pf-t').map((x) => x.textContent)).toEqual(['Minecraft Java', 'PlayStation', 'Xbox', 'Remote Desktop', 'Plex', 'Torrent', 'All presets', 'Custom port']);
    tile(app, 'Xbox');
    await settle();
    expect(title(app)).toBe('For which device?');
    expect(app.all('.pf-devs .pf-t').map((x) => x.textContent)).toEqual(['gaming-pc', 'MacBook-Air', 'synology-nas', '192.168.1.77']);
    device(app, 'gaming-pc');
    await settle();
    expect(title(app)).toBe('Xbox');
    // The ports stay one tap away, not fields in the way; a console goes around the VPN.
    expect(app.$('#vx-pf-port')).toBeNull();
    expect(app.$('.pf-line-s')?.textContent).toBe('Ports 3074 · TCP+UDP, 9002 · UDP' + 'change');
    expect(app.$('.pf-opt[aria-checked="true"] .pf-t')?.textContent).toBe('Around VPN');
    app.button('Save')!.click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([
      {
        rules: [
          { preset: 'xbox', destIp: '192.168.1.50', port: '3074', proto: 'both', direct: true, enabled: true },
          { preset: 'xbox', destIp: '192.168.1.50', port: '9002', proto: 'udp', direct: true, enabled: true },
        ],
      },
    ]);
    expect(app.$('.pf-sheet')).toBeNull();
    expect(app.all('.pf-main .pf-t').map((x) => x.textContent)).toEqual(['Xbox']);
  });

  it('asks for the protocol where the game’s makers do not say it, and sends only what was picked', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    tile(app, 'Minecraft Java');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    // The ports are open for the choice; nothing is picked; Save waits.
    expect(app.$<HTMLInputElement>('#vx-pf-port')?.value).toBe('25565');
    expect(app.all('.pf-pr [role="radio"]').map((b) => b.getAttribute('aria-checked'))).toEqual(['false', 'false', 'false']);
    expect(app.$('.pf-port .hint')?.textContent).toBe('The game’s docs do not say which protocol — pick one.');
    expect(save(app).disabled).toBe(true);
    save(app).click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([]);
    app.button('UDP')!.click();
    await settle();
    expect(app.$('.pf-port .hint')).toBeNull();
    expect(save(app).disabled).toBe(false);
    save(app).click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([{ rules: [{ preset: 'minecraft-java', destIp: '192.168.1.50', port: '25565', proto: 'udp', direct: false, enabled: true }] }]);
  });

  it('sends «both» for such a game only when the person picks it', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    tile(app, 'All presets');
    await settle();
    app.type('.pf-find input', 'valheim');
    await settle();
    choice(app, 'Valheim');
    await settle();
    device(app, 'synology-nas');
    await settle();
    expect(save(app).disabled).toBe(true);
    app.button('Both')!.click();
    await settle();
    save(app).click();
    await settle(20);
    expect(app.sent('set_port_forwards')).toEqual([{ rules: [{ preset: 'valheim', destIp: '192.168.1.10', port: '2456-2457', proto: 'both', direct: false, enabled: true }] }]);
  });

  it('finds any preset under «All presets» by name, tag, category or port, and by category', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    tile(app, 'All presets');
    await settle();
    expect(title(app)).toBe('All presets');
    expect(app.all('.pf-cat').map((b) => b.textContent)).toEqual(['All', 'Games', 'Consoles', 'Remote access', 'Media & files', 'Home & cameras', 'Voice chat', 'Own VPN server']);
    expect(app.all('.pf-cat[aria-pressed="true"]').map((b) => b.textContent)).toEqual(['All']);
    // Everything, under its category's heading.
    expect(listed(app)).toHaveLength(PRESETS.length);
    expect(app.all('.pf-sec h3').map((h) => h.textContent)).toEqual(['Games', 'Consoles', 'Remote access', 'Media & files', 'Home & cameras', 'Voice chat', 'Own VPN server']);
    app.type('.pf-find input', 'RUST');
    await settle();
    expect(listed(app)).toEqual(['Rust']);
    app.type('.pf-find input', '25565');
    await settle();
    expect(listed(app)).toEqual(['Minecraft Java']);
    app.type('.pf-find input', 'transmission');
    await settle();
    expect(listed(app)).toEqual(['Torrent']);
    app.type('.pf-find input', 'vpn');
    await settle();
    expect(listed(app)).toEqual(['WireGuard', 'OpenVPN']);
    app.type('.pf-find input', '');
    app.button('Voice chat')!.click();
    await settle();
    expect(listed(app)).toEqual(['TeamSpeak 3', 'Mumble']);
    expect(app.all('.pf-sec h3')).toEqual([]);
    app.type('.pf-find input', 'minecraft');
    await settle();
    // Nothing there: say so, and offer one's own port.
    expect(app.$('.pf-sec .hint')?.textContent).toBe('Nothing found.');
    app.button('All')!.click();
    await settle();
    expect(listed(app)).toEqual(['Minecraft Java', 'Minecraft Bedrock']);
    app.type('.pf-find input', 'zzz');
    await settle();
    choice(app, 'Custom port');
    await settle();
    expect(title(app)).toBe('Which port?');
  });

  it('goes back along the way it came, at every step', async () => {
    const app = start({ scenario: 'cgnat' });
    await settle();
    open(app);
    await settle();
    expect(app.$('.pf-sheet-h button[aria-label="Back"]')).toBeNull();
    tile(app, 'All presets');
    await settle();
    app.type('.pf-find input', 'rust');
    await settle();
    choice(app, 'Rust');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    expect(title(app)).toBe('Rust');
    back(app);
    await settle();
    expect(title(app)).toBe('For which device?');
    back(app);
    await settle();
    expect(title(app)).toBe('All presets');
    back(app);
    await settle();
    expect(title(app)).toBe('What to open?');
    tile(app, 'Custom port');
    await settle();
    app.type('#vx-pf-port', '8080');
    await settle();
    app.button('Next')!.click();
    await settle();
    expect(title(app)).toBe('For which device?');
    back(app);
    await settle();
    expect(title(app)).toBe('Which port?');
    expect(app.$<HTMLInputElement>('#vx-pf-port')?.value).toBe('8080');
    back(app);
    await settle();
    expect(title(app)).toBe('What to open?');
  });

  it('lets an edit change the preset through «All presets», and back again', async () => {
    const app = start();
    await settle();
    app.all('.pf-main')[0].click();
    await settle();
    expect(app.$('.pf-sheet-h button[aria-label="Back"]')).toBeNull();
    app.button('Minecraft Java' + 'change')!.click();
    await settle();
    expect(title(app)).toBe('What to open?');
    tile(app, 'All presets');
    await settle();
    back(app);
    await settle();
    back(app);
    await settle();
    expect(title(app)).toBe('Minecraft Java');
    app.button('Minecraft Java' + 'change')!.click();
    await settle();
    tile(app, 'All presets');
    await settle();
    app.type('.pf-find input', 'terraria');
    await settle();
    choice(app, 'Terraria');
    await settle();
    expect(title(app)).toBe('Terraria');
    expect(app.$('.pf-sheet-h button[aria-label="Back"]')).toBeNull();
    app.button('Save')!.click();
    await settle(20);
    expect((app.sent('set_port_forwards')[0] as { rules: unknown[] }).rules[0]).toEqual({ id: '3fa1c09e', preset: 'terraria', destIp: '192.168.1.50', port: '7777', proto: 'tcp', direct: false, enabled: true });
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

  it('checks every port of a preset before it asks the router', async () => {
    const app = start();
    await settle();
    open(app);
    await settle();
    tile(app, 'PlayStation');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    app.button('Save')!.click();
    await settle();
    // The saved PlayStation takes all of these ports: said beside each.
    expect(app.all('.pf-pr .fld-err').map((x) => x.textContent)).toEqual(['This port is already taken.', 'This port is already taken.']);
    expect(app.sent('set_port_forwards')).toEqual([]);
  });

  it('shows the router’s port refusal once, under the ports, and keeps the sheet', async () => {
    const app = start({ pfFail: 'port_conflict' });
    await settle();
    open(app);
    await settle();
    tile(app, 'All presets');
    await settle();
    app.type('.pf-find input', 'sunshine');
    await settle();
    choice(app, 'Sunshine');
    await settle();
    device(app, 'gaming-pc');
    await settle();
    app.button('Save')!.click();
    await settle(20);
    expect(app.$('.pf-sheet')).not.toBeNull();
    expect(app.all('.pf-pr')).toHaveLength(4);
    expect(app.all('.pf-sheet .fld-err').map((x) => x.textContent)).toEqual(['This port is already taken.']);
    expect(app.$('.pf-port > .fld-err')).not.toBeNull();
    expect(app.$('.toast')).toBeNull();
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
    expect(app.all('.pf-sheet .fld-err').map((x) => x.textContent)).toEqual(['This address is not on the home network.']);
    // Said once, on the sheet: no toast repeats it.
    expect(app.$('.toast')).toBeNull();
  });

  for (const [code, words] of [
    ['too_many', 'Too many rules.'],
    ['busy', 'The controller is busy, try later'],
    ['locked', 'Not available: the operator allows only the simple view on this router'],
  ] as const) {
    it(`says a refusal about no field (${code}) once, under the form`, async () => {
      const app = start({ pfFail: code });
      await settle();
      app.all('.pf-main')[0].click();
      await settle();
      app.button('Save')!.click();
      await settle(20);
      expect(app.all('.pf-sheet .note').map((n) => n.textContent)).toEqual([words]);
      expect(app.all('.pf-sheet .fld-err')).toEqual([]);
      expect(app.$('.toast')).toBeNull();
    });
  }

  it('opens a saved rule on its summary, and removes it after asking', async () => {
    const app = start();
    await settle();
    app.all('.pf-main')[0].click();
    await settle();
    expect(title(app)).toBe('Minecraft Java');
    expect(app.all('.pf-line .pf-t').map((x) => x.textContent)).toEqual(['gaming-pc']);
    // A saved rule has its protocol: nothing to ask.
    expect(app.$('.pf-port .hint')).toBeNull();
    expect(app.$('.pf-line-s')?.textContent).toBe('Port 25565 · TCP' + 'change');
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

  it('renders the list, every wizard step and «All presets» in every language without a stray null', async () => {
    for (const lang of LANGS) {
      const app = start({ lang });
      await settle();
      const texts = [app.root.textContent ?? ''];
      open(app);
      await settle();
      texts.push(app.root.textContent ?? '');
      app.all('.pf-tile')[POPULAR.length].click();
      await settle();
      texts.push(app.root.textContent ?? '');
      app.all('.pf-sec .pf-ch')[0].click();
      await settle();
      texts.push(app.root.textContent ?? '');
      app.all('.pf-devs .pf-ch')[1].click();
      await settle();
      texts.push(app.root.textContent ?? '');
      // Minecraft Java: the protocol is asked for.
      expect(app.all('.pf-pr [role="radio"]'), lang).toHaveLength(3);
      for (const x of texts) expect(x, lang).not.toMatch(/undefined|NaN|\bnull\b|pf\.\w/);
      cleanup.forEach((fn) => fn());
      cleanup = [];
      document.body.innerHTML = '';
    }
  });
});
