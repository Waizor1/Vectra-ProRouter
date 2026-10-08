import { describe, expect, it } from 'vitest';
import { glue, interpolate, LANGS, makeT, normLang, pickLang, pluralIndex, withParams } from '../src/i18n';
import { S } from '../src/i18n/strings';

const CYRILLIC = /[Ѐ-ӿ]/;
const placeholders = (s: string) => (s.match(/\{\w+\}/g) || []).sort().join(',');
const PLURAL_FORMS = [3, 2, 1]; // ru one|few|many, en one|other, zh other

describe('string table', () => {
  const entries = Object.entries(S) as [string, readonly string[]][];

  it('has every key in ru, en and zh, none empty', () => {
    for (const [key, row] of entries) {
      expect(row, key).toHaveLength(3);
      row.forEach((s, i) => expect(s.trim(), `${key}[${LANGS[i]}]`).not.toBe(''));
    }
  });

  it('uses identical placeholders in every language and plural form', () => {
    for (const [key, row] of entries) {
      const want = placeholders(row[0].split('|')[0]);
      for (const s of row) for (const form of s.split('|')) expect(placeholders(form), key).toBe(want);
    }
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

describe('the brand in every sentence', () => {
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
