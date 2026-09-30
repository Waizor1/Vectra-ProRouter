// The simple view: one screen for the person who owns the router. It answers
// three questions — does the VPN work, which location am I on, what do I do if
// a site does not open — in words without xray, balancers, nodes or addresses.

import type { ComponentChildren, RefObject } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import type { ErrInfo } from '../api/errors';
import type { Entry, Status } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import { powerOpts } from '../app/power';
import type { Key } from '../i18n';
import { parseRemark } from '../lib/flags';
import { flagged } from '../lib/names';
import { hasSubscription, powerSwitching, simpleVerdict, type LinkState, type SimpleVerdict } from '../lib/health';
import { buildReport } from '../lib/report';
import { copyText } from '../lib/storage';
import { Icon, type IconName } from '../ui/icons';
import { Badge, Button, Note, Skeleton, Spinner } from '../ui/kit';
import { MySites } from './MySites';
import { Services } from './Services';
import { around, SLOT } from './Nodes';
import { face, groupServers } from '../lib/servers';
import { CONFIG_SLOW_MS, needsSetup, owned, Setup, useLate, type Screen } from './Setup';
import { Tour, tourSeen } from './Tour';

const ICON: Record<SimpleVerdict['tone'], IconName> = { ok: 'ok', info: 'info', warn: 'warn', fail: 'fail', mute: 'power' };
const NO_LINK_TITLE: Record<ErrInfo['kind'], Key> = {
  not_found: 's.nl.not_found.t',
  access: 's.nl.access.t',
  method: 's.nl.method.t',
  network: 's.nl.network.t',
  timeout: 's.nl.timeout.t',
  locked: 's.nl.other.t',
  other: 's.nl.other.t',
};
const NO_LINK_LINES: Record<ErrInfo['kind'], Key[]> = {
  not_found: ['s.nl.not_found.d', 's.reboot'],
  access: ['s.nl.access.d'],
  method: ['s.nl.method.d'],
  network: ['s.nl.network.d'],
  timeout: ['s.nl.retrying'],
  locked: ['s.nl.retrying'],
  other: ['s.nl.retrying'],
};

const locName = (remark: string | null | undefined, fallback: string) => {
  const r = parseRemark(remark);
  return r.name || r.flag || fallback;
};

/** The first status never arrived: say why in plain words, keep the raw text for support. */
function NoLink({ err }: { err: ErrInfo }) {
  const { t, refresh } = useApp();
  return (
    <div class="sv" id="vx-panel" tabIndex={-1}>
      <section class="sv-st t-fail" aria-labelledby="vx-verdict">
        <span class="beacon" aria-hidden="true">
          <Icon name="fail" size={24} />
        </span>
        <div class="sv-st-m" role="alert">
          <h2 id="vx-verdict" class="verdict">
            {t(NO_LINK_TITLE[err.kind])}
          </h2>
          {NO_LINK_LINES[err.kind].map((k) => (
            <p key={k}>{t(k)}</p>
          ))}
          <div class="row sv-acts">
            <Button kind="p" icon="refresh" onClick={refresh}>
              {t('retry')}
            </Button>
          </div>
          <details class="sv-raw">
            <summary>{t('details')}</summary>
            <code>{err.raw}</code>
          </details>
        </div>
      </section>
    </div>
  );
}

interface Acts {
  restart(): void;
  locations(): void;
  unpin(): void;
  report(): void;
  link(): void;
  power(on: boolean): void;
}

function StatusCard({ v, checkedAt, acts }: { v: SimpleVerdict; checkedAt: number | null; acts: Acts }) {
  const { t, f, pending, store } = useApp();
  const [checking, setChecking] = useState(false);
  // Only a click shows as busy: the 10 s poll must not make the button flicker.
  const check = () => {
    setChecking(true);
    Promise.all([store.fetch('status'), store.fetch('diagnostics')]).then(() => setChecking(false));
  };
  const act =
    v.act === 'restart' ? (
      <Button kind="p" icon="refresh" busy={pending === 'restart'} disabled={!!pending} onClick={acts.restart}>
        {t('s.act.restart')}
      </Button>
    ) : v.act === 'location' ? (
      <Button kind="p" icon="globe" disabled={!!pending} onClick={acts.locations}>
        {t('s.act.location')}
      </Button>
    ) : v.act === 'unpin' ? (
      <Button kind="p" icon="pin" busy={pending === 'unpin'} disabled={!!pending} onClick={acts.unpin}>
        {t('s.unpinAll')}
      </Button>
    ) : v.act === 'report' ? (
      <Button kind="p" icon="copy" onClick={acts.report}>
        {t('s.help.support.btn')}
      </Button>
    ) : v.act === 'link' ? (
      <Button kind="p" icon="shield" onClick={acts.link}>
        {t('s.act.link')}
      </Button>
    ) : v.act === 'power' ? (
      <Button kind="p" icon="power" busy={pending === 'power:on'} disabled={!!pending} onClick={() => acts.power(true)}>
        {t('s.pw.on')}
      </Button>
    ) : null;
  return (
    <section class={'sv-st sv-st-split t-' + v.tone} aria-labelledby="vx-verdict">
      <span class="beacon" aria-hidden="true">
        {v.kind === 'connecting' || v.kind === 'checking' || v.kind === 'switching' ? <Spinner /> : <Icon name={ICON[v.tone]} size={24} />}
      </span>
      <div class="sv-st-m">
        {/* Announced when the verdict changes; the "checked" clock below is not. */}
        <div aria-live="polite" aria-atomic="true">
          <h2 id="vx-verdict" class="verdict">
            {t(v.title)}
          </h2>
          {v.lines.map((k) => (
            <p key={k}>{t(k)}</p>
          ))}
        </div>
        <div class="row sv-acts">
          {act}
          <span class="row sv-chk">
            {checkedAt !== null ? <span class="hint">{t('s.checked', { when: f.agoSec((Date.now() - checkedAt) / 1000) })}</span> : null}
            <Button kind="g" small icon="refresh" busy={checking} onClick={check}>
              {t('s.checkNow')}
            </Button>
          </span>
        </div>
      </div>
    </section>
  );
}

function Picker({ focus, onPicked }: { focus: boolean; onPicked: () => void }) {
  const { t, run, pending, store } = useApp();
  const res = useRes('entries');
  const data = res.data;
  const current = useRef<HTMLButtonElement>(null);
  const [more, setMore] = useState(false);
  // Opened from the status or the help card, the list is far from where the
  // person was: put keyboard focus on it. Opened by its own toggle, it is not.
  useEffect(() => {
    if (focus && data) current.current?.focus({ preventScroll: true });
  }, [focus, !!data]);
  if (!data) {
    return res.error ? (
      <div class="row sv-list-e">
        <Note tone="fail">{t(NO_LINK_TITLE[res.error.kind])}</Note>
        <Button small icon="refresh" onClick={() => void store.fetch('entries')}>
          {t('retry')}
        </Button>
      </div>
    ) : (
      <Skeleton rows={2} />
    );
  }
  if (!data.entries.length) return <p class="hint">{t('s.loc.none')}</p>;
  const choose = async (e: Entry, name: string) => {
    const ok = await run('select_entry', { index: e.index }, {
      key: 'entry:' + e.index,
      confirm: { title: t('s.loc.q', { name }), body: t('s.help.restart.d'), ok: t('s.loc.go') },
      landed: (s) => s.subscription.entryIndex === e.index,
    });
    if (ok) onPicked();
  };
  const g = groupServers(data);
  // The less usual variants stay folded unless the one in use is among them.
  const showOther = more || g.other.some((e) => e.index === data.active);
  const row = (e: Entry) => {
    const fc = face(e.remark, '#' + e.index);
    const name = locName(e.remark, '#' + e.index);
    const on = e.index === data.active;
    const bd = pending === 'entry:' + e.index || on || e.index === g.recommended;
    return (
      <li key={e.index}>
        <button
          ref={on ? current : undefined}
          type="button"
          class={'sv-li' + (on ? ' on' : '') + (bd ? ' has-bd' : '')}
          title={e.remark}
          aria-current={on || undefined}
          aria-disabled={on || !!pending || undefined}
          onClick={() => on || pending || void choose(e, e.remark.trim() || name)}
        >
          <span class="fl" aria-hidden="true">
            {fc.auto ? <Icon name="split" size={20} /> : fc.flag || <Icon name="globe" size={20} />}
          </span>
          <span class="nm">
            {fc.title}
            {fc.sub ? <span class="nm-sub">{fc.sub}</span> : null}
          </span>
          {pending === 'entry:' + e.index ? (
            <span class="bd t-info">
              <Spinner /> {t('s.loc.switching')}
            </span>
          ) : on ? (
            <Badge tone="info" icon="ok">
              {t('s.loc.now')}
            </Badge>
          ) : e.index === g.recommended ? (
            <Badge tone="ok">{t('w.srv.rec')}</Badge>
          ) : null}
        </button>
      </li>
    );
  };
  return (
    <div id="vx-sl-list" class="sv-servers" role="group" aria-label={t('s.loc.list')}>
      {g.auto.length ? (
        <>
          <h4 class="k">{t('w.srv.auto')}</h4>
          <ul class="sv-list auto">{g.auto.map(row)}</ul>
        </>
      ) : null}
      {g.countries.length ? (
        <>
          <h4 class="k">{t('w.srv.countries')}</h4>
          <ul class="sv-list">{g.countries.map(row)}</ul>
        </>
      ) : null}
      {g.other.length ? (
        showOther ? (
          <>
            <h4 class="k">{t('w.srv.other')}</h4>
            <ul class="sv-list">{g.other.map(row)}</ul>
          </>
        ) : (
          <Button kind="g" small onClick={() => setMore(true)}>
            {t.n('n.moreVariants', g.other.length)}
          </Button>
        )
      ) : null}
    </div>
  );
}

function LocationCard(p: { s: Status; open: 0 | 1 | 2; setOpen: (v: 0 | 1 | 2) => void; cardRef: RefObject<HTMLElement>; acts: Acts }) {
  const { t, run, pending } = useApp();
  const sub = p.s.subscription;
  const has = hasSubscription(p.s);
  const loc = parseRemark(sub.entryRemark);
  const fc = face(sub.entryRemark, t('hero.noLocation'));
  const local = sub.source === 'local';
  const canChange = has && p.s.controller.running !== false;
  const reset = () =>
    run('reset_entry', {}, {
      key: 'entry:reset',
      confirm: { title: t('s.loc.resetQ'), body: t('s.help.restart.d'), ok: t('s.loc.reset') },
      landed: (s) => s.subscription.source === 'panel',
    });
  return (
    <section ref={p.cardRef} class="card sv-loc" aria-labelledby="vx-sl">
      <div class="sv-loc-m">
        <span class="flag" aria-hidden="true">
          {has && fc.auto ? <Icon name="split" size={24} /> : (has && loc.flag) || <Icon name="globe" size={24} />}
        </span>
        <div title={sub.entryRemark ?? undefined}>
          <h3 class="k" id="vx-sl">
            {t('s.loc')}
          </h3>
          <b>{has ? fc.title : t('hero.noLocation')}</b>
          {has && (fc.sub || local || sub.source === 'panel') ? (
            <span class="hint">{[fc.sub, local || sub.source === 'panel' ? t(local ? 's.loc.mine' : 's.loc.default') : null].filter(Boolean).join(' · ')}</span>
          ) : null}
        </div>
        {canChange ? (
          <Button
            icon={p.open ? undefined : 'globe'}
            aria-label={p.open ? undefined : t('s.loc.changeAria')}
            aria-expanded={!!p.open}
            aria-controls={p.open ? 'vx-sl-list' : undefined}
            onClick={() => p.setOpen(p.open ? 0 : 1)}
          >
            {t(p.open ? 's.loc.hide' : 's.loc.change')}
          </Button>
        ) : null}
      </div>
      {has ? <RouteLine s={p.s} /> : null}
      {!has ? <p class="hint">{t('s.loc.none')}</p> : null}
      {sub.overrideStale ? <Note tone="info">{t('s.loc.stale')}</Note> : null}
      {Object.keys(p.s.pins || {}).length ? (
        <Note
          tone="warm"
          action={
            <Button kind="g" small icon="pin" busy={pending === 'unpin'} disabled={!!pending} onClick={p.acts.unpin}>
              {t('s.unpinAll')}
            </Button>
          }
        >
          {t('s.pins')}
        </Note>
      ) : null}
      {p.open && canChange ? <Picker focus={p.open === 2} onPicked={() => p.setOpen(0)} /> : null}
      {local && canChange ? (
        <div class="row">
          <Button kind="g" small icon="refresh" busy={pending === 'entry:reset'} disabled={!!pending} onClick={reset}>
            {t('s.loc.reset')}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

/** Where the main traffic goes now and, after the watchdog moved it, off what (spec decision 6). */
function RouteLine({ s }: { s: Status }) {
  const { t, f } = useApp();
  const r = s.route;
  if (!r || !r.nodes.length) return null;
  const where = (cc: string) => flagged(t.lang, cc);
  const place = (n: { tag: string; country: string | null; egress?: string | null }) => {
    const name = n.country ? where(n.country) : n.tag;
    return n.egress ? t('x.egress', { name, egress: where(n.egress) }) : name;
  };
  const via = Array.from(new Set(r.nodes.map(place))).join(', ');
  const moved = r.movedFrom;
  const key: Key = r.reason === 'failing' ? 's.route.failing' : r.reason === 'fallback' ? 's.route.fallback' : 's.route.borrowed';
  const unfit = Array.from(new Set((r.unfit || []).map(place)));
  const kept = Array.from(new Set((r.unfitKept || []).map(place)));
  return (
    <>
      <p class="hint sv-route">{t('s.route.via', { via })}</p>
      {unfit.length ? (
        <div class="sv-route-unfit">
          <Note tone="info">{t(unfit.length === 1 ? 's.route.unfit.one' : 's.route.unfit.many', { what: unfit.join(', ') })}</Note>
        </div>
      ) : null}
      {kept.length ? (
        <div class="sv-route-unfit">
          <Note tone="warm">{t(kept.length === 1 ? 's.route.unfitKept.one' : 's.route.unfitKept.many', { what: kept.join(', ') })}</Note>
        </div>
      ) : null}
      {moved ? (
        <div class="sv-route-moved">
          <Note tone="warm">
            {t(key, { from: place(moved), to: via })}
            {r.movedAt ? ' · ' + f.ago(r.movedAt) : ''}
          </Note>
        </div>
      ) : null}
    </>
  );
}

/** `restartShown`: the verdict card already offers "Restart the VPN" — no second button for it here. */
function HelpCard({ s, acts, manual, bot, restartShown }: { s: Status; acts: Acts; manual: string | null; bot: string | null; restartShown: boolean }) {
  const { t, pending } = useApp();
  // One action per step; anything more is a link in the step's own sentence.
  const step = (title: Key, hint: Key, btn: ComponentChildren, link?: ComponentChildren) => (
    <li>
      <div>
        <b>{t(title)}</b>
        <span class="hint">
          {t(hint)}
          {link ? <> {link}</> : null}
        </span>
      </div>
      {btn}
    </li>
  );
  return (
    <section class="card sv-help" aria-labelledby="vx-sh">
      <h3 class="k" id="vx-sh">
        {t('s.help')}
      </h3>
      <ol class="sv-steps">
        {s.engine.state !== 'idle' && !restartShown
          ? step(
              's.help.restart.t',
              's.help.restart.d',
              <Button icon="refresh" busy={pending === 'restart'} disabled={!!pending} onClick={acts.restart}>
                {t('s.help.restart.btn')}
              </Button>,
            )
          : null}
        {step(
          's.help.loc.t',
          's.help.loc.d',
          <Button icon="globe" disabled={!!pending} onClick={acts.locations}>
            {t('s.help.loc.btn')}
          </Button>,
        )}
        {step(
          's.help.support.t',
          's.help.support.d',
          <Button icon="copy" onClick={acts.report}>
            {t('s.help.support.btn')}
          </Button>,
          bot ? (
            <a class="lnk" href={'https://t.me/' + encodeURIComponent(bot)} target="_blank" rel="noopener noreferrer">
              {t('s.help.support.open')}
              <Icon name="arrow" size={14} />
            </a>
          ) : null,
        )}
      </ol>
      {manual ? <ManualCopy text={manual} /> : null}
    </section>
  );
}

/** The report, selected, for a browser that refused to copy it. */
function ManualCopy({ text }: { text: string }) {
  const { t } = useApp();
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => box.current?.select(), [text]);
  return (
    <div class="sv-manual">
      <Note tone="warn">{t('s.copyManual')}</Note>
      <textarea ref={box} readOnly rows={10} aria-label={t('s.help.support.t')} value={text} />
    </div>
  );
}

export function Simple() {
  const { t, f, run, toast, store, root, locked, pending } = useApp();
  const st = useRes('status');
  const dg = useRes('diagnostics');
  const setup = useRes('setup');
  // Opened from the footer, or by itself on a router that still misses something —
  // and, once open, it stays until the person closes it: the step that completes
  // the setup must still show its tick and the "All set" screen.
  const [wizard, setWizard] = useState<Screen | null>(null);
  // The tour of this screen, right after the wizard (once per browser) or from the footer.
  const [tour, setTour] = useState(false);
  // Closed once, it stays closed until the page is opened again — even when the
  // router could not note that the setup was finished.
  const [dismissed, setDismissed] = useState(false);
  // Switched off on purpose: the wizard sets Vectra up, and would not open by itself on that.
  const need = needsSetup(setup.data) && !dismissed && st.data?.power.enabled !== false;
  useEffect(() => {
    if (need) setWizard('welcome');
  }, [need]);
  // Linking: the router writes the operator's config before it has the
  // subscription, so "linked" alone is not "set up" yet.
  const vx = setup.data?.vectra;
  const sub = !!st.data && hasSubscription(st.data);
  const waiting = !!vx && !!setup.data && !sub && owned(setup.data) && (vx.linked === true || vx.claim?.state === 'claimed');
  const late = useLate(waiting, CONFIG_SLOW_MS);
  // A subscription that has just gone may be the owner releasing the router:
  // ask `setup` now rather than at its next calm read, so the screen does not
  // claim "getting the settings" in between.
  useEffect(() => {
    if (!sub && vx?.linked === true) void store.fetch('setup', 2000);
  }, [sub]);
  // 0 closed · 1 opened by its toggle · 2 opened from elsewhere (focus moves to the list)
  const [open, setOpen] = useState<0 | 1 | 2>(0);
  const [manual, setManual] = useState<string | null>(null);
  const locRef = useRef<HTMLElement>(null);

  // Fresh locations every time the list opens: the subscription may have changed.
  useEffect(() => {
    if (open) void store.fetch('entries');
  }, [!!open]);

  if (!st.data) return st.error ? <NoLink err={st.error} /> : <Skeleton rows={3} />;
  if (wizard || need) {
    return (
      <div class="sv" id="vx-panel" tabIndex={-1}>
        <Setup
          start={wizard || 'welcome'}
          onClose={(showAround) => {
            setDismissed(true);
            setWizard(null);
            void store.fetch('setup');
            if (showAround && !tourSeen()) setTour(true);
          }}
        />
      </div>
    );
  }
  const s = st.data;
  // Not configured and the setup not read yet: say nothing rather than "not set up".
  if (!hasSubscription(s) && !setup.data && !setup.error) return <Skeleton rows={3} />;
  const bot = vx?.botUsername ?? null;
  // `setup` older than the status that shows the settings gone: re-read before saying which it is.
  const recheck = !sub && vx?.linked === true && (setup.at ?? 0) + 3000 < (st.at ?? 0);
  const link: LinkState = recheck ? 'recheck' : waiting ? (late ? 'late' : 'claimed') : vx && vx.linked === false ? 'unlinked' : null;
  // Vectra being turned on or off from here: say so until the router shows it.
  const v = pending === 'power:on' || pending === 'power:off' ? powerSwitching(pending === 'power:on') : simpleVerdict(s, dg.data, !!dg.error && !dg.data, link);
  // Switched off, what would run through Vectra does not pretend to: no server, no sites, no restart.
  const off = s.power.enabled === false;
  // Restarting or switching needs the controller and something to run.
  const up = !off && s.controller.running !== false && hasSubscription(s);
  // "Checked" is when the servers were last judged: the diagnostics' time.
  const checkedAt = dg.at ?? st.at;

  const acts: Acts = {
    restart: () =>
      void run('restart_xray', {}, {
        key: 'restart',
        confirm: { title: t('s.restartQ'), body: t('s.help.restart.d'), ok: t('s.help.restart.btn') },
        // An answer of `pending` lands when xray runs again under a new pid.
        landed: ({ engine: e }, before) => {
          const b = before?.engine;
          return e.state === 'running' && (e.pid !== null ? e.pid !== b?.pid : e.uptimeSec !== null && b?.uptimeSec != null && e.uptimeSec < b.uptimeSec);
        },
      }),
    locations: () => {
      setOpen(2);
      locRef.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
    },
    // Pins are made in Pro; here there is one way back: all of them, live, no restart.
    unpin: async () => {
      const tags = Object.keys(s.pins || {});
      for (let i = 0; i < tags.length; i++) {
        const ok = await run('unpin_balancer', { balancer: tags[i] }, {
          key: 'unpin',
          confirm: i === 0 ? { title: t('s.unpinQ'), body: t('s.unpinBody'), ok: t('s.unpinAll') } : undefined,
        });
        if (!ok) break;
      }
    },
    link: () => setWizard('vectra'),
    power: (on) => void run('set_power', { on }, powerOpts(t, s, on)),
    report: async () => {
      const text = buildReport(t, f, s, dg.data, t(v.title));
      const ok = await copyText(text, root);
      setManual(ok ? null : text);
      if (ok) toast('ok', t('s.copied'));
    },
  };

  return (
    <div class="sv sv-home" id="vx-panel" tabIndex={-1}>
      {st.error ? (
        <Note tone="warn">
          <b>{t(NO_LINK_TITLE[st.error.kind])}</b> {t('err.stale')}
        </Note>
      ) : dg.error && dg.data ? (
        <Note tone="warn">{t('s.dgStale')}</Note>
      ) : null}
      <StatusCard v={v} checkedAt={checkedAt} acts={acts} />
      {/* A router without settings has no location to show and nothing to try:
          the status card already says so and offers the report. */}
      {!off && (hasSubscription(s) || up) ? (
        // The server and "My sites" stack as one column beside the help on a monitor.
        <div class="sv-col">
          {hasSubscription(s) ? <LocationCard s={s} open={open} setOpen={setOpen} cardRef={locRef} acts={acts} /> : null}
          {up && hasSubscription(s) ? <Services /> : null}
          {up ? <MySites compact /> : null}
        </div>
      ) : null}
      {up ? (
        <HelpCard s={s} acts={acts} manual={manual} bot={bot} restartShown={v.act === 'restart'} />
      ) : (
        <>
          {manual ? <ManualCopy text={manual} /> : null}
          {/* No help card without settings: the way to support stays on screen. */}
          {bot ? (
            <p class="sv-sup">
              <a class="btn bg" href={'https://t.me/' + encodeURIComponent(bot)} target="_blank" rel="noopener noreferrer">
                <Icon name="arrow" size={16} />
                {t('s.help.support.open')}
              </a>
            </p>
          ) : null}
        </>
      )}
      <footer class="sv-foot">
        <span class="sv-dev">
          {[s.router.model, s.router.release, s.version ? 'Vectra ' + s.version : null]
            .filter(Boolean)
            .map((x, i) => (
              <span key={i}>
                {i ? ' · ' : null}
                <span class="nw">{x}</span>
              </span>
            ))}
        </span>
        {locked ? <span>{t('s.lockedNote')}</span> : null}
        {setup.data && !off ? (
          <Button kind="g" small icon="arrow" onClick={() => setWizard('welcome')}>
            {t('w.reopen')}
          </Button>
        ) : null}
        {up ? (
          <Button kind="g" small icon="info" onClick={() => setTour(true)}>
            {t('tour.replay')}
          </Button>
        ) : null}
        {/* Quiet on purpose: the way back to PassWall, or to no VPN at all — also
            while Vectra still runs switched off: a change in flight answers busy,
            one that stopped half way is tried again. */}
        {s.power.enabled === true || s.power.running === true ? (
          <Button kind="g" small icon="power" busy={pending === 'power:off'} disabled={!!pending} onClick={() => acts.power(false)}>
            {t('s.pw.offBtn')}
          </Button>
        ) : null}
        {/* The router answers vectra.lan since the version that has the switch. */}
        {s.power.enabled !== null ? (
          <span class="sv-lan">
            {around(
              t('s.lan', { addr: SLOT }),
              <a class="lnk" href="http://vectra.lan/">
                vectra.lan
              </a>,
            )}
          </span>
        ) : null}
      </footer>
      {tour ? <Tour onDone={() => setTour(false)} /> : null}
    </div>
  );
}
