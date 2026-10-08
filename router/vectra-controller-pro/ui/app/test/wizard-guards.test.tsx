// What a person must never see, pinned at the moment it could happen: a flash
// of the old list, "connected" before the subscription, "not set up" before
// the router was asked, a stolen or lost focus, a router polled from a hidden
// tab. Every DOM state is recorded (a MutationObserver), not just the last one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn } from '../src/api/types';
import { createMock, type MockOptions } from '../src/mock/transport';
import type { Scenario } from '../src/mock/scenarios';
import { mount } from '../src/mount';

/** Text as a person reads it: the word joiner that holds "Wi-Fi" together (i18n `glue`) is invisible. */
const read = (n: Node | null | undefined) => n?.textContent?.replace(/\u2060/g, '');

// The answers are plain JSON the tests reshape.
type Answer = Record<string, any>;
type Twist = (method: string, answer: Answer) => Answer | Error | void;

let cleanup: (() => void)[] = [];
beforeEach(() => localStorage.clear());
afterEach(async () => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  // Let Preact flush its after-paint queue before the fake clock goes.
  if (vi.isFakeTimers()) await vi.advanceTimersByTimeAsync(1000);
  vi.useRealTimers();
  document.body.innerHTML = '';
});

function start(opts: { scenario?: Scenario; twist?: Twist; mock?: Partial<MockOptions>; delay?: (m: string) => number } = {}) {
  localStorage.setItem('vectra.ui.lang', 'ru');
  const mock = createMock({ scenario: opts.scenario ?? 'unboxed', latencyMs: 0, live: false, applyMs: 10, ...opts.mock });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const call: CallFn = async (m, p) => {
    calls.push([m, p]);
    // The router answers as of now; the answer may arrive later (a slow router).
    const r = (await mock.call(m, p)) as Answer;
    const out = opts.twist?.(m, r);
    const d = opts.delay?.(m) ?? 0;
    if (d > 0) await new Promise((res) => setTimeout(res, d));
    if (out instanceof Error) throw out;
    return out ?? r;
  };
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  const text = () => read(root) ?? '';
  const verdict = () => read($('.verdict')) ?? undefined;
  const button = (label: string) => all('button, a.btn').find((b) => read(b)?.trim() === label);
  const type = async (sel: string, value: string) => {
    const el = $<HTMLInputElement>(sel)!;
    el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    for (let i = 0; i < 3; i++) await Promise.resolve();
  };
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  // Every state of the page, as it happened.
  const seen: { at: number; verdict: string | undefined; list: string[] | null }[] = [];
  const mo = new MutationObserver(() =>
    seen.push({ at: Date.now(), verdict: verdict(), list: $('.ms-list') ? all('.ms-list li').map((li) => read(li) ?? '') : null }),
  );
  mo.observe(root, { subtree: true, childList: true, characterData: true, attributes: true });
  cleanup.push(() => mo.disconnect());
  const count = (m: string) => calls.filter(([x]) => x === m).length;
  return { root, $, all, text, verdict, button, type, confirm, calls, count, seen };
}

const fake = () => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);
async function until(ok: () => boolean, maxMs: number) {
  for (let t = 0; t < maxMs && !ok(); t += 100) await tick(100);
  return ok();
}
const offline =
  (link: boolean): Twist =>
  (m, r) => {
    if (m === 'wan_check') Object.assign(r, { link, internet: false, panel: false });
    if (m === 'setup') Object.assign(r.wan, { link, ipv4: null });
  };
/** Claimed and linked, the provider's subscription not there yet. */
const owned: Twist = (m, r) => void (m === 'setup' && Object.assign(r.vectra, { linked: true, owner: { label: '@vectra_user' } }));

describe('My sites never flashes the old list after saving', () => {
  async function addAndSave(app: ReturnType<typeof start>) {
    await app.type('#vx-ms-in', 'online.bank.example');
    app.$<HTMLFormElement>('.ms-add')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await tick(20);
    app.button('Сохранить')!.click();
    await tick(20);
    const from = app.seen.length;
    app.confirm();
    return from;
  }

  it('when the router answers at once', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    const from = await addAndSave(app);
    await tick(3000);
    const lists = app.seen.slice(from).map((s) => s.list);
    expect(lists.length).toBeGreaterThan(0);
    expect(lists.filter((l) => l === null || l.indexOf('online.bank.example') < 0)).toEqual([]);
    expect(app.text()).toContain('Мои сайты сохранены');
  });

  it('when a slow read from before the save is still on its way', async () => {
    fake();
    let slow = false;
    const app = start({ scenario: 'healthy', delay: (m) => (m === 'rules' && slow ? 1500 : 0) });
    await tick(6300); // the list is older than the open card's 5 s
    slow = true;
    app.button('Настроить')!.click(); // opening re-reads: slowly, with the old lists
    await tick(100);
    const from = await addAndSave(app);
    await tick(6000);
    const lists = app.seen.slice(from).map((s) => s.list);
    expect(lists.filter((l) => l === null || l.indexOf('online.bank.example') < 0)).toEqual([]);
    expect(app.$('.ms-bar')).toBeNull();
  });
});

describe('the router is asked no more than needed', () => {
  it('the live check slows to about every 30 s once online', async () => {
    fake();
    const app = start();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    expect(await until(() => app.verdict() === 'Интернет работает', 8000)).toBe(true);
    await tick(1000);
    const n = app.count('wan_check');
    await tick(60_000);
    expect(app.count('wan_check') - n).toBeGreaterThanOrEqual(1);
    expect(app.count('wan_check') - n).toBeLessThanOrEqual(3);
  });

  it('and stays quick while there is no internet', async () => {
    fake();
    const app = start({ twist: offline(true) });
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    const n = app.count('wan_check');
    await tick(35_000);
    expect(app.count('wan_check') - n).toBeGreaterThanOrEqual(8);
  });

  it('a set-up, linked router reads `setup` about once a minute', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(500);
    const n = app.count('setup');
    await tick(120_000);
    expect(app.count('setup') - n).toBeLessThanOrEqual(3);
  });

  it('a hidden tab looks once, then asks nothing', async () => {
    fake();
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
    cleanup.push(() => {
      // @ts-expect-error back to the prototype getter
      delete document.hidden;
    });
    const app = start();
    await tick(300);
    const n = app.calls.length;
    expect(n).toBeGreaterThan(0);
    await tick(30_000);
    expect(app.calls.slice(n).map(([m]) => m)).toEqual([]);
  });
});

describe('focus', () => {
  it('moves to each new step heading, and is not taken on the first render', async () => {
    fake();
    const app = start();
    await tick(200);
    expect(app.root.activeElement).toBeNull();
    app.button('Начать')!.click();
    await tick(200);
    expect(app.root.activeElement?.id).toBe('vx-wz-t');
    expect(read(app.root.activeElement)).toBe(app.verdict());
    expect(app.root.activeElement?.getAttribute('aria-live')).toBe('polite');
    app.button('Пропустить шаг')!.click();
    await tick(200);
    expect(read(app.root.activeElement)).toBe('Прокачаем Wi-Fi');
    app.all('.wz-steps button')[3].click();
    await tick(200);
    expect(app.root.activeElement?.id).toBe('vx-wz-t');
    expect(read(app.root.activeElement)).toBe(app.verdict());
  });
});

describe('the Wi-Fi step', () => {
  async function toWifi(app: ReturnType<typeof start>) {
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.button('Пропустить шаг')!.click(); // the internet
    await tick(300);
  }

  it('takes 16 Cyrillic letters (32 bytes) and refuses 17', async () => {
    fake();
    const app = start({ twist: offline(true) });
    await toWifi(app);
    await app.type('#vx-w-ssid', 'ж'.repeat(17));
    app.button('Прокачать Wi-Fi')!.click();
    await tick(50);
    expect(app.text()).toContain('Слишком длинное название');
    expect(app.count('optimize_wifi')).toBe(0);
    await app.type('#vx-w-ssid', 'ж'.repeat(16));
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(3000);
    expect(app.calls.find(([m]) => m === 'optimize_wifi')?.[1]?.radios).toMatchObject({ radio0: { ssid: 'ж'.repeat(16) } });
    expect(app.verdict()).toBe('Wi-Fi прокачан');
  });

  it('never invents the air of a band the router could not hear', async () => {
    fake();
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => {
        if (m === 'wifi_scan') Object.assign(r.radios[1], { networks: null, channels: [], error: 'failed', recommended: 149 });
      },
    });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(300);
    const cards = app.all('.band');
    expect(read(cards[0])).toContain('Вокруг 14 сетей');
    expect(cards[1].querySelector('.air')).toBeNull();
    expect(read(cards[1])).not.toContain('Вокруг');
  });

  it('never makes the router listen by itself: the step reads the last boost, only a boost listens', async () => {
    fake();
    const app = start({ scenario: 'boxed' });
    await toWifi(app);
    // `wifi_scan` is the air heard at the last boost; this router never had one.
    expect(app.count('wifi_scan')).toBeGreaterThan(0);
    expect(app.$('.air')).toBeNull();
    expect(app.count('optimize_wifi')).toBe(0);
    // The boost is the one moment the router listens — the person is told first.
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    expect(read(app.$('[role="alertdialog"]'))).toContain('Роутер послушает эфир и перезапустит Wi-Fi');
    expect(app.count('optimize_wifi')).toBe(0);
  });
});

describe('nothing is said before the router was asked', () => {
  it('an unboxed router with a slow `setup`: the wizard, never a verdict first', async () => {
    fake();
    const app = start({ delay: (m) => (m === 'setup' ? 800 : 0) });
    await tick(3000);
    const verdicts = [...new Set(app.seen.map((s) => s.verdict).filter(Boolean))];
    expect(verdicts[0]).toBe('Настроим роутер');
    expect(verdicts).not.toContain('Роутер ещё не настроен');
    expect(verdicts).not.toContain('Роутер не подключён к Vectra');
  });

  it('a claimed router with a slow `setup`: "getting the settings", never "not set up" first', async () => {
    fake();
    const app = start({ scenario: 'empty', delay: (m) => (m === 'setup' ? 800 : 0), twist: owned });
    await tick(3000);
    expect([...new Set(app.seen.map((s) => s.verdict).filter(Boolean))]).toEqual(['Получаем настройки VPN']);
  });
});

// These run minutes of the fake clock (up to five): every timer the page sets
// in that time fires, and each one is a turn of the real event loop — about
// 0.1 s for the longest alone, but 4-5 s with four suites running at once,
// against the default 5 s timeout. That timeout is for a hang; these get room
// for a busy machine instead.
describe('"connected" and "all set" wait for the subscription', { timeout: 30_000 }, () => {
  it('in the wizard: not before a status with a subscription', async () => {
    fake();
    let subAt: number | null = null;
    const app = start({
      twist: (m, r) => {
        if (m === 'status' && subAt === null && (r.subscription.entryIndex !== null || (r.subscription.entryCount ?? 0) > 0)) subAt = Date.now();
      },
    });
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    await until(() => app.verdict() === 'Интернет работает', 8000);
    app.button('Далее')!.click();
    await tick(300);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(300);
    app.button('Далее')!.click();
    await tick(200);
    expect(await until(() => app.verdict() === 'Роутер подключён к Vectra', 30_000)).toBe(true);
    expect(subAt).not.toBeNull();
    expect(app.seen.filter((s) => s.at < subAt! && /Всё готово|Роутер подключён к Vectra|Всё работает/.test(s.verdict ?? ''))).toEqual([]);
  });

  it('in the wizard: after five minutes of waiting, a note and the way to support', async () => {
    fake();
    const app = start({ scenario: 'empty', twist: owned });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click(); // the first step that is not fine: Vectra
    await tick(200);
    expect(app.verdict()).toBe('Аккаунт подтверждён');
    expect(app.text()).not.toContain('Настройки всё ещё не пришли');
    await tick(5 * 60_000 + 1500);
    expect(app.text()).toContain('Настройки всё ещё не пришли');
    expect(app.button('Написать в Vectra')?.getAttribute('href')).toBe('https://t.me/VectraConnect_support_bot');
    expect(app.button('Пропустить шаг')).toBeDefined();
  });

  it('after a release: straight to "not connected", never a false "getting the settings"', async () => {
    fake();
    let released = false;
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => {
        if (!released) return;
        if (m === 'status') Object.assign(r.subscription, { entryIndex: null, entryRemark: null, entryCount: null, fetchedAt: null, source: null });
        if (m === 'setup') Object.assign(r.vectra, { linked: false, owner: null, claim: { state: 'unclaimed', code: 'ZFCRWC8T', qr: null, expiresAt: null, botUrl: null, owner: null } });
      },
    });
    await tick(300);
    expect(app.verdict()).toBe('Всё работает');
    await tick(20_000); // `setup` was read a while ago: a set-up router reads it once a minute
    released = true;
    const verdicts: string[] = [];
    for (let t = 0; t < 90_000; t += 100) {
      await tick(100);
      const v = app.verdict() ?? '';
      if (verdicts[verdicts.length - 1] !== v) verdicts.push(v);
    }
    expect(verdicts).not.toContain('Получаем настройки VPN');
    expect(verdicts[verdicts.length - 1]).toBe('Роутер не подключён к Vectra');
  });
});
