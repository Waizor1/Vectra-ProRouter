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
const PIN_DE = 'Pin Bridge → Germany (bridge-de5) in “Main traffic”';

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
  it('healthy: an honest verdict, the location, the header', async () => {
    const app = start({ lang: 'ru' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Работает, есть замечания');
    expect(app.text()).toContain('Авто Самый стабильный');
    expect(app.text()).toContain('vectra-ax3000t');
    expect(app.text()).toContain('vctl 0.4.0-r1');
    expect(app.$('.pill')?.textContent).toBe('Замечания');
  });

  it('degraded: not working, with the first failure as the reason — and no route claims to carry anything', async () => {
    const app = start({ scenario: 'degraded', lang: 'en' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Not working');
    expect(app.text()).toContain('Xray down: Waiting to restart');
    const guard = app.all('.kv > div').find((r) => r.textContent?.includes('Blocked attempts at the xray API'));
    expect(guard?.textContent).toContain('3');
    expect(app.text()).not.toContain('vctl_local_guard');
    app.$('#vx-tab-balancing')!.click();
    await settle();
    expect(app.text()).toContain('Cannot tell which nodes are chosen now: xray does not answer');
    // xray is down: the pin the router will re-apply carries nothing yet.
    expect(app.$('.note.t-fail')?.textContent).toContain('Xray down: Waiting to restart');
    expect(app.all('.rt-h .bd').map((b) => b.textContent)).toEqual(['no data', 'no data', 'no data']);
    expect(app.all('.stop-t .bd').map((b) => b.textContent)).not.toContain('Traffic is here');
    expect(app.text()).toContain('port:25'); // a rule on something other than sites still shows
  });

  it('routes: named by what they carry, each with its chain, tags kept beside the names', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    const cards = app.all('.rt');
    expect(cards.map((c) => c.querySelector('h3')?.textContent)).toEqual(['Main traffic', 'Russian sites and YouTube', 'Telegram and TikTok']);
    expect(cards.map((c) => c.querySelector('.rt-h .tg')?.textContent)).toEqual(['BL-MAIN', 'BL-RU', 'BL-TK']);
    expect(cards.map((c) => c.querySelector('.rt-h .bd')?.textContent)).toEqual(['Working', 'Working', 'Working']);
    // The main chain: the bridges carry it, then the Hysteria2 backup, then the whitelist levels as one stop.
    const stops = (c: HTMLElement) => Array.from(c.querySelectorAll<HTMLElement>('.stop')).map((s) => [s.className, s.querySelector('.stop-t b')?.textContent]);
    expect(stops(cards[0])).toEqual([
      ['stop here', '8 bridges'],
      ['stop standby', 'Hysteria2 backup'],
      ['stop standby', 'Whitelists'],
    ]);
    expect(cards[0].textContent).toContain('Everything the other routes do not take');
    expect(cards[0].textContent).toContain('Down: 🇫🇷France, 🇦🇪United Arab Emirates');
    expect(cards[0].querySelectorAll('.lvls li').length).toBe(4); // levels 1, 2, 3 and the last backup node
    // A routed chain: one bridge, then on as the main traffic.
    expect(cards[1].querySelector('.stop-t')?.textContent).toContain('Bridge → 🇷🇺Russia');
    expect(cards[1].querySelector('.stop-t .tg')?.textContent).toBe('bridge-ru-tcp');
    // Its bridge carries it, so the main route waits behind it.
    expect(stops(cards[1])).toEqual([
      ['stop here', 'Bridge → 🇷🇺Russia'],
      ['stop standby', 'Then as “Main traffic”'],
    ]);
    expect(cards[1].querySelector('.chips')?.textContent).toContain('YouTube');
    expect(cards[2].querySelector('.chips')?.textContent).toMatch(/^TikTokTelegram/); // services named, raw domains counted
  });

  it('reserve: the main route runs on its backup, and says which nodes carry it now', async () => {
    const app = start({ scenario: 'reserve', lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    const main = app.all('.rt')[0];
    expect(main.querySelector('.rt-h .bd')?.textContent).toBe('On a backup');
    const [bridges, backup] = Array.from(main.querySelectorAll<HTMLElement>('.stop'));
    expect(bridges.className).toBe('stop skip');
    expect(bridges.querySelector('.stop-t .bd')?.textContent).toBe('None working');
    expect(backup.className).toBe('stop here');
    expect(backup.querySelector('.then')?.textContent).toBe('If none works');
    expect(backup.querySelector('.now')?.textContent).toBe('Now: 🇩🇪Germany, 🇳🇱Netherlands');
  });

  it('a pinned balancer shows the strategy’s pick as inactive, not as selected, and one click undoes the pin', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    // BL-MAIN: the strategy picks bridge-de5 and bridge-nl5, the pin is bridge-nl5.
    const stop = app.all('.rt')[0].querySelector<HTMLElement>('.stop')!;
    expect(stop.querySelector('.note > span')?.textContent).toBe('Pinned by hand: Bridge → Netherlands. The automatic choice is off.');
    expect(stop.textContent).not.toContain('least loaded'); // no strategy while the pin decides
    expect(stop.textContent).not.toContain('Now:');
    const row = (tag: string) => Array.from(stop.querySelectorAll('.mem')).find((r) => r.textContent?.includes(tag))!;
    expect(row('bridge-de5').querySelector('.bd')?.className).toBe('bd t-mute');
    expect(row('bridge-de5').textContent).toContain('would be chosen');
    expect(row('bridge-nl5').textContent).toContain('pinned');
    expect(stop.textContent).not.toContain('selected');
    expect(Array.from(stop.querySelectorAll('.mem.on')).map((r) => r.querySelector('.tg')?.textContent)).toEqual(['bridge-nl5']);
    // The unpinned backup still says which nodes its strategy selected.
    const backup = app.all('.rt')[0].querySelectorAll<HTMLElement>('.stop')[1];
    expect(backup.textContent).toContain('selected');
    expect(backup.textContent).toContain('the 2 least loaded are used');
    expect(backup.querySelector('.note')).toBeNull();
    expect(stop.querySelector('.note-act button')?.textContent).toBe('Restore auto choice');
  });

  it('nodes: grouped by kind, in plain names, each with its tag and the route that uses it', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-nodes')!.click();
    await settle();
    expect(app.all('.grp b').map((b) => b.textContent)).toEqual(['Bridges', 'Hysteria2', 'Whitelists']);
    expect(app.all('.grp .cnt').map((b) => b.textContent)).toEqual(['10', '3', '9']);
    const row = (tag: string) => app.all('.tbl tbody tr').find((r) => r.querySelector('.addr')?.textContent?.startsWith(tag + ' '))!;
    expect(row('bridge-pl5').querySelector('.nn b')?.textContent).toBe('🇵🇱Poland');
    expect(row('bridge-pl5').querySelector('.c-bal')?.textContent).toBe('Main traffic');
    expect(row('hy2-de5').querySelector('.c-bal')?.textContent).toBe('Hysteria2 backup');
    expect(row('whitelist-lv3-2').querySelector('.nn b')?.textContent).toBe('Level 3');
    expect(row('bridge-ru-tcp').querySelector('.c-bal')?.textContent).toBe('Russian sites and YouTube');
  });

  it('servers: Auto first and recommended, then one card per country with its variants', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-locations')!.click();
    await settle();
    expect(app.all('.srv h3').map((h) => h.textContent)).toEqual(['Recommended', 'Countries', 'Other']);
    const top = app.$('.locs.top .lc')!;
    expect(top.textContent).toContain('Авто Самый стабильный');
    expect(top.textContent).toContain('Chooses a node out of 22 by itself');
    expect(top.getAttribute('aria-current')).toBe('true');
    const germany = app.all('.lc-g').find((g) => g.textContent?.includes('Германия'))!;
    // The provider names it in Russian; the flag says it in the reader's language.
    expect(germany.querySelector('.lc .hint')?.textContent).toBe('Germany · 4 nodes');
    expect(Array.from(germany.querySelectorAll('.lcv')).map((b) => b.textContent)).toEqual(['Hysteria2', 'прямой']);
    expect(app.all('.lc-g').length).toBe(18); // 26 entries: Auto, 18 countries (4 of them with variants), 2 whitelist levels
    expect(app.all('.srv')[2].textContent).toContain('Белые списки · Уровень 1');
    (germany.querySelector('.lcv') as HTMLElement).click();
    await settle();
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Switch to 🇩🇪 Германия · Hysteria2?');
    expect(dialog.textContent).toContain('The VPN restarts');
  });

  it('overview: the facts in plain words, dead nodes named, the technical details folded away', async () => {
    const app = start({ lang: 'en' });
    await settle();
    expect(app.all('.fact').map((f) => [f.querySelector('.k')?.textContent, f.querySelector('b')?.textContent])).toEqual([
      ['VPN', 'Running'],
      ['Traffic', 'Goes through the VPN'],
      ['Link to Vectra', 'Connected'],
      ['Router', 'Enough memory'],
    ]);
    const dead = app.all('.chk').find((c) => c.textContent?.includes('down:'))!;
    expect(dead.textContent).toContain('2 nodes down: Bridge → 🇫🇷France bridge-fr5, Bridge → 🇦🇪United Arab Emirates bridge-ae5');
    const tech = app.$<HTMLDetailsElement>('details.tech')!;
    expect(tech.open).toBe(false);
    expect(tech.textContent).toContain('Kill switch');
    expect(app.$('.hero .loc')?.textContent).toContain('Server');
  });

  it('overview: says where the traffic goes from the same evidence as the owner’s verdict', async () => {
    const traffic = (root: ShadowRoot) => Array.from(root.querySelectorAll('.fact')).find((f) => f.querySelector('.k')?.textContent === 'Traffic')!;
    const reserve = start({ scenario: 'reserve', lang: 'en' });
    await settle();
    expect(traffic(reserve.root).className).toContain('t-warn');
    expect(traffic(reserve.root).querySelector('b')?.textContent).toBe('Goes through backup nodes');
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
    expect(traffic(direct.root).className).toContain('t-fail');
    expect(traffic(direct.root).querySelector('b')?.textContent).toBe('Bypasses the VPN');
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
      ['nodes', '暂无节点'],
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
    expect(app.text()).toContain('выбран на роутере');
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
    // The pins sit behind the stop's "Nodes" disclosure.
    app.all('details').forEach((d) => ((d as HTMLDetailsElement).open = true));
    const pin = app.all('button').find((b) => b.getAttribute('aria-label') === PIN_DE)!;
    pin.focus();
    pin.click();
    await vi.advanceTimersByTimeAsync(100);
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await vi.advanceTimersByTimeAsync(200);
    expect(pin.isConnected).toBe(false); // the button became a "Pinned" badge
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
    app.$<HTMLElement>('#vx-tab-nodes')!.focus();
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

  it('pins a node, then restores the automatic choice', async () => {
    const app = start({ lang: 'en' });
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
    const main = app.all('.rt').find((c) => c.querySelector('h3')?.textContent === 'Main traffic')!;
    expect(main.querySelector('.mem.on .bd.t-warm')?.closest('.mem')?.textContent).toContain('bridge-de5');
    expect(main.querySelector('.note-act > span')?.textContent).toContain('Bridge → Germany');
    app.button('Restore auto choice')!.click();
    await settle();
    expect(app.text()).toContain('Auto choice restored');
    expect(app.button('Restore auto choice')).toBeUndefined();
  });

  it('changes the probe interval after warning that xray restarts', async () => {
    const app = start({ lang: 'en' });
    await settle();
    app.$('#vx-tab-balancing')!.click();
    await settle();
    app.all('[role="radio"]').find((b) => b.textContent === '10 min')!.click();
    await settle();
    app.button('Apply')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('Probe every 10 min?');
    app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
    await settle();
    expect(app.text()).toContain('Interval updated');
    expect(app.text()).toContain('Now: every 10 min · set on the router');
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

    app.$('#vx-tab-nodes')!.click();
    // Opening the tab reads the routes' names once; the polls after it do not.
    await vi.advanceTimersByTimeAsync(50);
    expect(calls).toContain('balancers');
    calls.length = 0;
    await vi.advanceTimersByTimeAsync(10_050);
    expect(calls).toContain('nodes');
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
      expect(again.$('#vx-tab-nodes')).toBeNull();
      again.all('[role="radio"]').find((b) => b.textContent === 'Pro')!.click();
      await settle();
      again.$('#vx-tab-nodes')!.click();
      await settle();
      expect(again.$('.tbl')).not.toBeNull();
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
    expect(app.all('.fact').map((f) => [f.querySelector('.k')?.textContent, f.querySelector('b')?.textContent]).slice(0, 2)).toEqual([
      ['VPN', 'Off'],
      ['Traffic', 'Through PassWall2'],
    ]);
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
    expect(off.className).toContain('bg');
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
