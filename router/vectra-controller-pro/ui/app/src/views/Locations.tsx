// The "Servers and services" tab: the subscription's entries. "Auto" — the
// entry that picks its nodes by itself — comes first, then one card per
// country (its variants, such as "· Hysteria2", inside it), then whatever
// names no country; under them the services with a country of their own,
// the same control as in the simple view.

import type { ComponentChildren } from 'preact';
import type { Entry } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import { parseRemark } from '../lib/flags';
import { face } from '../lib/servers';
import { countryName, flagCountry } from '../lib/names';
import { Icon } from '../ui/icons';
import { Badge, Button, Empty, ErrorBox, Note, Skeleton, Spinner } from '../ui/kit';
import { Services } from './Services';

/** "Авто", "Auto": an entry that chooses by itself. */
const AUTO = /(^|[\s(«"·-])(авто|auto)/i;

export function Locations() {
  const { t, run, pending, refresh } = useApp();
  const res = useRes('entries');
  const data = res.data;
  if (!data) return res.error ? <ErrorBox t={t} err={res.error} onRetry={refresh} /> : <Skeleton />;

  const name = (e: Entry) => {
    const r = parseRemark(e.remark);
    return r.name || r.flag || '#' + e.index;
  };
  const full = (e: Entry) => e.remark.trim() || name(e);
  // select_entry may answer `pending`: the card keeps its spinner until status shows the switch.
  const choose = (e: Entry) =>
    run('select_entry', { index: e.index }, {
      key: 'entry:' + e.index,
      confirm: { title: t('l.q', { name: full(e) }), body: t('restartWarn'), ok: t('l.go') },
      landed: (s) => s.subscription.entryIndex === e.index,
    });
  const local = data.source === 'local';

  const auto = data.entries.filter((e) => AUTO.test(name(e)));
  // Without an "Auto" entry, the panel's default leads instead.
  const top = auto.length ? auto : data.entries.filter((e) => e.index === data.panelIndex);
  const countries = new Map<string, Entry[]>();
  const other: Entry[] = [];
  for (const e of data.entries) {
    if (top.indexOf(e) >= 0) continue;
    const cc = flagCountry(parseRemark(e.remark).flag);
    if (cc) countries.set(cc, (countries.get(cc) || []).concat(e));
    else other.push(e);
  }

  /** A server as a button: its flag, its name and what it is, and whether it is on. */
  const pick = (e: Entry, cls: string, body: ComponentChildren, flag = true) => {
    const on = e.index === data.active;
    return (
      <button
        key={e.index}
        type="button"
        class={cls + (on ? ' on' : '')}
        aria-current={on || undefined}
        aria-disabled={on || !!pending || undefined}
        aria-label={flag ? undefined : t('l.pick', { name: full(e) })}
        onClick={() => on || pending || choose(e)}
      >
        {flag ? (
          <span class="fl" aria-hidden="true">
            {face(e.remark, '').auto ? <Icon name="split" size={22} /> : parseRemark(e.remark).flag || <Icon name="globe" size={22} />}
          </span>
        ) : null}
        {body}
        {pending === 'entry:' + e.index ? (
          <span class="bd t-info">
            <Spinner /> {t('l.busy')}
          </span>
        ) : on ? (
          <Badge tone="info" icon="ok">
            {t('l.current')}
          </Badge>
        ) : e.index === data.panelIndex && flag && auto.length ? (
          <Badge>{t('l.panel')}</Badge>
        ) : null}
      </button>
    );
  };
  const section = (id: string, title: string, items: ComponentChildren, cls = 'locs') => (
    <section class="srv" aria-labelledby={id}>
      <h3 class="k" id={id}>
        {title}
      </h3>
      <ul class={cls}>{items}</ul>
    </section>
  );

  return (
    <div class="stack">
      {local ? (
        <div class="row">
          <Button
            small
            icon="refresh"
            busy={pending === 'entry:reset'}
            disabled={!!pending}
            onClick={() =>
              run('reset_entry', {}, {
                key: 'entry:reset',
                confirm: { title: t('l.resetQ'), body: t('restartWarn'), ok: t('l.reset') },
                landed: (s) => s.subscription.source === 'panel',
              })
            }
          >
            {t('l.reset')}
          </Button>
        </div>
      ) : null}
      {data.overrideStale ? <Note tone="warn">{t('src.stale')}</Note> : null}
      {data.entries.length ? null : <Empty icon="globe">{t('l.none')}</Empty>}
      {top.length
        ? section(
            'vx-sv-top',
            t(auto.length ? 'l.rec' : 'l.panel'),
            top.map((e) => (
              <li key={e.index}>
                {pick(
                  e,
                  'lc',
                  <>
                    <b>{name(e)}</b>
                  </>,
                )}
              </li>
            )),
            'locs top',
          )
        : null}
      {countries.size
        ? section(
            'vx-sv-cc',
            t('l.countries'),
            Array.from(countries, ([cc, list]) => {
              // The shortest name is the country itself; the others are its variants.
              const main = list.reduce((a, e) => (name(e).length < name(a).length ? e : a));
              const base = name(main);
              // The provider names countries in its own language: the flag says it in the reader's.
              const own = t.lang === 'ru' ? '' : countryName(t.lang, cc);
              return (
                <li key={cc} class={'lc-g' + (list.some((e) => e.index === data.active) ? ' on' : '')}>
                  {pick(
                    main,
                    'lc',
                    <>
                      <b>{base}</b>
                      {own && own !== base ? <span class="hint">{own}</span> : null}
                    </>,
                  )}
                  {list.length > 1 ? (
                    <div class="lc-v" role="group" aria-label={t('l.more')}>
                      {list
                        .filter((e) => e !== main)
                        .map((e) => {
                          const n = name(e);
                          const tail = n.indexOf(base) === 0 ? n.slice(base.length).replace(/^[\s·•|:,–—-]+/, '') : '';
                          return pick(e, 'lcv', <span>{tail || n}</span>, false);
                        })}
                    </div>
                  ) : null}
                </li>
              );
            }),
          )
        : null}
      {other.length
        ? section(
            'vx-sv-other',
            t('l.other'),
            other.map((e) => (
              <li key={e.index}>
                {pick(
                  e,
                  'lc',
                  <b>{name(e)}</b>,
                )}
              </li>
            )),
          )
        : null}
      {data.entries.length ? <Services /> : null}
    </div>
  );
}
