// Services, as the router's `services` answer has them. Since r26 a row is the
// catalog's: a title, an icon, a group and the choices it offers («Авто», «Как
// основной VPN», «VPN · постоянный IP», «Напрямую», and a country of the running
// entry), so a service the catalog adds shows here with no release. A row
// without a title is an r25 service this UI knows by heart (YouTube, TikTok,
// Telegram, «Нейросети»): a country to pick, as it always was.
//
// A change restarts the VPN, like a server switch, so it asks first; the
// router keeps the choice once it runs it. A choice the router does not run
// (the subscription lost the country, or the service's own path) stays
// selected and says so, so «Авто» can clear it. The stable exit's address is
// never shown — only where it leaves.

import { useEffect } from 'preact/hooks';
import type { ActionMethod, ServiceGroup, ServiceInfo, Services as ServicesData } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { flagged } from '../lib/names';
import { Icon } from '../ui/icons';
import { Switch } from '../ui/kit';

/** The r25 services this UI has words for; a newer router's other ones come with a title of their own. */
const KNOWN = ['youtube', 'tiktok', 'telegram', 'ai'];
/** The modes with words here; `country` is the picker below them, `location` Vectra Connect's. */
const MODES = ['main', 'stable', 'direct'];
const byOrder = (a: { order: number | null }, b: { order: number | null }) => (a.order ?? 0) - (b.order ?? 0);

export function Services() {
  const { t, run, pending, store } = useApp();
  const res = useRes('services');

  useEffect(() => {
    void store.fetch('services', 30000);
  }, []);

  const data = res.data;
  if (!data || data.available === false) return null;

  const place = (cc: string) => flagged(t.lang, cc);
  /** The catalog's words in the UI's language, Russian where it has none. */
  const said = (m: Record<string, string> | null) => (m && (m[t.lang] || m.ru)) || '';
  const titled = (s: ServiceInfo) => !!said(s.title);
  const name = (s: ServiceInfo) => said(s.title) || t(('sv.name.' + s.id) as Key);
  const modesOf = (s: ServiceInfo) => (titled(s) ? s.modes.filter((m) => MODES.indexOf(m) >= 0) : []);
  const ccsOf = (s: ServiceInfo) => (titled(s) && s.modes.indexOf('country') < 0 ? [] : s.countries);
  // A service with nothing to choose is not shown — unless it holds a choice, to be cleared.
  const rows = data.services
    .filter((s) =>
      titled(s)
        ? modesOf(s).length || ccsOf(s).length || s.chosen || s.choice
        : KNOWN.indexOf(s.id) >= 0 && (s.countries.length || s.choice || (s.id === 'ai' && s.defaultCountry)),
    )
    .sort(byOrder);
  if (!rows.length) return null;

  const groups = data.groups.filter((g) => rows.some((s) => s.group === g.id)).sort(byOrder);
  const blocks = [
    { g: null as ServiceGroup | null, rs: rows.filter((s) => !groups.some((g) => g.id === s.group)) },
    ...groups.map((g) => ({ g, rs: rows.filter((s) => s.group === g.id) })),
  ].filter((b) => b.rs.length);

  // The stable exit is told once, where the first stable choice is: where it leaves, or that there is none.
  const se = data.stableExit;
  const home = se ? blocks.find((b) => b.rs.some((s) => modesOf(s).indexOf('stable') >= 0)) : undefined;
  const status = se && t.has('sv.st.' + se.state) ? t(('sv.st.' + se.state) as Key, { country: se.country ? place(se.country) : '—' }) : '';

  /** What the select shows: the owner's country, else their mode, else «Авто». */
  const current = (s: ServiceInfo) => s.choice || (titled(s) && s.chosen && s.mode && (MODES.indexOf(s.mode) >= 0 || s.mode === 'location') ? s.mode : '');
  const ask = async (method: ActionMethod, params: Record<string, unknown>, title: string, landed: (d: ServicesData) => boolean) => {
    await run(method, params, {
      key: 'services',
      confirm: { title, body: t('s.help.restart.d'), ok: t('sv.change') },
      settled: { read: 'services', ok: (d) => landed(d as ServicesData) },
    });
    // A cancelled or refused change leaves the list showing what runs.
    void store.fetch('services', 0);
  };
  const change = (s: ServiceInfo, v: string) => {
    if (v === current(s)) return;
    // «Авто» and the modes are the catalog's (set_service_mode); a country is as it was (set_service).
    const byMode = titled(s) && (!v || MODES.indexOf(v) >= 0);
    const title = t(byMode ? 'sv.modeQ' : 'sv.changeQ', { service: name(s) });
    return ask(byMode ? 'set_service_mode' : 'set_service', byMode ? { id: s.id, mode: v } : { id: s.id, country: v }, title, (d) => {
      const x = d.services.find((y) => y.id === s.id);
      return !!x && (byMode ? (v ? x.mode === v && !x.choice : !x.chosen) : (x.choice || '') === v);
    });
  };

  const row = (s: ServiceInfo, icon: boolean) => {
    const n = name(s);
    const ti = titled(s);
    const d = ti ? s.default : '';
    // «Авто» says what it means: a mode, or where the entry's own path leaves.
    const what = d === 'stable' || d === 'direct' ? t(('sv.m.' + d) as Key) : s.defaultCountry ? place(s.defaultCountry) : d === 'main' ? t('sv.m.main') : '';
    const auto = ti ? (what ? t('sv.autoOf', { what }) : t('sv.auto')) : s.defaultCountry ? t('sv.defaultIn', { country: place(s.defaultCountry) }) : t('sv.default');
    return (
      <li key={s.id} class="sv-svc-r">
        <label class="sv-svc-n" for={'vx-svc-' + s.id}>
          {icon ? <Icon name={s.icon || ''} /> : null}
          {n}
        </label>
        <span class="sel">
          <select
            id={'vx-svc-' + s.id}
            class="in"
            aria-label={ti ? undefined : t('sv.country', { service: n })}
            value={current(s)}
            disabled={pending !== null}
            onChange={(e) => void change(s, (e.currentTarget as HTMLSelectElement).value)}
          >
            <option value="">{auto}</option>
            {modesOf(s).map((m) => (
              <option key={m} value={m}>
                {t(('sv.m.' + m) as Key)}
              </option>
            ))}
            {ti && s.chosen && s.mode === 'location' && !s.choice ? (
              <option value="location" disabled>
                {t('sv.m.location')}
              </option>
            ) : null}
            {s.choice && s.countries.indexOf(s.choice) < 0 ? (
              <option value={s.choice} disabled>
                {place(s.choice) + ' · ' + t('sv.unavailable')}
              </option>
            ) : null}
            {ccsOf(s).map((cc) => (
              <option key={cc} value={cc}>
                {s.egress?.[cc] ? t('x.egress', { name: place(cc), egress: place(s.egress[cc]) }) : place(cc)}
              </option>
            ))}
          </select>
        </span>
        {s.stale ? <p class="hint sv-svc-w">{t('sv.stale', { via: s.defaultCountry ? place(s.defaultCountry) : t('sv.viaMain') })}</p> : null}
      </li>
    );
  };

  return (
    <section class="card sv-svc" aria-labelledby="vx-svc">
      <div>
        <h3 class="k" id="vx-svc">
          {t('sv.t')}
        </h3>
        <p class="hint">{t('sv.d')}</p>
      </div>
      {blocks.map((b) => (
        <div key={b.g ? b.g.id : ''} class={b.g ? 'sv-svc-g' : undefined}>
          {b.g ? (
            <>
              <h4 class="sv-svc-gh">
                <Icon name={b.g.icon || ''} />
                {said(b.g.title) || b.g.id}
              </h4>
              {said(b.g.hint) ? <p class="hint">{said(b.g.hint)}</p> : null}
            </>
          ) : null}
          {b === home && status ? <p class="hint">{status}</p> : null}
          <ul class="sv-svc-l">{b.rs.map((s) => row(s, !!b.g))}</ul>
          {b === home && se && se.state !== 'off' ? (
            <Switch
              label={t('sv.main')}
              checked={se.main === true}
              disabled={pending !== null}
              onChange={(on) => void ask('set_stable_main', { on }, t('sv.mainQ'), (d) => d.stableExit?.main === on)}
            />
          ) : null}
        </div>
      ))}
    </section>
  );
}
