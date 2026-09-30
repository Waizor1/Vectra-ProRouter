// Plain names for provider tags and balancers (src/lib/names.ts), checked
// against the provider document a router really runs and against tags that
// follow no convention — which must stay exactly as they are.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { normalize } from '../src/api/normalize';
import { makeT } from '../src/i18n';
import { countryName, flagCountry, joinList, kindOf, nodeName, routeName, tagCountry } from '../src/lib/names';
import { FIXTURES } from '../src/mock/fixtures';
import realBalancers from './real/balancers.json';

// Entry 1 of 26 of the real subscription, "🇷🇺🇪🇺 Авто Самый стабильный" (scrubbed).
const ENTRY = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/coreengine/xray/testdata/provider/entry-00.json'), 'utf8')) as {
  remarks: string;
  outbounds: { tag: string; protocol: string }[];
};

const ru = makeT('ru');
const en = makeT('en');
const zh = makeT('zh');

describe('node names from the real subscription', () => {
  // Every outbound of entry-00, in the document's order, with its name in plain words.
  const WANT: Record<string, [kind: string, name: string, flag: string | null]> = {
    'bridge-pl5': ['bridge', 'Мост → Польша', '🇵🇱'],
    'bridge-de5': ['bridge', 'Мост → Германия', '🇩🇪'],
    'bridge-fin5': ['bridge', 'Мост → Финляндия', '🇫🇮'],
    'bridge-fr5': ['bridge', 'Мост → Франция', '🇫🇷'],
    'bridge-nl5': ['bridge', 'Мост → Нидерланды', '🇳🇱'],
    'bridge-us5': ['bridge', 'Мост → США', '🇺🇸'],
    'bridge-tr5': ['bridge', 'Мост → Турция', '🇹🇷'],
    'bridge-ae5': ['bridge', 'Мост → ОАЭ', '🇦🇪'],
    'bridge-ru-tcp': ['bridge', 'Мост → Россия', '🇷🇺'],
    'whitelist-lv2': ['wl', 'Белый список, уровень 2', null],
    'whitelist-lv3': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-2': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-3': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-4': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-5': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-6': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-7': ['wl', 'Белый список, уровень 3', null],
    'whitelist-lv3-8': ['wl', 'Белый список, уровень 3', null],
    'bridge-by-tcp': ['bridge', 'Мост → Беларусь', '🇧🇾'],
    'hy2-de5': ['hy2', 'Hysteria2 → Германия', '🇩🇪'],
    'hy2-fin5': ['hy2', 'Hysteria2 → Финляндия', '🇫🇮'],
    'hy2-nl5': ['hy2', 'Hysteria2 → Нидерланды', '🇳🇱'],
    // The loopback stages the provider chains its balancers with: no convention, shown as they are.
    'stage-wl': ['other', 'stage-wl', null],
    'stage-main': ['other', 'stage-main', null],
    'stage-main-backup': ['other', 'stage-main-backup', null],
    DIRECT: ['direct', 'Напрямую, без VPN', null],
    BLOCK: ['block', 'Блокировка', null],
    'stage-wl-lv2': ['other', 'stage-wl-lv2', null],
    'stage-wl-lv3': ['other', 'stage-wl-lv3', null],
  };

  it('covers every outbound of the document', () => {
    expect(ENTRY.remarks).toBe('🇷🇺🇪🇺 Авто Самый стабильный');
    expect(ENTRY.outbounds.map((o) => o.tag)).toEqual(Object.keys(WANT));
  });

  it('names each one in plain words, with the country’s flag, and never names a stage', () => {
    for (const { tag } of ENTRY.outbounds) {
      const n = nodeName(ru, tag);
      expect([n.kind, n.name, n.flag], tag).toEqual(WANT[tag]);
      expect(n.known, tag).toBe(WANT[tag][0] !== 'other');
    }
  });

  it('keeps the part before the arrow apart, so a list of one kind can say just the country', () => {
    expect(nodeName(ru, 'bridge-pl5')).toMatchObject({ pre: 'Мост', short: 'Польша' });
    expect(nodeName(ru, 'hy2-de5')).toMatchObject({ pre: 'Hysteria2', short: 'Германия' });
    expect(nodeName(ru, 'whitelist-lv3-2')).toMatchObject({ pre: null, short: 'Уровень 3' });
  });

  it('speaks the reader’s language; the brand and the tag stay as they are', () => {
    expect(nodeName(en, 'bridge-pl5').name).toBe('Bridge → Poland');
    expect(nodeName(en, 'whitelist-lv3-2').name).toBe('Whitelist, level 3');
    expect(nodeName(en, 'hy2-nl5').name).toBe('Hysteria2 → Netherlands');
    expect(nodeName(zh, 'bridge-pl5').name).toBe('桥接 → 波兰');
    expect(nodeName(zh, 'whitelist-lv2').name).toBe('白名单 · 第 2 级');
    expect(nodeName(en, 'stage-main').name).toBe('stage-main');
  });

  it('derives the country exactly as the router derives nodes[].countryHint', () => {
    for (const n of FIXTURES.nodes.nodes) expect(tagCountry(n.tag), n.tag).toBe(n.countryHint);
    expect(tagCountry('bridge-fin5')).toBe('FI');
    expect(tagCountry('bridge-uae2')).toBe('AE');
  });
});

describe('what is not known stays as it is', () => {
  it('shows a tag that follows no convention unchanged, and knows it says nothing more', () => {
    for (const tag of ['node-a', 'node-dead', 'proxy-1', 'my server', 'BL-MAIN', '__proto__', 'constructor', 'toString', '']) {
      expect(nodeName(ru, tag), tag).toEqual({ kind: 'other', pre: null, flag: null, short: tag, name: tag, known: false });
    }
  });

  it('never reads a level as a country, nor a word as a flag', () => {
    // "lv" is the whitelist level, not Latvia; "in", "it", "at", "am" are words.
    for (const tag of ['whitelist-lv3', 'wl-lv1', 'bridge-in', 'hy2-it', 'node-at', 'node-am']) expect(nodeName(ru, tag).flag, tag).toBeNull();
    expect(nodeName(ru, 'whitelist')).toMatchObject({ kind: 'wl', name: 'Белый список', short: 'Белые списки' });
  });

  it('keeps the kind of a tag without a country and adds no country of its own', () => {
    expect(nodeName(ru, 'bridge-xyz')).toMatchObject({ kind: 'bridge', name: 'Мост', flag: null, known: true });
    expect(nodeName(ru, 'hysteria2-edge')).toMatchObject({ kind: 'hy2', name: 'Hysteria2', flag: null });
  });

  it('takes the router’s hint over its own reading, and names a plain country tag like the router flags it', () => {
    expect(nodeName(ru, 'bridge-x', 'DE').name).toBe('Мост → Германия');
    expect(nodeName(ru, 'de-1')).toMatchObject({ kind: 'other', pre: null, flag: '🇩🇪', name: 'Германия', known: true });
  });

  it('knows the kinds only by their conventions', () => {
    expect(['bridge-pl5', 'hy2-de5', 'hysteria-x', 'whitelist-lv1', 'wl-lv2', 'direct', 'freedom', 'block', 'blackhole', 'bridgeless', 'directly'].map(kindOf)).toEqual([
      'bridge',
      'hy2',
      'hy2',
      'wl',
      'wl',
      'direct',
      'direct',
      'block',
      'block',
      'other',
      'other',
    ]);
  });
});

describe('route names from what the rules carry', () => {
  const list = normalize('balancers', FIXTURES.balancers).balancers;
  const name = (t: typeof ru, tag: string) =>
    routeName(
      t,
      list.find((b) => b.tag === tag)!,
    );

  it('names the fixture’s routes, which are the real subscription’s', () => {
    expect(name(ru, 'BL-MAIN')).toEqual({ name: 'Основной трафик', known: true });
    expect(name(ru, 'BL-RU')).toEqual({ name: 'Российские сайты и YouTube', known: true });
    expect(name(ru, 'BL-TK')).toEqual({ name: 'Telegram и TikTok', known: true });
    // Reached only as another's fallback: named by its nodes.
    expect(name(ru, 'BL-MAIN-BACKUP')).toEqual({ name: 'Резерв Hysteria2', known: true });
    expect(name(ru, 'BL-WL-LV1')).toEqual({ name: 'Белые списки, уровень 1', known: true }); // no node: read from the selector
    expect(name(ru, 'BL-WL-LV3')).toEqual({ name: 'Белые списки, уровень 3', known: true });
  });

  it('in every language', () => {
    expect(['BL-MAIN', 'BL-RU', 'BL-TK', 'BL-MAIN-BACKUP'].map((t) => name(en, t).name)).toEqual([
      'Main traffic',
      'Russian sites and YouTube',
      'Telegram and TikTok',
      'Hysteria2 backup',
    ]);
    expect(['BL-MAIN', 'BL-RU', 'BL-TK', 'BL-WL-LV2'].map((t) => name(zh, t).name)).toEqual(['主流量', '俄罗斯网站和YouTube', 'Telegram和TikTok', '白名单 · 第 2 级']);
  });

  it('falls back to the tag when nothing tells, and calls a mixed reserve just a backup', () => {
    const real = normalize('balancers', realBalancers).balancers;
    expect(real.map((b) => routeName(ru, b))).toEqual([
      { name: 'Основной трафик', known: true },
      { name: 'BL-PING', known: false }, // domain:example.org names no service
      { name: 'Резерв', known: true },
    ]);
    const base = list.find((b) => b.tag === 'BL-TK')!;
    expect(routeName(ru, { ...base, tag: '__proto__', matchers: [{ kind: 'domain', network: null, sample: ['__proto__', 'constructor'], total: 2 }] })).toEqual({
      name: '__proto__',
      known: false,
    });
    // An unused balancer is not a backup, whatever its nodes.
    expect(routeName(ru, { ...base, tag: 'BL-X', role: 'unused', matchers: [], members: ['hy2-de5'] }).name).toBe('BL-X');
  });
});

describe('flags, countries and lists', () => {
  it('reads one country flag and nothing else', () => {
    expect(flagCountry('🇩🇪')).toBe('DE');
    expect(flagCountry('🇷🇺🇪🇺')).toBeNull();
    expect(flagCountry('⚪')).toBeNull();
    expect(flagCountry(null)).toBeNull();
    expect(flagCountry('DE')).toBeNull();
  });

  it('names a country in the reader’s language, and shows a code it cannot name as it is', () => {
    expect([countryName('ru', 'DE'), countryName('en', 'DE'), countryName('zh', 'DE')]).toEqual(['Германия', 'Germany', '德国']);
    expect(countryName('en', 'not-a-code')).toBe('not-a-code');
  });

  it('joins a list the way the language does', () => {
    // The UI's English is British (en-GB): no serial comma.
    expect([joinList('ru', ['A', 'B']), joinList('en', ['A', 'B', 'C']), joinList('zh', ['A', 'B'])]).toEqual(['A и B', 'A, B and C', 'A和B']);
  });
});
