import { describe, expect, it } from 'vitest';
import { makeT } from '../src/i18n';
import { makeFmt } from '../src/lib/format';
import { tuneInPlace, tuneWords } from '../src/lib/tune';

describe('the tune in plain words', () => {
  it('names the quiet cron log, and never vctl\'s own leftovers in RAM', () => {
    const t = makeT('en');
    const f = makeFmt(t);
    const { ids, zramMiB } = tuneInPlace({
      enabled: true,
      profile: 'lowmem',
      items: [
        { id: 'zram', state: 'applied', value: '117', target: 'on', reason: null },
        { id: 'cron_loglevel', state: 'applied', value: '9', target: '9', reason: null },
        { id: 'tmp_leftovers', state: 'already', value: null, target: '0', reason: null },
      ],
    });
    const words = tuneWords(t, f, ids, zramMiB, true);
    expect(words).toContain('the log kept free of scheduler noise');
    expect(words.join(' ')).not.toContain('tmp_leftovers');
    expect(words.join(' ')).not.toContain('cron_loglevel');
  });
});
