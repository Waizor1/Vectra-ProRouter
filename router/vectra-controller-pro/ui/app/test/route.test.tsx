// The server card's line (spec decision 6): where the main traffic goes now,
// and — when the watchdog moved it — off which node and onto what.
import { afterEach, beforeEach, expect, it } from 'vitest';
import type { CallFn, ReadData } from '../src/api/types';
import { FIXTURES } from '../src/mock/fixtures';
import { clone } from '../src/mock/scenarios';
import { createMock } from '../src/mock/transport';
import { mount } from '../src/mount';

let cleanup: (() => void)[] = [];
beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  document.body.innerHTML = '';
});

/** What a reader sees: no-break spaces read as spaces. */
const words = (el: Element | null) => el!.textContent!.replace(/\u00a0/g, ' ');

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(edit: (w: ReadData) => void) {
  const w = clone(FIXTURES);
  edit(w);
  const fallback = createMock({ latencyMs: 0, live: false });
  const call: CallFn = (m, p) => (m in w ? Promise.resolve(clone(w[m as keyof ReadData])) : fallback.call(m, p));
  const host = document.createElement('div');
  document.body.appendChild(host);
  const unmount = mount(host, { call, lang: 'ru' });
  cleanup.push(() => {
    unmount();
    fallback.dispose();
  });
  return host.shadowRoot!;
}

// «Авто» and a country on one card read as two choices (an owner, 2026-10-04):
// the country is said under «Авто», as where it goes now.
it('says which country the main traffic goes through now, under «Авто»', async () => {
  const root = start((w) => {
    w.status.route = { nodes: [{ tag: 'bridge-fin5', country: 'FI', egress: null }], movedFrom: null, movedAt: null, reason: null, unfit: [], unfitKept: [] };
  });
  await settle();
  const card = root.querySelector('.sv-loc')!;
  expect(card.querySelector('b')?.textContent).toBe('Авто');
  expect(words(card.querySelector('.hint'))).toBe('выбирает лучший сервер · сейчас: 🇫🇮 Финляндия · по умолчанию');
  expect(words(card)).not.toContain('Сейчас через');
  expect(card.querySelector('.sv-route-moved')).toBeNull();
});

it('names a server chosen by name and nothing else', async () => {
  const root = start((w) => {
    w.status.subscription.entryRemark = '🇩🇪 Германия';
    w.status.route = { nodes: [{ tag: 'bridge-de5', country: 'DE', egress: null }], movedFrom: null, movedAt: null, reason: null, unfit: [], unfitKept: [] };
  });
  await settle();
  const card = root.querySelector('.sv-loc')!;
  expect(card.querySelector('b')?.textContent).toBe('Германия');
  expect(words(card)).not.toMatch(/сейчас|выбирает/i);
});

it('says the watchdog lent another country, and off which node', async () => {
  const root = start((w) => {
    w.status.route = {
      nodes: [{ tag: 'sticky-by5', country: 'BY', egress: null }],
      movedFrom: { tag: 'sticky-de5', country: 'DE', egress: null },
      movedAt: new Date(Date.now() - 60000).toISOString(),
      reason: 'borrowed',
      unfit: [],
      unfitKept: [],
    };
  });
  await settle();
  const note = root.querySelector('.sv-loc .sv-route-moved')!;
  expect(words(note)).toContain('Не отвечает: 🇩🇪 Германия. Трафик пока идёт через 🇧🇾 Беларусь');
});

it('says nothing where the router has no word from its watchdog', async () => {
  const root = start((w) => {
    w.status.route = null;
  });
  await settle();
  expect(words(root.querySelector('.sv-loc .hint'))).toBe('выбирает лучший сервер · по умолчанию');
});

it('names the server the router left out because blocked sites do not open through it', async () => {
  const root = start((w) => {
    w.status.route = {
      nodes: [{ tag: 'bridge-fin5', country: 'FI', egress: null }],
      movedFrom: null,
      movedAt: null,
      reason: null,
      unfit: [{ tag: 'bridge-us5', country: 'US', egress: null }],
      unfitKept: [],
    };
  });
  await settle();
  const note = root.querySelector('.sv-loc .sv-route-unfit')!;
  expect(words(note)).toBe('Не открывает заблокированные сайты: 🇺🇸 США. Пока не используется');
});

it('names several such servers at once, and none when there are none', async () => {
  let root = start((w) => {
    w.status.route = {
      nodes: [{ tag: 'bridge-fin5', country: 'FI', egress: null }],
      movedFrom: null,
      movedAt: null,
      reason: null,
      unfit: [
        { tag: 'bridge-us5', country: 'US', egress: null },
        { tag: 'odd-node', country: null, egress: null },
      ],
      unfitKept: [],
    };
  });
  await settle();
  expect(words(root.querySelector('.sv-loc .sv-route-unfit'))).toBe('Не открывают заблокированные сайты: 🇺🇸 США, odd-node. Пока не используются');
  cleanup.forEach((fn) => fn());
  cleanup = [];
  root = start((w) => {
    w.status.route = { nodes: [{ tag: 'bridge-fin5', country: 'FI', egress: null }], movedFrom: null, movedAt: null, reason: null, unfit: [], unfitKept: [] };
  });
  await settle();
  expect(root.querySelector('.sv-loc .sv-route-unfit')).toBeNull();
});

it('says when the only servers left fail blocked sites and still carry the traffic', async () => {
  const root = start((w) => {
    w.status.route = {
      nodes: [{ tag: 'bridge-us5', country: 'US', egress: null }],
      movedFrom: null,
      movedAt: null,
      reason: null,
      unfit: [],
      unfitKept: [{ tag: 'bridge-us5', country: 'US', egress: null }],
    };
  });
  await settle();
  expect(words(root.querySelector('.sv-loc .sv-route-unfit'))).toBe(
    'Не открывает заблокированные сайты: 🇺🇸 США. Другого сервера нет, поэтому заблокированные сайты могут не открываться',
  );
});

it('says where a server really leaves when it is not where its name says', async () => {
  const root = start((w) => {
    w.status.route = {
      nodes: [
        { tag: 'bridge-ae5', country: 'AE', egress: 'PL' },
        { tag: 'bridge-de5', country: 'DE', egress: null },
      ],
      movedFrom: null,
      movedAt: null,
      reason: null,
      unfit: [],
      unfitKept: [],
    };
  });
  await settle();
  expect(words(root.querySelector('.sv-loc .hint'))).toBe('выбирает лучший сервер · сейчас: 🇦🇪 ОАЭ (выход: 🇵🇱 Польша), 🇩🇪 Германия · по умолчанию');
});

// 390 px, 30.09: the line broke as «ОАЭ (выход:» / «🇵🇱 Польша)». A flag keeps
// its country, and the real exit stays in one piece.
it('never breaks a flag from its country or the real exit in two', async () => {
  const root = start((w) => {
    w.status.route = {
      nodes: [{ tag: 'bridge-ae5', country: 'AE', egress: 'PL' }],
      movedFrom: null,
      movedAt: null,
      reason: null,
      unfit: [{ tag: 'bridge-us5', country: 'US', egress: null }],
      unfitKept: [],
    };
  });
  await settle();
  const via = root.querySelector('.sv-loc .hint')!.textContent!;
  expect(via).toContain('🇦🇪\u00a0ОАЭ');
  expect(via).toContain('(выход:\u00a0🇵🇱\u00a0Польша)');
  expect(root.querySelector('.sv-loc .sv-route-unfit')!.textContent).toContain('🇺🇸\u00a0США');
});
