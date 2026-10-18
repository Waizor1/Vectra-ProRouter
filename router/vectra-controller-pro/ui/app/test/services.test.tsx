// Services, in the simple view and in Pro. Since r26 a row is the catalog's: its
// title, its group, the modes it offers; a row without a title is an r25
// service with a country to pick. The default first and named by what it
// means, a change asked first and seen landing, a stale choice said plainly,
// the stable exit told without its address, nothing at all where there is
// nothing to choose.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { normalize } from '../src/api/normalize';
import type { CallFn, ReadData, ServiceInfo, Services } from '../src/api/types';
import { buildWorld, type Scenario } from '../src/mock/scenarios';
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

function start(call?: CallFn, scenario: Scenario = 'healthy', lang = 'ru') {
  const mock: Mock = createMock({ scenario, latencyMs: 0, live: false, applyMs: 10 });
  const calls: string[] = [];
  const params: unknown[] = [];
  const base = call ?? mock.call;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call: (m, p) => (calls.push(m), params.push(p), base(m, p)), lang });
  cleanup.push(() => {
    unmount();
    mock.dispose();
  });
  const root = host.shadowRoot!;
  const $ = <E extends Element = HTMLElement>(sel: string) => root.querySelector<E>(sel);
  const all = (sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
  return { root, $, all, calls, params };
}

function world(edit: (w: ReadData) => void, scenario: Scenario = 'healthy'): CallFn {
  const w = buildWorld(scenario)!;
  edit(w);
  const fallback = createMock({ scenario, latencyMs: 0, live: false });
  return (m, p) => (m in w ? Promise.resolve(JSON.parse(JSON.stringify(w[m as keyof ReadData]))) : fallback.call(m, p));
}

/** What a router of r25 answers: the three services with a country, none of the catalog's fields. */
function r25(w: ReadData) {
  w.services = {
    available: true,
    services: w.services.services
      .filter((s) => ['youtube', 'tiktok', 'telegram'].indexOf(s.id) >= 0)
      .map(({ id, choice, defaultCountry, countries, active, stale, egress }) => ({ id, choice, defaultCountry, countries, active, stale, egress })),
  } as unknown as Services;
}

/** The row for `id` in the world the test builds. */
const row = (w: ReadData, id: string) => w.services.services.find((s) => s.id === id)!;
const names = (rows: HTMLElement[]) => rows.map((r) => r.querySelector('.sv-svc-n')?.textContent);

// ── an r25 service: no title, a country to pick, as it always was ────────────

it('offers a country per service, the entry’s own path first and named by its country', async () => {
  const app = start(world(r25));
  await settle();
  const rows = app.all('.sv-svc-r');
  expect(names(rows)).toEqual(['YouTube', 'TikTok', 'Telegram']);
  const yt = rows[0].querySelector('select')!;
  expect(yt.value).toBe('');
  expect(words(yt.options[0].textContent)).toBe('По умолчанию · 🇷🇺 Россия');
  expect(yt.getAttribute('aria-label')).toBe('Страна для YouTube');
  // TikTok runs through Germany in the fixture; the other countries follow.
  const tk = rows[1].querySelector('select')!;
  expect(tk.value).toBe('DE');
  expect(words(tk.options[0].textContent)).toBe('По умолчанию · 🇧🇾 Беларусь');
  expect(Array.from(tk.options).map((o) => o.value)).toEqual(['', 'AE', 'BY', 'DE', 'FI', 'FR', 'NL', 'PL', 'RU', 'TR', 'US']);
  // Nothing of the catalog's: no group, no icon, no stable exit.
  expect(app.$('.sv-svc-g')).toBeNull();
  expect(app.$('.sv-svc svg')).toBeNull();
  expect(app.$('.sv-svc [role="switch"]')).toBeNull();
});

it('names where a country really leaves when the router saw it leave elsewhere', async () => {
  const app = start(
    world((w) => {
      row(w, 'tiktok').egress = { TR: 'PL' };
    }),
  );
  await settle();
  const tk = app.all('.sv-svc-r')[1].querySelector('select')!;
  const tr = Array.from(tk.options).find((o) => o.value === 'TR')!;
  expect(words(tr.textContent)).toBe('🇹🇷 Турция (выход: 🇵🇱 Польша)');
  // One way to say a country everywhere (the review of r29–r31): the flag
  // stays with its name, as on the server card.
  expect(tr.textContent).toContain('🇹🇷 Турция');
});

it('shows nothing where the operator’s policy routes the router', async () => {
  const app = start(world((w) => (w.services = { available: false, services: [], groups: [], stableExit: null })));
  await settle();
  expect(app.$('.sv-svc')).toBeNull();
  expect(app.$('.sv')).not.toBeNull();
});

it('says a choice the router does not run is on the default, and lets it be cleared', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start(
    world((w) => {
      r25(w);
      const tk = row(w, 'tiktok');
      tk.choice = 'NL';
      tk.active = false;
      tk.stale = true;
    }),
  );
  await vi.advanceTimersByTimeAsync(20);
  const tk = app.all('.sv-svc-r')[1];
  const sel = tk.querySelector('select')!;
  expect(sel.value).toBe('NL');
  // Where it runs meanwhile: its default, by the country the router names for it.
  expect(words(tk.querySelector('.sv-svc-w')?.textContent)).toBe('Эта страна сейчас недоступна для сервиса — пока он работает по умолчанию, через 🇧🇾 Беларусь.');
  sel.value = '';
  sel.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(100);
  const i = app.calls.indexOf('set_service');
  expect(i).toBeGreaterThanOrEqual(0);
  expect(app.params[i]).toEqual({ id: 'tiktok', country: '' });
  expect(app.calls).not.toContain('set_service_mode');
});

it('shows a service the entry has no path for only while it holds a choice, marked unavailable', async () => {
  const app = start(
    world((w) => {
      r25(w);
      const yt = row(w, 'youtube');
      yt.countries = [];
      yt.defaultCountry = null;
      const tk = row(w, 'tiktok');
      tk.countries = [];
      tk.defaultCountry = null;
      tk.choice = 'NL';
      tk.active = false;
      tk.stale = true;
    }),
  );
  await settle();
  const rows = app.all('.sv-svc-r');
  expect(names(rows)).toEqual(['TikTok', 'Telegram']);
  const opts = Array.from(rows[0].querySelector('select')!.options);
  expect(opts.map((o) => [o.value, o.disabled, words(o.textContent)])).toEqual([
    ['', false, 'По умолчанию'],
    ['NL', true, '🇳🇱 Нидерланды · недоступна'],
  ]);
  expect(rows[0].querySelector('select')!.value).toBe('NL');
  // No path of its own: it goes with everything else.
  expect(rows[0].querySelector('.sv-svc-w')?.textContent).toBe('Эта страна сейчас недоступна для сервиса — пока он работает по умолчанию, через основной VPN.');
});

it('says a stale «Нейросети» choice runs on the Kazakh default when that is what runs', async () => {
  const app = start(
    world((w) => {
      r25(w);
      w.services.services.push({ id: 'ai', choice: 'TR', defaultCountry: 'KZ', countries: [], active: false, stale: true, egress: {} } as unknown as ServiceInfo);
    }),
  );
  await settle();
  const ai = app.all('.sv-svc-r').find((r) => r.querySelector('.sv-svc-n')?.textContent === 'Нейросети')!;
  expect(words(ai.querySelector('.sv-svc-w')?.textContent)).toBe('Эта страна сейчас недоступна для сервиса — пока он работает по умолчанию, через 🇰🇿 Казахстан.');
});

it('keeps a row without a title as it was when the router titles the others', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start(
    world((w) => {
      // An empty title is what the router sends for an r25 service.
      row(w, 'youtube').title = {};
    }),
  );
  await vi.advanceTimersByTimeAsync(20);
  const [yt, tk] = app.all('.sv-svc-r');
  const sel = yt.querySelector('select')!;
  expect(words(sel.options[0].textContent)).toBe('По умолчанию · 🇷🇺 Россия');
  expect(Array.from(sel.options).map((o) => o.value)).toEqual(['', 'AE', 'BY', 'DE', 'FI', 'FR', 'NL', 'PL', 'RU', 'TR', 'US']);
  expect(sel.getAttribute('aria-label')).toBe('Страна для YouTube');
  // The titled one beside it offers its modes too.
  expect(Array.from(tk.querySelector('select')!.options).map((o) => o.value)).toContain('main');
  sel.value = 'DE';
  sel.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  expect(app.$('[role="alertdialog"]')!.textContent).toContain('Сменить страну для YouTube?');
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(100);
  expect(app.calls).not.toContain('set_service_mode');
  expect(app.params[app.calls.indexOf('set_service')]).toEqual({ id: 'youtube', country: 'DE' });
});

// ── the catalog's rows ───────────────────────────────────────────────────────

it('shows the catalog’s rows by their title, the games under their heading and hint, each list in order', async () => {
  // The router's order is not the display order: `order` is.
  const app = start(world((w) => w.services.services.reverse()));
  await settle();
  const groups = app.all('.sv-svc-g');
  expect(groups).toHaveLength(1);
  expect(groups[0].querySelector('.sv-svc-gh')?.textContent).toBe('Игры');
  expect(groups[0].querySelector('.hint')?.textContent).toBe('Постоянный IP — лаунчеры не выкидывают');
  expect(names(Array.from(groups[0].querySelectorAll<HTMLElement>('.sv-svc-r')))).toEqual(['Steam']);
  // The top level is the services without a group, under the card's own line.
  expect(names(app.all('.sv-svc > div:not(.sv-svc-g) .sv-svc-r'))).toEqual(['YouTube', 'TikTok', 'Telegram']);
});

it('puts every launcher of the games scenario in the group, by order', async () => {
  const app = start(world((w) => w.services.services.reverse(), 'games'), 'games');
  await settle();
  const g = app.$('.sv-svc-g')!;
  expect(names(Array.from(g.querySelectorAll<HTMLElement>('.sv-svc-r')))).toEqual(['Steam', 'Epic Games', 'Riot Games', 'Battle.net', 'Star Citizen']);
});

it('says the title in the reader’s language, Russian where the catalog has not that one', async () => {
  const w = (w: ReadData) => {
    row(w, 'steam').title = { ru: 'Стим', en: 'Steam' };
    w.services.groups[0].title = { ru: 'Игры', en: 'Games' };
    w.services.groups[0].hint = { ru: 'Постоянный IP', en: 'One steady IP' };
  };
  const en = start(world(w), 'healthy', 'en');
  const zh = start(world(w), 'healthy', 'zh');
  await settle();
  expect(en.$('.sv-svc-gh')?.textContent).toBe('Games');
  expect(en.$('.sv-svc-g .sv-svc-n')?.textContent).toBe('Steam');
  expect(en.$('.sv-svc-g .hint')?.textContent).toBe('One steady IP');
  // zh has no title for Steam and no group title: Russian, not nothing.
  expect(zh.$('.sv-svc-gh')?.textContent).toBe('Игры');
  expect(zh.$('.sv-svc-g .sv-svc-n')?.textContent).toBe('Стим');
});

it('offers exactly the modes a row lists, and «Авто» named by what it means', async () => {
  const app = start(undefined, 'games');
  await settle();
  const sel = (id: string) => app.$<HTMLSelectElement>('#vx-svc-' + id)!;
  const shape = (id: string) => Array.from(sel(id).options).map((o) => [o.value, words(o.textContent)]);
  expect(shape('steam')).toEqual([
    ['', 'Авто (VPN · постоянный IP)'],
    ['stable', 'VPN · постоянный IP'],
    ['direct', 'Напрямую'],
    ['main', 'Как основной VPN'],
  ]);
  // Star Citizen does not run without the VPN: no «Напрямую».
  expect(shape('starcitizen').map((o) => o[0])).toEqual(['', 'stable', 'main']);
  // A service of the entry's own path: «Авто» is where that path leaves, the countries follow the modes.
  expect(shape('youtube').slice(0, 3)).toEqual([
    ['', 'Авто (🇷🇺 Россия)'],
    ['main', 'Как основной VPN'],
    ['AE', '🇦🇪 ОАЭ'],
  ]);
  expect(Array.from(sel('youtube').options).some((o) => o.value === 'location')).toBe(false);
  // The name beside the select names it: no second label to say less.
  expect(sel('steam').getAttribute('aria-label')).toBeNull();
  expect(app.$('label[for="vx-svc-steam"]')?.textContent).toBe('Steam');
  // «Авто» in the other tongues, and by the row's default.
  const w = (x: ReadData) => {
    row(x, 'steam').default = 'direct';
    row(x, 'epic').default = 'main';
    row(x, 'riot').default = 'location';
  };
  const en = start(world(w, 'games'), 'games', 'en');
  await settle();
  const enSel = (id: string) => Array.from(en.$<HTMLSelectElement>('#vx-svc-' + id)!.options)[0].textContent;
  expect(enSel('steam')).toBe('Auto (Direct)');
  expect(enSel('epic')).toBe('Auto (Like the main VPN)');
  expect(enSel('riot')).toBe('Auto');
  expect(enSel('youtube')).toBe('Auto (🇷🇺\u00a0Russia)');
});

it('shows the current choice: the owner’s country, else the mode that runs because they chose it, else «Авто»', async () => {
  const app = start(
    world((w) => {
      row(w, 'telegram').choice = null;
      // Connect's location, which this UI does not offer but must not lie about.
      const yt = row(w, 'youtube');
      yt.mode = 'location';
      yt.chosen = true;
    }, 'games'),
    'games',
  );
  await settle();
  const sel = (id: string) => app.$<HTMLSelectElement>('#vx-svc-' + id)!;
  expect(sel('steam').value).toBe('direct');
  expect(sel('tiktok').value).toBe('DE');
  expect(sel('telegram').value).toBe('');
  expect(sel('epic').value).toBe('');
  expect(sel('youtube').value).toBe('location');
  const loc = Array.from(sel('youtube').options).find((o) => o.value === 'location')!;
  expect([loc.disabled, loc.textContent]).toEqual([true, 'Выбрано в приложении']);
});

it('hides a catalog row with nothing to choose, unless it holds a choice', async () => {
  const edit = (chosen: boolean) =>
    start(
      world((w) => {
        const st = row(w, 'steam');
        st.modes = ['location'];
        st.chosen = chosen;
        st.mode = chosen ? 'location' : 'stable';
      }),
    );
  const quiet = edit(false);
  await settle();
  expect(quiet.$('.sv-svc-g')).toBeNull();
  const held = edit(true);
  await settle();
  expect(held.all('.sv-svc-g .sv-svc-r')).toHaveLength(1);
});

it('sets a mode through set_service_mode after asking, and shows it landed', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start(undefined, 'games');
  await vi.advanceTimersByTimeAsync(20);
  const sel = app.$<HTMLSelectElement>('#vx-svc-epic')!;
  expect(sel.value).toBe('');
  sel.value = 'direct';
  sel.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  const dialog = app.$('[role="alertdialog"]')!;
  expect(dialog.textContent).toContain('Сменить маршрут для Epic Games?');
  expect(dialog.textContent).toContain('Интернет пропадёт на несколько секунд.');
  dialog.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  const i = app.calls.indexOf('set_service_mode');
  expect(i).toBeGreaterThanOrEqual(0);
  expect(app.params[i]).toEqual({ id: 'epic', mode: 'direct' });
  expect(app.calls).not.toContain('set_service');
  expect(app.root.textContent).toContain('Выбор для сервиса сохранён');
  expect(app.$<HTMLSelectElement>('#vx-svc-epic')!.value).toBe('direct');
});

it('«Авто» sends the empty mode and clears the owner’s choice', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start(undefined, 'games');
  await vi.advanceTimersByTimeAsync(20);
  // Steam is on «Напрямую» in the fixture.
  let sel = app.$<HTMLSelectElement>('#vx-svc-steam')!;
  sel.value = '';
  sel.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  expect(app.params[app.calls.indexOf('set_service_mode')]).toEqual({ id: 'steam', mode: '' });
  sel = app.$<HTMLSelectElement>('#vx-svc-steam')!;
  expect(sel.value).toBe('');
});

it('leaves a mode alone when the same one is picked again, and asks nothing', async () => {
  const app = start(undefined, 'games');
  await settle();
  const sel = app.$<HTMLSelectElement>('#vx-svc-steam')!;
  sel.value = 'direct';
  sel.dispatchEvent(new Event('change'));
  await settle();
  expect(app.$('[role="alertdialog"]')).toBeNull();
  expect(app.calls).not.toContain('set_service_mode');
});

it('picks a country on a catalog row through set_service, and the mode clears', async () => {
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
  expect(app.params[app.calls.indexOf('set_service')]).toEqual({ id: 'youtube', country: 'DE' });
  expect(app.root.textContent).toContain('Выбор для сервиса сохранён');
  const after = app.all('.sv-svc-r')[0].querySelector('select')!;
  expect(after.value).toBe('DE');
  // And back to a mode: the country is cleared by the router.
  after.value = 'main';
  after.dispatchEvent(new Event('change'));
  await vi.advanceTimersByTimeAsync(100);
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  expect(app.params[app.calls.indexOf('set_service_mode')]).toEqual({ id: 'youtube', mode: 'main' });
  expect(app.all('.sv-svc-r')[0].querySelector('select')!.value).toBe('main');
});

it('draws a launcher’s own icon, the globe for one it lacks, and none on the top level', async () => {
  const icons = new Map(
    readFileSync(resolve(__dirname, '../src/ui/icons.txt'), 'utf8')
      .trim()
      .split('\n')
      .map((l) => [l.slice(0, l.indexOf(' ')), l.slice(l.indexOf(' ') + 1)] as const),
  );
  const app = start(
    world((w) => {
      row(w, 'steam').icon = 'steam';
      // Provider data is data: a key of Object.prototype is just an unknown icon.
      w.services.services.push({ ...row(w, 'steam'), id: 'odd', title: { ru: 'Odd' }, order: 90, icon: 'constructor' });
      w.services.services.push({ ...row(w, 'steam'), id: 'blank', title: { ru: 'Blank' }, order: 95, icon: '' });
    }, 'games'),
    'games',
  );
  await settle();
  const d = (id: string) => app.$('#vx-svc-' + id)!.closest('li')!.querySelector('.sv-svc-n path')?.getAttribute('d');
  expect(d('steam')).toBe(icons.get('steam'));
  expect(d('epic')).toBe(icons.get('epic'));
  expect(d('odd')).toBe(icons.get('globe'));
  expect(d('blank')).toBe(icons.get('globe'));
  expect(app.$('.sv-svc-gh path')?.getAttribute('d')).toBe(icons.get('games'));
  expect(app.$('#vx-svc-youtube')!.closest('li')!.querySelector('svg')).toBeNull();
});

// ── the stable exit ──────────────────────────────────────────────────────────

const stableExit = (state: string, country: string | null = null, main = false) => (w: ReadData) => {
  w.services.stableExit = { state, country, checkedAt: null, main };
};

it('tells the stable exit by where it leaves, never by its address', async () => {
  const app = start(undefined, 'games');
  await settle();
  const g = app.$('.sv-svc-g')!;
  const lines = Array.from(g.querySelectorAll('.hint')).map((h) => words(h.textContent));
  expect(lines).toContain('Постоянный IP · 🇫🇷 Франция');
  expect(g.textContent).not.toMatch(/\d+\.\d+\.\d+\.\d+/);
});

it('tells that it is being looked for, that none keeps one address, and nothing where there is no probe', async () => {
  const say = async (state: string) => {
    const app = start(world(stableExit(state), 'games'), 'games');
    await settle();
    return { text: words(app.$('.sv-svc-g')!.textContent), sw: app.$('.sv-svc [role="switch"]') };
  };
  const check = await say('checking');
  expect(check.text).toContain('Ищем узел с постоянным IP');
  expect(check.text).not.toContain('Постоянный IP ·');
  const none = await say('none');
  expect(none.text).toContain('Нет узла с постоянным IP — идут общим путём VPN');
  expect(none.sw).not.toBeNull();
  const off = await say('off');
  expect(off.text).not.toMatch(/Ищем|Нет узла|Постоянный IP ·/);
  expect(off.sw).toBeNull();
});

it('tells nothing of the exit where no row offers the stable mode, or where the router sends none', async () => {
  const no = start(
    world((w) => {
      for (const s of w.services.services) s.modes = s.modes.filter((m) => m !== 'stable');
    }),
  );
  const old = start(world((w) => (w.services.stableExit = null)));
  await settle();
  for (const app of [no, old]) {
    expect(app.$('.sv-svc [role="switch"]')).toBeNull();
    expect(words(app.$('.sv-svc')!.textContent)).not.toMatch(/Ищем узел|Нет узла|Постоянный IP ·/);
  }
});

it('has a switch for the whole VPN through the stable exit, asked first, and seen landing', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  const app = start();
  await vi.advanceTimersByTimeAsync(20);
  const sw = () => app.$<HTMLButtonElement>('.sv-svc [role="switch"]')!;
  expect(sw().textContent).toBe('Постоянный IP для всего VPN');
  expect(sw().getAttribute('aria-checked')).toBe('false');
  // It is the owner's quiet setting: one, beside the games, not among the rows.
  expect(app.all('.sv-svc [role="switch"]')).toHaveLength(1);
  sw().click();
  await vi.advanceTimersByTimeAsync(100);
  expect(app.$('[role="alertdialog"]')!.textContent).toContain('Изменить «Постоянный IP для всего VPN»?');
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  expect(app.params[app.calls.indexOf('set_stable_main')]).toEqual({ on: true });
  expect(app.root.textContent).toContain('Настройка сохранена');
  expect(sw().getAttribute('aria-checked')).toBe('true');
  // And off again.
  sw().click();
  await vi.advanceTimersByTimeAsync(100);
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[1].click();
  await vi.advanceTimersByTimeAsync(2100);
  expect(app.params[app.calls.lastIndexOf('set_stable_main')]).toEqual({ on: false });
  expect(sw().getAttribute('aria-checked')).toBe('false');
});

it('leaves the switch where it is when the change is declined', async () => {
  const app = start();
  await settle();
  app.$<HTMLButtonElement>('.sv-svc [role="switch"]')!.click();
  await settle();
  app.$('[role="alertdialog"]')!.querySelectorAll('button')[0].click();
  await settle();
  expect(app.calls).not.toContain('set_stable_main');
  expect(app.$('.sv-svc [role="switch"]')!.getAttribute('aria-checked')).toBe('false');
});

// ── what the router sends, and what the mock sends like it ───────────────────

it('reads an answer of r25 as a router with no catalog, and a hostile catalog as data', () => {
  const old = normalize('services', { available: true, services: [{ id: 'youtube', choice: null, countries: ['DE'] }] });
  expect(old.groups).toEqual([]);
  expect(old.stableExit).toBeNull();
  expect(old.services[0]).toMatchObject({ title: null, group: null, order: null, icon: null, modes: [], mode: null, default: null, chosen: null });
  const odd = normalize('services', {
    available: true,
    services: [{ id: '__proto__', title: { ru: 'Х', en: 42, zh: '' }, modes: ['main', 7, 'stable'], order: 'x', chosen: 'yes' }],
    groups: [{ id: 'constructor', title: 'Игры' }, { title: { ru: 'no id' } }],
    stableExit: 'pinned',
  });
  expect(odd.services[0].title).toEqual({ ru: 'Х' });
  expect(odd.services[0].modes).toEqual(['main', 'stable']);
  expect(odd.services[0]).toMatchObject({ order: null, chosen: null });
  expect(odd.groups).toEqual([{ id: 'constructor', title: null, order: null, icon: null, hint: null }]);
  expect(odd.stableExit).toBeNull();
});

it('answers set_service_mode and set_stable_main like the router, refusing what it does not offer', async () => {
  const mock = createMock({ scenario: 'games', latencyMs: 0, live: false, applyMs: 5 });
  cleanup.push(() => mock.dispose());
  const code = async (m: string, p: Record<string, unknown>) => ((await mock.call(m, p)) as { ok: boolean; code: string }).code;
  expect(await code('set_service_mode', { id: 'starcitizen', mode: 'direct' })).toBe('invalid_params');
  expect(await code('set_service_mode', { id: 'nope', mode: 'main' })).toBe('unknown_service');
  expect(await code('set_service_mode', { id: 'steam' })).toBe('invalid_params');
  expect(await code('set_service_mode', { id: 'steam', mode: 'stable' })).toBe('pending');
  expect(await code('set_stable_main', { on: 'yes' })).toBe('invalid_params');
  expect(await code('set_stable_main', { on: true })).toBe('pending');
  await new Promise((r) => setTimeout(r, 30));
  const now = (await mock.call('services', {})) as Services;
  expect(now.services.find((s) => s.id === 'steam')).toMatchObject({ mode: 'stable', chosen: true });
  expect(now.stableExit?.main).toBe(true);
  // Served to a locked router too: the owner's choices (contract: Operator lock).
  const locked = createMock({ scenario: 'games', latencyMs: 0, live: false, locked: true });
  cleanup.push(() => locked.dispose());
  expect(((await locked.call('set_service_mode', { id: 'steam', mode: '' })) as { code: string }).code).toBe('pending');
  expect(((await locked.call('set_stable_main', { on: false })) as { code: string }).code).toBe('pending');
});

it('is the Pro tab’s too, with the same rows and the same switch', async () => {
  localStorage.setItem('vectra.ui.mode', 'pro');
  localStorage.setItem('vectra.ui.tab', 'locations');
  const app = start(undefined, 'games');
  await settle();
  expect(app.all('.sv-svc-r').length).toBe(8);
  expect(app.all('.sv-svc [role="switch"]')).toHaveLength(1);
});
