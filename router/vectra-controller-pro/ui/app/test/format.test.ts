import { describe, expect, it } from 'vitest';
import { makeT } from '../src/i18n';
import { delayTone, makeFmt } from '../src/lib/format';

const ru = makeFmt(makeT('ru'));
const en = makeFmt(makeT('en'));
const zh = makeFmt(makeT('zh'));
// Russian groups digits and separates decimals with (narrow) no-break spaces and commas.
const plain = (s: string) => s.replace(/[  ]/g, ' ');

describe('bytes', () => {
  it('scales by 1024 with one decimal below ten', () => {
    expect(en.bytes(0)).toBe('0 B');
    expect(en.bytes(512)).toBe('512 B');
    expect(en.bytes(1536)).toBe('1.5 KB');
    expect(en.bytes(51338112)).toBe('49 MB');
    expect(en.bytes(1902114220)).toBe('1.8 GB');
    expect(plain(ru.bytes(1902114220))).toBe('1,8 ГБ');
    expect(zh.bytes(3004188)).toBe('2.9 MB');
  });

  it('never turns an unknown into zero', () => {
    expect(en.bytes(null)).toBe('no data');
    expect(en.bytes(undefined, '—')).toBe('—');
    expect(en.bytes(-1)).toBe('no data');
    expect(en.bytes(NaN)).toBe('no data');
  });
});

describe('durations', () => {
  it('shows the two largest units', () => {
    expect(en.dur(0)).toBe('0 s');
    expect(en.dur(59)).toBe('59 s');
    expect(en.dur(90)).toBe('1 min 30 s');
    expect(en.dur(300)).toBe('5 min');
    expect(en.dur(3600)).toBe('1 h');
    expect(en.dur(38461)).toBe('10 h 41 min');
    expect(en.dur(93211)).toBe('1 d 1 h');
    expect(ru.dur(38461)).toBe('10 ч 41 мин');
    expect(zh.dur(43200)).toBe('12 小时');
  });

  it('renders null as unknown', () => {
    expect(ru.dur(null)).toBe('нет данных');
    expect(en.dur(undefined)).toBe('no data');
  });
});

describe('relative times', () => {
  const now = Date.parse('2026-09-27T12:00:00Z');
  const before = (sec: number) => new Date(now - sec * 1000).toISOString();

  it('speaks each language', () => {
    expect(ru.ago(before(300), now)).toBe('5 мин назад');
    expect(en.ago(before(300), now)).toBe('5 min ago');
    expect(zh.ago(before(300), now)).toBe('5 分钟前');
    expect(en.ago(before(42), now)).toBe('42 s ago');
    expect(en.ago(before(7200), now)).toBe('2 h ago');
    expect(en.ago(before(3 * 86400), now)).toBe('3 d ago');
  });

  it('says "just now" for fresh and slightly future times', () => {
    expect(en.ago(before(3), now)).toBe('just now');
    expect(ru.ago(before(-120), now)).toBe('только что');
  });

  it('handles null and garbage', () => {
    expect(en.ago(null, now)).toBe('no data');
    expect(en.ago('not a time', now)).toBe('no data');
    expect(ru.ago(null, now, 'ещё не было')).toBe('ещё не было');
    expect(en.agoSec(null)).toBe('no data');
    expect(en.agoSec(11)).toBe('11 s ago');
  });
});

describe('delays', () => {
  it('formats milliseconds and keeps "not probed" distinct from zero', () => {
    expect(en.delay(96)).toBe('96 ms');
    expect(ru.delay(148)).toBe('148 мс');
    expect(zh.delay(38)).toBe('38 毫秒');
    expect(en.delay(null, '—')).toBe('—');
    expect(en.delay(0)).toBe('0 ms');
  });

  it('grades on the 120 / 250 ms scale', () => {
    expect(delayTone(96)).toBe('good');
    expect(delayTone(119)).toBe('good');
    expect(delayTone(120)).toBe('ok');
    expect(delayTone(249)).toBe('ok');
    expect(delayTone(250)).toBe('slow');
    expect(delayTone(null)).toBeNull();
  });
});

describe('other numbers', () => {
  it('formats MiB and counters per locale', () => {
    expect(en.mib(61.4)).toBe('61.4 MB');
    expect(plain(ru.mib(61.4))).toBe('61,4 МБ');
    expect(en.mib(234)).toBe('234 MB');
    expect(plain(ru.num(184213))).toBe('184 213');
    expect(en.num(184213)).toBe('184,213');
    expect(en.num(null)).toBe('no data');
  });

  it('prints wall-clock time', () => {
    expect(en.clock(new Date(2026, 8, 27, 9, 5, 7).getTime())).toBe('09:05:07');
    expect(en.clock(null, '--:--:--')).toBe('--:--:--');
  });
});

import { memTone } from '../src/lib/health';

// A 234 MB router lives at 40-65 MB free under load, as the fleet did under
// PassWall2: only the kernel reclaiming running programs' pages is a failure
// (5% of RAM, at least 12 MiB — the router's memguard.CriticalKB), twice that
// a warning. At 48 the UI said «сбой» while everything worked (1111,
// 2026-09-30).
it('judges memory by where the kernel starts reclaiming', () => {
  const cases: [number, number, string][] = [
    [11, 234, 'fail'],
    [12, 234, 'warn'],
    [23, 234, 'warn'],
    [24, 234, 'ok'],
    [50, 234, 'ok'],
    [40, 1024, 'fail'],
  ];
  for (const [free, total, want] of cases) expect([free, total, memTone(free, total)]).toEqual([free, total, want]);
  expect(memTone(null, 234)).toBe('mute');
  expect(memTone(30, null)).toBe('ok');
});
