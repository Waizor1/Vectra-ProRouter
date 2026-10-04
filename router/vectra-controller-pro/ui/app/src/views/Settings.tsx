// The Pro settings: the router's own controls, the same ones the simple view
// and its wizard use — the Wi-Fi (the wizard's step), the router's password
// (LuCI's change), support's access, and how often the nodes are probed.

import { useEffect, useState } from 'preact/hooks';
import { useApp, useRes } from '../app/ctx';
import { probeSource } from '../lib/labels';
import { Button, Card, Seg, Skeleton, type Opt } from '../ui/kit';
import { PasswordDialog, PasswordNote } from './Password';
import { bandName, byBand, hasAp, Setup } from './Setup';
import { SupportAccess } from './SupportAccess';

const PRESETS = [60, 300, 600, 1800];

/** How often xray probes the nodes. Both a change and the way back restart xray, so both ask first. */
function Probe() {
  const { t, f, run, pending, store } = useApp();
  const probe = useRes('status').data!.probe;
  const prov = useRes('balancers').data?.probe.providerIntervalSec ?? null;
  // The provider's own interval is offered when it is none of the presets: read once.
  useEffect(() => void store.fetch('balancers', 60_000), [store]);
  const cur = probe.intervalSec;
  const [draft, setDraft] = useState(cur);
  useEffect(() => setDraft(cur), [cur]);
  const opts: Opt<number>[] = PRESETS.map((s) => ({ value: s, label: f.dur(s) }));
  if (prov && PRESETS.indexOf(prov) < 0) opts.push({ value: prov, label: t('p.provider', { d: f.dur(prov) }) });
  const apply = (seconds: number) =>
    run('set_probe_interval', { seconds }, {
      key: seconds ? 'probe' : 'probe0',
      confirm: { title: seconds ? t('p.q', { d: f.dur(seconds) }) : t('p.resetQ'), body: t('restartWarn'), ok: t(seconds ? 'p.apply' : 'p.reset') },
      landed: (s) => (seconds ? s.probe.intervalSec === seconds : s.probe.source !== 'local'),
    });
  return (
    <Card id="vx-st-p" title={t('p.title')}>
      <p class="fv">
        <span class="k">{t('p.cur')}</span>
        <b>{cur === null ? t('na') : t('p.every', { d: f.dur(cur) }) + ' · ' + probeSource(t, probe.source)}</b>
      </p>
      <div class="row set-a">
        <Seg label={t('p.title')} value={draft} options={opts} onChange={setDraft} disabled={!!pending} />
      </div>
      <div class="row set-a">
        <Button kind="p" small busy={pending === 'probe'} disabled={!!pending || draft === null || draft === cur} onClick={() => draft && apply(draft)}>
          {t('p.apply')}
        </Button>
        {probe.source === 'local' ? (
          <Button kind="g" small busy={pending === 'probe0'} disabled={!!pending} onClick={() => apply(0)}>
            {t('p.reset')}
          </Button>
        ) : null}
      </div>
    </Card>
  );
}

export function Settings() {
  const { t, store, setPassword } = useApp();
  const s = useRes('status').data!;
  const su = useRes('setup');
  const sd = su.data;
  // The wizard's Wi-Fi step, in place of this tab until it is closed.
  const [wifi, setWifi] = useState(false);
  const [pw, setPw] = useState<'set' | 'change' | null>(null);
  if (wifi) {
    const back = () => {
      setWifi(false);
      void store.fetch('setup', 0);
    };
    return (
      <div class="stack">
        <div class="row">
          <Button kind="g" small icon="arrow" class="back" onClick={back}>
            {t('set.back')}
          </Button>
        </div>
        <Setup start="wifi" onClose={back} />
      </div>
    );
  }
  const radios = sd ? sd.wifi.radios.filter(hasAp).sort(byBand) : [];
  // Each part whole on its line ("Vectra 0.4.0-r1" never split after "Vectra").
  const about = [s.router.model, s.router.release, s.version && 'Vectra ' + s.version, s.engine.xrayVersion && 'Xray ' + s.engine.xrayVersion].filter(Boolean);
  return (
    <div class="stack">
      <div class="setg">
        {/* A router older than the wizard has no `setup` to read: no Wi-Fi here either. */}
        {!sd ? (
          su.error ? null : <Skeleton rows={2} />
        ) : radios.length ? (
          <Card id="vx-st-w" title="Wi-Fi">
            <dl class="fvs">
              {radios.map((r) => (
                <div key={r.device}>
                  <dt>{t('set.net', { band: bandName(t, r.band) })}</dt>
                  <dd>
                    <b class="val">{r.ssid || '—'}</b>
                    {r.enabled === false ? <span class="hint">{t('w.off')}</span> : r.secured === false ? <span class="hint t-warn">{t('set.open')}</span> : null}
                  </dd>
                </div>
              ))}
            </dl>
            <Button icon="wifi" onClick={() => setWifi(true)}>
              {t('set.wifi')}
            </Button>
          </Card>
        ) : null}
        {/* LuCI's own change: there is none to offer outside LuCI. */}
        {setPassword && sd ? (
          <Card id="vx-st-pw" title={t('set.pw')}>
            {sd.passwordSet === false ? (
              <PasswordNote onSet={() => setPw('set')} />
            ) : (
              <>
                <p class="hint">{t('set.pw.d')}</p>
                <Button icon="lock" onClick={() => setPw('change')}>
                  {t('pw.change')}
                </Button>
              </>
            )}
          </Card>
        ) : null}
        {typeof s.remoteShell === 'boolean' ? (
          // The switch names itself: no title above it saying the same.
          <section class="card" aria-label={t('sa.t')}>
            <SupportAccess on={s.remoteShell} />
          </section>
        ) : null}
        <Probe />
      </div>
      {about.length ? (
        <p class="fv about">
          <span class="k">{t('set.about')}</span>
          <span>
            {about.map((x, i) => (
              <span key={i}>
                {i ? ' · ' : null}
                <span class="nw">{x}</span>
              </span>
            ))}
          </span>
        </p>
      ) : null}
      {pw ? <PasswordDialog first={pw === 'set'} onClose={() => setPw(null)} /> : null}
    </div>
  );
}
