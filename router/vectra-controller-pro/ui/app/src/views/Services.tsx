// Services with a country of their own: the few a person really picks a
// country for (YouTube, TikTok, Telegram). Everything else goes where the
// subscription sends it. A change restarts the VPN, like a server switch, so
// it asks first; the router keeps the choice once it runs it. A choice the
// router does not run (the subscription lost the country, or the service's own
// path) stays selected and says so, so «По умолчанию» can clear it.

import { useEffect } from 'preact/hooks';
import type { ServiceInfo, Services as ServicesData } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { flagged } from '../lib/names';

/** The services this UI has words for; a newer router's other ones wait for a newer UI. */
const KNOWN = ['youtube', 'tiktok', 'telegram', 'ai'];

export function Services() {
  const { t, run, pending, store } = useApp();
  const res = useRes('services');

  useEffect(() => {
    void store.fetch('services', 30000);
  }, []);

  const data = res.data;
  // A service the entry has no path for has no country to offer: shown only
  // while it holds a choice, to be cleared.
  const rows =
    data && data.available !== false ? data.services.filter((s) => KNOWN.indexOf(s.id) >= 0 && (s.countries.length > 0 || !!s.choice || (s.id === 'ai' && !!s.defaultCountry))) : [];
  if (!rows.length) return null;

  const place = (cc: string) => flagged(t.lang, cc);
  const name = (s: ServiceInfo) => t(('sv.name.' + s.id) as Key);
  const current = (s: ServiceInfo) => s.choice ?? '';
  const change = async (s: ServiceInfo, cc: string) => {
    if (cc === current(s)) return;
    await run(
      'set_service',
      { id: s.id, country: cc },
      {
        key: 'services',
        confirm: { title: t('sv.changeQ', { service: name(s) }), body: t('s.help.restart.d'), ok: t('sv.change') },
        settled: { read: 'services', ok: (r) => ((r as ServicesData | null)?.services.find((x) => x.id === s.id)?.choice ?? '') === cc },
      },
    );
    // A cancelled or refused change leaves the list showing what runs.
    void store.fetch('services', 0);
  };

  return (
    <section class="card sv-svc" aria-labelledby="vx-svc">
      <div>
        <h3 class="k" id="vx-svc">
          {t('sv.t')}
        </h3>
        <p class="hint">{t('sv.d')}</p>
      </div>
      <ul class="sv-svc-l">
        {rows.map((s) => (
          <li key={s.id} class="sv-svc-r">
            <label class="sv-svc-n" for={'vx-svc-' + s.id}>
              {name(s)}
            </label>
            <span class="sel">
              <select
                id={'vx-svc-' + s.id}
                class="in"
                aria-label={t('sv.country', { service: name(s) })}
                value={current(s)}
                disabled={pending !== null}
                onChange={(e) => void change(s, (e.currentTarget as HTMLSelectElement).value)}
              >
                <option value="">{s.defaultCountry ? t('sv.defaultIn', { country: place(s.defaultCountry) }) : t('sv.default')}</option>
                {s.choice && s.countries.indexOf(s.choice) < 0 ? (
                  <option value={s.choice} disabled>
                    {place(s.choice) + ' · ' + t('sv.unavailable')}
                  </option>
                ) : null}
                {s.countries.map((cc) => (
                  <option key={cc} value={cc}>
                    {s.egress?.[cc] ? t('x.egress', { name: place(cc), egress: place(s.egress[cc]) }) : place(cc)}
                  </option>
                ))}
              </select>
            </span>
            {s.stale ? <p class="hint sv-svc-w">{t('sv.stale')}</p> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
