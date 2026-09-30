// "My sites": the owner's own exceptions — always without the VPN (a bank that
// refuses VPN addresses) or always through it (a blocked site the provider's
// lists miss). Changes are collected and applied in one go, because applying
// restarts xray and drops the internet for a few seconds.

import { useEffect, useMemo, useState } from 'preact/hooks';
import type { Rules } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { findServices, SERVICE_PREFIX, serviceName, serviceOf } from '../lib/services';
import { normalizeSite } from '../lib/sites';
import { Icon } from '../ui/icons';
import { Button, ErrorBox, Note, Seg, Skeleton } from '../ui/kit';

type List = 'direct' | 'proxy';
const LISTS: readonly List[] = ['direct', 'proxy'];
const MAX = 100;

export function MySites({ compact = false }: { compact?: boolean }) {
  const { t, run, pending, store } = useApp();
  const res = useRes('rules');
  const saved = res.data;
  const [open, setOpen] = useState(!compact);
  const [list, setList] = useState<List>('direct');
  const [draft, setDraft] = useState<Record<List, string[]> | null>(null);
  const [input, setInput] = useState('');
  const [error, setError] = useState<string | null>(null);
  // «+ сервис»: the picker open, and what was typed into it.
  const [svcOpen, setSvcOpen] = useState(false);
  const [svcQ, setSvcQ] = useState('');

  // The folded card shows the counts too, so the lists load either way.
  useEffect(() => {
    void store.fetch('rules', open ? 5000 : 30000);
  }, [open]);

  const cur: Record<List, string[]> = draft ?? { direct: saved?.direct ?? [], proxy: saved?.proxy ?? [] };
  const max = saved?.max ?? MAX;
  const changes = useMemo(() => {
    if (!draft || !saved) return 0;
    const diff = (a: string[], b: string[]) => a.filter((x) => b.indexOf(x) < 0).length + b.filter((x) => a.indexOf(x) < 0).length;
    return diff(draft.direct, saved.direct) + diff(draft.proxy, saved.proxy);
  }, [draft, saved]);

  const label = (l: List) => t(('ms.' + l) as Key);

  if (compact && !open) {
    return (
      <section class="card sv-sites" aria-labelledby="vx-ms">
        <div class="sv-sites-m">
          <div>
            <h3 class="k" id="vx-ms">
              {t('ms.t')}
            </h3>
            <p class="hint">
              {changes ? t.n('n.unsaved', changes) : saved ? t('ms.summary', { direct: saved.direct.length, proxy: saved.proxy.length }) : t('ms.d')}
            </p>
          </div>
          <Button icon="list" onClick={() => setOpen(true)}>
            {t('ms.open')}
          </Button>
        </div>
      </section>
    );
  }

  // A site or a service into the list on screen; false when it cannot go.
  const put = (value: string): boolean => {
    const other: List = list === 'direct' ? 'proxy' : 'direct';
    if (cur[list].indexOf(value) >= 0) return setError(t('ms.dup', { list: label(list) })), false;
    if (cur[other].indexOf(value) >= 0) return setError(t('ms.dup', { list: label(other) })), false;
    if (cur[list].length >= max) return setError(t('ms.full', { max })), false;
    setDraft({ ...cur, [list]: cur[list].concat(value) });
    setError(null);
    return true;
  };
  const add = (e: Event) => {
    e.preventDefault();
    const site = normalizeSite(input);
    if ('error' in site) {
      setError(site.error === 'empty' ? t('w.req') : t('ms.bad'));
      return;
    }
    if (put(site.value)) setInput('');
  };
  const catalog = saved?.catalog ?? [];
  const taken = cur.direct.concat(cur.proxy).map(serviceOf).filter((x): x is string => !!x);
  const found = svcOpen ? findServices(t, catalog, svcQ, taken) : [];
  const addService = (id: string) => {
    if (put(SERVICE_PREFIX + id)) {
      setSvcOpen(false);
      setSvcQ('');
    }
  };
  const shown = (v: string) => {
    const id = serviceOf(v);
    return id ? serviceName(t, id) : v;
  };
  const remove = (l: List, v: string) => setDraft({ ...cur, [l]: cur[l].filter((x) => x !== v) });
  const save = async () => {
    if (!draft || !saved) return;
    // The router keeps the lists only once xray runs them; it may answer
    // `pending` and show them in `rules` a moment later — in its own
    // canonical form, which is what this list shows afterwards.
    const was = JSON.stringify([saved.direct, saved.proxy]);
    const kept = (r: Rules | null) => (r ? JSON.stringify([r.direct, r.proxy]) : was);
    const ok = await run('set_rules', { direct: draft.direct, proxy: draft.proxy }, {
      key: 'rules',
      confirm: { title: t('ms.saveQ'), body: t('s.help.restart.d'), ok: t('ms.save') },
      settled: { read: 'rules', ok: (r) => kept(r as Rules) !== was },
    });
    if (ok) {
      // A read already on its way may carry the old lists: read until the new
      // ones show (the router may also have kept exactly the old ones), and
      // only then drop the draft, so the list never flashes back.
      for (let i = 0; i < 3 && kept(store.get('rules').data) === was; i++) await store.fetch('rules', 0);
      setDraft(null);
    }
  };

  return (
    <section class="card sv-sites" aria-labelledby="vx-ms">
      <div class="card-h">
        <h3 class="k" id="vx-ms">
          {t('ms.t')}
        </h3>
        {compact ? (
          <Button kind="g" small onClick={() => setOpen(false)}>
            {t('s.loc.hide')}
          </Button>
        ) : null}
      </div>
      <p class="lede">{t('ms.d')}</p>
      {!saved ? (
        res.error ? (
          // The simple view speaks the owner's language; Pro shows what went wrong.
          compact ? (
            <Note tone="warn">
              {t('ms.loadFail')}{' '}
              <Button kind="g" small icon="refresh" onClick={() => void store.fetch('rules', 0)}>
                {t('retry')}
              </Button>
            </Note>
          ) : (
            <ErrorBox t={t} err={res.error} onRetry={() => void store.fetch('rules', 0)} />
          )
        ) : (
          <Skeleton rows={2} />
        )
      ) : (
        <>
          <div class="row ms-tabs">
            <Seg
              label={t('ms.lists')}
              value={list}
              onChange={(v) => {
                setList(v);
                setError(null);
              }}
              options={LISTS.map((l) => ({
                value: l,
                label: (
                  <>
                    {label(l)}
                    <span class="sr">: </span>
                    <span class="cnt">{cur[l].length}</span>
                  </>
                ),
              }))}
            />
            <span class="hint push">{t('ms.count', { n: cur[list].length, max })}</span>
          </div>
          <p class="hint">{t(('ms.' + list + '.d') as Key)}</p>
          <form class="ms-add" onSubmit={add}>
            <label class="sr" for="vx-ms-in">
              {t('ms.input')}
            </label>
            <input
              id="vx-ms-in"
              class="in"
              type="text"
              inputMode="url"
              autoComplete="off"
              autoCapitalize="none"
              spellcheck={false}
              placeholder={t('ms.ph')}
              value={input}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? 'vx-ms-err' : 'vx-ms-sub'}
              onInput={(e) => {
                setInput((e.currentTarget as HTMLInputElement).value);
                if (error) setError(null);
              }}
            />
            <Button kind="p" icon="arrow" type="submit" disabled={!!pending}>
              {t('ms.add')}
            </Button>
          </form>
          {error ? (
            <p id="vx-ms-err" class="ms-err" role="alert">
              {error}
            </p>
          ) : (
            <p id="vx-ms-sub" class="hint">
              {t('ms.sub')}
            </p>
          )}
          {catalog.length ? (
            <div class="ms-svc">
              <Button
                kind="g"
                small
                icon="plus"
                aria-expanded={svcOpen}
                onClick={() => {
                  setSvcOpen(!svcOpen);
                  setSvcQ('');
                }}
              >
                {t('ms.svc.add')}
              </Button>
              {svcOpen ? (
                <div class="ms-svc-p">
                  <label class="sr" for="vx-ms-svc">
                    {t('ms.svc.find')}
                  </label>
                  <input
                    id="vx-ms-svc"
                    class="in"
                    type="search"
                    autoComplete="off"
                    spellcheck={false}
                    placeholder={t('ms.svc.ph')}
                    value={svcQ}
                    onInput={(e) => setSvcQ((e.currentTarget as HTMLInputElement).value)}
                  />
                  {found.length ? (
                    <ul class="ms-svc-l" aria-label={t('ms.svc.add')}>
                      {found.map((id) => (
                        <li key={id}>
                          <button type="button" class="ms-svc-r" disabled={!!pending} onClick={() => addService(id)}>
                            {serviceName(t, id)}
                          </button>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p class="hint">{t('ms.svc.none')}</p>
                  )}
                </div>
              ) : null}
            </div>
          ) : null}
          {cur[list].length ? (
            <ul class="ms-list" aria-label={label(list)}>
              {cur[list].map((v) => (
                <li key={v}>
                  <span class="clip">
                    {shown(v)}
                    {serviceOf(v) ? (
                      <>
                        {' '}
                        <span class="ms-tag">
                          {t('ms.svc.tag')}
                          {(saved?.missing ?? []).indexOf(v) >= 0 ? ' · ' + t('ms.svc.missing') : ''}
                        </span>
                      </>
                    ) : null}
                  </span>
                  <button type="button" class="btn bi bs bg" aria-label={t('ms.remove', { site: shown(v) })} title={t('ms.remove', { site: shown(v) })} disabled={!!pending} onClick={() => remove(list, v)}>
                    <Icon name="close" size={14} />
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p class="ms-none">{t('ms.empty')}</p>
          )}
          {changes ? (
            <div class="row ms-bar" role="status">
              <span>{t.n('n.unsaved', changes)}</span>
              <span class="row push">
                <Button kind="g" small disabled={!!pending} onClick={() => setDraft(null)}>
                  {t('ms.discard')}
                </Button>
                <Button kind="p" small busy={pending === 'rules'} disabled={!!pending} onClick={save}>
                  {t('ms.save')}
                </Button>
              </span>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
