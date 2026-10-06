// Port forwarding (Pro), so a person does not have to think about ports: a
// rule starts from what to open (Minecraft, a console, Remote Desktop…), then
// the device, then whether its traffic goes through the VPN. The preset fills
// the port and protocol; they stay one tap away, not in the way. The whole list
// goes back to the router on every change, as with My sites.

import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import type { LanDevice, PortForward, PortForwardIn, PortForwards as Data } from '../api/types';
import { useApp, useRes, type RunOpts } from '../app/ctx';
import type { Key, T } from '../i18n';
import { actionText } from '../lib/labels';
import {
  checkDraft,
  clean,
  draftOf,
  emptyDraft,
  FAIL,
  failKey,
  FIELD_OF,
  groups,
  landed,
  PF_MAX,
  portLabel,
  switched,
  type Draft,
  type DraftRule,
  type Proto,
  withDraft,
  without,
} from '../lib/portForwards';
import { CATS, POPULAR, presetOf, PRESETS, type Cat, type Preset } from '../lib/presets';
import { Icon, type IconName } from '../ui/icons';
import { Button, ErrorBox, Field, Note, Seg, Skeleton } from '../ui/kit';

const CAT_ICON: Record<Cat, IconName> = { games: 'cube', consoles: 'pad', remote: 'system', media: 'play', home: 'home', voice: 'mic', vpn: 'shield' };
const OWN_ICON: Record<string, IconName> = { transmission: 'down', hikvision: 'cam', dahua: 'cam' };
const iconOf = (id: string | null): IconName => {
  const p = presetOf(id);
  return p ? OWN_ICON[p.id] || CAT_ICON[p.cat] : 'ports';
};
const protoName = (t: T, p: string | null) => (p === 'both' ? 'TCP+UDP' : p === 'tcp' || p === 'udp' ? p.toUpperCase() : t('na'));
const presetName = (t: T, p: Preset) => p.name ?? t(('pf.pre.' + p.id) as Key);
/** What a rule opens, in a word: the preset's name, or "Port 25565". */
const titleOf = (t: T, preset: string | null, port: string) => {
  const p = presetOf(preset);
  return p ? presetName(t, p) : t('pf.port.n', { port: portLabel(port) });
};
/** A preset's ports in a few characters: "28015, 28017". */
const portsOf = (rules: { port: string }[]) => rules.map((r) => portLabel(r.port)).join(', ');
/** The rules summed up: "Port 3074 · TCP+UDP", "Ports 3074 · TCP+UDP, 9002 · UDP". */
const linePorts = (t: T, rules: DraftRule[]) =>
  t(rules.length > 1 ? 'pf.line.ports' : 'pf.line.port', { list: rules.map((r) => portLabel(r.port) + ' · ' + protoName(t, r.proto)).join(', ') });
const catName = (t: T, c: Cat) => t(('pf.cat.' + c) as Key);
const deviceOf = (devices: LanDevice[], ip: string) => devices.find((d) => d.ip === ip);

const Tile = ({ icon }: { icon: IconName }) => (
  <span class="pf-ic" aria-hidden="true">
    <Icon name={icon} size={20} />
  </span>
);

/** One line of the list: a preset's rules for one device, or a single rule. */
function Row(p: { rules: PortForward[]; devices: LanDevice[]; onToggle: () => void; onOpen: () => void }) {
  const { t, pending } = useApp();
  const r = p.rules[0];
  const title = titleOf(t, r.preset, r.port);
  const who = r.deviceName ?? deviceOf(p.devices, r.destIp)?.name ?? r.destIp;
  const on = p.rules.some((x) => x.enabled !== false);
  const name = title + ', ' + who;
  return (
    <li class={'pf-r' + (on ? '' : ' off')}>
      <button type="button" class="pf-main" aria-label={t('pf.edit', { name })} disabled={!!pending} onClick={p.onOpen}>
        <Tile icon={iconOf(r.preset)} />
        <span class="pf-txt">
          <span class="pf-t clip">{title}</span>
          <span class="pf-s clip">
            {who}
            {r.direct ? <span class="pf-around"> · {t('pf.direct')}</span> : null}
          </span>
        </span>
      </button>
      <button type="button" role="switch" class="sw pf-sw" aria-checked={on} aria-label={t('pf.rule', { name })} disabled={!!pending} onClick={p.onToggle}>
        <i aria-hidden="true" />
      </button>
    </li>
  );
}

type Step = 'what' | 'all' | 'port' | 'device' | 'done';

/** A choice the size of a finger: icon, a name, a word under it. */
const Choice = (p: { icon?: IconName; title: ComponentChildren; sub?: ComponentChildren; on?: boolean; onClick: () => void; cls?: string }) => (
  <button type="button" class={'pf-ch' + (p.cls ? ' ' + p.cls : '')} aria-pressed={p.on} onClick={p.onClick}>
    {p.icon ? <Tile icon={p.icon} /> : null}
    <span class="pf-txt">
      <span class="pf-t">{p.title}</span>
      {p.sub ? <span class="pf-s clip">{p.sub}</span> : null}
    </span>
  </button>
);

/**
 * The wizard: what (a popular tile, any preset from «All presets», or one's
 * own port) → which device → through the VPN or past it, and save. A saved
 * line opens on the last step, which sums it up; every line there leads back
 * to its step, and Back retraces the way.
 */
function Sheet(p: { data: Data; at: number[]; save: (rules: PortForwardIn[], o: Partial<RunOpts>) => Promise<boolean>; onClose: () => void }) {
  const { t, pending, root } = useApp();
  const { data, at } = p;
  const editing = at.length > 0;
  const [d, setD] = useState<Draft>(() => (editing ? draftOf(data.rules, at) : emptyDraft()));
  // The way here: Back steps along it; going to a step on it again cuts it there.
  const [trail, setTrail] = useState<Step[]>([editing ? 'done' : 'what']);
  const [manual, setManual] = useState(() => editing && !deviceOf(data.devices, d.destIp));
  const [portOpen, setPortOpen] = useState(false);
  const [tried, setTried] = useState(false);
  const [refusal, setRefusal] = useState<{ code: string } | null>(null);
  const [q, setQ] = useState('');
  const [cat, setCat] = useState<Cat | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const step = trail[trail.length - 1];
  const busy = !!pending && pending.startsWith('pf-sheet');
  const shut = () => !busy && p.onClose();
  // From the latest draft: two inputs within one frame must not undo each other.
  const put = (x: Partial<Draft>) => {
    setD((cur) => ({ ...cur, ...x }));
    setRefusal(null);
  };
  const putRule = (i: number, x: Partial<DraftRule>) => {
    setD((cur) => ({ ...cur, rules: cur.rules.map((r, k) => (k === i ? { ...r, ...x } : r)) }));
    setRefusal(null);
  };
  const go = (s: Step) => {
    setTried(false);
    setTrail((tr) => (tr.indexOf(s) >= 0 ? tr.slice(0, tr.indexOf(s) + 1) : tr.concat(s)));
  };

  // Each step starts at its first control; the focus never leaves the sheet.
  useEffect(() => {
    const dlg = ref.current!;
    // The step's question takes the focus (a screen reader reads it); a field, when the step is one.
    (dlg.querySelector<HTMLElement>('.pf-body input') ?? dlg.querySelector<HTMLElement>('h2'))?.focus({ preventScroll: true });
  }, [step]);
  useEffect(() => {
    const dlg = ref.current!;
    const r = root as ShadowRoot;
    let back: HTMLElement | null = null;
    try {
      back = r.activeElement as HTMLElement | null;
    } catch {
      back = null;
    }
    // The "remove?" question sits above the sheet: its focus is its own.
    const trap = (e: Event) =>
      void (dlg.contains(e.target as Node) || (e.target as Element).closest?.('[role="alertdialog"]') || dlg.querySelector<HTMLElement>('.pf-body button')?.focus());
    r.addEventListener('focusin', trap);
    return () => {
      r.removeEventListener('focusin', trap);
      setTimeout(() =>
        back && back.isConnected && !(back as HTMLButtonElement).disabled
          ? back.focus({ preventScroll: true })
          : (r.querySelector?.('#vx-panel') as HTMLElement | null)?.focus({ preventScroll: true }),
      );
    };
  }, []);

  // Esc clears the search first, then leaves; Tab stays inside (the trap above brings a stray focus back).
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    e.preventDefault();
    e.stopPropagation();
    if (step === 'all' && q) setQ('');
    else shut();
  };

  const errors = checkDraft(d, data.rules, at);
  const known = refusal ? failKey(refusal.code) : null;
  const refusalText = refusal ? (known ? t(known) : actionText(t, refusal.code, false, false)) : '';
  const refusalAt = refusal ? FIELD_OF[refusal.code] : undefined;
  const devErr = refusalAt === 'destIp' ? refusalText : tried && errors.destIp ? t(errors.destIp) : null;
  const onFail = (code: string) => {
    setRefusal({ code });
    // A port the router refuses is fixed where the ports are.
    if (FIELD_OF[code] === 'port') setPortOpen(true);
    return true;
  };

  const pick = (x: Preset | null) => {
    setD({ ...emptyDraft(x ?? undefined), destIp: d.destIp, enabled: d.enabled });
    setRefusal(null);
    // Where the makers do not say the protocol, the person picks it: the ports are open for that.
    setPortOpen(!!x && x.rules.some((r) => !r.proto));
    go(x ? (editing ? 'done' : 'device') : 'port');
  };
  const portNext = () => {
    setTried(true);
    if (!errors.port[0]) go(editing ? 'done' : 'device');
  };
  const choose = (ip: string) => {
    setManual(false);
    put({ destIp: ip });
    go('done');
  };
  const ipNext = () => {
    setTried(true);
    if (!errors.destIp) go('done');
  };
  const submit = async () => {
    setTried(true);
    setRefusal(null);
    if (errors.port.some(Boolean)) return setPortOpen(true);
    if (errors.destIp) return go('device');
    if (!clean(errors)) return;
    if (await p.save(withDraft(data.rules, at, d), { key: 'pf-sheet', onFail })) p.onClose();
  };
  const remove = async () => {
    const first = data.rules[at[0]];
    const name = titleOf(t, first.preset, first.port);
    const ok = await p.save(without(data.rules, at), {
      key: 'pf-sheet-del',
      quiet: true,
      onFail,
      confirm: { title: t('pf.delQ'), body: t('pf.delD', { name }), ok: t('pf.del') },
    });
    if (ok) p.onClose();
  };

  const preset = presetOf(d.preset);
  const title =
    step === 'what' ? t('pf.q.what') : step === 'all' ? t('pf.q.all') : step === 'port' ? t('pf.q.port') : step === 'device' ? t('pf.q.device') : titleOf(t, d.preset, d.rules[0].port);
  const dev = deviceOf(data.devices, d.destIp);
  const many = d.rules.length > 1;
  const option = (x: Preset, cls?: string) => (
    <Choice key={x.id} cls={cls} icon={iconOf(x.id)} title={presetName(t, x)} sub={portsOf(x.rules)} on={d.preset === x.id} onClick={() => pick(x)} />
  );
  const custom = (cls?: string) => <Choice cls={cls} icon="ports" title={t('pf.pre.custom')} on={editing && !d.preset} onClick={() => pick(null)} />;
  const protos = [
    { value: 'tcp' as const, label: 'TCP' },
    { value: 'udp' as const, label: 'UDP' },
    { value: 'both' as const, label: t('pf.proto.both') },
  ];
  const portFields = (
    <div class="pf-port">
      {d.rules.map((r, i) => {
        const err = tried ? errors.port[i] : undefined;
        return (
          <div key={i} class="pf-pr">
            <Field
              id={'vx-pf-port' + (i ? '-' + i : '')}
              label={many ? t('pf.port.n', { port: i + 1 }) : t('pf.port')}
              ph={t('pf.port.ph')}
              mode="numeric"
              value={r.port}
              onInput={(v) => putRule(i, { port: v })}
              error={err ? t(err) : null}
            />
            <Seg<Proto>
              label={t('pf.proto')}
              value={r.proto}
              onChange={(v) => putRule(i, { proto: v })}
              options={protos}
            />
          </div>
        );
      })}
      {errors.proto ? <p class="hint">{t('pf.proto.q')}</p> : null}
      {refusalAt === 'port' ? (
        <span class="fld-err" role="alert">
          {refusalText}
        </span>
      ) : null}
    </div>
  );

  let body: ComponentChildren;
  if (step === 'what') {
    body = (
      <div class="pf-grid">
        {POPULAR.map((id) => option(presetOf(id)!, 'pf-tile'))}
        <Choice cls="pf-tile" icon="list" title={t('pf.q.all')} sub={t('pf.all.d')} onClick={() => go('all')} />
        {custom('pf-tile')}
      </div>
    );
  } else if (step === 'all') {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const shown = PRESETS.filter((x) => {
      if (cat && x.cat !== cat) return false;
      const hay = [presetName(t, x), x.id, catName(t, x.cat), portsOf(x.rules)].join(' ').toLowerCase();
      return words.every((w) => hay.indexOf(w) >= 0);
    });
    body = (
      <>
        <div class="pf-find">
          <Icon name="search" size={18} />
          <input type="search" class="in" value={q} placeholder={t('pf.find')} aria-label={t('pf.find')} autoComplete="off" spellcheck={false} onInput={(e) => setQ(e.currentTarget.value)} />
        </div>
        <div class="pf-cats" role="group" aria-label={t('pf.q.all')}>
          {[null, ...CATS].map((c) => (
            <button key={String(c)} type="button" class="pf-cat" aria-pressed={cat === c} onClick={() => setCat(c)}>
              {c ? catName(t, c) : t('pf.cat.all')}
            </button>
          ))}
        </div>
        {shown.length ? (
          CATS.filter((c) => shown.some((x) => x.cat === c)).map((c) => (
            <section key={c} class="pf-sec" aria-label={catName(t, c)}>
              {cat ? null : <h3 class="k">{catName(t, c)}</h3>}
              {shown.filter((x) => x.cat === c).map((x) => option(x))}
            </section>
          ))
        ) : (
          <div class="pf-sec">
            <p class="hint">{t('pf.none')}</p>
            {custom()}
          </div>
        )}
      </>
    );
  } else if (step === 'port') {
    body = (
      <>
        {portFields}
        <Button kind="p" class="pf-go" onClick={portNext}>
          {t('pf.next')}
        </Button>
      </>
    );
  } else if (step === 'device') {
    body = (
      <>
        <div class="pf-devs">
          {data.devices.map((x) => (
            <Choice key={x.ip} title={x.name ?? x.ip} sub={x.name ? x.ip : null} on={!manual && d.destIp === x.ip} onClick={() => choose(x.ip)} />
          ))}
        </div>
        {manual || !data.devices.length ? (
          <div class="pf-port">
            <Field
              id="vx-pf-ip"
              label={t('pf.ip')}
              ph="192.168.1.50"
              mode="decimal"
              value={d.destIp}
              onInput={(v) => put({ destIp: v })}
              error={devErr}
            />
            <Button kind="p" class="pf-go" onClick={ipNext}>
              {t('pf.next')}
            </Button>
          </div>
        ) : (
          <Button kind="g" class="pf-other" icon="plus" onClick={() => (setManual(true), put({ destIp: '' }))}>
            {t('pf.other')}
          </Button>
        )}
      </>
    );
  } else {
    body = (
      <>
        <div class="pf-sum">
          <button type="button" class="pf-line" onClick={() => go('device')}>
            <span class="pf-txt">
              <span class="k">{t('pf.device')}</span>
              <span class="pf-t clip">{dev?.name ?? d.destIp}</span>
            </span>
            <span class="pf-chg">{t('pf.change')}</span>
          </button>
          {devErr ? (
            <span class="fld-err" role="alert">
              {devErr}
            </span>
          ) : null}
          {portOpen ? (
            portFields
          ) : (
            <button type="button" class="pf-line pf-line-s" aria-expanded={false} onClick={() => setPortOpen(true)}>
              <span>{linePorts(t, d.rules)}</span>
              <span class="pf-chg">{t('pf.change')}</span>
            </button>
          )}
          {editing ? (
            <button type="button" class="pf-line pf-line-s" onClick={() => go('what')}>
              <span>{preset ? presetName(t, preset) : t('pf.pre.custom')}</span>
              <span class="pf-chg">{t('pf.change')}</span>
            </button>
          ) : null}
        </div>
        <div class="pf-vpn" role="radiogroup" aria-label={t('pf.vpn.t')}>
          <span class="k" aria-hidden="true">
            {t('pf.vpn.t')}
          </span>
          {([false, true] as const).map((past) => (
            <button key={String(past)} type="button" role="radio" class="pf-ch pf-opt" aria-checked={d.direct === past} onClick={() => put({ direct: past })}>
              <i aria-hidden="true" />
              <span class="pf-txt">
                <span class="pf-t">{t(past ? 'pf.vpn.past' : 'pf.vpn.via')}</span>
                <span class="pf-s">{t(past ? 'pf.vpn.past.d' : 'pf.vpn.via.d')}</span>
              </span>
            </button>
          ))}
        </div>
        {refusal && !refusalAt ? <Note tone="fail">{refusalText}</Note> : null}
        <div class="pf-sheet-a">
          {editing ? (
            <Button kind="g" class="pf-rm" busy={pending === 'pf-sheet-del'} disabled={!!pending} onClick={() => void remove()}>
              {t('pf.del')}
            </Button>
          ) : null}
          <Button kind="p" busy={pending === 'pf-sheet'} disabled={!!pending || errors.proto} onClick={() => void submit()}>
            {t('pf.save')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <div class="scrim pf-scrim" onMouseDown={(e) => e.target === e.currentTarget && shut()}>
      <div ref={ref} class="dlg pf-sheet" role="dialog" aria-modal="true" aria-labelledby="vx-pf-t" onKeyDown={onKey}>
        <div class="pf-sheet-h">
          {trail.length > 1 ? (
            <button type="button" class="btn bi bs bg" aria-label={t('pf.back')} title={t('pf.back')} disabled={busy} onClick={() => setTrail((tr) => tr.slice(0, -1))}>
              <Icon name="arrow" size={18} class="flip" />
            </button>
          ) : step === 'done' ? (
            <Tile icon={iconOf(d.preset)} />
          ) : null}
          <h2 id="vx-pf-t" class="clip" tabIndex={-1}>
            {title}
          </h2>
          <button type="button" class="btn bi bs bg" aria-label={t('close')} title={t('close')} disabled={busy} onClick={shut}>
            <Icon name="close" size={16} />
          </button>
        </div>
        <div class="pf-body">{body}</div>
      </div>
    </div>
  );
}

export function PortForwards() {
  const { t, run, store, pending } = useApp();
  const res = useRes('port_forwards');
  // The line being edited, by its rules' places; [] for a new one.
  const [edit, setEdit] = useState<number[] | null>(null);
  const data = res.data;

  useEffect(() => void store.fetch('port_forwards', 5000), [store]);

  /** The whole list to the router; a `pending` answer is watched until the list shows it. */
  const save = async (rules: PortForwardIn[], o: Partial<RunOpts>): Promise<boolean> => {
    const ok = await run('set_port_forwards', { rules }, { key: 'pf', fail: FAIL, settled: { read: 'port_forwards', ok: (x) => landed(rules, x as Data) }, ...o });
    // The list on screen is the router's: read it before the sheet goes.
    if (ok) await store.fetch('port_forwards', 0);
    return ok;
  };

  if (!data) {
    if (!res.error) return <Skeleton rows={2} />;
    // A vctl older than port forwarding: a calm word, not an error.
    if (res.error.kind === 'method') return <Note tone="info">{t('pf.old')}</Note>;
    return <ErrorBox t={t} err={res.error} onRetry={() => void store.fetch('port_forwards', 0)} />;
  }

  const max = data.max ?? PF_MAX;
  const full = data.rules.length >= max;
  const lines = groups(data.rules);
  const add = (
    <Button kind="p" icon="plus" class="pf-add" disabled={full || !!pending} onClick={() => setEdit([])}>
      {t('pf.add')}
    </Button>
  );

  return (
    <div class="pf">
      <section class="card pf-card" aria-label={t('pf.region')}>
        {data.cgnat ? <Note tone="warn">{t('pf.cgnat')}</Note> : null}
        {lines.length ? (
          <>
            <ul class="pf-list" aria-label={t('pf.region')}>
              {lines.map((at) => {
                const rules = at.map((i) => data.rules[i]);
                return (
                  <Row
                    key={rules[0].id ?? 'i' + at[0]}
                    rules={rules}
                    devices={data.devices}
                    onToggle={() => void save(switched(data.rules, at, !rules.some((r) => r.enabled !== false)), { key: 'pf-' + at[0], quiet: true })}
                    onOpen={() => setEdit(at)}
                  />
                );
              })}
            </ul>
            {data.directActive === false ? <p class="hint">{t('pf.directOff')}</p> : null}
            {full ? <p class="hint">{t('pf.full')}</p> : add}
          </>
        ) : (
          <div class="pf-none">
            <span class="pf-big" aria-hidden="true">
              <Icon name="ports" size={28} />
            </span>
            <p>{t('pf.empty')}</p>
            {add}
          </div>
        )}
      </section>
      {edit && edit.every((i) => i < data.rules.length) ? <Sheet data={data} at={edit} save={save} onClose={() => setEdit(null)} /> : null}
    </div>
  );
}
