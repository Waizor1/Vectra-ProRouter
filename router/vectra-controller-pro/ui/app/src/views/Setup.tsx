// The setup wizard: what a person sees after unboxing the router. Every step
// checks itself first — a step that is already fine shows a tick and a
// "Next" — so a router the operator prepared goes straight through. Nothing
// here asks for what the router does by itself (the internet connection is
// its own), nothing says "done" before the router shows it, and nothing leaves
// a person without a way forward: every step can be skipped, and the wizard
// closes even when the router could not note that it was finished. One
// exception: a router without a password — anyone on the LAN can open its
// settings — gets that step first, and no way past it until it has one.

import { createContext, type ComponentChildren } from 'preact';
import { useContext, useEffect, useRef, useState } from 'preact/hooks';
import type { Entry, Setup as SetupData, Status, Tune, WanCheck, WifiRadio, WifiScanRadio } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { parseTime } from '../lib/format';
import { hasSubscription } from '../lib/health';
import { face, groupServers } from '../lib/servers';
import { copyText } from '../lib/storage';
import { tuneInPlace, tuneWords } from '../lib/tune';
import { Icon, type IconName } from '../ui/icons';
import { Button, Field, Note, Skeleton, Spinner } from '../ui/kit';
import { Qr } from '../ui/Qr';
import { around, SLOT } from '../ui/words';
import { PasswordFields, usePasswordForm } from './Password';

export type StepId = 'password' | 'internet' | 'wifi' | 'vectra' | 'server';
/** The steps every router goes through; one without a password starts with that (WITH_PW). */
export const STEPS: readonly StepId[] = ['internet', 'wifi', 'vectra', 'server'];
const WITH_PW: readonly StepId[] = ['password', ...STEPS];
export type Screen = 'welcome' | StepId | 'done';

/** The wizard asks for a password: the router has none, and the page can set one (LuCI's change). */
const asksPassword = (s: SetupData, canSet: boolean) => canSet && s.passwordSet === false;

/** The router reaches the outside world: the internet probe answered, or at least the panel did. */
export const online = (w: WanCheck | null | undefined) => !!w && (w.internet === true || w.panel === true);

/** A customer's router: an account claimed it (a fleet router has no owner). */
export const owned = (s: SetupData) => s.vectra.owner != null || s.vectra.claim?.state === 'claimed';

/**
 * Linked, and for a customer's router also running what it got: the router
 * writes the operator's config before it has fetched the subscription, so
 * "linked" alone is not "set up" yet.
 */
const configured = (s: SetupData, st: Status | null | undefined) => s.vectra.linked === true && (!owned(s) || !st || hasSubscription(st));

/** Is this step fine as it is? The internet is confirmed live by wan_check; the link by a subscription in `status`. */
export function stepOk(s: SetupData, id: StepId, wan?: WanCheck | null, st?: Status | null): boolean {
  switch (id) {
    case 'password':
      return s.passwordSet !== false;
    case 'internet':
      return wan ? online(wan) : !!s.wan.ipv4;
    case 'wifi':
      return wifiOk(s.wifi);
    case 'vectra':
      return configured(s, st);
    case 'server':
      // A server is chosen once the router runs one; without the status, as linked.
      return st ? configured(s, st) && hasSubscription(st) : configured(s, st);
  }
}

/**
 * The wizard opens by itself only on a router that was never set up and still
 * misses something — a password among it, where the page can set one.
 */
export const needsSetup = (s: SetupData | null, canSetPassword = false) =>
  !!s && s.done === false && (asksPassword(s, canSetPassword) || STEPS.some((id) => !stepOk(s, id)));

const ICON: Record<StepId, IconName> = { password: 'lock', internet: 'globe', wifi: 'wifi', vectra: 'shield', server: 'server' };

/**
 * A step's badge: what the router shows, in so many words — the internet
 * works, the Wi-Fi runs at full power (or works, with more to get), the
 * subscription is on the router.
 */
function badge(s: SetupData, id: StepId, ok: boolean, wan: WanCheck | null, st: Status | null): Key {
  if (id === 'password') return ok ? 'w.ok.pw' : 'w.pw.todo';
  if (id === 'internet') return ok ? 'w.works' : !wan ? 'w.checking' : wan.link === false ? 'w.nocable' : 'w.noinet';
  if (!ok) return id === 'wifi' && wifiMore(s.wifi) ? 'w.more' : 'w.todo';
  // "At full power" only where the tuning is in place; a working Wi-Fi of the owner's own is "set up".
  if (id === 'wifi') return !s.wifi.radios.some(hasAp) ? 'w.ok.noWifi' : s.wifi.tunable !== false && s.wifi.tuned === true ? 'w.ok.wifi' : 'w.ok.wifiSet';
  if (id === 'vectra') return st && hasSubscription(st) ? 'w.ok.sub' : 'w.ok.linked';
  return 'w.ok.server';
}

/** How long a thing the router does by itself gets before the wizard says what a person can check. */
const SLOW_MS = 60_000;
/** How long the settings may take to arrive after the account was confirmed. */
export const CONFIG_SLOW_MS = 5 * 60_000;

/** A timer that turns true after `ms` while `on`, and resets whenever `key` changes. */
export function useLate(on: boolean, ms: number, key?: unknown): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    setLate(false);
    if (!on) return;
    const id = setTimeout(() => setLate(true), ms);
    return () => clearTimeout(id);
  }, [on, key]);
  return late;
}

function Stepper(p: { s: SetupData; st: Status | null; at: Screen; wan: WanCheck | null; steps: readonly StepId[]; go: (to: StepId) => void }) {
  const { s, st, at, wan, go } = p;
  const { t, pending } = useApp();
  // No step past the password until there is one.
  const shut = p.steps[0] === 'password' && !stepOk(s, 'password');
  return (
    <ol class="wz-steps" aria-label={t('w.steps')}>
      {p.steps.map((id, i) => {
        const ok = stepOk(s, id, wan, st);
        const on = at === id;
        const locked = shut && id !== 'password';
        return (
          <li key={id} class={(on ? 'on ' : '') + (ok ? 'ok' : '') + (locked ? ' shut' : '')}>
            <button type="button" aria-current={on ? 'step' : undefined} disabled={!!pending || locked} onClick={() => go(id)}>
              <i aria-hidden="true">{ok ? <Icon name="ok" size={14} /> : i + 1}</i>
              <span>{t(('w.step.' + id) as Key)}</span>
              <span class="sr">: {t(badge(s, id, ok, wan, st))}</span>
            </button>
          </li>
        );
      })}
    </ol>
  );
}

/** The way back and the way past the current step: the wizard gives them to the step's card. */
const WzNav = createContext<{ back?: () => void; skip?: () => void }>({});

/**
 * A step's card. With `onSubmit` its fields and buttons are one form: Enter
 * saves, as the button does. Its foot holds every way on — "Back" on the left,
 * "Skip this step" and the step's own buttons on the right — and stays in
 * reach while a long step scrolls. A card with its own "Next" (`onward`)
 * needs no "Skip" beside it: both would lead to the same place. `busy`: the
 * step's own work is under way, and the way off waits for it.
 */
function Frame(p: {
  icon: IconName;
  title: string;
  tone?: string;
  children?: ComponentChildren;
  foot: ComponentChildren;
  onSubmit?: () => void;
  onward?: boolean;
  busy?: boolean;
}) {
  const { t, pending } = useApp();
  const nav = useContext(WzNav);
  const skip = p.onward ? undefined : nav.skip;
  const main = p.foot == null || p.foot === false ? null : p.foot;
  const inner = (
    <>
      {p.children == null || p.children === false ? null : <div class="wz-body">{p.children}</div>}
      {main || nav.back || skip ? (
        <div class="wz-foot wz-bar">
          {nav.back ? (
            <Button kind="g" small class="wz-back" disabled={!!pending || p.busy} onClick={nav.back}>
              {t('w.back')}
            </Button>
          ) : null}
          {skip ? (
            <Button kind="g" small class="wz-skip" disabled={!!pending || p.busy} onClick={skip}>
              {t('w.skipStep')}
            </Button>
          ) : null}
          {main ? <div class="wz-main">{main}</div> : null}
        </div>
      ) : null}
    </>
  );
  return (
    <section class={'wz-card card t-' + (p.tone || 'info')} aria-labelledby="vx-wz-t">
      <div class="wz-head">
        <span class="beacon" aria-hidden="true">
          <Icon name={p.icon} size={22} />
        </span>
        {/* Focused when the step changes; announced when the router changes what it says. */}
        <h2 id="vx-wz-t" class="verdict" tabIndex={-1} aria-live="polite">
          {p.title}
        </h2>
      </div>
      {p.onSubmit ? (
        <form
          class="wz-form"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            p.onSubmit!();
          }}
        >
          {inner}
        </form>
      ) : (
        inner
      )}
    </section>
  );
}

/** An IPv4 address, dotted: the only thing put in a link to the router's own address. */
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;

/**
 * Where this page opens, by the router's brand (status.brand). dnsmasq
 * answers the router's names on the LAN, the service on or off: the brand's
 * public site first where it has one (my.vectra-pro.net) — a browser opens a
 * real top-level domain as an address — then its LAN name as it must be typed
 * (a bare `vectra.lan` is a search to a browser); router.lan on a router with
 * no brand. A device with a VPN app or a private DNS asks neither of the
 * router: the router's own address, which always works, when the router says
 * it. `links`: each is a link.
 */
export function WayIn({ ip, links }: { ip: string | null | undefined; links?: boolean }) {
  const { t } = useApp();
  const b = useRes('status').data?.brand;
  const addr = ip && IPV4.test(ip) ? ip : null;
  const at = (text: string, host: string) =>
    links ? (
      <a class="lnk" href={'http://' + host + '/'}>
        {text}
      </a>
    ) : (
      <b class="nw">{text}</b>
    );
  const lan = b?.lanName || 'router.lan';
  const names = [at('http://' + lan, lan)];
  if (b?.site) names.unshift(at(b.site, b.site));
  // 's.lan' / 's.lanIp' name two; 's.lan1' / 's.lanIp1' one.
  const one = names.length > 1 ? '' : '1';
  const p = { a: SLOT, b: SLOT, ip: SLOT };
  return addr ? around(t(('s.lanIp' + one) as Key, p), ...names, at(addr, addr)) : around(t(('s.lan' + one) as Key, p), ...names);
}

/**
 * The way to support: the brand's support bot (status.brand.support — the
 * subscription's, else the brand's own). Never the bot a claim code goes to,
 * and nothing where the router names none.
 */
function Support() {
  const { t } = useApp();
  const bot = useRes('status').data?.brand.support;
  if (!bot) return null;
  return (
    <p>
      <a class="btn bg" href={'https://t.me/' + encodeURIComponent(bot)} target="_blank" rel="noopener noreferrer">
        <Icon name="arrow" size={16} />
        {/* "Message <brand>"; with no brand, words that name no service (`.any`). */}
        {t('s.help.support.open')}
      </a>
    </p>
  );
}

// ── the steps ───────────────────────────────────────────────────────────────

const PW_IDS = ['vx-pw-1', 'vx-pw-2'] as const;

// The router's password, on a router that has none: out of the box anyone on
// the LAN can open its settings. The one step that cannot be skipped; once
// LuCI has the password it says plainly what changes from now on.
function Password({ s, saved, onSaved, next }: { s: SetupData; saved: boolean; onSaved: () => void; next: () => void }) {
  const { t, pending } = useApp();
  const f = usePasswordForm(PW_IDS, onSaved);
  if (saved || s.passwordSet !== false) {
    return (
      <Frame icon="ok" tone="ok" title={t(saved ? 'pw.saved.t' : 'pw.set.t')} foot={<Button kind="p" icon="arrow" onClick={next}>{t('w.next')}</Button>} onward>
        <p>{t(saved ? 'pw.saved.d' : 'pw.set.d')}</p>
      </Frame>
    );
  }
  return (
    <Frame
      icon="lock"
      title={t('pw.t')}
      onSubmit={f.save}
      busy={f.busy}
      foot={
        <Button kind="p" icon="lock" type="submit" busy={f.busy} disabled={!!pending}>
          {t('pw.save')}
        </Button>
      }
    >
      <p class="lede">{t('pw.d')}</p>
      <PasswordFields f={f} ids={PW_IDS} />
    </Frame>
  );
}

// The internet is the router's own business: it connects to the provider by
// itself, so this step has nothing to fill in. It watches, says when the cable
// is missing, and after a minute without internet says what a person can check.
function Internet({ wan, failed, next }: { wan: WanCheck | null; failed: boolean; next: () => void }) {
  const { t } = useApp();
  const ok = online(wan);
  const noLink = wan?.link === false;
  // The minute starts again when the cable goes in or out.
  const slow = useLate(!!wan && !ok, SLOW_MS, noLink);
  const nextBtn = (
    <Button kind="p" icon="arrow" onClick={next}>
      {t('w.next')}
    </Button>
  );

  if (!wan) {
    // The check itself failed: say so, and do not hold the person here.
    return failed ? (
      <Frame icon="warn" tone="warn" title={t('w.net.t')} foot={nextBtn} onward>
        <p>{t('w.net.cantCheck')}</p>
      </Frame>
    ) : (
      <Frame icon="globe" title={t('w.net.t')} foot={null}>
        <p class="row">
          <Spinner /> {t('w.net.checking')}
        </p>
      </Frame>
    );
  }
  if (ok) {
    return (
      <Frame icon="ok" tone="ok" title={t('w.net.ok')} foot={nextBtn} onward>
        <p>{t('w.net.ok.d')}</p>
        {wan.panel === false ? <Note tone="warn">{t('w.net.noPanel')}</Note> : null}
      </Frame>
    );
  }
  return (
    <Frame icon={noLink ? 'warn' : 'globe'} tone={noLink ? 'warn' : 'info'} title={t(noLink ? 'w.net.nolink.t' : 'w.net.wait.t')} foot={null}>
      <p>{t(noLink ? 'w.net.nolink.d' : 'w.net.wait.d')}</p>
      <p class="hint row" role="status">
        <Spinner /> {t('w.net.retrying')}
      </p>
      {slow ? (
        <>
          <Note tone="warn">{t(noLink ? 'w.net.nolink.slow' : 'w.net.wait.slow')}</Note>
          <Support />
        </>
      ) : null}
    </Frame>
  );
}

const WIFI_CHARS = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789';
/** 12 characters without look-alikes (no l/1/I/0/O): easy to type from a phone. */
function genKey(): string {
  const a = new Uint8Array(12);
  (window.crypto || (window as unknown as { msCrypto: Crypto }).msCrypto).getRandomValues(a);
  return Array.from(a, (x) => WIFI_CHARS[x % WIFI_CHARS.length]).join('');
}
/** A value inside a `WIFI:` QR: \\ ; , : and " are escaped with a backslash. */
export const wifiEsc = (v: string) => v.replace(/([\\;,:"])/g, '\\$1');

const BAND_KEY: Record<string, Key> = { '2g': 'w.wifi.band.2g', '5g': 'w.wifi.band.5g', '6g': 'w.wifi.band.6g', '60g': 'w.wifi.band.60g' };
const BAND_ORDER = ['2g', '5g', '6g', '60g'];
export const bandName = (t: (k: Key) => string, band: string | null) => (band && BAND_KEY[band] ? t(BAND_KEY[band]) : band || '—');
export const byBand = (a: WifiRadio, b: WifiRadio) => BAND_ORDER.indexOf(a.band ?? '') - BAND_ORDER.indexOf(b.band ?? '');
/** OpenWrt's own name, or none: a network nobody named yet. */
const unnamed = (r: WifiRadio) => !r.ssid || r.ssid === 'OpenWrt';

/** A radio that carries an access point: the network a name and a password are for. */
export const hasAp = (r: WifiRadio) => r.ap === true;

/** A change the router has not finished, or finished without every band. */
const UNSETTLED = ['applying', 'partial', 'failed'];

/**
 * The router suggests the boost: its verdict says there is more to get (a band
 * down, 2.4 GHz on a channel that overlaps). A router older than the verdict
 * says only whether its own tuning is in place (`tuned`).
 */
export const moreToGet = (w: SetupData['wifi']) => (w.verdict != null ? w.verdict === 'boost' : w.tuned !== true);

/**
 * The Wi-Fi works — a network is on, and every one that is on has a password —
 * and the router sees more to get (its verdict `boost`): a suggestion, not a
 * step left undone.
 */
export function wifiMore(w: SetupData['wifi']): boolean {
  const on = w.radios.filter((r) => hasAp(r) && r.enabled === true);
  return w.tunable !== false && w.verdict === 'boost' && on.length > 0 && on.every((r) => r.secured === true);
}

/**
 * The Wi-Fi is set up: at least one network is on, every network that is on
 * has a password, where the router can tune it its verdict suggests no boost —
 * a working setup of the owner's own is set up, whatever its country, channel
 * or power — and the last change came back whole — no restart still under way,
 * no band left down. A router without an access point has nothing to set up
 * here.
 */
export function wifiOk(w: SetupData['wifi']): boolean {
  const aps = w.radios.filter(hasAp);
  if (!aps.length) return true;
  const on = aps.filter((r) => r.enabled === true);
  if (!on.length || !on.every((r) => r.secured === true)) return false;
  if (w.tunable !== false && moreToGet(w)) return false;
  if (w.apply && UNSETTLED.indexOf(w.apply.state ?? '') >= 0) return false;
  return !on.some((r) => r.up === false);
}

/** The tallest bar: the chart's 72 px less the channel number under it. */
const BAR_PX = 52;

/** How busy each channel was around one radio: a bar per channel, the one the router took marked. */
function Air({ scan }: { scan: WifiScanRadio }) {
  const { t } = useApp();
  const top = Math.max(1, ...scan.channels.map((c) => c.networks));
  // A few bars stand together, not spread across the card.
  return (
    <ul class="air" aria-label={t('w.wifi.air')} style={{ maxWidth: scan.channels.length * 64 + 'px' }}>
      {scan.channels.map((c) => (
        <li key={c.channel} class={c.channel === scan.recommended ? 'best' : undefined}>
          <span class="bar" style={{ height: Math.max(4, Math.round((c.networks / top) * BAR_PX)) + 'px' }} aria-hidden="true" />
          <b>{c.channel}</b>
          <span class="sr">{t.n('n.networks', c.networks)}</span>
        </li>
      ))}
    </ul>
  );
}

/** What the router heard around one band at the boost — or why it did not listen, and which channel it took. */
function Heard({ scan, r }: { scan: WifiScanRadio; r: WifiRadio }) {
  const { t } = useApp();
  if (r.mesh === true) return <p class="hint">{t('w.wifi.air.mesh')}</p>;
  // Not heard: the router kept a channel that works, or took the default (6 or 36).
  const kept = scan.current !== null && scan.recommended === scan.current;
  if (scan.error === 'off') return <p class="hint">{t(kept ? 'w.wifi.air.offKept' : 'w.wifi.air.off')}</p>;
  if (scan.error === 'empty') return <p class="hint">{t('w.wifi.air.empty')}</p>;
  if (scan.error) return <p class="hint">{t(kept ? 'w.wifi.air.failKept' : 'w.wifi.air.fail')}</p>;
  return (
    <>
      <p class="hint">
        {t.n('n.around', scan.networks ?? 0)}
        {scan.recommended !== null ? ' · ' + t('w.wifi.picked', { ch: scan.recommended }) : ''}
      </p>
      {scan.channels.length ? <Air scan={scan} /> : null}
    </>
  );
}

/**
 * One band as it is now, before the boost. `ok`: the Wi-Fi works as it is —
 * its power is said as it is, not as the boost would set it.
 */
function BandCard({ r, tunable, ok, heard, ago }: { r: WifiRadio; tunable: boolean; ok: boolean; heard: WifiScanRadio | undefined; ago: string | null }) {
  const { t } = useApp();
  const channel = r.auto === true || r.channel === null ? t('w.wifi.chAuto') : String(r.channel);
  const full = r.maxPower === true && r.country === 'PA';
  const power: Key = full ? 'w.wifi.powerMax' : !ok ? 'w.wifi.powerUp' : r.maxPower === false ? 'w.wifi.powerOwn' : 'w.wifi.powerOk';
  const state =
    r.enabled !== true ? t(r.secured !== true ? 'w.wifi.offOpen' : 'w.wifi.off') : r.up === false ? t('w.wifi.down') : r.mesh === true ? t('w.wifi.mesh') : null;
  return (
    <article class="band" aria-label={bandName(t, r.band)}>
      <div class="band-h">
        <b>{bandName(t, r.band)}</b>
      </div>
      {state ? <p class={'hint' + (r.enabled === true && r.up === false ? ' t-warn' : '')}>{state}</p> : null}
      {heard && !heard.error && ago ? (
        <>
          <p class="hint">{t('w.wifi.last', { ago })}</p>
          <Heard scan={heard} r={r} />
        </>
      ) : null}
      <dl class="kv">
        {r.ssid && !unnamed(r) ? (
          <div>
            <dt>{t('w.wifi.net')}</dt>
            <dd class="val clip">{r.ssid}</dd>
          </div>
        ) : null}
        <div>
          <dt>{t('w.wifi.channel')}</dt>
          <dd>{channel}</dd>
        </div>
        {tunable ? (
          <div>
            <dt>{t('w.wifi.power')}</dt>
            <dd>{t(power)}</dd>
          </div>
        ) : null}
      </dl>
    </article>
  );
}

/** A Wi-Fi password field: "Generate" and an empty field that keeps the current one where there is one. */
function KeyField(p: { id: string; value: string; onInput: (v: string) => void; error?: string; keep: boolean; show: boolean }) {
  const { t } = useApp();
  return (
    <div class="fld">
      <Field
        id={p.id}
        label={t('w.wifi.key')}
        type={p.show ? 'text' : 'password'}
        value={p.value}
        onInput={p.onInput}
        error={p.error}
        hint={p.keep ? t('w.wifi.keepKey') : undefined}
        auto="new-password"
      />
      <span class="row">
        <Button kind="g" small icon="refresh" onClick={() => p.onInput(genKey())}>
          {t('w.wifi.gen')}
        </Button>
      </span>
    </div>
  );
}

/** A network as the person will join it: its name, and the new password ('' when it stays). */
type Net = { ssid: string; key: string };

const APPLY_TITLE: Record<string, Key> = {
  applying: 'w.wifi.applying.t',
  unverified: 'w.wifi.unver.t',
  partial: 'w.wifi.partial.t',
  rolled_back: 'w.wifi.back.t',
  failed: 'w.wifi.fail.t',
};
/** What went wrong with the last change, in a sentence (the states the router could not finish). */
const APPLY_NOTE: Record<string, Key> = { partial: 'w.wifi.partial.d', rolled_back: 'w.wifi.back.d', failed: 'w.wifi.fail.d' };

/** A Wi-Fi change the wizard is showing the result of: kept by the wizard, so leaving the step does not lose the new password. */
export interface WifiResult {
  nets: Net[];
  /** Listened and tuned (a boost), or only named. */
  tuned: boolean;
  /** `apply.at` before the change: an `apply` still carrying it is the previous change's. */
  prevAt: string | null;
}

/**
 * After the change: what the router says once the Wi-Fi has restarted — every
 * band back up, one that did not come up, or the old settings put back. Until
 * the router writes this change's own result it is "checking", whatever the
 * last one said. The new name and password stay on screen meanwhile: a phone
 * on the old Wi-Fi needs them to come back.
 */
function WifiDone({ s, result, next, again }: { s: SetupData; result: WifiResult; next: () => void; again: () => void }) {
  const { t } = useApp();
  const scan = useRes('wifi_scan').data;
  const { nets, tuned } = result;
  const a = s.wifi.apply;
  const state = a && a.at !== result.prevAt ? a.state ?? 'applying' : 'applying';
  const radios = s.wifi.radios.filter(hasAp).sort(byBand);
  const heard = new Map((scan?.radios ?? []).map((x) => [x.device, x] as const));
  const tone = state === 'ok' ? 'ok' : state === 'applying' || state === 'unverified' ? 'info' : state === 'failed' ? 'fail' : 'warn';
  const gone = state === 'rolled_back' || state === 'failed';
  return (
    <Frame
      icon={state === 'applying' ? 'wifi' : tone === 'ok' ? 'ok' : tone === 'info' ? 'info' : tone}
      tone={tone}
      title={t(state === 'ok' ? (tuned ? 'w.wifi.done.t' : 'w.wifi.saved.t') : APPLY_TITLE[state] ?? 'w.wifi.unver.t')}
      onward
      foot={
        <>
          {state === 'applying' ? null : <Button onClick={again}>{t('w.wifi.redo')}</Button>}
          <Button kind="p" icon="arrow" onClick={next}>
            {t('w.next')}
          </Button>
        </>
      }
    >
      {state === 'applying' ? (
        <p class="row wz-wait" role="status">
          <Spinner /> {t('w.wifi.applying')}
        </p>
      ) : null}
      {APPLY_NOTE[state] ? <Note tone={state === 'failed' ? 'fail' : 'warn'}>{t(APPLY_NOTE[state])}</Note> : null}
      {state === 'unverified' ? <p class="hint">{t('w.wifi.unverified')}</p> : null}
      {/* One card per band: how it came back, and — after a boost — what the router heard. */}
      <div class="bands">
        {radios.map((r) => {
          const settled = state !== 'applying';
          const up = settled && r.up === true;
          const tone = up ? 'ok' : !settled || r.enabled !== true || r.up === null ? 'mute' : 'warn';
          const badge = !settled
            ? t('w.wifi.restarting')
            : r.enabled !== true
              ? t('w.wifi.off')
              : r.up === null
                ? t('w.wifi.unknown')
                : up
                  ? tuned && !gone && r.channel !== null
                    ? t('w.wifi.done.ch', { ch: r.channel })
                    : t('w.works')
                  : t('w.wifi.noUp');
          const h = tuned && !gone ? heard.get(r.device) : undefined;
          return (
            <article class={'band' + (up ? ' up' : '')} key={r.device} aria-label={bandName(t, r.band)}>
              <div class="band-h">
                <span class="sicon" aria-hidden="true">
                  <Icon name={up ? 'ok' : 'wifi'} size={16} />
                </span>
                <b>{bandName(t, r.band)}</b>
                <span class={'bd t-' + tone}>{badge}</span>
              </div>
              {h ? <Heard scan={h} r={r} /> : null}
              {up && tuned && !gone && r.maxPower === true ? (
                <dl class="kv">
                  <div>
                    <dt>{t('w.wifi.power')}</dt>
                    <dd>{t('w.wifi.powerMax')}</dd>
                  </div>
                </dl>
              ) : null}
            </article>
          );
        })}
      </div>
      {!gone
        ? nets.map((n) =>
            n.key ? (
              <div class="wz-qr" key={n.ssid + '\n' + n.key}>
                <Qr text={'WIFI:T:WPA;S:' + wifiEsc(n.ssid) + ';P:' + wifiEsc(n.key) + ';;'} label={t('w.wifi.qr')} px={160} />
                <div>
                  <p>{t('w.wifi.join')}</p>
                  <dl class="kv">
                    <div>
                      <dt>{t('w.wifi.name')}</dt>
                      <dd class="val">{n.ssid}</dd>
                    </div>
                    <div>
                      <dt>{t('w.wifi.key')}</dt>
                      <dd class="val">{n.key}</dd>
                    </div>
                  </dl>
                </div>
              </div>
            ) : (
              // Renamed, the password as it was: no QR (the router never shows a key it keeps).
              <dl class="kv" key={n.ssid}>
                <div>
                  <dt>{t('w.wifi.name')}</dt>
                  <dd class="val">{n.ssid}</dd>
                </div>
                <div>
                  <dt>{t('w.wifi.key')}</dt>
                  <dd>{t('w.wifi.keyKept')}</dd>
                </div>
              </dl>
            ),
          )
        : null}
      {state === 'applying' ? <p class="hint">{t(nets.length ? 'w.wifi.after2' : 'w.wifi.reconnect')}</p> : null}
    </Frame>
  );
}

// Wi-Fi at full power: one click, and the router listens to the air around
// each band, puts every radio on the freest fixed channel at its maximum power
// and restarts the Wi-Fi once — it listens only then, when the person has
// agreed to the restart. Renaming is optional, except for a network that has no
// password yet: that one gets a name and a key first. A router with a band
// Panama's rules do not cover (6 GHz) is only renamed.
function Wifi({ s, next, result, onResult }: { s: SetupData; next: () => void; result: WifiResult | null; onResult: (r: WifiResult | null) => void }) {
  const { t, f, run, pending, store } = useApp();
  const scanRes = useRes('wifi_scan');
  const brandName = useRes('status').data?.brand.name;
  const radios = s.wifi.radios.filter(hasAp).sort(byBand);
  // An open network must get a name and a password when it is on the air — or,
  // on a box with every band off, to have Wi-Fi at all. One that is off beside
  // a working one is left as it is.
  const noneOn = !radios.some((r) => r.enabled === true);
  const open = radios.filter((r) => r.secured !== true && (r.enabled === true || noneOn));
  const mustName = (r: WifiRadio) => open.indexOf(r) >= 0;
  const tunable = s.wifi.tunable !== false;
  const ok = wifiOk(s.wifi);
  // The air as the router heard it at the last boost: reading it does not make the router listen.
  useEffect(() => {
    if (radios.length && tunable) void store.fetch('wifi_scan', 60_000);
  }, []);
  const scan = scanRes.data;
  const byDev = new Map((scan?.radios ?? []).map((x) => [x.device, x] as const));
  const ago = scan?.scannedAt ? f.ago(scan.scannedAt) : null;
  // A router that suggests no name (an older one): the brand's, as the router names it, else its own.
  const base = radios.find((r) => !unnamed(r))?.ssid || s.wifi.suggested || brandName || 'Router';
  const [edit, setEdit] = useState(open.length > 0);
  const [same, setSame] = useState(radios.every((r) => r.ssid === radios[0]?.ssid) || radios.every(unnamed));
  const [shared, setShared] = useState<Net>(() => ({ ssid: base, key: open.length ? genKey() : '' }));
  const [per, setPer] = useState<Record<string, Net>>(() => {
    const out: Record<string, Net> = {};
    for (const r of radios) out[r.device] = { ssid: unnamed(r) ? base + (r.band === '5g' ? '-5G' : '') : r.ssid!, key: mustName(r) ? genKey() : '' };
    return out;
  });
  const [show, setShow] = useState(open.length > 0);
  const [errs, setErrs] = useState<Record<string, string>>({});

  if (!radios.length) {
    return (
      <Frame icon="info" title={t('w.wifi.t')} foot={<Button kind="p" icon="arrow" onClick={next}>{t('w.next')}</Button>} onward>
        <p>{t('w.wifi.none')}</p>
      </Frame>
    );
  }
  if (result) return <WifiDone s={s} result={result} next={next} again={() => onResult(null)} />;

  const check = (id: string, v: Net, needKey: boolean, e: Record<string, string>) => {
    const name = v.ssid.trim();
    if (!name) e[id + ':n'] = t('w.wifi.noName');
    else if (new TextEncoder().encode(name).length > 32) e[id + ':n'] = t('w.wifi.longName');
    if ((needKey || v.key) && !/^[\x20-\x7e]{8,63}$/.test(v.key)) e[id + ':k'] = t('w.wifi.badKey');
  };
  // `boost`: listen to the air and tune (with any new names) — else only the names are saved.
  const save = async (boost: boolean) => {
    if (pending) return;
    const tune = tunable && boost;
    const e: Record<string, string> = {};
    const names: Record<string, { ssid: string; key?: string }> = {};
    const nets: Net[] = [];
    // A network to join again: a new password, or a new name.
    const join = (r: WifiRadio, v: Net) => {
      const n = { ssid: v.ssid.trim(), key: v.key };
      if ((n.key || n.ssid !== r.ssid) && !nets.some((x) => x.ssid === n.ssid && x.key === n.key)) nets.push(n);
    };
    // A network is named when it has a password (its own or the new one); an open
    // one with no new password is left untouched (it stays off).
    if (edit) {
      if (same) {
        check('all', shared, open.length > 0, e);
        for (const r of radios) {
          if (r.secured !== true && !shared.key) continue;
          names[r.device] = shared.key ? { ssid: shared.ssid.trim(), key: shared.key } : { ssid: shared.ssid.trim() };
          join(r, shared);
        }
      } else {
        for (const r of radios) {
          const v = per[r.device];
          check(r.device, v, mustName(r), e);
          if (r.secured !== true && !v.key) continue;
          names[r.device] = v.key ? { ssid: v.ssid.trim(), key: v.key } : { ssid: v.ssid.trim() };
          join(r, v);
        }
      }
    }
    setErrs(e);
    if (Object.keys(e).length) return;
    if (!tune && !Object.keys(names).length) return;
    // The last change's result, as the router has it now: a new one carries a new `at`.
    const prevAt = s.wifi.apply?.at ?? null;
    const applied = await run(tune ? 'optimize_wifi' : 'set_wifi', edit && Object.keys(names).length ? { radios: names } : {}, {
      key: 'wifi',
      // The next screen says how it went, once the router has seen the Wi-Fi come back:
      // a "done" toast now would come before the router knows.
      quiet: true,
      // "Busy" here is another Wi-Fi change under way — or changes left unapplied in LuCI.
      fail: { busy: 'w.wifi.busy' },
      confirm: tune
        ? { title: t('w.wifi.boostQ'), body: t('w.wifi.boostD'), ok: t('w.wifi.boost') }
        : { title: t('w.wifi.saveQ'), body: t('w.wifi.warn2'), ok: t('w.wifi.save') },
    });
    if (!applied) return;
    // What the router heard and how the restart went: both change right now.
    void store.fetch('setup', 0);
    if (tune) void store.fetch('wifi_scan', 0);
    onResult({ nets, tuned: tune, prevAt });
  };
  const set = (id: string, k: keyof Net) => (v: string) =>
    id === 'all' ? setShared((p) => ({ ...p, [k]: v })) : setPer((p) => ({ ...p, [id]: { ...p[id], [k]: v } }));
  const last = s.wifi.apply?.state;
  // How the Wi-Fi is, in the router's words: its own tuning in place, a working
  // setup of the owner's (fine, or theirs by hand), or more to get — a band down,
  // 2.4 GHz on a channel that overlaps, or on 12-13 that some devices do not
  // see. The boost is in reach whatever it says.
  const tuned = s.wifi.tuned === true;
  const manual = s.wifi.verdict === 'manual';
  const more = wifiMore(s.wifi);
  const down = radios.find((r) => r.enabled === true && r.up === false);
  const odd = radios.find((r) => r.enabled === true && r.band === '2g' && r.mesh !== true && r.auto !== true && r.channel !== null && [1, 6, 11].indexOf(r.channel) < 0);
  const title: Key = !tunable ? 'w.wifi.t' : ok ? (tuned ? 'w.wifi.tuned' : manual ? 'w.wifi.manual' : 'w.wifi.fine') : more ? 'w.wifi.more' : 'w.wifi.boost.t';
  const lede = !tunable
    ? t('w.wifi.untunable')
    : ok
      ? t(tuned ? 'w.wifi.tuned.d' : manual ? 'w.wifi.manual.d' : 'w.wifi.fine.d')
      : more && down
        ? t('w.wifi.more.down', { band: bandName(t, down.band) })
        : more && odd
          ? t(odd.channel! > 11 ? 'w.wifi.more.edge' : 'w.wifi.more.ch', { ch: odd.channel! })
          : t('w.wifi.boost.d');

  return (
    <Frame
      icon={ok ? 'ok' : 'wifi'}
      tone={ok ? 'ok' : 'info'}
      title={t(title)}
      onSubmit={() => void save(!ok)}
      onward={ok && !edit}
      foot={
        ok && !edit ? (
          <>
            {tunable ? (
              <Button icon="wifi" onClick={() => void save(true)} busy={pending === 'wifi'} disabled={!!pending}>
                {t(tuned ? 'w.wifi.again' : 'w.wifi.boost')}
              </Button>
            ) : null}
            <Button kind="p" icon="arrow" onClick={next}>
              {t('w.next')}
            </Button>
          </>
        ) : !tunable && !edit ? null : (
          <>
            {/* Renaming is optional: it can be put back. */}
            {edit && !open.length ? (
              <Button
                onClick={() => {
                  setEdit(false);
                  setErrs({});
                }}
              >
                {t('cancel')}
              </Button>
            ) : null}
            <Button kind="p" icon="wifi" type="submit" busy={pending === 'wifi'} disabled={!!pending}>
              {t(tunable && !ok ? 'w.wifi.boost' : 'w.wifi.save')}
            </Button>
          </>
        )
      }
    >
      <p class="lede">{lede}</p>
      {last === 'rolled_back' ? <Note tone="warn">{t('w.wifi.lastBack')}</Note> : last === 'partial' || last === 'failed' ? <Note tone="warn">{t(APPLY_NOTE[last])}</Note> : null}
      <div class="bands">
        {radios.map((r) => (
          <BandCard key={r.device} r={r} tunable={tunable} ok={ok} heard={byDev.get(r.device)} ago={ago} />
        ))}
      </div>
      {edit ? (
        <fieldset class="names">
          <legend class="k">{t(open.length ? 'w.wifi.namesReq' : 'w.wifi.names')}</legend>
          {radios.length > 1 ? (
            <label class="chkbx">
              <input type="checkbox" checked={same} onChange={(e) => setSame((e.currentTarget as HTMLInputElement).checked)} />
              {t('w.wifi.same')}
            </label>
          ) : null}
          {same ? (
            <div class="fld-g">
              <Field id="vx-w-ssid" label={t('w.wifi.name')} value={shared.ssid} onInput={set('all', 'ssid')} error={errs['all:n']} />
              <KeyField id="vx-w-key" value={shared.key} onInput={set('all', 'key')} error={errs['all:k']} keep={open.length === 0} show={show} />
            </div>
          ) : (
            radios.map((r) => (
              <div class="fld-g" key={r.device} role="group" aria-label={bandName(t, r.band)}>
                <Field id={'vx-w-ssid-' + r.device} label={t('w.wifi.nameOf', { band: bandName(t, r.band) })} value={per[r.device].ssid} onInput={set(r.device, 'ssid')} error={errs[r.device + ':n']} />
                <KeyField id={'vx-w-key-' + r.device} value={per[r.device].key} onInput={set(r.device, 'key')} error={errs[r.device + ':k']} keep={r.secured === true} show={show} />
              </div>
            ))
          )}
          <label class="chkbx">
            <input type="checkbox" checked={show} onChange={(e) => setShow((e.currentTarget as HTMLInputElement).checked)} />
            {t('w.wifi.show')}
          </label>
        </fieldset>
      ) : (
        <p>
          <Button kind="g" small icon="lock" onClick={() => setEdit(true)}>
            {t('w.wifi.names')}
          </Button>
        </p>
      )}
      {!ok || edit ? <Note tone="info">{t('w.wifi.warn2')}</Note> : null}
    </Frame>
  );
}

// The subscription's servers: "Auto" (recommended) picks the route by itself;
// a person who needs one country picks it. Switching restarts the VPN for a
// moment, which is fine here: the router is being set up.
function Server({ s, st, next }: { s: SetupData; st: Status | null; next: () => void }) {
  const { t, run, pending, store } = useApp();
  const res = useRes('entries');
  const ready = configured(s, st) && !!st && hasSubscription(st);
  useEffect(() => {
    if (ready) void store.fetch('entries', 5000);
  }, [ready]);
  const [pick, setPick] = useState<number | null>(null);
  const [more, setMore] = useState(false);
  // A switch that lands after the person left the step does not move them on.
  const here = useRef(true);
  useEffect(
    () => () => {
      here.current = false;
    },
    [],
  );
  if (!ready) {
    return (
      <Frame icon="server" title={t('w.srv.t')} foot={null}>
        <p class="row">
          <Spinner /> {t('w.srv.wait')}
        </p>
      </Frame>
    );
  }
  if (!res.data) {
    return (
      <Frame icon="server" title={t('w.srv.t')} foot={<Button kind="p" icon="arrow" onClick={next}>{t('w.next')}</Button>} onward>
        {res.error ? (
          <p class="row">
            {t('w.srv.fail')}{' '}
            <Button small icon="refresh" onClick={() => void store.fetch('entries', 0)}>
              {t('retry')}
            </Button>
          </p>
        ) : (
          <Skeleton rows={2} />
        )}
      </Frame>
    );
  }
  const g = groupServers(res.data);
  const active = res.data.active ?? st?.subscription.entryIndex ?? null;
  const chosen = pick ?? active ?? g.recommended;
  const go = async () => {
    if (pending) return;
    if (chosen === null || chosen === active) return next();
    const ok = await run('select_entry', { index: chosen }, { key: 'entry:' + chosen, landed: (x) => x.subscription.entryIndex === chosen });
    if (ok && here.current) next();
  };
  const item = (e: Entry) => {
    const fc = face(e.remark, '#' + e.index);
    const on = e.index === chosen;
    const bds = e.index === g.recommended || e.index === active;
    return (
      <li key={e.index}>
        <button type="button" class={'sv-li' + (on ? ' on' : '') + (bds ? ' has-bd' : '')} aria-pressed={on} title={e.remark} onClick={() => setPick(e.index)}>
          <span class="fl" aria-hidden="true">
            {fc.auto ? <Icon name="split" size={20} /> : fc.flag || <Icon name="globe" size={20} />}
          </span>
          <span class="nm">
            {fc.title}
            {fc.sub ? <span class="nm-sub">{fc.sub}</span> : null}
          </span>
          {bds ? (
            <span class="bds">
              {e.index === g.recommended ? <span class="bd t-ok">{t('w.srv.rec')}</span> : null}
              {e.index === active ? <span class="bd t-info">{t('s.loc.now')}</span> : null}
            </span>
          ) : null}
        </button>
      </li>
    );
  };
  return (
    <Frame
      icon="server"
      title={t('w.srv.t')}
      onSubmit={go}
      onward
      foot={
        <Button kind="p" icon="arrow" type="submit" busy={!!pending && pending.startsWith('entry:')} disabled={!!pending}>
          {t('w.next')}
        </Button>
      }
    >
      <p class="lede">{t('w.srv.d')}</p>
      {g.auto.length ? (
        <>
          <h3 class="k">{t('w.srv.auto')}</h3>
          <ul class="sv-list auto">{g.auto.map(item)}</ul>
        </>
      ) : null}
      {g.countries.length ? (
        <>
          <h3 class="k">{t('w.srv.countries')}</h3>
          <ul class="sv-list">{g.countries.map(item)}</ul>
        </>
      ) : null}
      {g.other.length ? (
        more ? (
          <>
            <h3 class="k">{t('w.srv.other')}</h3>
            <ul class="sv-list">{g.other.map(item)}</ul>
          </>
        ) : (
          <p>
            <Button kind="g" small onClick={() => setMore(true)}>
              {t.n('n.moreVariants', g.other.length)}
            </Button>
          </p>
        )
      ) : null}
      <p class="hint">{t('w.srv.later')}</p>
    </Frame>
  );
}

function Vectra({ s, st, wan, next }: { s: SetupData; st: Status | null; wan: WanCheck | null; next: () => void }) {
  const { t, f, toast, root } = useApp();
  const c = s.vectra.claim;
  const [, tick] = useState(0);
  // The expiry countdown moves once a second only while it is on screen.
  useEffect(() => {
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);
  const owner = s.vectra.owner?.label || c?.owner?.label || null;
  const done = configured(s, st);
  // Confirmed in the app, or linked with the subscription still on its way.
  const waiting = !done && (s.vectra.linked === true || c?.state === 'claimed');
  const late = useLate(waiting, CONFIG_SLOW_MS);

  if (done) {
    return (
      <Frame icon="ok" tone="ok" title={t('w.v.ok')} foot={<Button kind="p" icon="arrow" onClick={next}>{t('w.next')}</Button>} onward>
        {owner ? <p>{t('w.v.owner', { owner })}</p> : null}
      </Frame>
    );
  }
  if (waiting) {
    return (
      <Frame icon="ok" tone="ok" title={t('w.v.claimed')} foot={null}>
        {owner ? <p>{t('w.v.owner', { owner })}</p> : null}
        <p class="row wz-wait" role="status">
          <Spinner /> {t('w.v.config')}
        </p>
        {late ? (
          <>
            <Note tone="warn">{t('w.v.slow')}</Note>
            <Support />
          </>
        ) : null}
      </Frame>
    );
  }
  if (!c || !c.code) {
    return (
      <Frame icon="warn" tone="warn" title={t('w.v.t')} foot={<Button kind="p" icon="arrow" onClick={next}>{t('w.next')}</Button>} onward>
        <p>{wan && !online(wan) ? t('w.v.offline') : t('w.v.unavailable')}</p>
        <Support />
      </Frame>
    );
  }
  const code = c.code.length === 8 ? c.code.slice(0, 4) + '-' + c.code.slice(4) : c.code;
  // How long THIS code keeps working. The router puts a new one on screen a
  // little before (contract: 2 minutes), and the old one still works until then.
  const exp = parseTime(c.expiresAt);
  const left = exp === null ? null : Math.max(0, Math.round((exp - Date.now()) / 1000));
  const copy = async () => {
    const ok = await copyText(c.code!, root);
    toast(ok ? 'ok' : 'fail', t(ok ? 'w.v.copied' : 'j.copyFail'));
  };
  // A router with no brand says the `.any` sentences (i18n brandT): the owner's own VPN service takes the code.
  return (
    <Frame icon="shield" title={t('w.v.t')} foot={null}>
      {/* A computer: the QR to scan with the phone, beside how. A phone: this page
          is on the phone already — Telegram first, the code, the QR last. */}
      <div class={'wz-link' + (c.qr ? '' : ' no-qr')}>
        {c.qr ? (
          <div class="wz-link-qr">
            <Qr text={c.qr} label={t('w.v.qr')} />
          </div>
        ) : null}
        {c.qr ? <p class="k wz-link-other">{t('w.v.other')}</p> : null}
        <ol class="wz-howto wz-link-how">
          <li>{t('w.v.s1')}</li>
          <li>{t('w.v.s2')}</li>
          {/* No QR: "not yet" on a branded router; never one on a router with no brand (`w.v.noQr.any`). */}
          <li>{c.qr ? t('w.v.s3') : t('w.v.noQr')}</li>
        </ol>
        <div class="wz-link-code">
          <p class="hint">{t(c.qr ? 'w.v.code' : 'w.v.codeOnly')}</p>
          <p class="row wz-code">
            <b class="val">{code}</b>
            <Button kind="g" small icon="copy" onClick={copy}>
              {t('w.v.copy')}
            </Button>
          </p>
          {left !== null ? <p class="hint">{t('w.v.expires', { d: f.dur(left) })}</p> : null}
        </div>
        {c.botUrl ? (
          <div class="wz-link-tg">
            <a class="btn bp" href={c.botUrl} target="_blank" rel="noopener noreferrer">
              <Icon name="arrow" size={16} />
              {t('w.v.tg')}
            </a>
            <p class="hint">{t('w.v.tgHint')}</p>
          </div>
        ) : null}
      </div>
      {wan && !online(wan) ? (
        <Note tone="warn">{t('w.v.offline')}</Note>
      ) : wan && wan.panel === false ? (
        <Note tone="warn">{t('w.v.noPanel')}</Note>
      ) : null}
      <p class="row wz-wait" role="status">
        <Spinner /> {t('w.v.waiting')}
      </p>
    </Frame>
  );
}

/**
 * What the router's tune set up (contract: Tune), in plain words, at the end
 * of the wizard. "At the maximum" once the tune has nothing left to do; until
 * then (switched off, or a change still to come) it only names what is set.
 */
function TuneDone({ tune }: { tune: Tune }) {
  const { t, f } = useApp();
  const { ids, zramMiB } = tuneInPlace(tune);
  const words = tuneWords(t, f, ids, zramMiB);
  if (!words.length) return null;
  const max = tune.enabled !== false && !tune.items.some((it) => it.state === 'pending');
  return (
    <div class="wz-tune">
      <div class="wz-tune-h">
        <span class="sicon" aria-hidden="true">
          <Icon name="gauge" size={18} />
        </span>
        <h3 id="vx-wz-tune">{t(max ? 'w.tune.t' : 'd.tune.t')}</h3>
      </div>
      <ul aria-labelledby="vx-wz-tune">
        {words.map((w) => (
          <li key={w}>
            <Icon name="ok" size={16} />
            <span>{w}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The brand's network name, offered at the end (setup.wifi.rename) while every
 * network still has the model's. Only when asked — renaming drops every device
 * off the Wi-Fi for a moment — after the Wi-Fi step's own dialog; then what the
 * router says once the Wi-Fi is back (setup.wifi.apply), as that step does.
 */
function Rename({ s }: { s: SetupData }) {
  const { t, run, pending, store } = useApp();
  // `apply.at` when the change was sent (undefined: not sent): an `apply` still carrying it is the previous change's.
  const [prevAt, setPrevAt] = useState<string | null | undefined>(undefined);
  const ssid = s.wifi.rename;
  const a = s.wifi.apply;
  const state = prevAt === undefined ? null : a && a.at !== prevAt ? a.state ?? 'applying' : 'applying';
  // Offered until it is under way or done; again after a change that did not go through.
  const offer = !!ssid && state !== 'applying' && state !== 'ok';
  const rename = async () => {
    const at = a?.at ?? null;
    // Only the name: a secured network keeps its password (contract: set_wifi).
    const radios = Object.fromEntries(s.wifi.radios.filter((r) => r.ap === true).map((r) => [r.device, { ssid }]));
    const sent = await run('set_wifi', { radios }, {
      key: 'rename',
      quiet: true,
      fail: { busy: 'w.wifi.busy' },
      confirm: { title: t('w.wifi.saveQ'), body: t('w.done.renameHint'), ok: t('w.wifi.save') },
    });
    if (!sent) return;
    setPrevAt(at);
    void store.fetch('setup', 0);
  };
  if (!offer && !state) return null;
  return (
    <div class="wz-rename">
      {state ? (
        <p class="row" role="status">
          {state === 'applying' ? <Spinner /> : <Icon name={state === 'ok' ? 'ok' : 'warn'} size={16} />}
          {t(state === 'ok' ? 'a.wifi_set' : APPLY_TITLE[state] ?? 'w.wifi.unver.t')}
        </p>
      ) : null}
      {state === 'applying' ? <p class="hint">{t('w.wifi.after2')}</p> : null}
      {offer ? (
        <>
          <p>
            <Button icon="wifi" busy={pending === 'rename'} disabled={!!pending} onClick={rename}>
              {/* The name whole on its line: "BloopCat-4E2A" never breaks at its hyphen. */}
              <span>{around(t('w.done.rename', { ssid: SLOT }), <span class="nw">{ssid}</span>)}</span>
            </Button>
          </p>
          <p class="hint">{t('w.done.renameHint')}</p>
        </>
      ) : null}
    </div>
  );
}

// ── the wizard ──────────────────────────────────────────────────────────────

/** `onClose(tour)`: tour — the person went through to the end, show them around the main screen. */
export function Setup({ onClose, start = 'welcome' }: { onClose: (tour: boolean) => void; start?: Screen }) {
  const { t, store, run, pending, setPassword } = useApp();
  const res = useRes('setup');
  const wanRes = useRes('wan_check');
  const stRes = useRes('status');
  const wan = wanRes.data;
  const st = stRes.data;
  // The password step, once the router was seen without a password, stays for
  // the wizard's life — ticked once it has one. It cannot be walked past: a
  // person who came for a later step starts there.
  const askPw = useRef(false);
  if (res.data && asksPassword(res.data, !!setPassword)) askPw.current = true;
  const steps = askPw.current ? WITH_PW : STEPS;
  const [screen, setScreen] = useState<Screen>(() => (askPw.current && start !== 'welcome' && start !== 'done' ? 'password' : start));
  // LuCI took the password: it counts as set until `setup` says so too.
  const [pwSaved, setPwSaved] = useState(false);
  const s = res.data && pwSaved && res.data.passwordSet === false ? { ...res.data, passwordSet: true } : res.data;
  // The last Wi-Fi change and its new password: shown again if the person comes back to the step.
  const [wifiResult, setWifiResult] = useState<WifiResult | null>(null);
  const top = useRef<HTMLDivElement>(null);
  const moved = useRef(false);

  // The wizard watches the router closely while it is open — and only while the
  // page is on screen: every answer is a process on a small router. The live
  // checks run only where their answer is shown, and slow down once online.
  useEffect(() => {
    // The first look happens even in a hidden tab (opened in the background);
    // only the watching waits for the page to be on screen.
    let first = true;
    const poll = () => {
      if (document.hidden && !first) return;
      first = false;
      void store.fetch('setup', 2500);
      if (screen !== 'wifi' && screen !== 'server') void store.fetch('wan_check', online(store.get('wan_check').data) ? 30_000 : 3500);
      // Linked, the subscription on its way: `status` says when it has arrived.
      if ((screen === 'vectra' || screen === 'server') && store.get('setup').data?.vectra.linked) void store.fetch('status', 2500);
      // At the end, what the router's tune set up: `status` says, once.
      if (screen === 'done') void store.fetch('status', 60_000);
    };
    poll();
    const id = setInterval(poll, 1000);
    return () => clearInterval(id);
  }, [screen]);

  // A new step takes the focus to its heading, so a keyboard or a screen reader starts there.
  useEffect(() => {
    if (moved.current) top.current?.querySelector<HTMLElement>('#vx-wz-t')?.focus({ preventScroll: true });
  }, [screen]);

  const go = (to: Screen) => {
    moved.current = true;
    setScreen(to);
    const calm = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
    top.current?.scrollIntoView?.({ block: 'start', behavior: calm ? 'auto' : 'smooth' });
  };
  if (!s) return <Skeleton rows={3} />;

  const firstTodo = steps.find((id) => !stepOk(s, id, wan, st)) ?? 'server';
  const allOk = steps.every((id) => stepOk(s, id, wan, st));
  const after = (id: StepId) => () => {
    const i = steps.indexOf(id);
    go(i + 1 < steps.length ? steps[i + 1] : 'done');
  };
  // "Set up later", "Close" and "Go to the main screen" close the wizard.
  // Success is no news worth a toast; a router that could not note it still
  // lets the person go (it says so in a toast) and offers the wizard again
  // next time.
  const finish = async (tour: boolean) => {
    await run('finish_setup', {}, { key: 'finish', quiet: true });
    onClose(tour);
  };

  const list = (tone: 'mute' | 'warn') => (
    <ul class="wz-list">
      {steps.map((id) => {
        const ok = stepOk(s, id, wan, st);
        return (
          <li key={id} class={ok ? 'ok' : undefined}>
            <span class="sicon" aria-hidden="true">
              <Icon name={ok ? 'ok' : tone === 'warn' ? 'warn' : ICON[id]} size={16} />
            </span>
            <b>{t(('w.step.' + id) as Key)}</b>
            <span class="sr">: </span>
            <span class={'bd ' + (ok ? 't-ok' : 't-' + tone)}>{t(badge(s, id, ok, wan, st))}</span>
          </li>
        );
      })}
    </ul>
  );

  return (
    <div class="wz" ref={top}>
      {screen !== 'welcome' && screen !== 'done' ? <Stepper s={s} st={st} at={screen} wan={wan} steps={steps} go={go} /> : null}
      {screen === 'welcome' ? (
        <section class="wz-card card wz-hello" aria-labelledby="vx-wz-t">
          <h2 id="vx-wz-t" class="verdict" tabIndex={-1}>
            {t('w.welcome.t')}
          </h2>
          <p class="lede">{t(steps[0] === 'password' ? 'w.welcome.dPw' : 'w.welcome.d')}</p>
          {list('mute')}
          <div class="row wz-foot">
            <Button kind="p" icon="arrow" onClick={() => go(firstTodo)}>
              {t('w.start')}
            </Button>
            <Button kind="g" busy={pending === 'finish'} disabled={!!pending} onClick={() => finish(false)}>
              {t(allOk ? 'close' : 'w.skip')}
            </Button>
          </div>
        </section>
      ) : screen !== 'done' ? (
        // A step that is done has its own "Next"; skipping is for the ones that are not
        // (some people keep their Wi-Fi exactly as it is) — never the password.
        <WzNav.Provider
          value={{
            back: () => go(screen === steps[0] ? 'welcome' : steps[steps.indexOf(screen) - 1]),
            skip: screen === 'password' || stepOk(s, screen, wan, st) ? undefined : after(screen),
          }}
        >
          {screen === 'password' ? (
            <Password s={s} saved={pwSaved} onSaved={() => setPwSaved(true)} next={after('password')} />
          ) : screen === 'internet' ? (
            <Internet wan={wan} failed={!!wanRes.error} next={after('internet')} />
          ) : screen === 'wifi' ? (
            <Wifi s={s} next={after('wifi')} result={wifiResult} onResult={setWifiResult} />
          ) : screen === 'vectra' ? (
            <Vectra s={s} st={st} wan={wan} next={after('vectra')} />
          ) : (
            <Server s={s} st={st} next={after('server')} />
          )}
        </WzNav.Provider>
      ) : (
        <section class={'wz-card card ' + (allOk ? 't-ok' : 't-warn')} aria-labelledby="vx-wz-t">
          <div class="wz-head">
            <span class="beacon" aria-hidden="true">
              <Icon name={allOk ? 'ok' : 'warn'} size={22} />
            </span>
            <h2 id="vx-wz-t" class="verdict" tabIndex={-1}>
              {t(allOk ? 'w.done.t' : 'w.done.part.t')}
            </h2>
          </div>
          <p>{t(allOk ? 'w.done.d' : 'w.done.part.d')}</p>
          {/* Where to find this page again — named, not linked: a click would leave the
              wizard before it has noted it is done. The router answers its names since
              the version with Vectra's switch. */}
          {st && st.power.enabled !== null ? (
            <p class="hint">
              <WayIn ip={s.lan.ipv4} />
            </p>
          ) : null}
          {list('warn')}
          <Rename s={s} />
          {st?.tune ? <TuneDone tune={st.tune} /> : null}
          <div class="row wz-foot">
            <Button kind="p" icon="arrow" busy={pending === 'finish'} disabled={!!pending} onClick={() => finish(true)}>
              {t('w.done.go')}
            </Button>
          </div>
        </section>
      )}
    </div>
  );
}
