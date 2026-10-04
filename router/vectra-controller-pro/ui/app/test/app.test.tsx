// The whole app, mounted into a shadow root against the mock router, in every
// scenario, tab and language. These are the Pro view's tests; the simple view
// has its own in simple.test.tsx.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn } from '../src/api/types';
import { TABS } from '../src/app/ctx';
import { LANGS, type Lang } from '../src/i18n';
import { createMock, type Mock } from '../src/mock/transport';
import { SCENARIOS, type Scenario } from '../src/mock/scenarios';
import { mount } from '../src/mount';

const BAD_TEXT = /undefined|NaN|\bnull\b|\[object Object\]/;
// The pin for bridge-de5 in BL-MAIN, as a screen reader hears it.
const PIN_DE = 'Pin Bridge → Germany in “Main traffic”';

let cleanup: (() => void)[] = [];
let consoleErrors: unknown[] = [];

beforeEach(() => {
  localStorage.clear();
  consoleErrors = [];
  vi.spyOn(console, 'error').mockImplementation((...a: unknown[]) => void consoleErrors.push(a));
});

afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  vi.useRealTimers();
  document.body.innerHTML = '';
});

/** Let promises, Preact's render queue and zero-latency mock answers run. */
async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(opts: { scenario?: Scenario; lang?: Lang; call?: CallFn; mode?: 'simple' | 'pro' | null } = {}) {
  if (opts.lang) localStorage.setItem('vectra.ui.lang', opts.lang);
  // null: leave the view to the app (it opens the simple one).
  if (opts.mode !== null) localStorage.setItem('vectra.ui.mode', opts.mode ?? 'pro');
  const mock: Mock = createMock({ scenario: opts.scenario ?? 'healthy', latencyMs: 0, live: false, applyMs: 10 });
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: opts.call ?? mock.call, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  const text = () => root.textContent ?? '';
  const button = (label: string | RegExp) =>
    all('button').find((b) => (typeof label === 'string' ? b.textContent?.trim() === label : label.test(b.textContent ?? '')));
  return { host, root, unmount, mock, $, all, text, button };
}

describe.each(SCENARIOS)('scenario %s', (scenario) => {
  it.each(LANGS)('renders every tab in %s without a crash or a stray null', async (lang) => {
    const app = start({ scenario, lang });
    await settle();
    expect(app.$('.vx')).not.toBeNull();
    for (const tab of TABS) {
      const btn = app.$('#vx-tab-' + tab);
      if (scenario === 'down') {
        expect(btn).toBeNull(); // one clear error instead of five empty tabs
        break;
      }
      btn!.click();
      await settle();
      expect(app.$('[role="tabpanel"]')?.getAttribute('aria-labelledby')).toBe('vx-tab-' + tab);
      expect(app.text(), `${scenario}/${lang}/${tab}`).not.toMatch(BAD_TEXT);
    }
    expect(app.text()).not.toMatch(BAD_TEXT);
    expect(consoleErrors).toEqual([]);
  });
});

describe('what each scenario says', () => {
  it('Pro has six tabs in the owner’s order, and no "Nodes" tab', async () => {
    expect(TABS).toEqual(['overview', 'locations', 'balancing', 'sites', 'settings', 'journal']);
    const app = start({ lang: 'ru' });
    await settle();
    expect(app.all('[role="tab"]').map((b) => b.textContent)).toEqual(['Обзор', 'Серверы и сервисы', 'Маршруты', 'Мои сайты', 'Настройки', 'Журнал']);
    expect(app.$('#vx-tab-nodes')).toBeNull();
  });

  it('healthy: an honest verdict, the location, the header without the raw version', async () => {
    const app = start({ lang: 'ru' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Работает, есть замечания');
    expect(app.text()).toContain('Авто Самый стабильный');
    expect(app.text()).toContain('vectra-ax3000t');
    expect(app.$('.hdr')?.textContent).not.toContain('vctl');
    expect(app.$('.pill')?.textContent).toBe('Замечания');
    // The version moved to Settings, under "About the router".
    app.$('#vx-tab-settings')!.click();
    await settle();
    expect(app.$('.about')?.textContent).toContain('Vectra 0.4.0-r1');
  });

  it('degraded: not working, with the first failure as the reason — and no route claims to carry anything', async () => {
    const app = start({ scenario: 'degraded', lang: 'en' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Not working');
    expect(app.text()).toContain('Xray down: Waiting to restart');
    // The engineer's numbers are in the support report, not on the screen.
    expect(app.text()).not.toContain('vctl_local_guard');
    expect(app.text()).not.toContain('Blocked attempts at the xray API');
    app.$('#vx-tab-balancing')!.click();
    await settle();
    expect(app.text()).toContain('Cannot tell which nodes are chosen now: xray does not answer');
    // xray is down: the pin the router will re-apply carries nothing yet.
    expect(app.$('.note.t-fail')?.textContent).toContain('Xray down: Waiting to restart');
    expect(app.all('.rt-h .bd').map((b) => b.textContent)).toEqual(['no data', 'no data', 'no data']);
    expect(app.text()).toContain('port:25'); // a rule on something other than sites still shows
  });

  it('routes: each route by what it carries, its nodes by name, where it goes next — no tags, no strategy', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    const panel = app.$('#vx-panel')!;
    expect(panel.querySelectorAll('.tg, code').length).toBe(0);
    expect(panel.textContent).not.toMatch(/BL-MAIN|bridge-de5|least loaded|Unused|Samples/);
    const cards = app.all('.rt');
    // Only the routes traffic can take; none for a balancer nothing leads to.
    expect(cards.map((c) => c.querySelector('h3')?.textContent)).toEqual(['Main traffic', 'Russian sites and YouTube', 'Telegram and TikTok']);
    expect(cards.map((c) => c.querySelector('.rt-h .bd')?.textContent)).toEqual(['Working', 'Working', 'Working']);
    expect(cards[0].querySelector('.chips')?.textContent).toContain('Everything the other routes do not take');
    // The main route's own nodes, read by country: a dot, the name, the delay, the pin.
    const own = Array.from(cards[0].querySelectorAll(':scope > .mems .mem'));
    expect(own.length).toBe(8);
    const fr = own.find((r) => r.textContent?.includes('France'))!;
    expect(fr.querySelector('.ndot.nd-dead')).not.toBeNull();
    expect(fr.querySelector('.nn')?.textContent).toBe('🇫🇷France');
    expect(fr.querySelector('button')?.textContent).toBe('Pin');
    // Where it goes next, in words, the backups and their pins folded behind it.
    expect(cards[0].querySelector('details > summary')?.textContent).toBe('If none works, next: Hysteria2 backup, Whitelists');
    expect(cards[0].querySelector<HTMLDetailsElement>('details')!.open).toBe(false);
    // A routed chain: one bridge, then on as the main traffic (nothing of its own to pin there).
    expect(cards[1].querySelector('.mem .nn')?.textContent).toBe('🇷🇺Russia');
    expect(cards[1].querySelector('p.hint')?.textContent).toBe('If none works, next: as “Main traffic”');
    expect(cards[1].querySelector('.chips')?.textContent).toContain('YouTube');
    expect(cards[2].querySelector('.chips')?.textContent).toMatch(/^TikTokTelegram/); // services named, raw domains counted
  });

  it('reserve: the main route runs on its backup, which opens and says which nodes carry it now', async () => {
    const app = start({ scenario: 'reserve', lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    const main = app.all('.rt')[0];
    expect(main.querySelector('.rt-h .bd')?.textContent).toBe('On a backup');
    expect(main.querySelectorAll(':scope > .mems .mem .nd-dead').length).toBe(8);
    const backups = main.querySelector<HTMLDetailsElement>('details')!;
    expect(backups.open).toBe(true);
    const hy2 = backups.querySelector('section')!;
    expect(hy2.querySelector('h4')?.textContent).toBe('Hysteria2 backup');
    expect(Array.from(hy2.querySelectorAll('.mem.on .nn')).map((n) => n.textContent)).toEqual(['🇩🇪Germany', '🇳🇱Netherlands']);
    expect(hy2.querySelector('.mem.on .bd')?.textContent).toBe('in use');
  });

  it('a pinned node says so, with "Unpin" beside it, and the strategy’s pick is not shown', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    // BL-MAIN: the strategy picks bridge-de5 and bridge-nl5, the pin is bridge-nl5.
    const main = app.all('.rt')[0];
    const rows = Array.from(main.querySelectorAll<HTMLElement>(':scope > .mems .mem'));
    expect(rows.filter((r) => r.classList.contains('on')).map((r) => r.querySelector('.nn')?.textContent)).toEqual(['🇳🇱Netherlands']);
    const nl = rows.find((r) => r.classList.contains('on'))!;
    expect(nl.querySelector('.bd.t-warm')?.textContent).toBe('pinned');
    expect(nl.querySelector('button')?.textContent).toBe('Unpin');
    expect(main.textContent).not.toContain('in use');
    expect(main.textContent).not.toContain('would be chosen');
  });

  it('servers and services: Auto first, one card per country, no node counts or fetch time, and the services', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-locations')!.click();
    await settle();
    expect(app.all('.srv h3').map((h) => h.textContent)).toEqual(['Recommended', 'Countries', 'Other']);
    const top = app.$('.locs.top .lc')!;
    expect(top.textContent).toContain('Авто Самый стабильный');
    expect(top.getAttribute('aria-current')).toBe('true');
    const germany = app.all('.lc-g').find((g) => g.textContent?.includes('Германия'))!;
    // The provider names it in Russian; the flag says it in the reader's language.
    expect(germany.querySelector('.lc .hint')?.textContent).toBe('Germany');
    expect(Array.from(germany.querySelectorAll('.lcv')).map((b) => b.textContent)).toEqual(['Hysteria2', 'прямой']);
    expect(app.all('.lc-g').length).toBe(18); // 26 entries: Auto, 18 countries (4 of them with variants), 2 whitelist levels
    expect(app.all('.srv')[2].textContent).toContain('Белые списки · Уровень 1');
    const panel = app.$('#vx-panel')!.textContent ?? '';
    expect(panel).not.toMatch(/\d+ nodes?\b|by itself|Subscription fetched|saved copy/);
    // The services with a country of their own: the simple view's control, here too.
    expect(app.$('.sv-svc')).not.toBeNull();
    (germany.querySelector('.lcv') as HTMLElement).click();
    await settle();
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Switch to 🇩🇪 Германия · Hysteria2?');
    expect(dialog.textContent).toContain('The VPN restarts');
  });

  it('overview: the verdict, the server, the checks with node names — no fact cards, no technical details', async () => {
    const app = start({ lang: 'en' });
    await settle();
    expect(app.$('.fact')).toBeNull();
    expect(app.$('details.tech')).toBeNull();
    expect(app.text()).not.toMatch(/Kill switch|GOMEMLIMIT|Go soft limit|Router ID|Overlay|vctl_/);
    const dead = app.all('.chk').find((c) => c.textContent?.includes('down:'))!;
    expect(dead.textContent).toContain('2 nodes down: Bridge → France, Bridge → United Arab Emirates');
    expect(dead.textContent).not.toMatch(/bridge-fr5|bridge-ae5/);
    expect(app.$('#vx-panel')!.querySelectorAll('.tg, code').length).toBe(0);
    expect(app.$('.hero .loc')?.textContent).toContain('Server');
    expect(app.button('Copy report')).toBeDefined();
  });

  it('overview: says where the traffic goes when the verdict leaves it open, from the same evidence', async () => {
    const traffic = (root: ShadowRoot) => root.querySelector('.hero .fv b');
    const reserve = start({ scenario: 'reserve', lang: 'en' });
    await settle();
    expect(reserve.$('.hero .fv .k')?.textContent).toBe('Traffic');
    expect(traffic(reserve.root)?.className).toBe('tx-warn');
    expect(traffic(reserve.root)?.textContent).toBe('Goes through backup nodes');
    // The main chain ends in freedom: the interception works, and still nothing goes through the VPN.
    const mock = createMock({ latencyMs: 0, live: false });
    const call: CallFn = async (m, p) => {
      const r = (await mock.call(m, p)) as { checks?: { id: string; status: string; params: unknown }[] };
      if (m === 'diagnostics' && r.checks) {
        for (const c of r.checks) if (c.id === 'balancer_fallback') Object.assign(c, { status: 'fail', params: { balancers: ['BL-MAIN'], main: true, mainDirect: true, blocked: false } });
      }
      return r;
    };
    const direct = start({ lang: 'en', call });
    await settle();
    expect(traffic(direct.root)?.className).toBe('tx-fail');
    expect(traffic(direct.root)?.textContent).toBe('Bypasses the VPN');
    // …and the check names the route, not its tag.
    expect(direct.all('.chk').map((c) => c.textContent).join(' ')).toContain('no working node in Main traffic');
  });

  it('down: the LuCI rejection explained, with the raw message', async () => {
    const app = start({ scenario: 'down', lang: 'ru' });
    await settle();
    expect(app.text()).toContain('Модуль vectra не найден');
    expect(app.text()).toContain('Object not found');
    expect(app.$('.pill')?.textContent).toBe('Нет связи');
  });

  it('empty: waiting for the first subscription, empty states everywhere', async () => {
    const app = start({ scenario: 'empty', lang: 'zh' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('尚无订阅');
    for (const [tab, words] of [
      ['balancing', '暂无路由'],
      ['locations', '尚无订阅'],
    ]) {
      app.$('#vx-tab-' + tab)!.click();
      await settle();
      expect(app.text()).toContain(words);
    }
  });
});

describe('actions', () => {
  it('switches the location through a confirm dialog and waits for a pending answer to land', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({ lang: 'ru' });
    await vi.advanceTimersByTimeAsync(10);
    app.$('#vx-tab-locations')!.click();
    await vi.advanceTimersByTimeAsync(10);
    const germany = app.all('.lc').find((b) => b.textContent?.includes('Германия'))!;
    germany.click();
    await vi.advanceTimersByTimeAsync(100); // effects run after the next frame
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Переключиться на 🇩🇪 Германия?');
    expect(dialog.textContent).toContain('интернет пропадёт');
    expect(app.root.activeElement?.textContent).toBe('Отмена'); // the safe choice has focus
    dialog.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(10);
    expect(app.text()).toContain('Применяется…');
    expect(app.text()).toContain('Переключаем…');
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.text()).toContain('Сервер сменён');
    expect(app.all('.lc.on').map((b) => b.textContent)).toEqual([expect.stringContaining('Германия')]);
    // …and back to the default: the panel's choice.
    app.button('Вернуть по умолчанию')!.click();
    await vi.advanceTimersByTimeAsync(10);
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(50);
    expect(app.text()).toContain('Включён сервер по умолчанию');
    expect(app.all('.lc.on').map((b) => b.textContent)).toEqual([expect.stringContaining('Авто')]);
  });

  it('says a pending change did not take effect when status never shows it', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    const mock = createMock({ latencyMs: 0, live: false });
    // The controller accepts the switch, then drops it: status never changes.
    const call: CallFn = (m, p) => (m === 'select_entry' ? Promise.resolve({ ok: true, code: 'pending', detail: null }) : mock.call(m, p));
    const app = start({ lang: 'en', call });
    await vi.advanceTimersByTimeAsync(10);
    app.$('#vx-tab-locations')!.click();
    await vi.advanceTimersByTimeAsync(10);
    app.all('.lc').find((b) => b.textContent?.includes('Германия'))!.click();
    await vi.advanceTimersByTimeAsync(100);
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(31_000);
    const toasts = app.all('.toast').map((x) => x.className + ' ' + x.textContent);
    expect(toasts.some((x) => x.includes('t-info') && x.includes('still applying'))).toBe(true);
    expect(toasts.some((x) => x.includes('Server changed'))).toBe(false);
    expect(app.all('.lc.on').map((b) => b.textContent)).toEqual([expect.stringContaining('Авто')]);
  });

  it('knows a pending restart landed when xray runs under a new pid, and keeps focus on the busy button', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    const mock = createMock({ latencyMs: 0, live: false });
    let restarted = false;
    const call: CallFn = async (m, p) => {
      if (m === 'restart_xray') {
        setTimeout(() => (restarted = true), 3000);
        return { ok: true, code: 'pending', detail: null };
      }
      const r = (await mock.call(m, p)) as { engine?: { pid: number; uptimeSec: number } };
      // A recent restart: uptime is already small, so only the pid tells.
      if (m === 'status' && r.engine) r.engine.uptimeSec = 2;
      if (m === 'status' && restarted && r.engine) r.engine.pid = 5150;
      return r;
    };
    const app = start({ lang: 'en', call });
    await vi.advanceTimersByTimeAsync(10);
    const trigger = app.button(/Restart the VPN/)!;
    trigger.focus();
    trigger.click();
    await vi.advanceTimersByTimeAsync(100);
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(20);
    // Busy, announced as such, and still holding keyboard focus.
    expect(app.root.activeElement).toBe(trigger);
    expect(trigger.getAttribute('aria-busy')).toBe('true');
    expect(trigger.getAttribute('aria-disabled')).toBe('true');
    expect(trigger.hasAttribute('disabled')).toBe(false);
    trigger.click(); // ignored while busy
    await vi.advanceTimersByTimeAsync(4_500);
    expect(app.all('.toast').map((x) => x.textContent).join(' | ')).toContain('Xray restarted');
    expect(app.root.activeElement).toBe(trigger);
    expect(trigger.hasAttribute('aria-busy')).toBe(false);
  });

  it('moves focus to the panel when the focused control goes away after an action', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({ lang: 'en' });
    await vi.advanceTimersByTimeAsync(10);
    app.$('#vx-tab-balancing')!.click();
    await vi.advanceTimersByTimeAsync(10);
    const pin = app.all('button').find((b) => b.getAttribute('aria-label') === PIN_DE)!;
    pin.focus();
    pin.click();
    await vi.advanceTimersByTimeAsync(100);
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(200);
    expect(pin.isConnected).toBe(false); // the button became a "pinned" badge and "Unpin"
    expect(app.root.activeElement?.id).toBe('vx-panel');
  });

  it('keeps Esc and Tab inside the dialog when the dialog itself has focus', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.button(/Restart the VPN/)!.click();
    await settle();
    const dlg = app.$('[role="alertdialog"]')!;
    const [cancel, ok] = Array.from(dlg.querySelectorAll('button'));
    dlg.focus(); // what a click on the dialog's text does
    expect(app.root.activeElement).toBe(dlg);
    dlg.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }));
    expect(app.root.activeElement).toBe(cancel);
    dlg.focus();
    dlg.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }));
    expect(app.root.activeElement).toBe(ok);
    // Focus pulled behind the dialog comes back to it (a browser without `inert`).
    app.$('.vx-in')!.removeAttribute('inert');
    app.$<HTMLElement>('#vx-tab-journal')!.focus();
    expect(app.root.activeElement).toBe(cancel);
    dlg.focus();
    dlg.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    await settle();
    expect(app.$('[role="alertdialog"]')).toBeNull();
  });

  it('keeps a tab’s data when its refresh fails, marked as stale', async () => {
    const mock = createMock({ latencyMs: 0, live: false });
    let fail = false;
    const call: CallFn = (m, p) =>
      fail && m === 'balancers' ? Promise.reject(new Error('RPC call to vectra/balancers failed with ubus code 7: Request timed out')) : mock.call(m, p);
    const app = start({ lang: 'en', call });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    expect(app.$('.banner')).toBeNull();
    fail = true;
    app.$('.upd')!.click();
    await settle();
    expect(app.$('.banner')?.textContent).toContain('Showing the last data received.');
    expect(app.$('.banner')?.textContent).toContain('ubus code 7');
    expect(app.all('.rt').length).toBeGreaterThan(0);
    fail = false;
    app.$('.upd')!.click();
    await settle();
    expect(app.$('.banner')).toBeNull();
  });

  it('cancelling a dialog changes nothing and gives focus back to its trigger', async () => {
    const call = vi.fn(createMock({ latencyMs: 0, live: false }).call);
    const app = start({ call });
    await settle();
    const trigger = app.button(/Перезапустить VPN/)!;
    trigger.focus();
    trigger.click();
    await settle();
    expect(app.$('.vx-in')?.hasAttribute('inert')).toBe(true);
    app.$('[role="alertdialog"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(app.$('[role="alertdialog"]')).toBeNull();
    expect(app.$('.vx-in')?.hasAttribute('inert')).toBe(false);
    expect(app.root.activeElement).toBe(trigger);
    expect(call.mock.calls.map((c) => c[0])).not.toContain('restart_xray');
  });

  it('pins a node, then unpins it: pin_balancer and unpin_balancer, nothing else', async () => {
    const mock = createMock({ latencyMs: 0, live: false, applyMs: 10 });
    const call = vi.fn(mock.call);
    const app = start({ lang: 'en', call });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    app.all('button').find((b) => b.getAttribute('aria-label') === PIN_DE)!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('Pin “Bridge → Germany”?');
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('All “Main traffic” traffic goes through this node only');
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('survives reboots');
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await settle();
    expect(app.text()).toContain('Node pinned');
    expect(call.mock.calls.filter(([m]) => m === 'pin_balancer')).toEqual([['pin_balancer', { balancer: 'BL-MAIN', node: 'bridge-de5' }]]);
    const main = app.all('.rt').find((c) => c.querySelector('h3')?.textContent === 'Main traffic')!;
    expect(main.querySelector('.mem.on .bd.t-warm')?.closest('.mem')?.querySelector('.nn')?.textContent).toBe('🇩🇪Germany');
    app.button('Unpin')!.click();
    await settle();
    expect(call.mock.calls.filter(([m]) => m === 'unpin_balancer')).toEqual([['unpin_balancer', { balancer: 'BL-MAIN' }]]);
    expect(app.text()).toContain('Auto choice restored');
    expect(app.button('Unpin')).toBeUndefined();
  });

  it('changes the probe interval in Settings after warning that xray restarts', async () => {
    const mock = createMock({ latencyMs: 0, live: false, applyMs: 10 });
    const call = vi.fn(mock.call);
    const app = start({ lang: 'en', call });
    await settle();
    app.$('#vx-tab-settings')!.click();
    await settle();
    app.all('[role="radio"]').find((b) => b.textContent === '10 min')!.click();
    await settle();
    app.button('Apply')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('Probe every 10 min?');
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await settle();
    expect(app.text()).toContain('Interval updated');
    expect(call.mock.calls.filter(([m]) => m === 'set_probe_interval')).toEqual([['set_probe_interval', { seconds: 600 }]]);
    expect(app.$('#vx-st-p')!.closest('section')!.querySelector('.fv b')?.textContent).toBe('every 10 min · set on the router');
  });

  it('shows an action failure with the router detail', async () => {
    const app = start({ scenario: 'degraded', lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    app.all('button').find((b) => b.getAttribute('aria-label') === PIN_DE)!.click();
    await settle();
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await settle();
    const toast = app.$('.toast.t-fail')!;
    expect(toast.getAttribute('role')).toBe('alert');
    expect(toast.textContent).toContain('Xray API unavailable');
    expect(toast.textContent).toContain('connection refused');
  });
});

describe('polling', () => {
  it('asks for status and the visible tab every 10 s, pauses while hidden, and never overlaps', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    const mock = createMock({ latencyMs: 0, live: false });
    const calls: string[] = [];
    const app = start({ call: (m, p) => (calls.push(m), mock.call(m, p)) });
    await vi.advanceTimersByTimeAsync(50);
    expect(calls.sort()).toEqual(['diagnostics', 'status']);
    calls.length = 0;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls.sort()).toEqual(['diagnostics', 'status']);

    let hidden = true;
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
    document.dispatchEvent(new Event('visibilitychange'));
    calls.length = 0;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(calls).toEqual([]);
    hidden = false;
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(50);
    expect(calls.sort()).toEqual(['diagnostics', 'status']);

    app.$('#vx-tab-settings')!.click();
    // Opening Settings reads the provider's probe interval once; the polls after it do not.
    await vi.advanceTimersByTimeAsync(50);
    expect(calls).toContain('balancers');
    expect(calls).toContain('setup');
    calls.length = 0;
    await vi.advanceTimersByTimeAsync(10_050);
    expect(calls).toContain('status');
    expect(calls).not.toContain('balancers');
    // @ts-expect-error restore the prototype getter
    delete document.hidden;
  });

  it('leaves no timers behind after unmount', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({});
    await vi.advanceTimersByTimeAsync(50);
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    app.unmount();
    cleanup = [];
    app.mock.dispose();
    expect(vi.getTimerCount()).toBe(0);
    expect(app.root.childNodes.length).toBe(0);
  });
});

describe('preferences', () => {
  it('remembers the tab, theme and language, and survives broken storage', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-journal')!.click();
    // Dark by default, like Vectra's apps; the button walks system → light → dark.
    expect(app.$('.vx')?.getAttribute('data-theme')).toBe('dark');
    app.all('button').find((b) => b.getAttribute('aria-label') === 'Theme: dark')!.click();
    await settle();
    app.all('button').find((b) => b.getAttribute('aria-label') === 'Theme: system')!.click();
    await settle();
    expect(localStorage.getItem('vectra.ui.tab')).toBe('journal');
    expect(localStorage.getItem('vectra.ui.theme')).toBe('light');
    expect(app.$('.vx')?.getAttribute('data-theme')).toBe('light');

    // Blocked site data: even touching localStorage throws.
    const own = Object.getOwnPropertyDescriptor(window, 'localStorage');
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('SecurityError');
      },
    });
    try {
      // Nothing can be remembered: the app opens its default, the simple view,
      // and the switch still works for this page.
      const again = start({ mode: null });
      await settle();
      expect(again.$('.verdict')).not.toBeNull();
      expect(again.$('#vx-tab-settings')).toBeNull();
      again.all('[role="radio"]').find((b) => b.textContent === 'Pro')!.click();
      await settle();
      again.$('#vx-tab-settings')!.click();
      await settle();
      expect(again.$('.setg')).not.toBeNull();
    } finally {
      if (own) Object.defineProperty(window, 'localStorage', own);
      else delete (window as { localStorage?: Storage }).localStorage;
    }
  });
});

describe('Vectra on and off, in Pro', () => {
  it('off: says so and where the traffic goes; the way back; nothing that runs through Vectra', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({ scenario: 'off', lang: 'en' });
    await vi.advanceTimersByTimeAsync(10);
    expect(app.$('.verdict')?.textContent).toBe('Vectra is off');
    expect(app.$('.hero')?.textContent).toContain('The internet goes through PassWall2.');
    expect(app.$('.pill')?.textContent).toBe('Off');
    expect(app.$('.hero .loc')).toBeNull();
    expect(app.button(/Restart the VPN/)).toBeUndefined();
    // The verdict's own line says where the traffic goes: no second line saying it again.
    expect(app.$('.hero .fv')).toBeNull();
    // The checks judge what Vectra runs: switched off, there is nothing to judge.
    const diag = app.$('#vx-c-diag')!.closest('section')!;
    expect(diag.textContent).toContain('The checks run while Vectra is on.');
    expect(diag.querySelector('.checks')).toBeNull();

    app.button('Turn on Vectra')!.click();
    await vi.advanceTimersByTimeAsync(100);
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('PassWall2 stops and the internet goes through Vectra.');
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(10);
    expect(app.$('.verdict')?.textContent).toBe('Turning on Vectra…');
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.all('.toast').map((x) => x.textContent).join(' | ')).toContain('Vectra turned on');
    expect(app.$('.verdict')?.textContent).not.toBe('Vectra is off');
    expect(app.$('.hero .loc')).not.toBeNull();
    expect(app.button('Turn off Vectra')).toBeDefined();
  });

  it('on: a quiet way off, next to the restart, that says where the traffic goes back to', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({ lang: 'en' });
    await vi.advanceTimersByTimeAsync(10);
    const off = app.button('Turn off Vectra')!;
    // A real button, outlined and grey, in one row with the restart.
    expect(off.className).toContain('bo b-off');
    expect(off.closest('.acts')?.querySelector('.btn')?.textContent).toBe('Restart the VPN');
    off.click();
    await vi.advanceTimersByTimeAsync(100);
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.querySelector('h2')?.textContent).toBe('Turn off Vectra?');
    expect(dialog.textContent).toContain('The internet goes through PassWall2 again, as before Vectra.');
    dialog.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.all('.toast').map((x) => x.textContent).join(' | ')).toContain('Vectra turned off');
    expect(app.$('.verdict')?.textContent).toBe('Vectra is off');
    expect(app.$('.pill')?.textContent).toBe('Off');
  });
});

describe('the router\'s tune in Pro', () => {
  it('says in the diagnostics what the tune set, in plain words', async () => {
    const app = start({ lang: 'ru' });
    await settle();
    const line = app.all('.chk').find((c) => c.textContent?.includes('Разгон роутера'));
    // One line: the memory settings, there for the swap, go without saying beside it.
    expect(line?.textContent).toContain('Разгон роутера: сжатая подкачка 117 МБ, все ядра обрабатывают сеть, ускоренная пересылка трафика');
    expect(line?.textContent).not.toContain('память');
  });

  it('lists what the tune set with the enumeration comma in Chinese', async () => {
    const app = start({ lang: 'zh' });
    await settle();
    const line = app.all('.chk').find((c) => c.textContent?.includes('路由器优化'));
    expect(line?.textContent).toContain('路由器优化：压缩交换空间 117 MB、所有 CPU 核心共同处理网络、更快的流量转发');
  });

  it('warns where a small router runs without its compressed swap', async () => {
    const mock = createMock({ scenario: 'healthy', latencyMs: 0, live: false, applyMs: 10 });
    cleanup.push(() => mock.dispose());
    const call: CallFn = async (m, p) => {
      const r = (await mock.call(m, p)) as { checks?: { id: string; status: string; params: Record<string, unknown> }[] };
      const c = r.checks?.find((x) => x.id === 'tune');
      if (c) Object.assign(c, { status: 'warn', params: { enabled: true, profile: 'lowmem', items: ['packet_steering'], zramMiB: null } });
      return r;
    };
    const app = start({ lang: 'en', call });
    await settle();
    const line = app.all('.chk').find((c) => c.textContent?.includes('Compressed swap'));
    expect(line?.textContent).toContain('Compressed swap is not running: under load the memory runs out sooner');
    expect(line?.className).toContain('warn');
  });

  it('says plainly when none of the tune is in place, never that there is nothing to change', async () => {
    // A router over 384 MiB right after the install: its two items wait for the tune's first run.
    const mock = createMock({ scenario: 'healthy', latencyMs: 0, live: false, applyMs: 10 });
    cleanup.push(() => mock.dispose());
    const call: CallFn = async (m, p) => {
      const r = (await mock.call(m, p)) as { checks?: { id: string; status: string; params: Record<string, unknown> }[] };
      const c = r.checks?.find((x) => x.id === 'tune');
      if (c) Object.assign(c, { status: 'ok', params: { enabled: true, profile: 'standard', items: [], zramMiB: null } });
      return r;
    };
    const app = start({ lang: 'en', call });
    await settle();
    const line = app.all('.chk').find((c) => c.textContent?.includes('Router tuning'));
    expect(line?.textContent).toContain('Router tuning: nothing switched on');
    expect(line?.textContent).not.toContain('nothing to change');
  });
});
