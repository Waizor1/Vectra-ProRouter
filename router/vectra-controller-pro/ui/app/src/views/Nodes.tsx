import type { ComponentChildren } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import type { Node } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key, T } from '../i18n';
import { delayTone } from '../lib/format';
import { kindOf, nodeName, routeName, type Kind, type Named } from '../lib/names';
import { loadOneOf, save } from '../lib/storage';
import { Bars, Empty, ErrorBox, NodeDot, Seg, Skeleton } from '../ui/kit';

type Filter = 'all' | 'alive' | 'dead' | 'unprobed';
const FILTERS: readonly Filter[] = ['all', 'alive', 'dead', 'unprobed'];
const SORTS = ['delay', 'tag'] as const;
// The groups in order: [kind, title (null: "Hysteria2" in every language), what the kind means].
const GROUPS: [Kind, Key | null, Key | null][] = [
  ['bridge', 'ng.bridge', 'ng.bridge.d'],
  ['hy2', null, null],
  ['wl', 'ng.wl', 'ng.wl.d'],
  ['other', 'ng.other', null],
];

const stateOf = (n: Node): Filter => (n.alive ? 'alive' : n.alive === false ? 'dead' : 'unprobed');
// Working nodes by delay, then working-but-unmeasured, then not probed, then dead.
const rank = (n: Node) => (n.alive ? (n.delayMs !== null ? 0 : 1) : n.alive === null ? 2 : 3);
// A direct or blocking outbound is not a node; should one ever be listed, it is "other".
const groupOf = (tag: string): Kind => {
  const k = kindOf(tag);
  return k === 'direct' || k === 'block' ? 'other' : k;
};

/** Stands for markup in a translated sentence; `around` puts the markup back in its place. */
export const SLOT = '\u0001';

/** A translated sentence with markup where its placeholder was, whatever the language's word order. */
export function around(text: string, inner: ComponentChildren) {
  const [a, b] = text.split(SLOT);
  return (
    <>
      {a}
      {inner}
      {b}
    </>
  );
}

/** Node names with their tags: "Мост → 🇫🇷 Франция bridge-fr5, …". A tag names its country as the router's hint does. */
export const NodeList = ({ tags, t }: { tags: string[]; t: T }) => (
  <>
    {tags.map((m, i) => (
      <span key={m}>
        {i ? t('rt.sep') : null}
        <Nm n={nodeName(t, m)} /> <code class="tg">{m}</code>
      </span>
    ))}
  </>
);

/** A node's name: "Мост → 🇵🇱 Польша"; in a list of its own kind, `short`: "🇵🇱 Польша". The flag is decoration. */
export function Nm({ n, short }: { n: Named; short?: boolean }) {
  const flag = n.flag ? (
    <span class="fl fl-i" aria-hidden="true">
      {n.flag}
    </span>
  ) : null;
  return n.pre && n.short !== n.pre && !short ? (
    <>
      {n.pre}
      <span aria-hidden="true"> → </span>
      {flag}
      {n.short}
    </>
  ) : (
    <>
      {flag}
      {short ? n.short : n.name}
    </>
  );
}

function usePref<V extends string>(key: string, allowed: readonly V[]): [V, (v: V) => void] {
  const [v, set] = useState(() => loadOneOf(key, allowed, allowed[0]));
  return [v, (next: V) => (set(next), save(key, next))];
}

export function Nodes() {
  const { t, f, store, refresh } = useApp();
  const res = useRes('nodes');
  const bals = useRes('balancers').data;
  const [filter, setFilter] = usePref('nodes.filter', FILTERS);
  const [sort, setSort] = usePref('nodes.sort', SORTS);
  // The routes' names come from the balancers: read them when the tab opens, unless fresh.
  useEffect(() => void store.fetch('balancers', 60_000), [store]);
  const data = res.data;
  if (!data) return res.error ? <ErrorBox t={t} err={res.error} onRetry={refresh} /> : <Skeleton />;
  if (!data.nodes.length) return <Empty icon="server">{t('nd.none')}</Empty>;

  const names = new Map(data.nodes.map((n) => [n.tag, nodeName(t, n.tag, n.countryHint)] as const));
  const routes = new Map((bals?.balancers || []).map((b) => [b.tag, routeName(t, b).name] as const));
  const counts = { all: data.nodes.length, alive: 0, dead: 0, unprobed: 0 };
  data.nodes.forEach((n) => counts[stateOf(n)]++);
  const byName = (a: Node, b: Node) =>
    names.get(a.tag)!.short.localeCompare(names.get(b.tag)!.short, t.lang, { numeric: true }) || a.tag.localeCompare(b.tag, 'en', { numeric: true });
  const rows = data.nodes
    .filter((n) => filter === 'all' || stateOf(n) === filter)
    .sort(sort === 'tag' ? byName : (a, b) => rank(a) - rank(b) || (a.delayMs ?? 0) - (b.delayMs ?? 0) || byName(a, b));
  const word = (n: Node) => t(('nd.' + stateOf(n)) as Key);
  const col = (k: string) => t(('col.' + k) as Key);
  // A column nothing can fill is noise: xray's burstObservatory (what the
  // providers ship) reports no probe times, and traffic needs xray's stats.
  const hasTry = data.nodes.some((n) => n.lastTry !== null);
  const hasTraffic = data.nodes.some((n) => !!n.traffic && (n.traffic.upBytes !== null || n.traffic.downBytes !== null));
  const cols = 5 + Number(hasTry) + Number(hasTraffic);

  return (
    <div class="stack">
      <div class="toolbar nd-bar">
        <Seg
          label={t('f.label')}
          value={filter}
          onChange={setFilter}
          options={FILTERS.map((v) => ({
            value: v,
            label: (
              <>
                {t(('f.' + v) as Key)}
                <span class="cnt">{counts[v]}</span>
              </>
            ),
          }))}
        />
        <Seg label={t('s.label')} value={sort} onChange={setSort} options={SORTS.map((v) => ({ value: v, label: t(('s.' + v) as Key) }))} />
        {data.observedAt ? <span class="hint push">{t('d.checked', { when: f.ago(data.observedAt) })}</span> : null}
      </div>

      {rows.length ? (
        <div class="card tbl-w">
          <table class="tbl">
            <thead>
              <tr>
                <th>
                  <span class="sr">{col('status')}</span>
                </th>
                <th>{col('node')}</th>
                <th class="num">{col('delay')}</th>
                <th>{col('bal')}</th>
                <th>{col('proto')}</th>
                {hasTry ? <th>{col('check')}</th> : null}
                {hasTraffic ? <th class="num">{col('traffic')}</th> : null}
              </tr>
            </thead>
            {GROUPS.map(([kind, title, hint]) => {
              const list = rows.filter((n) => groupOf(n.tag) === kind);
              return list.length ? (
                <tbody key={kind}>
                  <tr class="grp">
                    <th colSpan={cols} scope="rowgroup">
                      <b>{title ? t(title) : 'Hysteria2'}</b>
                      <span class="cnt">{list.length}</span>
                      {hint ? <span class="hint">{t(hint)}</span> : null}
                    </th>
                  </tr>
                  {list.map((n) => {
                    const nm = names.get(n.tag)!;
                    const tone = delayTone(n.delayMs);
                    const addr = n.address && n.address + (n.port !== null ? ':' + n.port : '');
                    // The tag stays next to a plain name: support asks for it.
                    const sub = [nm.known ? n.tag : null, addr].filter(Boolean).join(' · ');
                    const tr = n.traffic;
                    return (
                      <tr key={n.tag} class={stateOf(n)}>
                        <td class="c-st">
                          <NodeDot alive={n.alive} label={word(n)} />
                        </td>
                        <td class="c-node">
                          <span class="nn">
                            <b>
                              <Nm n={nm} short={kind !== 'other'} />
                            </b>
                            {n.alive ? null : <small>{word(n)}</small>}
                          </span>
                          {sub ? (
                            <span class="mono mu clip addr" title={sub}>
                              {sub}
                            </span>
                          ) : null}
                        </td>
                        <td class="num c-dly">
                          <span class={'dly' + (tone ? ' dl-' + tone : ' mu')} title={tone ? t(('dl.' + tone) as Key) : undefined}>
                            <Bars tone={tone} />
                            {f.delay(n.delayMs, '—')}
                          </span>
                        </td>
                        <td class="c-bal">
                          <span class="chips">
                            {n.balancers.length
                              ? n.balancers.map((b) => (
                                  <span key={b} class="chip" title={b}>
                                    {routes.get(b) || b}
                                  </span>
                                ))
                              : '—'}
                          </span>
                        </td>
                        <td class="c-proto">
                          <span class="chips">
                            {[n.protocol, n.transport, n.security]
                              .filter((x, i, a): x is string => !!x && a.indexOf(x) === i)
                              .map((x) => (
                                <span key={x} class="chip mono">
                                  {x}
                                </span>
                              ))}
                          </span>
                        </td>
                        {hasTry ? (
                          <td class="c-chk">
                            <span class="mu">{f.ago(n.lastTry, Date.now(), '—')}</span>
                            {n.alive === false && n.lastSeen ? <span class="hint">{t('nd.seen', { when: f.ago(n.lastSeen) })}</span> : null}
                          </td>
                        ) : null}
                        {hasTraffic ? (
                          <td class="num mono c-trf">
                            {tr && (tr.upBytes !== null || tr.downBytes !== null) ? (
                              <span class="trf">
                                <span>↑ {f.bytes(tr.upBytes, '—')}</span>
                                <span>↓ {f.bytes(tr.downBytes, '—')}</span>
                              </span>
                            ) : (
                              <span class="mu">—</span>
                            )}
                          </td>
                        ) : null}
                      </tr>
                    );
                  })}
                </tbody>
              ) : null;
            })}
          </table>
        </div>
      ) : (
        <Empty>{t('nd.noMatch')}</Empty>
      )}
    </div>
  );
}
