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
  draftOf,
  emptyDraft,
  FAIL,
  failKey,
  FIELD_OF,
  landed,
  PF_MAX,
  portLabel,
  presetOf,
  PRESETS,
  type Draft,
  type Preset,
  type Proto,
  wireOf,
  wireOfDraft,
} from '../lib/portForwards';
import { Icon, type IconName } from '../ui/icons';
import { Button, ErrorBox, Field, Note, Seg, Skeleton } from '../ui/kit';

const ICON: Record<string, IconName> = { minecraft: 'cube', playstation: 'pad', xbox: 'pad', rdp: 'system', plex: 'play', torrent: 'down' };
const iconOf = (id: string | null): IconName => (id && Object.prototype.hasOwnProperty.call(ICON, id) ? ICON[id] : 'ports');
const protoName = (t: T, p: string) => (p === 'both' ? 'TCP+UDP' : p === 'tcp' || p === 'udp' ? p.toUpperCase() : p || t('na'));
const presetName = (t: T, p: Preset) => p.name ?? t(('pf.pre.' + p.id) as Key);
/** What a rule opens, in a word: the preset's name, or "Port 25565". */
const titleOf = (t: T, preset: string | null, port: string) => {
  const p = presetOf(preset);
  return p ? presetName(t, p) : t('pf.port.n', { port: portLabel(port) });
};
const deviceOf = (devices: LanDevice[], ip: string) => devices.find((d) => d.ip === ip);
const devName = (r: PortForward, devices: LanDevice[]) => r.deviceName ?? deviceOf(devices, r.destIp)?.name ?? r.destIp;

const Tile = ({ icon }: { icon: IconName }) => (
  <span class="pf-ic" aria-hidden="true">
    <Icon name={icon} size={20} />
  </span>
);

function Row(p: { r: PortForward; devices: LanDevice[]; onToggle: () => void; onOpen: () => void }) {
  const { t, pending } = useApp();
  const { r } = p;
  const title = titleOf(t, r.preset, r.port);
  const who = devName(r, p.devices);
  const on = r.enabled !== false;
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

type Step = 'what' | 'port' | 'device' | 'done';

/** A choice the size of a finger: icon, a name, a word under it. */
const Choice = (p: { icon?: IconName; title: ComponentChildren; sub?: ComponentChildren; on?: boolean; onClick: () => void; cls?: string }) => (
  <button type="button" class={'pf-ch' + (p.cls ? ' ' + p.cls : '')} aria-pressed={p.on} onClick={p.onClick}>
    {p.icon ? <Tile icon={p.icon} /> : null}
    <span class="pf-txt">
      <span class="pf-t">{p.title}</span>
      {p.sub ? <span class="pf-s">{p.sub}</span> : null}
    </span>
  </button>
);

/**
 * The wizard: what → (the port, for one's own) → which device → through the
 * VPN or past it, and save. A saved rule opens on the last step, which sums it
 * up; every line there leads back to its step.
 */
function Sheet(p: { data: Data; index: number; save: (rules: PortForwardIn[], o: Partial<RunOpts>) => Promise<boolean>; onClose: () => void }) {
  const { t, pending, root } = useApp();
  const { data, index } = p;
  const editing = index >= 0 ? data.rules[index] : null;
  const [d, setD] = useState<Draft>(() => (editing ? draftOf(editing) : emptyDraft()));
  const [step, setStep] = useState<Step>(editing ? 'done' : 'what');
  const [manual, setManual] = useState(() => !!editing && !deviceOf(data.devices, editing.destIp));
  const [portOpen, setPortOpen] = useState(false);
  const [tried, setTried] = useState(false);
  const [refusal, setRefusal] = useState<{ code: string } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const busy = !!pending && pending.startsWith('pf-sheet');
  const shut = () => !busy && p.onClose();
  // From the latest draft: two inputs within one frame must not undo each other.
  const put = (x: Partial<Draft>) => {
    setD((cur) => ({ ...cur, ...x }));
    setRefusal(null);
  };
  const go = (s: Step) => {
    setTried(false);
    setStep(s);
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

  // Esc leaves; Tab stays inside (the trap above brings a stray focus back).
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    e.preventDefault();
    e.stopPropagation();
    shut();
  };

  const errors = checkDraft(d, data.rules, index);
  const known = refusal ? failKey(refusal.code) : null;
  const refusalText = refusal ? (known ? t(known) : actionText(t, refusal.code, false, false)) : '';
  const errOf = (f: 'destIp' | 'port') => (refusal && FIELD_OF[refusal.code] === f ? refusalText : tried && errors[f] ? t(errors[f]!) : null);
  const onFail = (code: string) => {
    setRefusal({ code });
    // A port the router refuses is fixed where the port is.
    if (FIELD_OF[code] === 'port') setPortOpen(true);
    return true;
  };

  const pick = (x: Preset | null) => {
    setD({ ...emptyDraft(x ?? undefined), destIp: d.destIp, enabled: d.enabled });
    go(x ? (editing ? 'done' : 'device') : 'port');
  };
  const portNext = () => {
    setTried(true);
    if (!errors.port) go(editing ? 'done' : 'device');
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
    if (errors.port) return setPortOpen(true);
    if (errors.destIp) return go('device');
    const rules = data.rules.map(wireOf);
    const rule = wireOfDraft(d, editing?.id ?? null);
    if (editing) rules[index] = rule;
    else rules.push(rule);
    if (await p.save(rules, { key: 'pf-sheet', onFail })) p.onClose();
  };
  const remove = async () => {
    const name = titleOf(t, editing!.preset, editing!.port);
    const ok = await p.save(
      data.rules.map(wireOf).filter((_, i) => i !== index),
      { key: 'pf-sheet-del', quiet: true, onFail, confirm: { title: t('pf.delQ'), body: t('pf.delD', { name }), ok: t('pf.del') } },
    );
    if (ok) p.onClose();
  };

  const preset = presetOf(d.preset);
  const title = step === 'what' ? t('pf.q.what') : step === 'port' ? t('pf.q.port') : step === 'device' ? t('pf.q.device') : titleOf(t, d.preset, d.port);
  const prev: Step | null = editing ? (step === 'done' ? null : 'done') : step === 'port' ? 'what' : step === 'device' ? (preset ? 'what' : 'port') : step === 'done' ? 'device' : null;
  const dev = deviceOf(data.devices, d.destIp);
  const portFields = (
    <div class="pf-port">
      <Field id="vx-pf-port" label={t('pf.port')} ph={t('pf.port.ph')} mode="numeric" value={d.port} onInput={(v) => put({ port: v })} error={errOf('port')} />
      <Seg<Proto>
        label={t('pf.proto')}
        value={d.proto}
        onChange={(v) => put({ proto: v })}
        options={[
          { value: 'tcp', label: 'TCP' },
          { value: 'udp', label: 'UDP' },
          { value: 'both', label: t('pf.proto.both') },
        ]}
      />
    </div>
  );

  let body: ComponentChildren;
  if (step === 'what') {
    body = (
      <div class="pf-grid">
        {PRESETS.map((x) => (
          <Choice key={x.id} cls="pf-tile" icon={iconOf(x.id)} title={presetName(t, x)} sub={portLabel(x.port)} on={d.preset === x.id} onClick={() => pick(x)} />
        ))}
        <Choice cls="pf-tile" icon="ports" title={t('pf.pre.custom')} on={!!editing && !d.preset} onClick={() => pick(null)} />
      </div>
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
            <Field id="vx-pf-ip" label={t('pf.ip')} ph="192.168.1.50" mode="decimal" value={d.destIp} onInput={(v) => put({ destIp: v })} error={errOf('destIp')} />
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
    const devErr = errOf('destIp');
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
              <span>{t('pf.line.port', { port: portLabel(d.port), proto: protoName(t, d.proto) })}</span>
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
        {refusal && !FIELD_OF[refusal.code] ? (
          <Note tone="fail">
            {refusalText}
          </Note>
        ) : null}
        <div class="pf-sheet-a">
          {editing ? (
            <Button kind="g" class="pf-rm" busy={pending === 'pf-sheet-del'} disabled={!!pending} onClick={() => void remove()}>
              {t('pf.del')}
            </Button>
          ) : null}
          <Button kind="p" busy={pending === 'pf-sheet'} disabled={!!pending} onClick={() => void submit()}>
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
          {prev ? (
            <button type="button" class="btn bi bs bg" aria-label={t('pf.back')} title={t('pf.back')} disabled={busy} onClick={() => go(prev)}>
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
  const [edit, setEdit] = useState<number | null>(null);
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

  const full = data.rules.length >= (data.max ?? PF_MAX);
  const toggle = (i: number) => {
    const rules = data.rules.map(wireOf);
    rules[i] = { ...rules[i], enabled: !rules[i].enabled };
    void save(rules, { key: 'pf-' + i, quiet: true });
  };
  const add = (
    <Button kind="p" icon="plus" class="pf-add" disabled={full || !!pending} onClick={() => setEdit(-1)}>
      {t('pf.add')}
    </Button>
  );

  return (
    <div class="pf">
      <section class="card pf-card" aria-label={t('pf.region')}>
        {data.cgnat ? <Note tone="warn">{t('pf.cgnat')}</Note> : null}
        {data.rules.length ? (
          <>
            <ul class="pf-list" aria-label={t('pf.region')}>
              {data.rules.map((r, i) => (
                <Row key={r.id ?? 'i' + i} r={r} devices={data.devices} onToggle={() => toggle(i)} onOpen={() => setEdit(i)} />
              ))}
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
      {edit !== null && edit < data.rules.length ? <Sheet data={data} index={edit} save={save} onClose={() => setEdit(null)} /> : null}
    </div>
  );
}
