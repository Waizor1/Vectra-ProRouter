// Services with a country of their own, in the simple view: the default
// first and named by its country, a change asked first and seen landing, a
// stale choice said plainly, nothing at all where there is nothing to choose.
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { CallFn, ReadData } from '../src/api/types';
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

/** What a reader sees: no-break spaces read as spaces. */
const words = (x: string | null | undefined) => (x ?? '').replace(/\u00a0/g, ' ');

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(call?: CallFn) {
  const mock: Mock = createMock({ scenario: 'healthy', latencyMs: 0, live: false, applyMs: 10 });
  const calls: string[] = [];
  const params: unknown[] = [];
  const base = call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: (m, p) => (calls.push(m), params.push(p), base(m, p)), lang: 'ru' });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  return { root, $, all, calls, params };
}

function world(edit: (w: ReadData) => void): CallFn {
  const w = clone(FIXTURES);
  edit(w);
  const fallback = createMock({ latencyMs: 0, live: false });
  return (m, p) => (m in w ? Promise.resolve(clone(w[m as keyof ReadData])) : fallback.call(m, p));
}

it('offers a country per service, the entry’s own path first and named by its country', async () => {
  const app = start();
  await settle();
  const rows = app.all('.sv-svc-r');
  expect(rows.map((r) => r.querySelector('.sv-svc-n')?.textContent)).toEqual(['YouTube', 'TikTok', 'Telegram']);
  const yt = rows[0].querySelector('select')!;
  expect(yt.value).toBe('');
  expect(words(yt.options[0].textContent)).toBe('По умолчанию · 🇷🇺 Россия');
  expect(yt.getAttribute('aria-label')).toBe('Страна для YouTube');
  // TikTok runs through Germany in the fixture; the other countries follow.
  const tk = rows[1].querySelector('select')!;
  expect(tk.value).toBe('DE');
  expect(words(tk.options[0].textContent)).toBe('По умолчанию · 🇧🇾 Беларусь');
  expect(Array.from(tk.options).map((o) => o.value)).toEqual(['', 'AE', 'BY', 'DE', 'FI', 'FR', 'NL', 'PL', 'RU', 'TR', 'US']);
});

it('names where a country really leaves when the router saw it leave elsewhere', async () => {
  const app = start(
    world((w) => {
      const tk = w.services.services.find((s) => s.id === 'tiktok')!;
      tk.egress = { TR: 'PL' };
    }),
  );
  await settle();
  const tk = app.all('.sv-svc-r')[1].querySelector('select')!;
  const tr = Array.from(tk.options).find((o) => o.value === 'TR')!;
  expect(words(tr.textContent)).toBe('🇹🇷 Турция (выход: 🇵🇱 Польша)');
  // One way to say a country everywhere (the review of r29–r31): the flag
  // stays with its name, as on the server card.
  expect(tr.textContent).toContain('🇹🇷\u00a0Турция');
});

it('shows nothing where the operator’s policy routes the router', async () => {
  const app = start(world((w) => (w.services = { available: false, services: [] })));
  await settle();
  expect(app.$('.sv-svc')).toBeNull();
  expect(app.$('.sv')).not.toBeNull();
});

it('says a choice the router does not run is on the default, and lets it be cleared', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start(
    world((w) => {
      const tk = w.services.services.find((s) => s.id === 'tiktok')!;
      tk.choice = 'NL';
      tk.active = false;
      tk.stale = true;
    }),
  );
  await vi.advanceTimersByTimeAsync(20);
  const tk = app.all('.sv-svc-r')[1];
  const sel = tk.querySelector('select')!;
  expect(sel.value).toBe('NL');
  expect(tk.querySelector('.sv-svc-w')?.textContent).toBe('Эта страна сейчас недоступна для сервиса, поэтому он работает по умолчанию.');
  sel.value = '';
  sel.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(100);
  const i = app.calls.indexOf('set_service');
  expect(i).toBeGreaterThanOrEqual(0);
  expect(app.params[i]).toEqual({ id: 'tiktok', country: '' });
});

it('shows a service the entry has no path for only while it holds a choice, marked unavailable', async () => {
  const app = start(
    world((w) => {
      const yt = w.services.services.find((s) => s.id === 'youtube')!;
      yt.countries = [];
      yt.defaultCountry = null;
      const tk = w.services.services.find((s) => s.id === 'tiktok')!;
      tk.countries = [];
      tk.defaultCountry = null;
      tk.choice = 'NL';
      tk.active = false;
      tk.stale = true;
    }),
  );
  await settle();
  const rows = app.all('.sv-svc-r');
  expect(rows.map((r) => r.querySelector('.sv-svc-n')?.textContent)).toEqual(['TikTok', 'Telegram']);
  const opts = Array.from(rows[0].querySelector('select')!.options);
  expect(opts.map((o) => [o.value, o.disabled, words(o.textContent)])).toEqual([
    ['', false, 'По умолчанию'],
    ['NL', true, '🇳🇱 Нидерланды · недоступна'],
  ]);
  expect(rows[0].querySelector('select')!.value).toBe('NL');
});

it('changes a service’s country through the router, after asking, and shows it landed', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start();
  await vi.advanceTimersByTimeAsync(20);
  const yt = app.all('.sv-svc-r')[0].querySelector('select')!;
  yt.value = 'DE';
  yt.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  const dialog = app.$('[role="alertdialog"]')!;
  expect(dialog.textContent).toContain('Сменить страну для YouTube?');
  expect(dialog.textContent).toContain('Интернет пропадёт на несколько секунд.');
  dialog.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  expect(app.calls).toContain('set_service');
  expect(app.root.textContent).toContain('Страна сервиса сохранена');
  expect(app.all('.sv-svc-r')[0].querySelector('select')!.value).toBe('DE');
});
