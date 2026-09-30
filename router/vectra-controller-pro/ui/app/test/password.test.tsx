// The router's password: on a router out of the box anyone on the LAN can open
// its settings (OpenWrt ships without one). The wizard asks for one first and
// cannot be walked past without it; the main screen says so calmly and offers
// to set one, and always to change it. The password goes to LuCI's own change
// (luci.setPassword, handed over by the LuCI view) — never to vctl.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn, SetPasswordFn, Setup } from '../src/api/types';
import type { Scenario } from '../src/mock/scenarios';
import { createMock, type MockOptions } from '../src/mock/transport';
import { mount } from '../src/mount';

/** Text as a person reads it: the word joiner that holds "Wi-Fi" together (i18n `glue`) is invisible. */
const read = (n: Node | null | undefined) => n?.textContent?.replace(/⁠/g, '');

let cleanup: (() => void)[] = [];
beforeEach(() => localStorage.clear());
afterEach(async () => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  if (vi.isFakeTimers()) await vi.advanceTimersByTimeAsync(1000);
  vi.useRealTimers();
  document.body.innerHTML = '';
});

type Answer = Record<string, unknown>;
type Twist = (method: string, answer: Answer) => Answer | void;

/**
 * The app on a mock router, with LuCI's password change handed over as the
 * LuCI view does (`setPassword: null`: a host page that hands none).
 */
function start(opts: { scenario?: Scenario; twist?: Twist; mock?: Partial<MockOptions>; setPassword?: SetPasswordFn | null } = {}) {
  localStorage.setItem('vectra.ui.lang', 'ru');
  const mock = createMock({ scenario: opts.scenario ?? 'unboxed', latencyMs: 0, live: false, applyMs: 10, ...opts.mock });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const passwords: string[] = [];
  const call: CallFn = async (m, p) => {
    calls.push([m, p]);
    const r = (await mock.call(m, p)) as Answer;
    return opts.twist?.(m, r) ?? r;
  };
  const change = opts.setPassword === undefined ? mock.setPassword : opts.setPassword;
  const setPassword = change ? (pw: string) => (passwords.push(pw), change(pw)) : undefined;
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
  const type = async (sel: string, value: string) => {
    const el = $<HTMLInputElement>(sel)!;
    el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    for (let i = 0; i < 3; i++) await Promise.resolve();
  };
  const submit = (sel: string) => $<HTMLFormElement>(sel)!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  const errors = () => all('.fld-err').map(read);
  return { root, $, all, text, verdict, button, type, submit, errors, calls, passwords, mock };
}

const fake = () => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);

describe('the mock router’s password', () => {
  it('takes LuCI’s change like a router: setup then says a password is set; failures as LuCI reports them', async () => {
    const mock = (fails?: 'refused' | 'denied' | 'offline') => {
      const m = createMock({ scenario: 'unboxed', latencyMs: 0, live: false, passwordFails: fails });
      cleanup.push(m.dispose);
      return m;
    };
    const ok = mock();
    expect(((await ok.call('setup')) as Setup).passwordSet).toBe(false);
    expect(await ok.setPassword('correct horse')).toBe(true);
    expect(((await ok.call('setup')) as Setup).passwordSet).toBe(true);
    expect(await mock('refused').setPassword('correct horse')).toBe(false);
    await expect(mock('denied').setPassword('correct horse')).rejects.toThrow(/luci\/setPassword failed with ubus code 6/);
    await expect(mock('offline').setPassword('correct horse')).rejects.toThrow(/aborted/);
    // A router with Vectra's plugin missing still has LuCI: the change works.
    const down = createMock({ scenario: 'down', latencyMs: 0, live: false });
    cleanup.push(down.dispose);
    expect(await down.setPassword('correct horse')).toBe(true);
  });
});

describe('the wizard’s password step', () => {
  it('comes first on a router without a password, and cannot be walked past without one', async () => {
    fake();
    const app = start();
    await tick(200);
    expect(app.verdict()).toBe('Настроим роутер');
    expect(read(app.$('.wz-hello .lede'))).toContain('Зададим пароль роутера');
    expect(app.all('.wz-list li').map(read)).toEqual(['Пароль: не задан', 'Интернет: пока нет', 'Wi-Fi: не настроено', 'Vectra: не настроено', 'Сервер: не настроено']);
    app.button('Начать')!.click();
    await tick(200);

    expect(app.verdict()).toBe('Задайте пароль роутера');
    expect(app.text()).toContain('например, дети');
    // No way past it: no "Skip", and the later steps are shut in the stepper.
    expect(app.button('Пропустить шаг')).toBeUndefined();
    const steps = app.all('.wz-steps button');
    expect(steps.map((b) => b.hasAttribute('disabled'))).toEqual([false, true, true, true, true]);
    expect(app.$<HTMLInputElement>('#vx-pw-1')!.type).toBe('password');
    expect(app.$<HTMLInputElement>('#vx-pw-2')!.type).toBe('password');
    expect(app.$<HTMLInputElement>('#vx-pw-1')!.getAttribute('autocomplete')).toBe('new-password');
    expect(app.text()).toContain('Не короче 8 символов.');

    // Each mistake beside its field, and nothing sent.
    app.submit('.wz-form');
    await tick(50);
    expect(app.errors()).toEqual(['Введите пароль.']);
    await app.type('#vx-pw-1', 'short');
    app.submit('.wz-form');
    await tick(50);
    expect(app.errors()).toEqual(['Пароль короче 8 символов.']);
    await app.type('#vx-pw-1', 'correct horse');
    app.submit('.wz-form');
    await tick(50);
    expect(app.errors()).toEqual(['Повторите пароль.']);
    await app.type('#vx-pw-2', 'correct hose');
    app.submit('.wz-form');
    await tick(50);
    expect(app.errors()).toEqual(['Пароли не совпадают.']);
    expect(app.$('#vx-pw-2')!.getAttribute('aria-invalid')).toBe('true');
    expect(app.passwords).toEqual([]);

    // Show it, to check it: both fields at once.
    app.$<HTMLInputElement>('.wz-card .chkbx input')!.click();
    await tick(50);
    expect(app.$<HTMLInputElement>('#vx-pw-1')!.type).toBe('text');
    expect(app.$<HTMLInputElement>('#vx-pw-2')!.type).toBe('text');

    await app.type('#vx-pw-2', 'correct horse');
    app.button('Сохранить пароль')!.click();
    await tick(200);
    expect(app.passwords).toEqual(['correct horse']);
    // Said plainly, the fields gone, and the way on.
    expect(app.verdict()).toBe('Пароль сохранён');
    expect(app.text()).toContain('Теперь роутер будет спрашивать этот пароль при входе в настройки.');
    expect(app.$('#vx-pw-1')).toBeNull();
    expect(app.all('.wz-steps li')[0].classList.contains('ok')).toBe(true);
    await tick(3000);
    expect(app.all('.wz-steps button').map((b) => b.hasAttribute('disabled'))).toEqual([false, false, false, false, false]);
    // The password went to LuCI only: never to vctl, never into the page's storage.
    expect(JSON.stringify(app.calls)).not.toContain('correct horse');
    expect(JSON.stringify(localStorage)).not.toContain('correct horse');
    app.button('Далее')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Интернета пока нет');
    // Back goes to the password step, which says it is done.
    app.button('Назад')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Пароль сохранён');
  });

  it.each([
    ['refused', 'Роутер не принял пароль. Попробуйте ещё раз.'],
    ['denied', 'Сессия истекла. Обновите страницу, войдите заново и повторите.'],
    ['offline', 'Нет связи с роутером. Проверьте подключение и сохраните пароль ещё раз.'],
  ] as const)('says in plain words when LuCI’s change fails (%s), beside the fields, and keeps them for another try', async (fails, said) => {
    fake();
    const app = start({ mock: { passwordFails: fails } });
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    await app.type('#vx-pw-1', 'correct horse');
    await app.type('#vx-pw-2', 'correct horse');
    app.button('Сохранить пароль')!.click();
    await tick(200);
    expect(app.passwords).toEqual(['correct horse']);
    expect(app.verdict()).toBe('Задайте пароль роутера');
    expect(read(app.$('.wz-card [role="alert"]'))).toBe(said);
    expect(app.text()).not.toMatch(/ubus|RPC|Permission|XHR|luci/);
    expect(app.$<HTMLInputElement>('#vx-pw-1')!.value).toBe('correct horse');
    expect(app.$<HTMLInputElement>('#vx-pw-2')!.value).toBe('correct horse');
    expect(app.button('Пропустить шаг')).toBeUndefined();
  });

  it('is not there on a router that has a password, nor where the page cannot set one', async () => {
    fake();
    const boxed = start({ scenario: 'boxed' });
    await tick(200);
    expect(boxed.all('.wz-list li').map(read)).toEqual(['Интернет: пока нет', 'Wi-Fi: не настроено', 'Vectra: не настроено', 'Сервер: не настроено']);
    cleanup.forEach((fn) => fn());
    cleanup = [];
    const noLuci = start({ setPassword: null });
    await tick(200);
    expect(noLuci.all('.wz-list li').map(read)).toEqual(['Интернет: пока нет', 'Wi-Fi: не настроено', 'Vectra: не настроено', 'Сервер: не настроено']);
    noLuci.button('Начать')!.click();
    await tick(200);
    expect(noLuci.verdict()).toBe('Интернета пока нет');
  });

  it('opens the wizard by itself on a router that has everything but a password, and starts there', async () => {
    fake();
    const app = start({ scenario: 'healthy', twist: (m, r) => (m === 'setup' ? { ...r, done: false, passwordSet: false } : undefined) });
    await tick(300);
    expect(app.verdict()).toBe('Настроим роутер');
    expect(app.all('.wz-list li').map(read)[0]).toBe('Пароль: не задан');
    app.button('Начать')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Задайте пароль роутера');
  });

  it('takes a person who came for another step through the password first', async () => {
    fake();
    // Set up already: the wizard waits for the footer, and starts at the password.
    const app = start({ scenario: 'healthy', twist: (m, r) => (m === 'setup' ? { ...r, passwordSet: false } : undefined) });
    await tick(300);
    app.button('Открыть мастер настройки')!.click();
    await tick(200);
    app.button('Начать')!.click();
    await tick(200);
    expect(app.verdict()).toBe('Задайте пароль роутера');
    cleanup.forEach((fn) => fn());
    cleanup = [];

    // Not linked: "Connect to Vectra" opens the wizard at the password, not past it.
    const claim = { state: 'unclaimed', code: '7KQ4M9XD', qr: null, expiresAt: '2099-01-01T00:00:00Z', botUrl: null, owner: null };
    const unlinked = start({
      scenario: 'empty',
      twist: (m, r) => (m === 'setup' ? { ...r, passwordSet: false, vectra: { ...(r.vectra as Answer), linked: false, owner: null, claim } } : undefined),
    });
    await tick(300);
    unlinked.button('Подключить к Vectra')!.click();
    await tick(200);
    expect(unlinked.verdict()).toBe('Задайте пароль роутера');
    expect(unlinked.all('.wz-steps li')[0].classList.contains('on')).toBe(true);
  });
});
