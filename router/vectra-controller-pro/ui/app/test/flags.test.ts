import { describe, expect, it } from 'vitest';
import { isoFlag, parseRemark } from '../src/lib/flags';

describe('parseRemark', () => {
  it('splits a flag from the name', () => {
    expect(parseRemark('🇩🇪 Германия')).toEqual({ flag: '🇩🇪', name: 'Германия' });
    expect(parseRemark('🇩🇪 Германия · Hysteria2')).toEqual({ flag: '🇩🇪', name: 'Германия · Hysteria2' });
  });

  it('keeps both flags of a two-flag remark', () => {
    expect(parseRemark('🇷🇺🇪🇺 Авто Самый стабильный')).toEqual({ flag: '🇷🇺🇪🇺', name: 'Авто Самый стабильный' });
  });

  it('treats a leading emoji that is not a flag the same way', () => {
    expect(parseRemark('⚪ Белые списки · Уровень 1')).toEqual({ flag: '⚪', name: 'Белые списки · Уровень 1' });
  });

  it('leaves remarks without a flag alone', () => {
    expect(parseRemark('Germany')).toEqual({ flag: null, name: 'Germany' });
    expect(parseRemark('  1 Poland ')).toEqual({ flag: null, name: '1 Poland' });
    expect(parseRemark('')).toEqual({ flag: null, name: '' });
    expect(parseRemark(null)).toEqual({ flag: null, name: '' });
  });

  it('handles a remark that is only a flag', () => {
    expect(parseRemark('🇫🇮')).toEqual({ flag: '🇫🇮', name: '' });
  });
});

describe('isoFlag', () => {
  it('builds the flag from an ISO code', () => {
    expect(isoFlag('DE')).toBe('🇩🇪');
    expect(isoFlag('nl')).toBe('🇳🇱');
  });

  it('refuses anything that is not a two-letter code', () => {
    expect(isoFlag(null)).toBeNull();
    expect(isoFlag('')).toBeNull();
    expect(isoFlag('DEU')).toBeNull();
    expect(isoFlag('lv3')).toBeNull();
  });
});
