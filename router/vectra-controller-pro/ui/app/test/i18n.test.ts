import { describe, expect, it } from 'vitest';
import { brandT, glue, interpolate, LANGS, makeT, normLang, pickLang, pluralIndex, withParams, type Key } from '../src/i18n';
import { S } from '../src/i18n/strings';

const CYRILLIC = /[Ѐ-ӿ]/;
const placeholders = (s: string) => (s.match(/\{\w+\}/g) || []).sort().join(',');
/**
 * `{fem}` is a Russian-only grammar parameter: the ending that agrees with the
 * brand («Vectra выключена», «BloopCat выключен»). en and zh need none, so it
 * is left out of the cross-language comparison — and must never appear there.
 */
const GRAMMAR_RU = '{fem}';
const shared = (s: string) => placeholders(s.split(GRAMMAR_RU).join(''));
const PLURAL_FORMS = [3, 2, 1]; // ru one|few|many, en one|other, zh other

describe('string table', () => {
  const entries = Object.entries(S) as [string, readonly string[]][];

  it('has every key in ru, en and zh, none empty', () => {
    for (const [key, row] of entries) {
      expect(row, key).toHaveLength(3);
      row.forEach((s, i) => expect(s.trim(), `${key}[${LANGS[i]}]`).not.toBe(''));
    }
  });

  it('uses identical placeholders in every language and plural form, but for Russian grammar ({fem})', () => {
    for (const [key, row] of entries) {
      const want = shared(row[0].split('|')[0]);
      for (const s of row) for (const form of s.split('|')) expect(shared(form), key).toBe(want);
      expect(row[1] + row[2], key).not.toContain(GRAMMAR_RU);
    }
  });

  it('writes a no-brand variant (`<key>.any`) only beside the sentence it replaces', () => {
    for (const [key] of entries) if (key.endsWith('.any')) expect(Object.keys(S), key).toContain(key.slice(0, -'.any'.length));
  });

  it('writes plural entries with the right number of forms, and only plural entries with "|"', () => {
    for (const [key, row] of entries) {
      row.forEach((s, i) => expect(s.split('|').length, `${key}[${LANGS[i]}]`).toBe(key.startsWith('n.') ? PLURAL_FORMS[i] : 1));
    }
  });

  it('has no Cyrillic in en or zh', () => {
    for (const [key, row] of entries) {
      expect(row[1], key).not.toMatch(CYRILLIC);
      expect(row[2], key).not.toMatch(CYRILLIC);
    }
  });

  it('names no brand by itself: every service name is {brand}', () => {
    for (const [key, forms] of Object.entries(S)) {
      for (const s of forms) expect(s, key).not.toMatch(/Vectra/);
    }
  });
});

const VECTRA = { id: 'vectra', name: 'Vectra' };
const BLOOPCAT = { id: 'bloopcat', name: 'BloopCat' };
const NONE = { id: null, name: null };

describe('the brand in every sentence', () => {
  // Every Russian sentence where the brand is the subject: the ending agrees
  // with it — Vectra is feminine, BloopCat and the neutral «VPN» masculine.
  const AGREEMENT: [Key, string, string, string][] = [
    ['hero.off', 'Vectra выключена', 'BloopCat выключен', 'VPN выключен'],
    ['ov.checksOff', 'Проверки идут, пока Vectra включена.', 'Проверки идут, пока BloopCat включен.', 'Проверки идут, пока VPN включен.'],
    ['a.power_on', 'Vectra включена', 'BloopCat включен', 'VPN включен'],
    ['a.power_off', 'Vectra выключена', 'BloopCat выключен', 'VPN выключен'],
    ['s.down.auto', 'Vectra сама пробует перезапустить VPN.', 'BloopCat сам пробует перезапустить VPN.', 'Роутер сам пробует перезапустить VPN.'],
    ['s.off.t', 'Vectra не запущена', 'BloopCat не запущен', 'VPN не запущен'],
    ['s.pw.off.t', 'Vectra выключена', 'BloopCat выключен', 'VPN выключен'],
    [
      's.unpinBody',
      'Vectra снова будет сама выбирать лучший сервер. Связь не прервётся.',
      'BloopCat снова будет сам выбирать лучший сервер. Связь не прервётся.',
      'VPN снова будет сам выбирать лучший сервер. Связь не прервётся.',
    ],
    ['s.a.controller_down', 'Vectra на роутере не запущена', 'BloopCat на роутере не запущен', 'VPN на роутере не запущен'],
    ['s.a.power_on', 'Vectra включена', 'BloopCat включен', 'VPN включен'],
    ['s.a.power_off', 'Vectra выключена', 'BloopCat выключен', 'VPN выключен'],
  ];
  it.each(AGREEMENT)('agrees in Russian with the brand: %s', (key, vectra, bloopcat, none) => {
    expect(brandT(makeT('ru'), VECTRA)(key)).toBe(vectra);
    expect(brandT(makeT('ru'), BLOOPCAT)(key)).toBe(bloopcat);
    expect(brandT(makeT('ru'), NONE)(key)).toBe(none);
  });

  it('leaves no Russian sentence with the brand as its subject in the feminine', () => {
    for (const [key, row] of Object.entries(S)) {
      const ru = row[0];
      // The brand as a subject (first in its sentence, or after «пока»), then within four words a feminine ending.
      if (ru.includes('{brand}')) {
        expect(brandT(makeT('ru'), BLOOPCAT)(key as Key), key).not.toMatch(/(^|[.:—] |пока )BloopCat (\S+ ){0,3}(\S+(на|ла)|сама|одна|готова)(?!\p{L})/u);
      }
    }
  });

  // A router with no brand is «VPN» in a sentence: never twice in one, and never
  // a name of a thing that is not ours to name — its program, its support, its app.
  const NEUTRAL_BAD: Record<'ru' | 'en', RegExp[]> = {
    ru: [/VPN[^]*VPN/, /(Программа|Поддержка|верси[яи]|приложени[еия]|аккаунт[ау]?|[Сс]ервер|обновить) VPN/, /(Связь|связи|связывался) с VPN/, /VPN на связи/],
    en: [/VPN[^]*VPN/, /VPN (program|support|app|account|server)\b/, /version of VPN/, /(Link to|checked in with) VPN|VPN (un)?reachable/, /VPN (on the router )?needs an update/],
  };
  for (const lang of ['ru', 'en'] as const) {
    it(`never says VPN twice, nor names a program, support or an app «VPN», on a router with no brand (${lang})`, () => {
      const t = brandT(makeT(lang), NONE);
      for (const [key, row] of Object.entries(S)) {
        if (!row.some((x) => x.includes('{brand}'))) continue;
        const text = t(key as Key, { ago: '5', v: '0.7.0', a: 'x', b: 'y', d: '1', n: 1 });
        for (const bad of NEUTRAL_BAD[lang]) expect(text, key).not.toMatch(bad);
      }
    });
  }

  it('says the brand on a branded router, and the no-brand sentence only without one', () => {
    expect(brandT(makeT('ru'), BLOOPCAT)('s.setup.d')).toMatch(/^BloopCat ждёт настройки VPN\./);
    expect(brandT(makeT('ru'), NONE)('s.setup.d')).toMatch(/^Роутер ждёт настройки VPN\./);
    expect(brandT(makeT('en'), NONE)('s.help.support.open')).toBe('Open the support chat');
    expect(brandT(makeT('en'), VECTRA)('s.help.support.open')).toBe('Message Vectra');
    // A caller's own brand still wins, as with withParams.
    expect(brandT(makeT('ru'), BLOOPCAT)('w.v.t', { brand: 'Vectra' })).toBe('Подключение к Vectra');
  });

  it('puts the default {brand} into every sentence, plural ones too; a caller’s own value wins', () => {
    const t = withParams(makeT('ru'), { brand: 'BloopCat' });
    expect(t('w.v.t')).toBe('Подключение к BloopCat');
    expect(t('s.help.support.open')).toBe('Написать в BloopCat');
    expect(t('w.v.t', { brand: 'Vectra' })).toBe('Подключение к Vectra');
    // Its other parameters still arrive.
    expect(t('d.panel_link.ok', { ago: '5 мин назад' })).toBe('BloopCat на связи, отчёт 5 мин назад');
    expect(t.n('n.node', 2)).toBe('2 узла');
    expect(t.lang).toBe('ru');
    expect(t.has('brand.none')).toBe(true);
    expect(t.has('no.such.key')).toBe(false);
  });
});

describe('language choice', () => {
  it('normalizes LuCI and browser language tags', () => {
    expect(normLang('ru')).toBe('ru');
    expect(normLang('ru-RU')).toBe('ru');
    expect(normLang('zh_Hans')).toBe('zh');
    expect(normLang('zh-cn')).toBe('zh');
    expect(normLang('en-GB')).toBe('en');
    expect(normLang('de')).toBeNull();
    expect(normLang(undefined)).toBeNull();
  });

  it('prefers the stored choice, then the host page, then the browser, then English', () => {
    expect(pickLang('zh', 'ru', 'en-US')).toBe('zh');
    expect(pickLang(null, 'ru', 'en-US')).toBe('ru');
    expect(pickLang(null, 'de', 'zh-TW')).toBe('zh');
    expect(pickLang('xx', 'fr', 'de-DE')).toBe('en');
  });
});

describe('translation', () => {
  it('interpolates and never prints undefined', () => {
    expect(interpolate('{a} of {b}', { a: 1, b: 'x' })).toBe('1 of x');
    expect(interpolate('{a} and {missing}', { a: 1 })).toBe('1 and —');
    expect(interpolate('{a}', { a: NaN })).toBe('—');
  });

  it('picks Russian plural forms by CLDR category', () => {
    const t = makeT('ru');
    expect(t.n('n.node', 1)).toBe('1 узел');
    expect(t.n('n.node', 2)).toBe('2 узла');
    expect(t.n('n.node', 5)).toBe('5 узлов');
    expect(t.n('n.node', 11)).toBe('11 узлов');
    expect(t.n('n.node', 21)).toBe('21 узел');
    expect(t.n('n.node', 22)).toBe('22 узла');
    expect(pluralIndex('ru', 1.5)).toBe(1);
  });

  it('picks English and Chinese plural forms', () => {
    expect(makeT('en').n('n.node', 1)).toBe('1 node');
    expect(makeT('en').n('n.node', 4)).toBe('4 nodes');
    expect(makeT('zh').n('n.node', 4)).toBe('4 个节点');
  });

  it('falls back to the raw key for a key it does not know', () => {
    const t = makeT('en');
    expect(t.has('tab.settings')).toBe(true);
    expect(t.has('no.such.key')).toBe(false);
  });

  it('never breaks a line inside "Wi-Fi", "QR-код" or "что-то", and leaves longer halves and values alone', () => {
    const J = '\u2060';
    expect(glue('по Wi-Fi, QR-код, что-то, IP-адрес')).toBe(`по Wi-${J}Fi, QR-${J}код, что-${J}то, IP-${J}адрес`);
    expect(glue('mesh-связь, User-Agent, vectra-controller-pro')).toBe('mesh-связь, User-Agent, vectra-controller-pro');
    // Glued once, however often it is asked.
    expect(glue(glue('Wi-Fi'))).toBe(`Wi-${J}Fi`);
    const t = makeT('ru');
    expect(t('w.wifi.done.t')).toBe(`Wi-${J}Fi прокачан`);
    // A value put into a sentence (a server's, a network's own name) stays as it came.
    expect(t('l.q', { name: 'RU-Wi-Fi' })).toBe('Переключиться на RU-Wi-Fi?');
  });
});
