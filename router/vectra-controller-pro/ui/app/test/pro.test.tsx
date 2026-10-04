// The Pro view after its restructure (r16): the services and the settings are
// the simple view's own controls, the journal reads as an owner needs it, and
// what was technical detail on screen is in the support report.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn, LogLine } from '../src/api/types';
import { makeT } from '../src/i18n';
import { makeFmt } from '../src/lib/format';
import { lineLevel, lineText, logRows } from '../src/lib/logs';
import { buildReport } from '../src/lib/report';
import { FIXTURES } from '../src/mock/fixtures';
import { clone } from '../src/mock/scenarios';
import { createMock } from '../src/mock/transport';
import { mount } from '../src/mount';

let cleanup: (() => void)[] = [];

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('vectra.ui.mode', 'pro');
});
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  vi.useRealTimers();
  document.body.innerHTML = '';
});

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(opts: { lang?: string; tab?: string; call?: CallFn; password?: boolean } = {}) {
  localStorage.setItem('vectra.ui.lang', opts.lang ?? 'en');
  if (opts.tab) localStorage.setItem('vectra.ui.tab', opts.tab);
  const mock = createMock({ scenario: 'healthy', latencyMs: 0, live: false, applyMs: 10 });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const base = opts.call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: (m, p) => (calls.push([m, p]), base(m, p)), setPassword: opts.password ? mock.setPassword : undefined, lang: 'en' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  const button = (label: string) => all('button').find((b) => b.textContent?.trim() === label);
  const sent = (m: string) => calls.filter(([x]) => x === m).map(([, p]) => p);
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  return { root, $, all, button, sent, confirm };
}

/** A router whose journal is `lines`; everything else from the mock. */
function withLogs(lines: LogLine[]): CallFn {
  const mock = createMock({ latencyMs: 0, live: false });
  cleanup.push(() => mock.dispose());
  return (m, p) => (m === 'logs' ? Promise.resolve({ lines: clone(lines) }) : mock.call(m, p));
}

describe('Servers and services', () => {
  it('has the simple view’s services, and a change calls set_service', async () => {
    const app = start({ tab: 'locations' });
    await settle();
    const rows = app.all('.sv-svc-r');
    expect(rows.map((r) => r.querySelector('.sv-svc-n')?.textContent)).toEqual(['YouTube', 'TikTok', 'Telegram']);
    const yt = rows[0].querySelector('select')!;
    yt.value = 'DE';
    yt.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    app.confirm();
    await settle(20);
    expect(app.sent('set_service')).toEqual([{ id: 'youtube', country: 'DE' }]);
  });
});

describe('Settings', () => {
  it('names each Wi-Fi network under its band, and opens the wizard’s Wi-Fi step and comes back', async () => {
    const app = start({ tab: 'settings' });
    await settle();
    const nets = app.all('.fvs > div').map((d) => [d.querySelector('dt')?.textContent?.replace(/\s/g, ' '), d.querySelector('dd b')?.textContent]);
    expect(nets).toEqual([
      ['Network, 2.4 GHz', 'Vectra-4E2A'],
      ['Network, 5 GHz', 'Vectra-4E2A'],
    ]);
    app.all('button').find((b) => b.textContent?.startsWith('Change the Wi'))!.click();
    await settle();
    expect(app.$('.wz')).not.toBeNull();
    expect(app.$('.wz-steps [aria-current="step"]')?.textContent?.replace(/\u2060/g, '')).toContain('Wi-Fi');
    app.button('Back to settings')!.click();
    await settle();
    expect(app.$('.wz')).toBeNull();
    expect(app.$('.setg')).not.toBeNull();
    expect(app.sent('set_wifi')).toEqual([]);
  });

  it('has support access, opening it confirmed, and set_remote_shell is what it calls', async () => {
    const app = start({ tab: 'settings' });
    await settle();
    const sw = app.$<HTMLButtonElement>('.setg [role="switch"]')!;
    expect(sw.textContent).toBe('Support access to the router');
    expect(sw.getAttribute('aria-checked')).toBe('false');
    sw.click();
    await settle();
    expect(app.$('[role="alertdialog"]')).not.toBeNull();
    app.confirm();
    await settle(20);
    expect(app.sent('set_remote_shell')).toEqual([{ on: true }]);
    expect(app.$('.setg [role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('offers the router password where LuCI can change it, and only there', async () => {
    const none = start({ tab: 'settings' });
    await settle();
    expect(none.$('#vx-st-pw')).toBeNull();
    cleanup.forEach((fn) => fn());
    cleanup = [];
    const app = start({ tab: 'settings', password: true });
    await settle();
    expect(app.$('#vx-st-pw')!.closest('section')!.textContent).toContain('The router’s settings ask for it at sign');
    app.button('Change the password')!.click();
    await settle();
    expect(app.$('[role="dialog"]')).not.toBeNull();
  });
});

describe('Journal', () => {
  const at = (s: number) => new Date(Date.parse('2026-09-27T12:00:00Z') + s * 1000).toISOString();
  const LINES: LogLine[] = [
    { time: at(0), level: 'error', source: 'crond', message: 'crond (busybox 1.36.1) started, log level 5' },
    { time: at(1), level: 'error', source: 'luci', message: 'accepted login on / for root from 192.168.1.137' },
    { time: at(2), level: 'warn', source: 'procd', message: 'Got unexpected signal 1' },
    { time: at(3), level: 'warn', source: 'dnsmasq', message: 'no servers found in /tmp/resolv.conf.d/resolv.conf.auto, will retry' },
    { time: at(4), level: 'info', source: 'vctl', message: 'time=2026-09-27T12:00:04Z level=WARN msg="subscription fetch failed" err=timeout' },
    { time: at(5), level: 'info', source: 'vctl', message: 'time=2026-09-27T12:00:05Z level=WARN msg="subscription fetch failed" err=timeout' },
    { time: at(6), level: 'info', source: 'vctl', message: 'time=2026-09-27T12:00:06Z level=WARN msg="subscription fetch failed" err=timeout' },
    { time: at(7), level: 'info', source: 'vctl', message: 'time=2026-09-27T12:00:07Z level=INFO msg="check-in ok"' },
    { time: at(8), level: 'error', source: 'xray', message: 'Failed to start: main: failed to load config files' },
  ];

  it('files OpenWrt’s benign lines as INFO and vctl’s at its own level', () => {
    expect(LINES.slice(0, 4).map(lineLevel)).toEqual(['info', 'info', 'info', 'info']);
    expect(lineLevel(LINES[4])).toBe('warn');
    expect(lineLevel(LINES[7])).toBe('info');
    expect(lineLevel(LINES[8])).toBe('error');
    expect(lineText(LINES[4].message)).toBe('subscription fetch failed err=timeout');
    expect(lineText(LINES[8].message)).toBe(LINES[8].message);
    // A level from the router's data is a name, never an object's property.
    expect(lineLevel({ time: null, level: 'unknown', source: null, message: 'level=constructor' })).toBe('unknown');
  });

  it('collapses a line repeated in a row into one with a count', () => {
    const rows = logRows(LINES, () => true);
    expect(rows.map((r) => [r.level, r.count])).toEqual([
      ['info', 1],
      ['info', 1],
      ['info', 1],
      ['info', 1],
      ['warn', 3],
      ['info', 1],
      ['error', 1],
    ]);
    expect(rows[4].time).toBe(at(6));
  });

  it('opens on warnings and errors, folds repeats, and shows everything on request', async () => {
    const app = start({ tab: 'journal', call: withLogs(LINES) });
    await settle();
    expect(app.$('.toolbar [role="radio"][aria-checked="true"]')?.textContent).toBe('Important');
    const rows = app.all('.ll');
    expect(rows.map((r) => r.querySelector('b')?.textContent)).toEqual(['WARN', 'ERR']);
    expect(rows[0].textContent).toContain('subscription fetch failed err=timeout');
    expect(rows[0].querySelector('.rep')?.textContent).toBe('×3');
    expect(app.root.textContent).not.toContain('crond');
    app.all('[role="radio"]').find((b) => b.textContent === 'All')!.click();
    await settle();
    const all = app.all('.ll').map((r) => [r.querySelector('b')?.textContent, r.querySelectorAll('span')[0]?.textContent]);
    expect(all.slice(0, 4)).toEqual([
      ['INFO', 'crond'],
      ['INFO', 'luci'],
      ['INFO', 'procd'],
      ['INFO', 'dnsmasq'],
    ]);
    expect(localStorage.getItem('vectra.ui.logs.filter')).toBe('all');
  });

  it('says there is nothing wrong when nothing is, rather than "no such lines"', async () => {
    const app = start({ tab: 'journal', call: withLogs([LINES[7]]) });
    await settle();
    expect(app.$('.empty')?.textContent).toBe('No errors or warnings');
  });
});

describe('the support report', () => {
  it('carries what left the screen: xray, the counters, the disk, and every node with its address', () => {
    const w = clone(FIXTURES);
    const t = makeT('en');
    const r = buildReport(t, makeFmt(t), w.status, w.diagnostics, 'Works', Date.parse('2026-09-28T09:00:00Z'), w.nodes.nodes);
    expect(r).toContain('xray ' + w.status.engine.xrayVersion);
    expect(r).toMatch(/^Xray: Running/m);
    expect(r).toContain('Go soft limit');
    expect(r).toContain('vctl_tproxy_hits=');
    expect(r).toContain('Overlay (flash)');
    expect(r).toContain('Router ID: ' + w.status.controlPlane.routerId);
    expect(r).toMatch(/^Nodes:$/m);
    expect(r).toContain('✓ Bridge → Poland · bridge-pl5 · ru12.provider.invalid:40053 · vless/tcp/reality · 148 ms');
    expect(r).toMatch(/✗ Bridge → France · bridge-fr5 · .* · down/);
    expect(r).not.toMatch(/undefined|NaN|\bnull\b/);
  });

  it('is copied from the overview with the nodes read for it', async () => {
    const writes: string[] = [];
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: (x: string) => (writes.push(x), Promise.resolve()) } });
    try {
      const app = start({});
      await settle();
      app.button('Copy report')!.click();
      await settle();
      expect(app.sent('nodes').length).toBe(1);
      expect(writes[0]).toContain('ru11.provider.invalid:40052');
      expect(writes[0]).toContain('Versions: Vectra');
    } finally {
      delete (navigator as { clipboard?: unknown }).clipboard;
      delete (window as { isSecureContext?: unknown }).isSecureContext;
    }
  });
});
