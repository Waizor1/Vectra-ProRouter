// A person with a router straight out of the box: the wizard takes them from
// "no internet" to "linked to Vectra", and never leaves them without a way on.
// Also "My sites", the owner's own exceptions. The mock answers as strictly as
// the router does (contract/README.md): a kinder mock would hide the bugs.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn } from '../src/api/types';
import { normalizeSite } from '../src/lib/sites';
import { createMock, type Mock, type MockOptions } from '../src/mock/transport';
import type { Scenario } from '../src/mock/scenarios';
import { mount } from '../src/mount';

/** Text as a person reads it: the word joiner that holds "Wi-Fi" together (i18n `glue`) is invisible. */
const read = (n: Node | null | undefined) => n?.textContent?.replace(/\u2060/g, '');

let cleanup: (() => void)[] = [];
beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  vi.useRealTimers();
  document.body.innerHTML = '';
});

type Answer = Record<string, unknown>;
/** Rewrites what the mock router answers, to put it in a state it would not reach by itself. */
type Twist = (method: string, answer: Answer, params?: Record<string, unknown>) => Answer | Error | void;

/**
 * `delay`: how long a method's answer takes to arrive (a router that listens to the air, a slow switch).
 * `pw`: the page hands over LuCI's password change, as the LuCI view does — the wizard then asks an
 * unboxed router for a password first (test/password.test.tsx); without it, the steps below are the others.
 */
function start(opts: { scenario?: Scenario; twist?: Twist; mock?: Partial<MockOptions>; delay?: (m: string) => number; pw?: boolean } = {}) {
  localStorage.setItem('vectra.ui.lang', 'ru');
  const mock: Mock = createMock({ scenario: opts.scenario ?? 'unboxed', latencyMs: 0, live: false, applyMs: 10, ...opts.mock });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const passwords: string[] = [];
  const call: CallFn = async (m, p) => {
    calls.push([m, p]);
    const r = (await mock.call(m, p)) as Answer;
    const d = opts.delay?.(m) ?? 0;
    if (d > 0) await new Promise((res) => setTimeout(res, d));
    const out = opts.twist?.(m, r, p);
    if (out instanceof Error) throw out;
    return out ?? r;
  };
  const setPassword = opts.pw ? (pw: string) => (passwords.push(pw), mock.setPassword(pw)) : undefined;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call, setPassword, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  const text = () => read(root) ?? '';
  const verdict = () => read($('.verdict'));
  const button = (label: string) => all('button, a.btn, a.lnk').find((b) => read(b)?.trim() === label);
  // A person's typing and their next click are separate browser tasks: the UI
  // re-renders in between. `await type(...)` gives it that turn.
  const type = async (sel: string, value: string) => {
    const el = $<HTMLInputElement>(sel)!;
    el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    for (let i = 0; i < 3; i++) await Promise.resolve();
  };
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  const badges = () => all('.wz-list .bd, .band-h .bd').map(read);
  return { root, $, all, text, verdict, button, type, confirm, calls, badges, passwords };
}

// The UI decides what to re-read by the age of its data: the clock moves with the timers.
const fake = () => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);
/** Fake time moves in small steps until the page shows what is expected (or the limit passes). */
async function until(ok: () => boolean, maxMs: number) {
  for (let t = 0; t < maxMs && !ok(); t += 100) await tick(100);
  return ok();
}

/** Real time: waits until the page shows what is expected (a loaded machine may take longer than a fixed pause). */
async function eventually(ok: () => boolean, maxMs = 3000) {
  const t0 = Date.now();
  while (!ok() && Date.now() - t0 < maxMs) await new Promise((r) => setTimeout(r, 10));
  return ok();
}

/** A router that stays offline: wan_check answers the same every time; `bot` overrides the one it knows. */
const offline =
  (link: boolean, bot?: string | null): Twist =>
  (m, r) => {
    if (m === 'wan_check') Object.assign(r, { link, internet: false, panel: false });
    if (m === 'setup') {
      Object.assign(r.wan as Answer, { link, ipv4: null });
      if (bot !== undefined) (r.vectra as Answer).botUsername = bot;
    }
  };

describe('the setup wizard', () => {
  it('walks an unboxed router to set up: a password, internet by itself, Wi-Fi at full power, Vectra, a server, then a tour', async () => {
    fake();
    const app = start({ pw: true });
    await tick(200);
    // Welcome: what will be set up; the internet says what the router sees. OpenWrt
    // ships without a password: that comes first.
    expect(app.verdict()).toBe('Настроим роутер');
    expect(app.all('.wz-list li').map(read)).toEqual(['Пароль: не задан', 'Интернет: пока нет', 'Wi-Fi: не настроено', 'Vectra: не настроено', 'Сервер: не настроено']);
    app.button('Начать')!.click();
    await tick(200);

    // The router's password, to LuCI's own change (test/password.test.tsx has the rest).
    expect(app.verdict()).toBe('Задайте пароль роутера');
    await app.type('#vx-pw-1', 'correct horse');
    await app.type('#vx-pw-2', 'correct horse');
    app.button('Сохранить пароль')!.click();
    await tick(200);
    expect(app.passwords).toEqual(['correct horse']);
    expect(app.verdict()).toBe('Пароль сохранён');
    app.button('Далее')!.click();
    await tick(200);

    // Internet: the router connects by itself — no form, no button, nothing technical.
    expect(app.verdict()).toBe('Интернета пока нет');
    expect(app.$('.wz-card input')).toBeNull();
    // The mock router gets its address; the step sees it at its next look (every 3.5 s).
    expect(await until(() => app.verdict() === 'Интернет работает', 10000)).toBe(true);
    expect(app.text()).not.toMatch(/DHCP|PPPoE|100\.64/);
    app.button('Далее')!.click();
    await tick(200);

    // Wi-Fi: both bands, off and open out of the box. Nothing is heard before the
    // boost: the router listens only when the person has agreed to the restart.
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
    expect(app.all('.band-h b').map(read)).toEqual(['2,4 ГГц', '5 ГГц']);
    expect(app.text()).toContain('выключена — включится с именем и паролем ниже');
    expect(app.text()).toContain('станет максимальной');
    expect(app.$('.air')).toBeNull();
    // Open networks out of the box: a name and a password first, one network for both bands by default.
    expect(read(app.$('.names legend'))).toBe('Сеть сейчас без пароля — задайте имя и пароль');
    expect(app.$<HTMLInputElement>('#vx-w-ssid')!.value).toBe('Vectra-4E2A');
    const key = app.$<HTMLInputElement>('#vx-w-key')!.value;
    expect(key).toMatch(/^[a-zA-Z2-9]{12}$/);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    expect(read(app.$('[role="alertdialog"]'))).toContain('Роутер послушает эфир и перезапустит Wi-Fi');
    app.confirm();
    await tick(100);
    // One call, one restart: the names, and the router picks the channels itself.
    expect(app.calls.filter(([m]) => m === 'optimize_wifi').map(([, p]) => p)).toEqual([
      { radios: { radio0: { ssid: 'Vectra-4E2A', key }, radio1: { ssid: 'Vectra-4E2A', key } } },
    ]);
    // Until the router has seen the radios come back, it says so — and the new network is already on screen.
    expect(app.verdict()).toBe('Проверяем Wi-Fi…');
    expect(app.badges()).toEqual(['перезапускается', 'перезапускается']);
    expect(app.$('svg.qr')?.getAttribute('aria-label')?.replace(/\u2060/g, '')).toBe('QR-код для подключения к Wi-Fi');
    expect(app.text()).toContain(key);
    expect(await until(() => app.verdict() === 'Wi-Fi прокачан', 5000)).toBe(true);
    // Both radios were off: nothing to listen through, so the defaults — and it says so.
    expect(app.all('.band-h').map(read)).toEqual(['2,4 ГГцканал 6', '5 ГГцканал 36']);
    expect(app.text()).toContain('Эфир не слушали: сеть была выключена — канал по умолчанию.');
    expect(app.text()).toContain(key);
    // Done is done: the step's own "Next", nothing left to skip.
    await tick(3000);
    expect(app.all('.wz-steps li')[2].classList.contains('ok')).toBe(true);
    expect(app.button('Пропустить шаг')).toBeUndefined();
    app.button('Далее')!.click();
    await tick(200);

    // Vectra: online by now, so the QR for the app and the Telegram link for the same phone.
    expect(app.verdict()).toBe('Подключение к Vectra');
    expect(read(app.$('.wz-code b'))).toBe('7KQ4-M9XD');
    expect(app.button('Открыть в Telegram')?.getAttribute('href')).toBe('https://t.me/VectraConnectBot?start=rt_7KQ4M9XD');
    expect(await until(() => app.verdict() === 'Аккаунт подтверждён', 10000)).toBe(true);
    expect(await until(() => app.verdict() === 'Роутер подключён к Vectra', 10000)).toBe(true);
    expect(app.text()).toContain('Аккаунт: @vectra_user');
    app.button('Далее')!.click();
    await tick(300);

    // The server: Auto is recommended and already on; a country is one click away.
    expect(app.verdict()).toBe('Выберите сервер');
    expect(app.all('.wz-body h3').map(read)).toEqual(['Автоматически', 'Страны']);
    const auto = app.all('.wz-body .sv-li')[0];
    // "Авто", what it does under it; the provider's own label stays in the tooltip.
    expect(read(auto.querySelector('.nm')?.firstChild)).toBe('Авто');
    expect(read(auto.querySelector('.nm-sub'))).toBe('самый стабильный');
    expect(auto.getAttribute('title')).toBe('🇷🇺🇪🇺 Авто Самый стабильный');
    expect(read(auto)).toContain('рекомендуем');
    expect(auto.getAttribute('aria-pressed')).toBe('true');
    app.all('.wz-body .sv-li').find((b) => read(b)?.includes('Германия'))!.click();
    await tick(50);
    app.button('Далее')!.click();
    await tick(50);
    expect(app.calls.find(([m]) => m === 'select_entry')?.[1]).toEqual({ index: 1 });
    expect(await until(() => app.verdict() === 'Всё готово', 5000)).toBe(true);

    // Done, then a short tour of the main screen, once.
    expect(app.badges()).toEqual(['задан', 'работает', 'на максимуме', 'подписка есть', 'выбран']);
    app.button('На главный экран')!.click();
    await tick(3000);
    expect(app.$('.wz')).toBeNull();
    expect(app.$('.tour [role="dialog"], .tour')?.getAttribute('role')).toBe('dialog');
    expect(read(app.$('.tour-bub .hint'))).toBe('1 из 4');
    expect(read(app.$('#vx-tour-t'))).toBe('Здесь видно, работает ли VPN');
    for (const title of ['Ваш сервер', 'Мои сайты', 'Если что-то не открывается']) {
      app.$<HTMLButtonElement>('.tour-bub .bp')!.click();
      await tick(300);
      expect(read(app.$('#vx-tour-t'))).toBe(title);
    }
    expect(read(app.$('.tour-bub .bp'))).toBe('Понятно');
    app.$<HTMLButtonElement>('.tour-bub .bp')!.click();
    await tick(100);
    expect(app.$('.tour')).toBeNull();
    expect(localStorage.getItem('vectra.ui.tour')).toBe('done');
    // The password went to LuCI alone: vctl has no password method, and heard none.
    expect(app.calls.map(([m]) => m)).not.toContain('set_admin_password');
    expect(JSON.stringify(app.calls)).not.toContain('correct horse');
  });

  /** From a fresh page to the Wi-Fi step, the internet up by then. */
  async function toWifi(app: ReturnType<typeof start>) {
    await tick(200);
    app.button('Начать')!.click();
    await tick(5200); // the mock router gets its address
    app.button('Далее')!.click();
    await tick(200);
  }

  it('boosts the Wi-Fi from the card: the router listens once, takes the freest channels and shows what it heard', async () => {
    fake();
    const app = start({ scenario: 'boxed' });
    await toWifi(app);
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
    // The card's network, on and secured: nothing to name, and nothing heard before the boost.
    expect(app.$('.names')).toBeNull();
    expect(app.$('.air')).toBeNull();
    expect(app.all('.band .kv dd').map(read)).toEqual(['Vectra-4E2A', 'авто', 'станет максимальной', 'Vectra-4E2A', 'авто', 'станет максимальной']);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(100);
    expect(app.calls.filter(([m]) => m === 'optimize_wifi').map(([, p]) => p)).toEqual([{}]);
    expect(await until(() => app.verdict() === 'Wi-Fi прокачан', 5000)).toBe(true);
    expect(app.all('.band-h').map(read)).toEqual(['2,4 ГГцканал 11', '5 ГГцканал 149']);
    expect(app.text()).toContain('Вокруг 14 сетей · выбран канал 11 — самый свободный');
    expect(app.all('.air li.best b').map(read)).toEqual(['11', '149']);
    // The same network comes back: no QR, no new password.
    expect(app.$('svg.qr')).toBeNull();
    // Back on the step later: the result is still there; "Change again" shows the
    // tuned Wi-Fi, with what the router heard and a boost again one click away.
    app.button('Далее')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(200);
    expect(app.verdict()).toBe('Wi-Fi прокачан');
    app.button('Изменить ещё раз')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Wi-Fi работает на полную мощность');
    expect(app.text()).toContain('Эфир проверен');
    expect(app.button('Прокачать ещё раз')).toBeDefined();
  });

  it('says so when a band did not come back and the router put the old settings back', async () => {
    fake();
    const app = start({ scenario: 'boxed', mock: { wifiDown: ['radio1'] } });
    await toWifi(app);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    expect(await until(() => app.verdict() === 'Wi-Fi вернули как было', 5000)).toBe(true);
    expect(app.text()).toContain('роутер сам вернул прежние настройки');
    // Nothing is claimed about a change that is gone: no air, no channel, no power.
    expect(app.$('.air')).toBeNull();
    expect(app.all('.band .kv')).toEqual([]);
    expect(app.badges()).toEqual(['работает', 'работает']);
    // The result has its own "Next": no "Skip" beside it, leading to the same place.
    expect(app.button('Далее')).toBeDefined();
    expect(app.button('Пропустить шаг')).toBeUndefined();
    // Back and forth: the result is still there; "Change again" opens the form,
    // which says what happened last time; skipping stays possible.
    app.button('Назад')!.click();
    await tick(200);
    app.button('Далее')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Wi-Fi вернули как было');
    app.button('Изменить ещё раз')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
    expect(app.text()).toContain('В прошлый раз Wi-Fi не поднялся с новыми настройками, и роутер вернул прежние.');
    expect(app.button('Пропустить шаг')).toBeDefined();
  });

  it('a second change starts as "checking", never with the result of the last one', async () => {
    fake();
    // Tuned already, the last change "ok": its result must not pass for the new one's.
    const app = start({ scenario: 'healthy', mock: { applyMs: 4000 } });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(300);
    expect(app.verdict()).toBe('Wi-Fi работает на полную мощность');
    app.button('Прокачать ещё раз')!.click();
    await tick(100);
    app.confirm();
    await tick(50);
    expect(app.verdict()).toBe('Проверяем Wi-Fi…');
    expect(app.badges()).toEqual(['перезапускается', 'перезапускается']);
    await tick(3000);
    expect(app.verdict()).toBe('Проверяем Wi-Fi…');
    expect(await until(() => app.verdict() === 'Wi-Fi прокачан', 6000)).toBe(true);
  });

  it('keeps the new password when the person leaves the step, and waits while the router applies it', async () => {
    fake();
    // The router listens to the air before it answers: seconds with the new key not yet on screen.
    const app = start({ delay: (m) => (m === 'optimize_wifi' ? 3000 : 0) });
    await toWifi(app);
    const key = app.$<HTMLInputElement>('#vx-w-key')!.value;
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(100);
    // Meanwhile the ways off the step wait: the result — the only place the key is shown — lands here.
    expect(app.button('Назад')?.hasAttribute('disabled')).toBe(true);
    expect(app.button('Пропустить шаг')?.hasAttribute('disabled')).toBe(true);
    expect(app.all('.wz-steps button').every((b) => b.hasAttribute('disabled'))).toBe(true);
    await tick(3500);
    expect(app.text()).toContain(key);
    // Leaving and coming back: the result, with its password and QR, is still there.
    app.button('Далее')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(200);
    expect(app.text()).toContain(key);
    expect(app.$('svg.qr')).not.toBeNull();
  });

  it('says "check the Wi-Fi" when the router could not tell, never "done"', async () => {
    fake();
    const app = start({ scenario: 'boxed', mock: { wifiEnd: 'unverified' } });
    await toWifi(app);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    expect(await until(() => app.verdict() === 'Проверьте Wi-Fi', 5000)).toBe(true);
    expect(app.badges()).toEqual(['не проверено', 'не проверено']);
    expect(app.text()).toContain('Роутер не смог сам проверить, что сеть поднялась.');
    expect(app.all('.band .kv')).toEqual([]);
  });

  it('says so plainly when the old settings could not be put back', async () => {
    fake();
    const app = start({ scenario: 'boxed', mock: { wifiEnd: 'failed' } });
    await toWifi(app);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    expect(await until(() => app.verdict() === 'Wi-Fi не поднялся', 5000)).toBe(true);
    expect(read(app.$('.note.t-fail'))).toContain('Подключитесь к роутеру кабелем');
    // Not "done": the step is not ticked, and there is a way on.
    expect(app.all('.wz-steps li')[1].classList.contains('ok')).toBe(false);
    expect(app.button('Далее')).toBeDefined();
  });

  it('leaves an open network that is off alone beside a working one: nothing to fill in, a way on', async () => {
    fake();
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => {
        if (m === 'setup') {
          const radios = (r.wifi as Answer).radios as Answer[];
          Object.assign(radios[1], { ssid: 'OpenWrt', secured: false, enabled: false, up: false });
        }
      },
    });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(300);
    expect(app.$('.names')).toBeNull();
    expect(app.button('Далее')).toBeDefined();
  });

  it('explains a busy router: another change, or LuCI changes not applied', async () => {
    fake();
    const app = start({ scenario: 'boxed', twist: (m) => (m === 'optimize_wifi' ? { ok: false, code: 'busy', detail: 'uncommitted changes in /tmp/.uci/wireless' } : undefined) });
    await toWifi(app);
    app.button('Прокачать Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(300);
    expect(app.text()).toContain('в LuCI остались непримененные изменения Wi-Fi');
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
  });

  it('only names the networks on a router with 6 GHz: no channel, no power, no boost', async () => {
    fake();
    const six = { device: 'radio2', band: '6g', channel: 37, auto: false, htmode: 'HE160', width: 160, country: 'DE', txpower: null, maxPower: true, ssid: 'Vectra-4E2A', secured: true, enabled: true, ap: true, mesh: false, up: true };
    let renamed = false;
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => {
        if (m === 'setup') {
          Object.assign(r.wifi as Answer, { radios: [...((r.wifi as Answer).radios as Answer[]), six], tunable: false, tuned: null });
          // What the router writes once the renamed Wi-Fi came back: a result of its own.
          if (renamed) (r.wifi as Answer).apply = { state: 'ok', at: '2026-09-28T10:00:00Z', detail: null, radios: { radio0: true, radio1: true, radio2: true } };
        }
        // The mock router has no third radio; the rename lands as the router would answer it.
        if (m === 'set_wifi') {
          renamed = true;
          return { ok: true, code: 'wifi_set', detail: null };
        }
      },
    });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(200);
    expect(app.verdict()).toBe('Сеть Wi-Fi');
    expect(app.text()).toContain('На этом роутере есть Wi-Fi 6 ГГц');
    expect(app.text()).not.toContain('Мощность');
    expect(app.button('Прокачать ещё раз')).toBeUndefined();
    expect(app.badges()).toEqual([]);
    app.button('Изменить имя и пароль сети')!.click();
    await tick(50);
    await app.type('#vx-w-ssid', 'Home');
    app.button('Сохранить Wi-Fi')!.click();
    await tick(100);
    expect(read(app.$('[role="alertdialog"] h2'))).toBe('Сохранить Wi-Fi?');
    app.confirm();
    await tick(300);
    expect(app.calls.map(([m]) => m)).not.toContain('optimize_wifi');
    expect(app.calls.find(([m]) => m === 'set_wifi')?.[1]).toEqual({ radios: { radio0: { ssid: 'Home' }, radio1: { ssid: 'Home' }, radio2: { ssid: 'Home' } } });
    expect(app.verdict()).toBe('Wi-Fi сохранён');
  });

  it('asks for the cable when there is none, and checks again by itself', async () => {
    fake();
    const app = start({ twist: offline(false) });
    await tick(200);
    expect(app.badges()[0]).toBe('нет кабеля');
    app.button('Начать')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Кабель интернета не подключён');
    expect(app.text()).toContain('порт WAN');
    expect(app.text()).toContain('Проверяем снова автоматически.');
    expect(app.$('.wz-card input')).toBeNull();
    // A minute on: what else to try, and still not a dead end.
    await tick(61000);
    expect(app.text()).toContain('попробуйте другой кабель');
    expect(app.button('Пропустить шаг')).toBeDefined();
  });

  it('says what to check after a minute without internet, and where to ask for help', async () => {
    fake();
    // The box was prepared with Vectra's bot, so support is reachable before the router ever was online.
    const app = start({ twist: offline(true, 'VectraSupportBot') });
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Интернета пока нет');
    expect(app.text()).not.toContain('перезагрузите модем');
    await tick(61000);
    expect(app.text()).toContain('перезагрузите модем провайдера');
    expect(app.button('Написать в Vectra')?.getAttribute('href')).toBe('https://t.me/VectraSupportBot');
    app.button('Пропустить шаг')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
  });

  it('does not invent a support link the router does not know', async () => {
    fake();
    const app = start({ twist: offline(true, null) });
    await tick(200);
    app.button('Начать')!.click();
    await tick(61200);
    expect(app.text()).toContain('напишите в поддержку');
    expect(app.button('Написать в Vectra')).toBeUndefined();
  });

  it('counts the panel answering as online, whatever the internet probe says', async () => {
    fake();
    const app = start({ twist: (m, r) => void (m === 'wan_check' && Object.assign(r, { internet: false, panel: true, link: true })) });
    await tick(200);
    expect(app.badges()[0]).toBe('работает');
  });

  it('does not hold anyone when the router cannot check the internet', async () => {
    fake();
    const app = start({ twist: (m) => (m === 'wan_check' ? new Error('RPC call to vectra/wan_check failed with ubus code 7: Request timed out') : undefined) });
    await tick(200);
    app.button('Начать')!.click();
    await tick(300);
    expect(app.text()).toContain('Не удалось проверить подключение. Можно продолжить настройку.');
    expect(app.button('Пропустить шаг')).toBeUndefined();
    app.button('Далее')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
  });

  it('checks the Wi-Fi name in bytes, asks a key for an open network, and says why', async () => {
    fake();
    const app = start();
    await tick(200);
    await until(() => app.badges()[0] === 'работает', 6000);
    app.button('Начать')!.click();
    await tick(300);
    expect(app.verdict()).toBe('Прокачаем Wi-Fi');
    await app.type('#vx-w-ssid', 'Домашняя сеть Ивановых'); // 22 letters, 42 bytes
    await app.type('#vx-w-key', '');
    app.button('Прокачать Wi-Fi')!.click();
    await tick(50);
    expect(app.text()).toContain('Слишком длинное название: до 32 латинских букв (русских — до 16).');
    expect(app.text()).toContain('Пароль — от 8 до 63 латинских букв, цифр или символов.');
    await app.type('#vx-w-ssid', '');
    app.button('Прокачать Wi-Fi')!.click();
    await tick(50);
    expect(app.text()).toContain('Введите название сети.');
    expect(app.calls.some(([m]) => m === 'optimize_wifi' || m === 'set_wifi')).toBe(false);
  });

  it('names each band on its own when asked, keeping the old key where there is one', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click(); // Wi-Fi
    await tick(300);
    expect(app.verdict()).toBe('Wi-Fi работает на полную мощность');
    // Tuned already: Next, and "change the name" as an option. Nothing to skip.
    expect(app.button('Далее')).toBeDefined();
    expect(app.button('Пропустить шаг')).toBeUndefined();
    app.button('Изменить имя и пароль сети')!.click();
    await tick(50);
    const same = app.all('.names .chkbx input')[0] as HTMLInputElement;
    expect(same.checked).toBe(true);
    same.click();
    await tick(50);
    expect(app.$<HTMLInputElement>('#vx-w-ssid-radio0')!.value).toBe('Vectra-4E2A');
    expect(app.$<HTMLInputElement>('#vx-w-ssid-radio1')!.value).toBe('Vectra-4E2A');
    expect(app.text()).toContain('Оставьте пустым, чтобы пароль остался прежним.');
    await app.type('#vx-w-ssid-radio1', 'Vectra-4E2A-5G');
    app.button('Сохранить Wi-Fi')!.click();
    await tick(100);
    app.confirm();
    await tick(300);
    // Only the names: a tuned Wi-Fi is not listened to or moved for a rename.
    expect(app.calls.map(([m]) => m)).not.toContain('optimize_wifi');
    expect(app.calls.find(([m]) => m === 'set_wifi')?.[1]).toEqual({ radios: { radio0: { ssid: 'Vectra-4E2A' }, radio1: { ssid: 'Vectra-4E2A-5G' } } });
    // The renamed network is on screen with its new name; no new key, so no QR.
    expect(app.$('svg.qr')).toBeNull();
    expect(app.all('.wz-body dl.kv dd').map(read)).toEqual(['Vectra-4E2A-5G', 'прежний']);
    expect(app.text()).toContain('подключитесь к новой сети');
    expect(await until(() => app.verdict() === 'Wi-Fi сохранён', 5000)).toBe(true);
  });

  it('lets the name change be cancelled on a tuned Wi-Fi', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    app.all('.wz-steps button')[1].click();
    await tick(200);
    app.button('Изменить имя и пароль сети')!.click();
    await tick(50);
    expect(app.$('#vx-w-ssid')).not.toBeNull();
    app.button('Отмена')!.click();
    await tick(50);
    expect(app.$('#vx-w-ssid')).toBeNull();
    expect(app.verdict()).toBe('Wi-Fi работает на полную мощность');
  });

  it('says "almost done" when a step was skipped, not "all set"', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    // Every step of a fleet router is already fine: "Close", not "Set up later".
    expect(app.button('Закрыть')).toBeDefined();
    app.button('Начать')!.click(); // everything is fine: straight to the last step
    await tick(300);
    expect(app.verdict()).toBe('Выберите сервер');
    app.button('Далее')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Всё готово');

    const fresh = start({ twist: offline(true) });
    await tick(200);
    fresh.button('Начать')!.click();
    await tick(200);
    for (let i = 0; i < 4; i++) {
      fresh.button('Пропустить шаг')!.click();
      await tick(200);
    }
    expect(fresh.verdict()).toBe('Почти готово');
    expect(fresh.text()).toContain('«Открыть мастер настройки»');
    expect(fresh.badges()).toEqual(['пока нет', 'не настроено', 'не настроено', 'не настроено']);
  });

  it('closes even when the router cannot note that the setup was finished', async () => {
    fake();
    const app = start({
      twist: (m, r) => {
        if (m === 'finish_setup') return { ok: false, code: 'internal', detail: 'uci commit failed' };
        if (m === 'setup') r.done = false; // the router never noted it
      },
    });
    await tick(200);
    app.button('Настроить позже')!.click();
    await tick(300);
    expect(app.$('.wz')).toBeNull();
    expect(app.text()).toContain('Не получилось.');
    // And it stays closed until the page is opened again.
    await tick(15000);
    expect(app.$('.wz')).toBeNull();
  });

  it('asks to wait, not to give up, while the code is not ready yet', async () => {
    fake();
    const app = start({ scenario: 'healthy', twist: (m, r) => void (m === 'setup' && Object.assign(r.vectra as Answer, { linked: false, owner: null, claim: null })) });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click(); // the first step that is not fine: Vectra
    await tick(200);
    expect(app.text()).toContain('Код для подключения ещё не готов — роутер запускает Vectra.');
    expect(app.button('Написать в Vectra')?.getAttribute('href')).toBe('https://t.me/VectraConnectBot');
  });

  it('looks once in a hidden tab, and watches the router only while the page is on screen', async () => {
    fake();
    let hidden = true;
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
    cleanup.push(() => {
      // @ts-expect-error drop the per-test override, back to the prototype getter
      delete document.hidden;
    });
    const app = start();
    await tick(200);
    expect(app.badges()[0]).toBe('пока нет'); // the first look happened
    const count = () => app.calls.filter(([m]) => m === 'wan_check').length;
    const before = count();
    await tick(20_000);
    expect(count()).toBe(before);
    hidden = false;
    await tick(5000);
    expect(count()).toBeGreaterThan(before);
  });

  it('stays away from a router that is already set up, and can be reopened from the footer', async () => {
    const app = start({ scenario: 'healthy' });
    expect(await eventually(() => !!app.button('Открыть мастер настройки'))).toBe(true);
    expect(app.$('.wz')).toBeNull();
    app.button('Открыть мастер настройки')!.click();
    expect(await eventually(() => app.verdict() === 'Настроим роутер')).toBe(true);
    expect(app.badges()).toEqual(['работает', 'на максимуме', 'подписка есть', 'выбран']);
  });

  it('offers to connect a router the wizard was skipped on, and opens the wizard right at that step', async () => {
    const app = start({ twist: (m, r) => void (m === 'setup' && (r.done = true)) }); // "Set up later" was pressed
    expect(await eventually(() => app.verdict() === 'Роутер не подключён к Vectra')).toBe(true);
    expect(app.$('.wz')).toBeNull();
    expect(app.text()).not.toContain('Скопировать отчёт');
    app.button('Подключить к Vectra')!.click();
    expect(await eventually(() => app.verdict() === 'Подключение к Vectra')).toBe(true);
    expect(read(app.$('.wz-code b'))).toBe('7KQ4-M9XD');
    // Not online yet: no QR, the code alone.
    expect(app.text()).toContain('Код для приложения:');
  });

  it('says the settings are on their way, and after minutes where to turn', async () => {
    fake();
    // Claimed and linked; the provider's subscription has not come yet.
    const app = start({ scenario: 'empty', twist: (m, r) => void (m === 'setup' && Object.assign(r.vectra as Answer, { linked: true, owner: { label: '@vectra_user' } })) });
    await tick(300);
    expect(app.verdict()).toBe('Получаем настройки VPN');
    expect(app.button('Скопировать отчёт')).toBeUndefined();
    await tick(5 * 60_000 + 1000);
    expect(app.verdict()).toBe('Настройки VPN всё ещё не пришли');
    expect(app.button('Скопировать отчёт')).toBeDefined();
    expect(app.button('Написать в Vectra')?.getAttribute('href')).toBe('https://t.me/VectraConnectBot');
  });

  it('goes straight to "not connected" when the owner releases the router, without "getting the settings"', async () => {
    fake();
    let released = false;
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => {
        if (!released) return;
        // The router wiped its settings: no subscription any more.
        if (m === 'status') Object.assign(r.subscription as Answer, { entryIndex: null, entryRemark: null, entryCount: null, fetchedAt: null, source: null });
        if (m === 'setup') Object.assign(r.vectra as Answer, { linked: false, owner: null, claim: { state: 'unclaimed', code: 'ZFCRWC8T', qr: null, expiresAt: null, botUrl: null, owner: null } });
      },
    });
    await tick(300);
    expect(app.verdict()).toBe('Всё работает');
    released = true;
    const seen: string[] = [];
    for (let t = 0; t < 12_000; t += 100) {
      await tick(100);
      const v = app.verdict() ?? '';
      if (seen[seen.length - 1] !== v) seen.push(v);
    }
    // At most a moment of "checking" while `setup` is re-read — never a false "getting the settings".
    expect(seen).not.toContain('Получаем настройки VPN');
    expect(seen[0]).toBe('Всё работает');
    expect(seen[seen.length - 1]).toBe('Роутер не подключён к Vectra');
    expect(seen.filter((v) => v !== 'Проверяем…').length).toBe(2);
  });

  it('opens support in the Vectra bot next to the report', async () => {
    const app = start({ scenario: 'healthy' });
    expect(await eventually(() => !!app.button('Написать в Vectra'))).toBe(true);
    expect(app.button('Написать в Vectra')?.getAttribute('href')).toBe('https://t.me/VectraConnectBot');
  });
});

describe('my sites', () => {
  it('normalizes what people paste exactly as the router does (contract/sites-vector.json)', () => {
    const vector = JSON.parse(readFileSync(resolve(__dirname, '../../contract/sites-vector.json'), 'utf8')) as {
      ok: [string, string][];
      singleLabel: [string, string][];
      refused: string[];
    };
    const v = (s: string) => {
      const r = normalizeSite(s);
      return 'error' in r ? r.error : r.value;
    };
    expect(vector.ok.length).toBeGreaterThan(40);
    for (const [input, kept] of vector.ok) expect(v(input), input).toBe(kept);
    // The router would keep a single label as a whole top-level domain; here it is a mistyped site.
    for (const [input] of vector.singleLabel) expect(v(input), input).toBe('invalid');
    for (const input of vector.refused) expect(v(input), JSON.stringify(input)).toBe(input.trim() ? 'invalid' : 'empty');
  });

  async function addSite(app: ReturnType<typeof start>, site: string) {
    await app.type('#vx-ms-in', site);
    app.$<HTMLFormElement>('.ms-add')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await tick(50);
  }

  it('adds, refuses a duplicate, and saves the changes in one go with a warning', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    expect(read(app.$('.sv-sites .hint'))).toBe('Без VPN: 3 · Через VPN: 1');
    app.button('Настроить')!.click();
    await tick(200);
    await addSite(app, 'https://www.sberbank.ru/ru/person');
    expect(app.text()).toContain('Этот адрес уже в списке «Без VPN».');
    await addSite(app, 'online.bank.example');
    expect(app.all('.ms-list li').map(read)).toContain('online.bank.example');
    expect(read(app.$('.ms-bar'))).toContain('1 несохранённое изменение');
    // Folded, the card still says there is something unsaved.
    app.button('Свернуть')!.click();
    await tick(50);
    expect(read(app.$('.sv-sites .hint'))).toBe('1 несохранённое изменение');
    app.button('Настроить')!.click();
    await tick(50);
    app.button('Сохранить')!.click();
    await tick(150);
    expect(read(app.$('[role="alertdialog"]'))).toContain('Интернет пропадёт на несколько секунд.');
    app.confirm();
    await tick(300);
    // The router answered at once: the new list is shown straight away, never the old one.
    expect(app.text()).toContain('Мои сайты сохранены');
    expect(app.all('.ms-list li').map(read)).toContain('online.bank.example');
    const sent = app.calls.find(([m]) => m === 'set_rules')![1]!;
    expect(sent.direct).toEqual(['sberbank.ru', 'госуслуги.рф', '1.2.3.0/24', 'online.bank.example']);
    expect(app.$('.ms-bar')).toBeNull();
  });

  it('waits for the router when it answers "pending"', async () => {
    fake();
    const app = start({ scenario: 'healthy', mock: { rulesPending: true } });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    await addSite(app, 'blocked.example');
    app.button('Сохранить')!.click();
    await tick(150);
    app.confirm();
    await tick(300);
    expect(app.text()).toContain('Применяем…');
    expect(app.$('.ms-bar')).not.toBeNull();
    await tick(2500);
    expect(app.text()).toContain('Мои сайты сохранены');
    expect(app.$('.ms-bar')).toBeNull();
  });

  // «+ сервис» (spec decision 4): a service from the router's catalog goes
  // into the chosen list as its category, shown by its name.
  it('adds a service from the router’s catalog, found by any of its names, to the chosen list', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    app.button('Через VPN: 1')!.click();
    await tick(50);
    app.button('Сервис')!.click();
    await tick(50);
    await app.type('#vx-ms-svc', 'Insta');
    await tick(20);
    expect(app.all('.ms-svc-r').map(read)).toEqual(['Instagram, Facebook, WhatsApp']);
    // As a person says it, too.
    await app.type('#vx-ms-svc', 'инстаграм');
    await tick(20);
    expect(app.all('.ms-svc-r').map(read)).toEqual(['Instagram, Facebook, WhatsApp']);
    app.all('.ms-svc-r')[0].click();
    await tick(50);
    expect(app.$('#vx-ms-svc')).toBeNull();
    expect(app.all('.ms-list li').map(read)).toEqual(['example.org', 'Instagram, Facebook, WhatsApp сервис']);
    app.button('Сохранить')!.click();
    await tick(150);
    app.confirm();
    await tick(300);
    const sent = app.calls.find(([m]) => m === 'set_rules')![1]!;
    expect(sent.proxy).toEqual(['example.org', 'geosite:meta']);
    expect(app.all('.ms-list li').map(read)).toEqual(['example.org', 'Instagram, Facebook, WhatsApp сервис']);
  });

  it('lists the popular services first, and says so when nothing matches', async () => {
    fake();
    const app = start({ scenario: 'healthy' });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    app.button('Сервис')!.click();
    await tick(50);
    expect(app.all('.ms-svc-r').slice(0, 3).map(read)).toEqual(['Discord', 'Instagram, Facebook, WhatsApp', 'X (Twitter)']);
    await app.type('#vx-ms-svc', 'zzzz');
    await tick(20);
    expect(app.text()).toContain('Такого сервиса нет в списке роутера');
    // By its category's id as well.
    await app.type('#vx-ms-svc', 'gov');
    await tick(20);
    expect(app.all('.ms-svc-r').map(read)).toEqual(['Госсайты России']);
  });

  it('names a service in the list, and shows no service button when the router offers none', async () => {
    fake();
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => (m === 'rules' ? { ...r, direct: ['geosite:category-gov-ru'], proxy: ['geosite:hodca'], catalog: [] } : undefined),
    });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    expect(app.all('.ms-list li').map(read)).toEqual(['Госсайты России сервис']);
    expect(app.button('Сервис')).toBeUndefined();
    app.button('Через VPN: 1')!.click();
    await tick(50);
    expect(app.all('.ms-list li').map(read)).toEqual(['HODCA сервис']);
  });

  // A service the router's geo file lost since (an update): kept in the
  // list, marked as not routed until the file has it again.
  it('marks a service the router no longer has', async () => {
    fake();
    const app = start({
      scenario: 'healthy',
      twist: (m, r) => (m === 'rules' ? { ...r, direct: ['geosite:hdrezka', 'bank.example'], proxy: [], missing: ['geosite:hdrezka'] } : undefined),
    });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    expect(app.all('.ms-list li').map(read)).toEqual(['HDRezka сервис · нет у роутера', 'bank.example']);
  });

  it('says in plain words when the list cannot be read, and retries it', async () => {
    fake();
    let broken = true;
    const app = start({ scenario: 'healthy', twist: (m) => (m === 'rules' && broken ? new Error('RPC call to vectra/rules failed with ubus code 6: Permission denied') : undefined) });
    await tick(300);
    app.button('Настроить')!.click();
    await tick(200);
    expect(app.text()).toContain('Не удалось загрузить ваши сайты.');
    expect(app.text()).not.toMatch(/ubus|RPC|vectra\/rules/);
    broken = false;
    app.button('Повторить')!.click();
    await tick(200);
    expect(app.all('.ms-list li').map(read)).toContain('sberbank.ru');
  });
});
