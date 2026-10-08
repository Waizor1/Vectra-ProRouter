// The simple view: what the router's owner sees. It must say what happens to
// their internet in plain words, in every state and language, and never show
// the Pro view's vocabulary or a server address.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn, Holder, ReadData } from '../src/api/types';
import { LANGS, makeT, type Lang } from '../src/i18n';
import { makeFmt } from '../src/lib/format';
import { simpleVerdict } from '../src/lib/health';
import { buildReport } from '../src/lib/report';
import { FIXTURES } from '../src/mock/fixtures';
import { clone, SCENARIOS, type Scenario } from '../src/mock/scenarios';
import { createMock, PRO_ONLY, type Mock } from '../src/mock/transport';
import { mount } from '../src/mount';

// The Pro view's words, in every language, and anything shaped like an address.
const TECH = /xray|vctl|balanc|балансир|负载均衡|\bnodes?\b|узел|узла|узлов|节点|\bPID\b|TPROXY|\bAPI\b|rpcd|LuCI|ubus|\b\d{1,3}(\.\d{1,3}){3}\b|:\d{4,5}\b/i;
const BAD_TEXT = /undefined|NaN|\bnull\b|\[object Object\]/;
/** The router's LAN address in the fixtures: the one address the simple view names. */
const LAN_IP = FIXTURES.setup.lan.ipv4!;
/** Where the page opens, as the footer says it (ru). */
const WAY_IN = 'Этот экран открывается по адресу my.vectra-pro.net или http://vectra.lan, а если не выходит — по адресу 192.168.1.1.';
const WAY_IN_HREFS = ['http://my.vectra-pro.net/', 'http://vectra.lan/', 'http://192.168.1.1/'];

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

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(opts: { scenario?: Scenario; lang?: Lang; call?: CallFn; locked?: boolean; mode?: 'simple' | 'pro'; holder?: Holder } = {}) {
  if (opts.lang) localStorage.setItem('vectra.ui.lang', opts.lang);
  if (opts.mode) localStorage.setItem('vectra.ui.mode', opts.mode);
  const mock: Mock = createMock({ scenario: opts.scenario ?? 'healthy', latencyMs: 0, live: false, applyMs: 10, locked: opts.locked, holder: opts.holder });
  const calls: string[] = [];
  const base = opts.call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  // As the LuCI view mounts it: with LuCI's own password change.
  const unmount = mount(host, { call: (m, p) => (calls.push(m), base(m, p)), setPassword: mock.setPassword, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  /** What a person reads: the text minus the collapsed raw error kept for support. */
  const text = () => {
    const clone = root.querySelector('.vx')!.cloneNode(true) as HTMLElement;
    clone.querySelectorAll('.sv-raw').forEach((n) => n.remove());
    return clone.textContent ?? '';
  };
  const button = (label: string | RegExp) =>
    all('button').find((b) => (typeof label === 'string' ? b.textContent?.trim() === label : label.test(b.textContent ?? '')));
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  return { root, $, all, text, button, confirm, calls, mock };
}

/** A router answering from a hand-edited copy of the fixtures. */
function world(edit: (w: ReadData) => void): CallFn {
  const w = clone(FIXTURES);
  edit(w);
  const fallback = createMock({ latencyMs: 0, live: false });
  return (m, p) => (m in w ? Promise.resolve(clone(w[m as keyof ReadData])) : fallback.call(m, p));
}

describe('opens as the simple view', () => {
  it.each(SCENARIOS)('in %s, in every language, without a stray null or a technical word', async (scenario) => {
    for (const lang of LANGS) {
      const app = start({ scenario, lang });
      await settle();
      expect(app.$('.sv'), `${scenario}/${lang}`).not.toBeNull();
      expect(app.$('.tabs'), `${scenario}/${lang}`).toBeNull();
      expect(app.text(), `${scenario}/${lang}`).not.toMatch(BAD_TEXT);
      // The footer names the product and its version, and the router's own address —
      // where this page opens, as on the box's card; everything else is plain words.
      const plain = app.text().replace(/Vectra \S+/g, '').split(LAN_IP).join('');
      expect(plain, `${scenario}/${lang}`).not.toMatch(TECH);
      cleanup.forEach((fn) => fn());
      cleanup = [];
      localStorage.clear();
    }
    expect(consoleErrors).toEqual([]);
  });

  it('says what each state means for the internet, and offers the one thing to do', async () => {
    // The brand this page remembers from the router's last answer (none: never answered here).
    const VECTRA = JSON.stringify({ id: 'vectra', name: 'Vectra' });
    const cases: [Scenario, string, string, string | null, string | null][] = [
      ['healthy', 'Всё работает', 'VPN работает, серверы отвечают.', null, null],
      ['reserve', 'Работает через запасные серверы', 'Основные серверы не отвечают.', 'Выбрать другой сервер', null],
      ['degraded', 'Не работает', 'Интернет идёт напрямую, без VPN: заблокированные сайты не откроются.', 'Перезапустить VPN', null],
      ['empty', 'Роутер ещё не настроен', 'Vectra ждёт настройки VPN.', 'Скопировать отчёт', null],
      // No answer: the brand it had the last time.
      ['down', 'Vectra не отвечает', 'Программа Vectra на роутере не установлена или не запущена.', 'Повторить', VECTRA],
      // No answer, ever, on this page: no name that may be wrong.
      ['down', 'Программа на роутере не отвечает', 'Программа на роутере не установлена или не запущена.', 'Повторить', null],
    ];
    for (const [scenario, title, line, act, remembered] of cases) {
      localStorage.removeItem('vectra.ui.brand');
      if (remembered) localStorage.setItem('vectra.ui.brand', remembered);
      const app = start({ scenario });
      await settle();
      expect(app.$('.verdict')?.textContent, scenario).toBe(title);
      expect(app.$('.sv-st')?.textContent, scenario).toContain(line);
      const primary = app.$('.sv-st .bp');
      expect(primary?.textContent ?? null, scenario).toBe(act);
      cleanup.forEach((fn) => fn());
      cleanup = [];
    }
  });

  it('remembers whose router it is, and says it when the router does not answer: Vectra’s name and mark', async () => {
    start({ scenario: 'healthy', mode: 'pro' });
    await settle();
    expect(JSON.parse(localStorage.getItem('vectra.ui.brand') ?? 'null')).toEqual({ id: 'vectra', name: 'Vectra' });
    cleanup.forEach((fn) => fn());
    cleanup = [];
    const down = start({ scenario: 'down', mode: 'simple' });
    // From the first frame, before any answer: the mark, not a blank header.
    expect(down.$('.brand .wm')).not.toBeNull();
    await settle();
    expect(down.$('.verdict')?.textContent).toBe('Vectra не отвечает');
    expect(down.$('.brand .wm')).not.toBeNull();
    expect(down.$('.brand .word-text')).toBeNull();
  });

  it('a router that never answered this page: no wordmark, and words that name no brand', async () => {
    const app = start({ scenario: 'down' });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Программа на роутере не отвечает');
    expect(app.$('.brand .wm')).toBeNull();
    expect(app.$('.brand .word')).toBeNull();
    expect(app.text()).not.toMatch(/Vectra|BloopCat|VPN/);
    expect(app.$('.vx')?.getAttribute('aria-label')).not.toMatch(/Vectra|VPN/);
  });

  it('a page whose storage throws remembers nothing: the same as never answered', async () => {
    localStorage.setItem('vectra.ui.brand', JSON.stringify({ id: 'vectra', name: 'Vectra' }));
    // As a private window or blocked site data: the accessor itself throws.
    const blocked = vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new Error('SecurityError: The operation is insecure.');
    });
    try {
      const app = start({ scenario: 'down' });
      await settle();
      expect(app.$('.verdict')?.textContent).toBe('Программа на роутере не отвечает');
      expect(app.$('.brand .word')).toBeNull();
    } finally {
      blocked.mockRestore();
    }
  });

  it('a remembered brand that is not this router’s gives way to the router’s answer, and is replaced', async () => {
    // First a memory that is not even JSON; then one that is a well-formed other brand.
    localStorage.setItem('vectra.ui.brand', '{not json');
    const garbled = start({ scenario: 'down' });
    await settle();
    expect(garbled.$('.verdict')?.textContent).toBe('Программа на роутере не отвечает');
    cleanup.forEach((fn) => fn());
    cleanup = [];
    localStorage.setItem('vectra.ui.brand', JSON.stringify({ id: 'bloopcat', name: 'BloopCat' }));
    const app = start({ scenario: 'healthy' });
    await settle();
    expect(app.$('.brand .wm')).not.toBeNull();
    expect(app.text()).not.toContain('BloopCat');
    expect(JSON.parse(localStorage.getItem('vectra.ui.brand') ?? 'null')).toEqual({ id: 'vectra', name: 'Vectra' });
  });

  it('keeps the raw transport error for support, folded away', async () => {
    const app = start({ scenario: 'down' });
    await settle();
    expect(app.$('.sv-raw summary')?.textContent).toBe('Подробности');
    expect(app.$('.sv-raw code')?.textContent).toContain('Object not found');
    expect(app.text()).not.toContain('Object not found');
  });

  it('names the kill switch when xray is down behind it', async () => {
    const app = start({
      call: world((w) => {
        w.status.engine.state = 'stopped';
        w.status.dataplane.killSwitch = true;
      }),
    });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Не работает');
    expect(app.$('.sv-st')?.textContent).toContain('Интернет отключён, пока VPN не заработает');
  });

  it('reports a pin made in Pro that stopped answering, and undoes it', async () => {
    const pinned: string[] = [];
    const fallback = createMock({ latencyMs: 0, live: false });
    const w = clone(FIXTURES);
    w.diagnostics.checks = w.diagnostics.checks.filter((c) => c.id !== 'pinned_node_dead');
    w.diagnostics.checks.push({ id: 'pinned_node_dead', status: 'warn', params: { balancer: 'BL-MAIN', node: 'bridge-nl5' } });
    const call: CallFn = (m, p) => {
      if (m === 'unpin_balancer') {
        pinned.push(String(p?.balancer));
        w.status.pins = {};
        w.diagnostics.checks = w.diagnostics.checks.filter((c) => c.id !== 'pinned_node_dead');
        return Promise.resolve({ ok: true, code: 'balancer_unpinned', detail: null });
      }
      return m in w ? Promise.resolve(clone(w[m as keyof ReadData])) : fallback.call(m, p);
    };
    const app = start({ call });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Часть сайтов может не открываться');
    expect(app.$('.sv-st')?.textContent).toContain('Сервер, выбранный вручную в режиме Pro, не отвечает.');
    app.$<HTMLElement>('.sv-st .bp')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('Связь не прервётся.');
    app.confirm();
    await settle();
    expect(pinned).toEqual(['BL-MAIN']);
    expect(app.text()).toContain('Автоматический выбор серверов включён');
    expect(app.$('.verdict')?.textContent).toBe('Всё работает');
    expect(app.$('.sv-pins')).toBeNull();
  });
});

// A browser takes a typed `vectra.lan` for a search, and a device with a VPN
// app or a private DNS asks neither name of the router: a name under a real
// top-level domain first, the .lan one as it must be typed, and the router's
// own address, which always works.
describe('where this page opens', () => {
  it('names my.vectra-pro.net first, then http://vectra.lan, then the router’s own address', async () => {
    const app = start();
    await settle();
    expect(app.$('.sv-lan')?.textContent).toBe(WAY_IN);
    expect(app.all('.sv-lan a').map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['my.vectra-pro.net', 'http://my.vectra-pro.net/'],
      ['http://vectra.lan', 'http://vectra.lan/'],
      ['192.168.1.1', 'http://192.168.1.1/'],
    ]);
  });

  it('names no address the router cannot say, nor one that is not an address', async () => {
    for (const ipv4 of [null, '<b>x</b>', '192.168.1.1/24', 'fd00::1']) {
      const app = start({
        call: world((w) => {
          w.setup.lan = { ipv4 };
        }),
      });
      await settle();
      expect(app.$('.sv-lan')?.textContent, String(ipv4)).toBe('Этот экран открывается по адресу my.vectra-pro.net или http://vectra.lan.');
      expect(app.all('.sv-lan a').map((a) => a.getAttribute('href')), String(ipv4)).toEqual(WAY_IN_HREFS.slice(0, 2));
      cleanup.forEach((fn) => fn());
      cleanup = [];
    }
  });

  it.each([
    ['en', 'This page opens at my.vectra-pro.net or http://vectra.lan; if neither works, at 192.168.1.1.'],
    ['zh', '本页面可通过 my.vectra-pro.net 或 http://vectra.lan 打开；如都打不开，请访问 192.168.1.1。'],
  ] as const)('says it in %s', async (lang, said) => {
    const app = start({ lang });
    await settle();
    expect(app.$('.sv-lan')?.textContent).toBe(said);
  });
});

describe('location', () => {
  it('shows the current location and whose choice it is', async () => {
    const app = start({});
    await settle();
    // The automatic server by what it does: "Авто", that it picks, where it goes now, and whose choice.
    expect(app.$('.sv-loc b')?.textContent).toBe('Авто');
    expect(app.$('.sv-loc .hint')?.textContent?.replace(/\u00a0/g, ' ')).toBe('выбирает лучший сервер · сейчас: 🇫🇮 Финляндия · по умолчанию');
  });

  it('switches through the list, then goes back to the default', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({});
    await vi.advanceTimersByTimeAsync(10);
    const toggle = app.button('Сменить')!;
    expect(toggle.getAttribute('aria-label')).toBe('Сменить сервер');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    toggle.click();
    await vi.advanceTimersByTimeAsync(150); // the list is fetched by an effect, after the next frame
    expect(app.button('Свернуть')?.getAttribute('aria-expanded')).toBe('true');
    // Auto first, then the countries; the rarer variants stay folded until asked for.
    expect(app.all('.sv-servers h4').map((h) => h.textContent)).toEqual(['Автоматически', 'Страны']);
    expect(app.all('.sv-li').length).toBe(19);
    app.button('Ещё 7 вариантов')!.click();
    await vi.advanceTimersByTimeAsync(20);
    const items = app.all('.sv-li');
    expect(items.length).toBe(26);
    expect(items[0].getAttribute('aria-current')).toBe('true');
    expect(items[0].textContent).toContain('сейчас');
    items.find((b) => b.textContent?.includes('Германия'))!.click();
    await vi.advanceTimersByTimeAsync(100);
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Переключиться на 🇩🇪 Германия?');
    expect(dialog.textContent).toContain('Интернет пропадёт на несколько секунд.');
    app.confirm();
    await vi.advanceTimersByTimeAsync(10);
    expect(app.text()).toContain('Применяем…');
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.text()).toContain('Сервер сменён');
    expect(app.$('.sv-loc b')?.textContent).toBe('Германия');
    expect(app.$('.sv-loc .hint')?.textContent).toBe('выбран вами');
    expect(app.$('.sv-list')).toBeNull(); // the list closes once the choice is in effect
    app.button('Вернуть по умолчанию')!.click();
    await vi.advanceTimersByTimeAsync(100);
    app.confirm();
    await vi.advanceTimersByTimeAsync(50);
    expect(app.text()).toContain('Включён сервер по умолчанию');
    expect(app.$('.sv-loc b')?.textContent).toBe('Авто');
  });

  it('opens the list from the status card and puts keyboard focus on the current location', async () => {
    const app = start({ scenario: 'reserve' });
    await settle();
    app.button('Выбрать другой сервер')!.click();
    await settle();
    const current = app.all('.sv-li').find((b) => b.getAttribute('aria-current') === 'true')!;
    expect(app.root.activeElement).toBe(current);
  });

  it('shows a fresh router one card: what is going on and the way to support', async () => {
    const app = start({ scenario: 'empty' });
    await settle();
    // No location to show and nothing to try yet — no empty cards repeating the verdict.
    expect(app.$('.sv-loc')).toBeNull();
    expect(app.$('.sv-help')).toBeNull();
    expect(app.all('button').filter((b) => b.textContent === 'Скопировать отчёт').length).toBe(1);
  });
});

describe('help', () => {
  it('restarts the VPN after saying what happens', async () => {
    const app = start({});
    await settle();
    // In the power row under the verdict, not again among the help's steps.
    expect(app.$('.sv-help')?.textContent).not.toContain('Перезапустите VPN');
    app.button('Перезапустить VPN')!.click();
    await settle();
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.querySelector('h2')?.textContent).toBe('Перезапустить VPN?');
    expect(dialog.textContent).toContain('Интернет пропадёт на несколько секунд.');
    app.confirm();
    await settle();
    expect(app.text()).toContain('VPN перезапущен');
  });

  it('copies a report with what support asks first and nothing secret', async () => {
    const writes: string[] = [];
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: (x: string) => (writes.push(x), Promise.resolve()) } });
    try {
      const app = start({});
      await settle();
      app.button('Скопировать отчёт')!.click();
      await settle();
      expect(app.text()).toContain('Отчёт скопирован');
      const report = writes[0];
      expect(report.split('\n')[0]).toBe('Vectra — отчёт о роутере');
      expect(report).toContain('Состояние: Всё работает');
      expect(report).toContain('Сервер: 🇷🇺🇪🇺 Авто Самый стабильный (назначен панелью)');
      expect(report).toContain('[dead_nodes]');
      // The router's id is how support finds it; credentials never reach the UI at all.
      expect(report).toContain('ID роутера: 00000000-0000-4000-8000-000000000001');
      expect(report).not.toMatch(/password|token|secret|https?:\/\/|vless:|hysteria|\bauth\b/i);
    } finally {
      // @ts-expect-error back to the prototype values
      delete navigator.clipboard;
      // @ts-expect-error back to the prototype values
      delete window.isSecureContext;
    }
  });

  it('shows the report to copy by hand when the browser will not copy it', async () => {
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false });
    // What a plain-HTTP LuCI page in a strict browser does: the old copy command refuses.
    const doc = document as unknown as { execCommand?: (c: string) => boolean };
    const had = Object.prototype.hasOwnProperty.call(doc, 'execCommand');
    const orig = doc.execCommand;
    doc.execCommand = () => false;
    try {
      const app = start({});
      await settle();
      app.button('Скопировать отчёт')!.click();
      await settle();
      expect(app.text()).toContain('Скопировать автоматически не получилось.');
      expect(app.$<HTMLTextAreaElement>('.sv-manual textarea')?.value).toContain('Vectra — отчёт о роутере');
    } finally {
      if (had) doc.execCommand = orig;
      else delete doc.execCommand;
      // @ts-expect-error back to the prototype value
      delete window.isSecureContext;
    }
  });

  it('checks again on request without flickering on the poll', async () => {
    const app = start({});
    await settle();
    expect(app.$('.sv-chk .hint')?.textContent).toBe('Проверено только что');
    const n = app.calls.length;
    app.button('Проверить сейчас')!.click();
    await settle();
    expect(app.calls.slice(n).sort()).toEqual(['diagnostics', 'status']);
  });
});

describe('the tour of the main screen', () => {
  it('never starts by itself outside the wizard; from the footer, Esc gives the page back and marks it seen', async () => {
    const app = start({ scenario: 'healthy' });
    await settle();
    expect(app.$('.tour')).toBeNull();
    expect(localStorage.getItem('vectra.ui.tour')).toBeNull();
    app.button('Как это работает')!.click();
    await settle();
    expect(app.$('.tour-bub .hint')?.textContent).toBe('1 из 4');
    expect(app.$('#vx-tour-t')?.textContent).toBe('Здесь видно, работает ли VPN');
    // Tab stays in the bubble: from its last button back to its first.
    const keys = app.all('.tour-bub button');
    keys[keys.length - 1].focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', cancelable: true }));
    expect(app.root.activeElement).toBe(keys[0]);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await settle();
    expect(app.$('.tour')).toBeNull();
    expect(localStorage.getItem('vectra.ui.tour')).toBe('done');
  });

  it('is not offered on a router without settings: there is nothing to show around yet', async () => {
    const app = start({ scenario: 'empty' });
    await settle();
    expect(app.button('Как это работает')).toBeUndefined();
  });
});

describe('the mode switch and the operator lock', () => {
  it('switches to Pro and remembers it', async () => {
    const app = start({});
    await settle();
    const group = app.$('[role="radiogroup"][aria-label="Режим"]')!;
    expect(Array.from(group.querySelectorAll('[role="radio"]')).map((b) => b.textContent)).toEqual(['Простой', 'Pro']);
    app.all('[role="radio"]').find((b) => b.textContent === 'Pro')!.click();
    await settle();
    expect(app.$('.tabs')).not.toBeNull();
    expect(localStorage.getItem('vectra.ui.mode')).toBe('pro');
  });

  it('under the lock: simple only, no switch, a note, and no Pro calls — even if Pro was chosen before', async () => {
    const app = start({ locked: true, mode: 'pro' });
    await settle();
    expect(app.$('.sv')).not.toBeNull();
    expect(app.$('.tabs')).toBeNull();
    expect(app.$('[role="radiogroup"][aria-label="Режим"]')).toBeNull();
    expect(app.$('.sv-foot')?.textContent).toContain('Режим Pro отключён оператором.');
    app.button('Сменить')!.click();
    await settle();
    expect(app.all('.sv-li').length).toBe(19); // the servers stay available under the lock
    expect(app.calls.filter((m) => PRO_ONLY.indexOf(m) >= 0)).toEqual([]);
    // The stored choice is kept for when the operator lifts the lock.
    expect(localStorage.getItem('vectra.ui.mode')).toBe('pro');
  });

  it('says why a Pro action is refused when the router is locked under an open Pro page', async () => {
    const t = makeT('ru');
    expect(t('a.locked')).toBe('Недоступно: на этом роутере оператор оставил только простой режим');
  });
});

describe('verdict and report, unit', () => {
  it('stays on "works" for what the owner cannot act on', () => {
    const w = clone(FIXTURES);
    w.diagnostics.checks.push(
      { id: 'memory', status: 'fail', params: { availableMiB: 40, totalMiB: 234 } },
      { id: 'panel_link', status: 'fail', params: {} },
      { id: 'subscription_ua', status: 'fail', params: { reason: 'malformed_happ' } },
      { id: 'balancer_fallback', status: 'warn', params: { balancers: ['BL-TK'], main: false, blocked: false } },
    );
    expect(simpleVerdict(w.status, w.diagnostics).kind).toBe('ok');
  });

  it('moves to "no route" when the main traffic has nowhere to go', () => {
    const w = clone(FIXTURES);
    w.diagnostics.checks = w.diagnostics.checks.filter((c) => c.id !== 'balancer_fallback');
    w.diagnostics.checks.push({ id: 'balancer_fallback', status: 'fail', params: { balancers: ['BL-MAIN'], main: true, blocked: true } });
    expect(simpleVerdict(w.status, w.diagnostics)).toMatchObject({ kind: 'noroute', act: 'location' });
  });

  it('writes the report in the reader’s language', () => {
    const w = clone(FIXTURES);
    for (const lang of LANGS) {
      const t = makeT(lang);
      const r = buildReport(t, makeFmt(t), w.status, w.diagnostics, t('s.ok.t'), Date.parse('2026-09-28T09:00:00Z'));
      expect(r.split('\n')[0], lang).toBe(t('rp.title'));
      expect(r, lang).toContain('2026-09-28T09:00:00Z');
      expect(r, lang).not.toMatch(BAD_TEXT);
      // Searchable as typed: no invisible word joiner in "check-in".
      expect(r, lang).not.toContain('\u2060');
    }
    // The one glued word that reaches the report: a lost panel link.
    const t = makeT('en');
    w.diagnostics.checks = w.diagnostics.checks.map((c) => (c.id === 'panel_link' ? { ...c, status: 'fail' as const } : c));
    expect(buildReport(t, makeFmt(t), w.status, w.diagnostics, t('s.ok.t'))).not.toContain('\u2060');
  });
});

describe('Vectra on and off', () => {
  it.each([
    ['passwall2', 'Интернет идёт через PassWall2.'],
    ['direct', 'Интернет идёт напрямую, без VPN: заблокированные сайты не откроются.'],
    ['agent', 'Роутером управляет прежняя версия Vectra.'],
  ] as const)('off, the traffic going %s: says so plainly, one way back, nothing that would not work', async (holder, line) => {
    const app = start({ scenario: 'off', holder });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Vectra выключена');
    expect(app.$('.sv-st')?.textContent).toContain(line);
    expect(app.all('.sv-st .bp').map((b) => b.textContent)).toEqual(['Включить Vectra']);
    // No server, no sites, no restart: they run through Vectra.
    expect(app.$('.sv-loc')).toBeNull();
    expect(app.$('.sv-sites')).toBeNull();
    expect(app.$('.sv-help')).toBeNull();
    expect(app.button('Перезапустить')).toBeUndefined();
    expect(app.button('Выключить Vectra')).toBeUndefined();
    expect(app.button('Открыть мастер настройки')).toBeUndefined();
    // dnsmasq answers the names with Vectra off too.
    expect(app.$('.sv-lan')?.textContent).toBe(WAY_IN);
    expect(app.all('.sv-lan a').map((a) => a.getAttribute('href'))).toEqual(WAY_IN_HREFS);
  });

  // status.power's holder is vectra only with the data plane loaded: a vctl
  // that runs without it (its render failed, say) carries nothing, and the
  // router says direct. Both views say it does not work and the internet goes
  // past the VPN — never "through Vectra" — and the way off stays.
  it('on, running without its data plane: not working, the internet goes directly', async () => {
    const idle = world((w) => {
      w.status.dataplane.loaded = false;
      w.status.power = { enabled: true, running: true, holder: 'direct', handBack: 'passwall2' };
    });
    const app = start({ call: idle });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Не работает');
    expect(app.$('.sv-st')?.textContent).toContain('Интернет идёт напрямую, без VPN');
    expect(app.button('Выключить Vectra')).toBeDefined();
    const pro = start({ call: idle, mode: 'pro' });
    await settle();
    expect(pro.text()).toContain('Идёт мимо VPN');
    expect(pro.text()).not.toContain('Идёт через VPN');
  });

  it('turns Vectra on: the dialog says what happens, then the screen waits for the router', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({ scenario: 'off' });
    await vi.advanceTimersByTimeAsync(10);
    app.button('Включить Vectra')!.click();
    await vi.advanceTimersByTimeAsync(100);
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.querySelector('h2')?.textContent).toBe('Включить Vectra?');
    expect(dialog.textContent).toContain('PassWall2 остановится, и интернет пойдёт через Vectra. На несколько секунд он пропадёт.');
    app.confirm();
    await vi.advanceTimersByTimeAsync(10);
    expect(app.text()).toContain('Применяем…');
    expect(app.$('.verdict')?.textContent).toBe('Включаем Vectra…');
    expect(app.$('.sv-st')?.textContent).toContain('Это займёт до минуты.');
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.text()).toContain('Vectra включена');
    expect(app.$('.verdict')?.textContent).not.toMatch(/Vectra выключена|Включаем/);
    expect(app.$('.sv-loc')).not.toBeNull();
    expect(app.button('Выключить Vectra')).toBeDefined();
  });

  it('turns Vectra off from the power row under the verdict, and says the internet goes back to PassWall2', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const app = start({});
    await vi.advanceTimersByTimeAsync(10);
    const off = app.button('Выключить Vectra')!;
    // One row with the restart, under the verdict: two outlined buttons, the way off the grey one.
    const row = off.closest('.sv-st .acts')!;
    expect(Array.from(row.querySelectorAll('.btn')).map((b) => [b.textContent, b.className])).toEqual([
      ['Перезапустить VPN', 'btn bo'],
      ['Выключить Vectra', 'btn bo b-off'],
    ]);
    expect(app.$('.sv-foot')?.textContent).not.toContain('Выключить Vectra');
    off.click();
    await vi.advanceTimersByTimeAsync(100);
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.querySelector('h2')?.textContent).toBe('Выключить Vectra?');
    expect(dialog.textContent).toContain('Интернет снова пойдёт через PassWall2, как до Vectra.');
    expect(app.root.activeElement?.textContent).toBe('Отмена'); // the safe choice has focus
    app.confirm();
    await vi.advanceTimersByTimeAsync(10);
    expect(app.$('.verdict')?.textContent).toBe('Выключаем Vectra…');
    await vi.advanceTimersByTimeAsync(2100);
    expect(app.text()).toContain('Vectra выключена');
    expect(app.$('.verdict')?.textContent).toBe('Vectra выключена');
    expect(app.$('.sv-st')?.textContent).toContain('Интернет идёт через PassWall2.');
    expect(app.$('.sv-loc')).toBeNull();
    expect(app.button('Включить Vectra')).toBeDefined();
  });

  it('warns that the internet goes without a VPN when Vectra took nothing from the router', async () => {
    const app = start({
      call: world((w) => {
        w.status.power.handBack = 'direct';
      }),
    });
    await settle();
    app.button('Выключить Vectra')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('Интернет пойдёт напрямую, без VPN: заблокированные сайты перестанут открываться.');
  });

  // power.wouldIdle: switched on now, Vectra would carry nothing (no operator
  // config, no PassWall2 to route by). The dialog says the internet goes
  // without a VPN until the router is linked, and only its confirmation sends
  // force — never a click that did not see it.
  it('asks before turning on a Vectra that would carry nothing, and only then sends force', async () => {
    const calls: string[] = [];
    const w = world((x) => {
      x.status.power = { enabled: false, running: false, holder: 'direct', handBack: null, wouldIdle: true };
    });
    const app = start({ call: (m, p) => (m === 'set_power' && calls.push(JSON.stringify(p)), w(m, p)) });
    await settle();
    app.button('Включить Vectra')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('до привязки интернет пойдёт напрямую, без VPN');
    expect(calls).toEqual([]);
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":true,"force":true}']);
  });

  // The page read wouldIdle false, the router answers would_idle (its status
  // changed since): the router's word is taken at once — the dialog that
  // says the internet goes without a VPN, then force, no status poll between.
  it('asks again at once when the router answers would_idle, then sends force', async () => {
    const calls: string[] = [];
    const w = world((x) => {
      x.status.power = { enabled: false, running: false, holder: 'passwall2', handBack: null, wouldIdle: false };
    });
    const call: CallFn = (m, p) => {
      if (m !== 'set_power') return w(m, p);
      calls.push(JSON.stringify(p));
      return Promise.resolve(p?.force === true ? { ok: true, code: 'pending', detail: null } : { ok: false, code: 'would_idle', detail: 'vctl would carry no traffic yet' });
    };
    const app = start({ call });
    await settle();
    app.button('Включить Vectra')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).not.toContain('без VPN');
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":true}']);
    expect(app.$('[role="alertdialog"]')?.textContent).toContain('до привязки интернет пойдёт напрямую, без VPN');
    expect(app.$('.toast.t-fail')).toBeNull();
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":true}', '{"on":true,"force":true}']);
  });

  it('turns on without force where Vectra carries the traffic', async () => {
    const calls: string[] = [];
    const w = world((x) => {
      x.status.power = { enabled: false, running: false, holder: 'passwall2', handBack: null, wouldIdle: false };
    });
    const app = start({ call: (m, p) => (m === 'set_power' && calls.push(JSON.stringify(p)), w(m, p)) });
    await settle();
    app.button('Включить Vectra')!.click();
    await settle();
    expect(app.$('[role="alertdialog"]')?.textContent).not.toContain('без VPN');
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":true}']);
  });

  it('says so when a change is already on its way, in its own words', async () => {
    const mock = createMock({ latencyMs: 0, live: false });
    const calls: string[] = [];
    const call: CallFn = (m, p) => {
      if (m === 'set_power') {
        calls.push(JSON.stringify(p));
        return Promise.resolve({ ok: false, code: 'busy', detail: 'another power change is still in flight' });
      }
      return mock.call(m, p);
    };
    const app = start({ call });
    await settle();
    app.button('Выключить Vectra')!.click();
    await settle();
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":false}']);
    expect(app.$('.toast.t-fail')?.textContent).toContain('Vectra уже включается или выключается. Подождите минуту.');
    expect(app.text()).not.toContain('another power change'); // the simple view gives no raw detail
    expect(app.$('.verdict')?.textContent).not.toBe('Выключаем Vectra…');
  });

  it('still running switched off, it says it is turning off, and the way off stays', async () => {
    const calls: string[] = [];
    const w = world((x) => {
      x.status.power = { enabled: false, running: true, holder: 'vectra', handBack: 'passwall2' };
    });
    const app = start({ call: (m, p) => (m === 'set_power' && calls.push(JSON.stringify(p)), w(m, p)) });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Выключаем Vectra…');
    expect(app.$('.sv-loc')).toBeNull();
    app.button('Выключить Vectra')!.click();
    await settle();
    app.confirm();
    await settle();
    expect(calls).toEqual(['{"on":false}']);
  });

  it('shows no switch on a router older than the switch', async () => {
    const app = start({
      call: world((w) => {
        delete (w.status as Partial<typeof w.status>).power;
      }),
    });
    await settle();
    expect(app.$('.verdict')?.textContent).toBe('Всё работает');
    expect(app.button('Выключить Vectra')).toBeUndefined();
    expect(app.$('.sv-lan')).toBeNull();
  });

  it('the mock refuses what the router refuses', async () => {
    const mock = createMock({ latencyMs: 0, live: false, applyMs: 5 });
    cleanup.push(() => mock.dispose());
    expect(await mock.call('set_power', { on: 'yes' })).toMatchObject({ ok: false, code: 'invalid_params' });
    expect(await mock.call('set_power', { on: true, trial: true })).toMatchObject({ ok: false, code: 'invalid_params' });
    expect(await mock.call('set_power', { on: true })).toMatchObject({ ok: true, code: 'power_on' });
    expect(await mock.call('set_power', { on: false })).toMatchObject({ ok: true, code: 'pending' });
    expect(await mock.call('set_power', { on: true })).toMatchObject({ ok: false, code: 'busy' });
    await new Promise((r) => setTimeout(r, 20));
    expect(await mock.call('set_power', { on: false })).toMatchObject({ ok: true, code: 'power_off' });
    const st = (await mock.call('status')) as ReadData['status'];
    expect(st.power).toEqual({ enabled: false, running: false, holder: 'passwall2', handBack: null, wouldIdle: false });
    expect(st.controller.running).toBe(false);
  });
});
