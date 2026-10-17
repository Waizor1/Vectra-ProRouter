// The geo file's lists in «Мои сайты» by what they hold. Until r24 the two
// Russia lists wore each other's names (the audit of 2026-10-17), so an owner
// who followed the names sent both the wrong way. itdoginfo/allow-domains names
// a list by where its reader is: "russia-inside" is for people in Russia (sites
// blocked there or closed to it), "russia-outside" for people abroad (Russian
// sites that answer only Russian addresses). Each name is pinned beside sites
// the shipped geosite.dat puts in that list.
import { describe, expect, it } from 'vitest';
import { LANGS, makeT } from '../src/i18n';
import { serviceName } from '../src/lib/services';
import { FIXTURES } from '../src/mock/fixtures';

const NAMES: Record<string, readonly [string, string, string]> = {
  // 1183 sites: rutracker.org, 1337x.to, chatgpt.com, youtube.com — through the VPN.
  'russia-inside': ['Заблокированные в России — весь список', 'Blocked in Russia — full list', '在俄罗斯被封锁的网站（完整列表）'],
  // 39 sites: gosuslugi.ru, nalog.ru, fssp.gov.ru — the fleet sends them direct.
  'russia-outside': ['Работают только из России', 'Work only from inside Russia', '仅在俄罗斯境内可用'],
  // 1551 sites Ukraine blocks: vk.com, yandex.ru, mail.ru.
  'ukraine-inside': ['Заблокированные в Украине', 'Blocked in Ukraine', '在乌克兰被封锁的网站'],
  // 337 sites: rutracker.org, linkedin.com, signal.org — a part of russia-inside.
  block: ['Заблокированные в России', 'Blocked in Russia', '在俄罗斯被封锁的网站'],
  // 466 sites that close themselves to Russia: chatgpt.com, claude.ai, spotify.com.
  geoblock: ['Сайты, закрытые для России', 'Sites closed to Russia', '对俄罗斯关闭的网站'],
};

describe('the geo lists in «Мои сайты»', () => {
  it('names each list by what it holds, in every language', () => {
    LANGS.forEach((lang, i) => {
      const t = makeT(lang);
      for (const [id, names] of Object.entries(NAMES)) expect(serviceName(t, id), `${id}[${lang}]`).toBe(names[i]);
    });
  });

  it('never calls the list of Russian-only sites blocked, nor the blocked list Russian-only', () => {
    const t = makeT('ru');
    expect(serviceName(t, 'russia-outside')).not.toMatch(/блок|недоступ/i);
    expect(serviceName(t, 'russia-inside')).toMatch(/^Заблокированные в России/);
    expect(serviceName(t, 'russia-inside')).not.toMatch(/только из/);
  });

  it('gives no two of the router’s lists one name, so the whole Russia list is told from its part', () => {
    const catalog = FIXTURES.rules.catalog!;
    expect(catalog).toEqual(expect.arrayContaining(Object.keys(NAMES)));
    for (const lang of LANGS) {
      const t = makeT(lang);
      const names = catalog.map((id) => serviceName(t, id));
      expect(new Set(names).size, lang).toBe(names.length);
    }
  });
});
