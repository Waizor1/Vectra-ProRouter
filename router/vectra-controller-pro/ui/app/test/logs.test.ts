import { describe, expect, it } from 'vitest';
import type { LogLine } from '../src/api/types';
import { lineLevel, lineText } from '../src/lib/logs';

const line = (message: string, level: LogLine['level'] = 'info'): LogLine => ({ time: null, level, source: 'vctl', message });

describe('journal levels', () => {
  it('re-levels a vctl slog line by its own level and strips time and level', () => {
    const m = 'time=2026-10-04T12:00:00.000+03:00 level=WARN msg="tunnel probe failed" node=pl2';
    expect(lineLevel(line(m, 'error'))).toBe('warn');
    expect(lineText(m)).toBe('tunnel probe failed node=pl2');
    expect(lineLevel(line('time=2026-10-04T12:00:00Z level=INFO msg=started', 'error'))).toBe('info');
  });

  it('leaves any other line that merely says level= at its syslog level', () => {
    for (const m of [
      'hostapd: wlan0: debug level=2 set',
      'kernel: something level=ERROR happened',
      'msg="x" level=INFO',
    ]) {
      expect(lineLevel(line(m, 'error'))).toBe('error');
      expect(lineText(m)).toBe(m);
    }
  });
});
