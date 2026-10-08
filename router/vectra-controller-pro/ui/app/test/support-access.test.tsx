// Support access is the router owner's: the panel's support may run commands on
// the router (as root) only while they allow it (contract: "Support shell").
// The main screen's footer has the switch with one plain sentence of what it
// allows; opening it is confirmed, closing it is not, and a router that has no
// such switch (an older vctl) shows none.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CallFn, ReadData, Status } from '../src/api/types';
import { LANGS, makeT, withParams, type Lang } from '../src/i18n';
import { FIXTURES } from '../src/mock/fixtures';
import { clone } from '../src/mock/scenarios';
import { createMock, type Mock } from '../src/mock/transport';
import { mount } from '../src/mount';

let cleanup: (() => void)[] = [];

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  vi.useRealTimers();
  document.body.innerHTML = '';
});

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(opts: { lang?: Lang; call?: CallFn; locked?: boolean } = {}) {
  localStorage.setItem('vectra.ui.lang', opts.lang ?? 'ru');
  const mock: Mock = createMock({ scenario: 'healthy', latencyMs: 0, live: false, applyMs: 10, locked: opts.locked });
  const calls: [string, Record<string, unknown> | undefined][] = [];
  const base = opts.call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: (m, p) => (calls.push([m, p]), base(m, p)), setPassword: mock.setPassword, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const sw = () => $<HTMLButtonElement>('.sv-foot .sv-sa [role="switch"]');
  const text = () => root.querySelector('.vx')?.textContent ?? '';
  const sent = () => calls.filter(([m]) => m === 'set_remote_shell').map(([, p]) => p);
  const confirm = () => $('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  const cancel = () => $('[role="alertdialog"]')!.querySelectorAll('button')[0].click();
  return { root, $, sw, text, sent, confirm, cancel, mock };
}

/** A router answering from a hand-edited copy of the fixtures. */
function world(edit: (w: ReadData) => void, onSet?: (p: Record<string, unknown>) => unknown): CallFn {
  const w = clone(FIXTURES);
  edit(w);
  return async (m, p) => {
    if (m === 'set_remote_shell') return onSet ? onSet(p ?? {}) : { ok: true, code: 'remote_shell_set', detail: null };
    return clone((w as unknown as Record<string, unknown>)[m]);
  };
}

describe('support access', () => {
  it('the mock router keeps the switch as vctl does', async () => {
    const m = createMock({ scenario: 'healthy', latencyMs: 0, live: false });
    cleanup.push(m.dispose);
    expect(((await m.call('status')) as Status).remoteShell).toBe(false);
    expect(await m.call('set_remote_shell', { on: 'yes' })).toMatchObject({ ok: false, code: 'invalid_params' });
    expect(await m.call('set_remote_shell', {})).toMatchObject({ ok: false, code: 'invalid_params' });
    expect(await m.call('set_remote_shell', { on: true, now: true })).toMatchObject({ ok: false, code: 'invalid_params' });
    expect(await m.call('set_remote_shell', { on: true })).toMatchObject({ ok: true, code: 'remote_shell_set' });
    expect(((await m.call('status')) as Status).remoteShell).toBe(true);
    expect(await m.call('set_remote_shell', { on: false })).toMatchObject({ ok: true, code: 'remote_shell_set' });
    expect(((await m.call('status')) as Status).remoteShell).toBe(false);
  });

  it('shows the switch off in the footer, with what it allows', async () => {
    const app = start();
    await settle();
    const sw = app.sw()!;
    expect(sw).not.toBeNull();
    expect(sw.getAttribute('aria-checked')).toBe('false');
    expect(sw.textContent).toBe('Доступ поддержки к роутеру');
    const hint = app.$('#' + sw.getAttribute('aria-describedby'))!;
    expect(hint.textContent).toBe(
      'Поддержка Vectra сможет выполнять команды на роутере, чтобы разобраться с неполадкой. Включайте, когда об этом попросит поддержка.',
    );
  });

  it('opens access only once the owner confirms, then shows it open', async () => {
    const app = start();
    await settle();
    app.sw()!.click();
    await settle();
    const dialog = app.$('[role="alertdialog"]')!;
    expect(dialog.querySelector('h2')?.textContent).toBe('Открыть доступ поддержке?');
    expect(dialog.textContent).toContain('Поддержка Vectra сможет выполнять на роутере любые команды. Закрыть доступ можно здесь же в любой момент.');
    expect(app.sent()).toEqual([]);
    // Cancelled: nothing is sent, the switch stays off.
    app.cancel();
    await settle();
    expect(app.sent()).toEqual([]);
    expect(app.sw()!.getAttribute('aria-checked')).toBe('false');
    app.sw()!.click();
    await settle();
    app.confirm();
    await settle(20);
    expect(app.sent()).toEqual([{ on: true }]);
    expect(app.sw()!.getAttribute('aria-checked')).toBe('true');
    expect(app.text()).toContain('Доступ поддержки открыт');
  });

  it('closes access at once, without asking', async () => {
    const app = start({ call: world((w) => void (w.status.remoteShell = true)) });
    await settle();
    expect(app.sw()!.getAttribute('aria-checked')).toBe('true');
    app.sw()!.click();
    await settle(20);
    expect(app.$('[role="alertdialog"]')).toBeNull();
    expect(app.sent()).toEqual([{ on: false }]);
    expect(app.text()).toContain('Доступ поддержки закрыт');
  });

  it('stays as it was when the router could not change it, and says so without the raw detail', async () => {
    const app = start({ call: world(() => {}, () => ({ ok: false, code: 'internal', detail: 'uci: commit failed' })) });
    await settle();
    app.sw()!.click();
    await settle();
    app.confirm();
    await settle(20);
    expect(app.sent()).toEqual([{ on: true }]);
    expect(app.sw()!.getAttribute('aria-checked')).toBe('false');
    expect(app.text()).not.toContain('uci: commit failed');
    expect(app.text()).not.toContain('Доступ поддержки открыт');
  });

  it('is there under the operator lock too: the switch is the owner’s', async () => {
    const app = start({ locked: true });
    await settle();
    app.sw()!.click();
    await settle();
    app.confirm();
    await settle(20);
    expect(app.sent()).toEqual([{ on: true }]);
    expect(app.sw()!.getAttribute('aria-checked')).toBe('true');
  });

  it('is not shown by a router without the switch (an older vctl)', async () => {
    const app = start({ call: world((w) => void delete (w.status as Partial<Status>).remoteShell) });
    await settle();
    expect(app.text()).toContain('Сменить пароль'); // the footer is there…
    expect(app.$('.sv-sa')).toBeNull(); // …without the switch
  });

  for (const lang of LANGS) {
    it(`speaks ${lang}`, async () => {
      // The fixture router is Vectra's: its name in every sentence.
      const t = withParams(makeT(lang), { brand: 'Vectra' });
      const app = start({ lang });
      await settle();
      const sw = app.sw()!;
      expect(sw.textContent).toBe(t('sa.t'));
      expect(app.$('#' + sw.getAttribute('aria-describedby'))?.textContent).toBe(t('sa.d'));
      if (lang !== 'ru') expect(sw.textContent! + t('sa.d') + t('sa.onQ') + t('sa.onQ.d')).not.toMatch(/[а-яё]/i);
    });
  }
});
