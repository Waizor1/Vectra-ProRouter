// A stateful stand-in for the `vectra` ubus object, used by `npm run dev` and the
// tests. It answers with the contract fixtures, waits like a small router, and
// applies mutations so every flow can be exercised without hardware.

import type { Action, CallFn, Holder, ReadData, ReadMethod, SetPasswordFn, Setup, WifiRadio, WifiScanRadio, WifiVerdict } from '../api/types';
import { FIXTURE_NOW, FIXTURES } from './fixtures';
import { normalizeSite } from '../lib/sites';
import { buildWorld, clone, off, wifiAs, type Scenario, type WifiAs } from './scenarios';

export interface MockOptions {
  scenario?: Scenario;
  /** Simulated round trip, ms. */
  latencyMs?: number;
  /** Re-anchor fixture timestamps to the real clock and let time pass. */
  live?: boolean;
  /** How long a `pending` select_entry takes to land, ms. */
  applyMs?: number;
  /** The operator's lock: status says so and the Pro methods are refused, as vctl does. */
  locked?: boolean;
  /** `set_rules` answers `pending` and lands later, as a router does when xray takes long to start. */
  rulesPending?: boolean;
  /** Radios that do not come back after a Wi-Fi change (the router then rolls a working one back). */
  wifiDown?: string[];
  /** How a Wi-Fi change ends when the router cannot tell (`unverified`) or cannot roll back (`failed`). */
  wifiEnd?: 'unverified' | 'failed';
  /** Who carries the traffic in the `off` scenario (PassWall2 unless said). */
  holder?: Holder;
  /** LuCI's password change does not take it (`refused`), refuses the session (`denied`), or the connection drops (`offline`). */
  passwordFails?: 'refused' | 'denied' | 'offline';
  /** The Wi-Fi as an owner may have it (scenarios.ts, `wifiAs`): the wizard's verdicts. */
  wifi?: WifiAs;
}

export interface Mock {
  call: CallFn;
  /** LuCI's own password change (the LuCI view hands the app `luci.setPassword`): the router then has one. */
  setPassword: SetPasswordFn;
  dispose(): void;
}

const READS: ReadMethod[] = ['status', 'balancers', 'nodes', 'entries', 'diagnostics', 'logs', 'setup', 'wan_check', 'rules', 'wifi_scan', 'services'];
/**
 * The channels the router takes for a radio (contract: "Changes"): 1–11 on
 * 2.4 GHz (HT40+ up to 9, HT40- from 5); on 5 GHz no DFS channel, 165 only at
 * 20 MHz, and at 160 MHz only the 36 block.
 */
function allowed(r: WifiRadio): number[] {
  const range = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => a + i);
  if (r.band === '2g') return r.htmode === 'HT40+' ? range(1, 9) : r.htmode === 'HT40-' ? range(5, 11) : range(1, 11);
  if (r.band !== '5g') return [];
  const low = [36, 40, 44, 48];
  const width = r.width ?? 20;
  return width >= 160 ? low : width >= 40 ? [...low, 149, 153, 157, 161] : [...low, 149, 153, 157, 161, 165];
}

/**
 * The wizard's verdict as the router gives it (contract: setup, `wifi.verdict`),
 * judged at every read: of the radios that are on, one not up or 2.4 GHz off
 * 1/6/11 is `boost`; 5 GHz on a channel the rules refuse is `manual`; else
 * `fine`. A mesh radio's channel is never judged. The mock knows no driver
 * power list: the power counts as fine.
 */
export function wifiVerdict(w: Setup['wifi']): WifiVerdict | null {
  if (w.tunable === false) return null;
  let v: WifiVerdict = 'fine';
  for (const r of w.radios) {
    if (r.enabled !== true) continue;
    if (r.up === false) return 'boost';
    if (r.mesh === true || r.auto === true || r.channel === null) continue;
    if (r.band === '2g' && [1, 6, 11].indexOf(r.channel) < 0) return 'boost';
    if (r.band === '5g' && allowed(r).indexOf(r.channel) < 0) v = 'manual';
  }
  return v;
}

/** The channel the router picks from what it heard: the least shared; a near tie keeps the current block. */
function pick(r: WifiRadio, air: { channel: number; networks: number }[]): number {
  const ok = allowed(r);
  const near = r.band === '2g' ? 4 : 0;
  const busy = (ch: number) =>
    air.filter((a) => Math.abs(a.channel - ch) <= near).reduce((n, a) => n + a.networks * (1 - Math.abs(a.channel - ch) / 5), 0);
  const fixed = r.auto !== true && r.channel !== null ? r.channel : null;
  if (r.band === '2g') {
    const cands = [1, 6, 11].filter((c) => ok.indexOf(c) >= 0);
    return cands.reduce((best, c) => (busy(c) < busy(best) || (busy(c) === busy(best) && c === fixed) ? c : best));
  }
  const blocks = [[36, 40, 44, 48], [149, 153, 157, 161, 165]].map((b) => b.filter((c) => ok.indexOf(c) >= 0)).filter((b) => b.length);
  const load = (b: number[]) => b.reduce((n, c) => n + busy(c), 0);
  const cur = blocks.find((b) => fixed !== null && b.indexOf(fixed) >= 0);
  let best = cur ?? blocks[0];
  for (const b of blocks) if (b !== best && load(b) < load(best) * (cur ? 2 / 3 : 1)) best = b;
  if ((r.width ?? 20) <= 20) return best.reduce((a, c) => (busy(c) < busy(a) ? c : a));
  return best === cur && fixed !== null ? fixed : best[0];
}

/** What a radio hears when the router listens (only a radio that is up), and the channel it takes. */
function listen(r: WifiRadio, around: WifiScanRadio[]): WifiScanRadio {
  const heard = around.find((a) => a.band === r.band);
  const fixed = r.auto !== true && r.channel !== null ? r.channel : null;
  // A radio that was not heard keeps a channel that works, else the default.
  const keep = r.mesh === true ? r.channel : fixed !== null && allowed(r).indexOf(fixed) >= 0 ? fixed : r.band === '2g' ? 6 : 36;
  // Not up: `off`; up unknown (netifd could not be asked) or nothing to hear through: `failed`.
  if (r.up !== true || !heard) return { device: r.device, band: r.band, current: fixed, recommended: keep, networks: null, channels: [], error: r.up === false ? 'off' : 'failed' };
  return {
    device: r.device,
    band: r.band,
    current: fixed,
    recommended: r.mesh === true ? r.channel : pick(r, heard.channels),
    networks: heard.networks,
    channels: clone(heard.channels),
    error: null,
  };
}
/** What vctl refuses under the operator's lock (contract/README.md, "Operator lock"). */
export const PRO_ONLY = ['balancers', 'nodes', 'logs', 'pin_balancer', 'set_probe_interval'];
const DEFAULT_PROBE_SEC = 300;
const ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/;
const iso = (ms: number) => new Date(ms).toISOString().replace(/\.\d+Z$/, 'Z');

/** LuCI's own wording for a failed ubus call. */
export function ubusError(method: string, code: number, text: string): Error {
  const e = new Error(`RPC call to vectra/${method} failed with ubus code ${code}: ${text}`);
  e.name = 'RPCError';
  return e;
}

function shiftTimes<T>(v: T, byMs: number): T {
  if (typeof v === 'string') return (ISO.test(v) ? iso(Date.parse(v) + byMs) : v) as T;
  if (Array.isArray(v)) return v.map((x) => shiftTimes(x, byMs)) as T;
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v)) out[k] = shiftTimes((v as Record<string, unknown>)[k], byMs);
    return out as T;
  }
  return v;
}

export function createMock(opts: MockOptions = {}): Mock {
  const scenario = opts.scenario ?? 'healthy';
  const latency = opts.latencyMs ?? 300;
  const live = opts.live ?? true;
  const applyMs = opts.applyMs ?? 1500;
  const born = Date.now();
  const built = buildWorld(scenario, opts.holder);
  if (built) built.status.ui = { locked: !!opts.locked };
  if (built && opts.wifi) wifiAs(built, opts.wifi);
  // Live mode moves the fixture router's clock to the moment the page opened;
  // from then on its data ages naturally.
  const world = built && live ? shiftTimes(built, born - FIXTURE_NOW) : built;
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const now = () => (live ? Date.now() : FIXTURE_NOW);
  let engineBase = world ? world.status.engine.uptimeSec : null;
  let wifiChanges = 0;
  let engineSince = now();
  // Vectra's switch: one change at a time, as the router's power lock; what
  // ran before `off`, to come back with `on`.
  let powerBusy = false;
  let beforeOff: Pick<ReadData, 'status' | 'balancers' | 'nodes' | 'entries' | 'diagnostics'> | null = null;

  const later = (ms: number, fn: () => void) => {
    const id = setTimeout(() => {
      timers.delete(id);
      fn();
    }, ms);
    timers.add(id);
  };
  const wait = () => new Promise<void>((resolve) => (latency > 0 ? later(latency, resolve) : resolve()));

  function log(message: string) {
    if (!world) return;
    const lines = world.logs.lines;
    lines.push({ time: iso(now()), level: 'info', source: 'vctl', message });
    if (lines.length > 200) lines.splice(0, lines.length - 200);
  }

  function restartEngine() {
    const e = world && world.status.engine;
    if (!e || e.state === 'idle') return;
    engineBase = 0;
    engineSince = now();
    e.state = 'running';
    e.pid = (e.pid ?? 4000) + 11;
    log('xray restarted');
  }

  // An unboxed router gets its address from the provider by itself, a few
  // seconds after the page first asks — nothing in the wizard sets it.
  let connecting = false;
  function connectSoon() {
    const w = world!;
    if (connecting || w.wan_check.internet === true || w.wan_check.link !== true) return;
    connecting = true;
    later(Math.max(applyMs * 2, 4000), () => {
      Object.assign(w.setup.wan, { link: true, ipv4: '100.64.12.7', gateway: '100.64.12.1', dns: ['100.64.12.1'] });
      w.wan_check = { link: true, ipv4: '100.64.12.7', dns: true, internet: true, panel: true, checkedAt: iso(now()) };
      log('wan up: 100.64.12.7 from the provider');
      // The panel's first answer names the bot and brings Vectra's key: the QR and the link appear.
      const c = w.setup.vectra.claim;
      w.setup.vectra.botUsername = 'VectraConnectBot';
      if (c && c.code) {
        // A stand-in with the real one's length; only the Vectra backend can open a real one.
        c.qr = 'VECTRA:R1:' + 'AQ'.padEnd(320, 'x7Kq4M9dZ2wLp0');
        c.botUrl = 'https://t.me/VectraConnectBot?start=rt_' + c.code;
      }
    });
  }

  // The mock's stand-in for "scanned in the Vectra app": a few seconds after the
  // wizard first shows an online router its code, the panel reports the owner;
  // the operator config follows a few seconds later, as through the real panel.
  let linking = false;
  function linkSoon() {
    const w = world!;
    // Like a person: the code is scanned once the earlier steps are done.
    if (linking || w.setup.vectra.linked || w.wan_check.internet !== true || !w.setup.vectra.claim) return;
    if (!w.setup.wifi.radios.some((r) => r.enabled === true && r.secured === true)) return;
    linking = true;
    later(Math.max(applyMs * 4, 6000), () => {
      const owner = { label: '@vectra_user' };
      w.setup.vectra = { ...w.setup.vectra, owner, claim: { ...w.setup.vectra.claim!, state: 'claimed', owner } };
      log('claimed by a Vectra account');
      // Like vctl: the operator config is written first ("linked"), the
      // provider's subscription is fetched after it.
      later(Math.max(applyMs * 2, 3000), () => {
        w.setup.vectra = { ...w.setup.vectra, linked: true, claim: null };
        log('operator config received; fetching the subscription');
        later(Math.max(applyMs * 2, 3000), () => {
          const fresh = clone(FIXTURES);
          for (const k of ['balancers', 'nodes', 'entries', 'diagnostics'] as const) (w as unknown as Record<string, unknown>)[k] = fresh[k];
          Object.assign(w.status, { engine: fresh.status.engine, dataplane: fresh.status.dataplane, subscription: fresh.status.subscription, api: fresh.status.api });
          log('subscription fetched; xray started');
        });
      });
    });
  }

  function snapshot<M extends ReadMethod>(m: M): ReadData[M] {
    const w = world!;
    if (m === 'setup') linkSoon();
    if (m === 'wan_check' && (scenario === 'unboxed' || scenario === 'boxed')) connectSoon();
    if (live) {
      const t = now();
      if (m === 'status' && scenario !== 'degraded' && w.status.controller.running !== false) {
        // A check-in every 30 s keeps "last check-in" plausible — while the controller runs to make them.
        w.status.controlPlane.lastCheckIn = iso(t - (t % 30000));
      }
      if (m === 'nodes' && scenario === 'healthy') {
        const tick = iso(t - (t % ((w.status.probe.intervalSec || DEFAULT_PROBE_SEC) * 1000)));
        w.nodes.observedAt = iso(t);
        for (const n of w.nodes.nodes) {
          if (n.lastTry !== null) n.lastTry = tick;
          if (n.alive) n.lastSeen = tick;
        }
      }
      if (m === 'diagnostics') w.diagnostics.checkedAt = iso(t);
    }
    const data = clone(w[m]);
    if (m === 'setup') {
      const wifi = (data as ReadData['setup']).wifi;
      wifi.verdict = wifiVerdict(wifi);
    }
    if (m === 'status' && live) {
      const s = data as ReadData['status'];
      const up = (Date.now() - born) / 1000;
      if (engineBase !== null && s.engine.uptimeSec !== null) s.engine.uptimeSec = Math.floor(engineBase + (now() - engineSince) / 1000);
      if (s.controller.uptimeSec !== null) s.controller.uptimeSec = Math.floor(s.controller.uptimeSec + up);
      if (s.router.uptimeSec !== null) s.router.uptimeSec = Math.floor(s.router.uptimeSec + up);
    }
    return data;
  }

  const ok = (code: string): Action => ({ ok: true, code, detail: null });
  const fail = (code: string, detail: string | null = null): Action => ({ ok: false, code, detail });

  function mutate(method: string, p: Record<string, unknown>): Action {
    const w = world!;
    const s = w.status;
    switch (method) {
      case 'select_entry': {
        const index = p.index;
        if (typeof index !== 'number') return fail('invalid_params', 'index must be a number');
        const entry = w.entries.entries.find((e) => e.index === index);
        if (!entry) return fail(w.entries.entries.length ? 'unknown_entry' : 'no_entries_cache', `no entry with index ${index}`);
        later(applyMs, () => {
          w.entries.active = index;
          w.entries.source = 'local';
          w.entries.overrideStale = false;
          Object.assign(s.subscription, { entryIndex: index, entryRemark: entry.remark, source: 'local', overrideStale: false });
          log(`entry selected index=${index}`);
          restartEngine();
        });
        return ok('pending');
      }
      case 'reset_entry': {
        const pi = w.entries.panelIndex;
        if (pi === null) return fail('no_entries_cache');
        const entry = w.entries.entries.find((e) => e.index === pi);
        w.entries.active = pi;
        w.entries.source = 'panel';
        Object.assign(s.subscription, { entryIndex: pi, entryRemark: entry ? entry.remark : null, source: 'panel', overrideStale: false });
        log('entry reset to the panel choice');
        restartEngine();
        return ok('entry_reset');
      }
      case 'pin_balancer':
      case 'unpin_balancer': {
        if (!w.balancers.apiReachable) return fail('xray_api_unavailable', 'dial tcp 127.0.0.1:10085: connect: connection refused');
        const b = w.balancers.balancers.find((x) => x.tag === p.balancer);
        if (!b) return fail('unknown_balancer', `no balancer ${String(p.balancer)}`);
        const pins = (s.pins = s.pins || {});
        if (method === 'unpin_balancer') {
          b.pinned = null;
          delete pins[b.tag];
          log(`balancer unpinned balancer=${b.tag}`);
          return ok('balancer_unpinned');
        }
        if (typeof p.node !== 'string' || b.members.indexOf(p.node) < 0) return fail('unknown_node', `${String(p.node)} is not a member of ${b.tag}`);
        b.pinned = p.node;
        pins[b.tag] = p.node;
        log(`balancer pinned balancer=${b.tag} node=${p.node}`);
        return ok('balancer_pinned');
      }
      case 'set_probe_interval': {
        const sec = p.seconds;
        if (typeof sec !== 'number' || sec < 0 || sec % 1) return fail('invalid_params', 'seconds must be a non-negative integer');
        const intervalSec = sec || DEFAULT_PROBE_SEC;
        const source = sec ? 'local' : 'default';
        s.probe = { intervalSec, source };
        w.balancers.probe.intervalSec = intervalSec;
        w.balancers.probe.source = source;
        log(`probe interval set to ${intervalSec}s (${source})`);
        restartEngine();
        return ok('probe_interval_set');
      }
      case 'restart_xray':
        if (s.engine.state === 'idle') return fail('apply_failed', 'no config to run yet');
        restartEngine();
        return ok('xray_restarted');
      // ── the wizard ──
      // The router's own rules (contract: Setup wizard, "Changes").
      case 'set_wifi':
      case 'optimize_wifi': {
        const wifi = w.setup.wifi;
        const radios = wifi.radios;
        const tunes = method === 'optimize_wifi';
        // One Wi-Fi change at a time: the router holds a lock until it has seen the radios come back.
        if (wifi.apply?.state === 'applying') return fail('busy', 'wifi: a change is still being applied');
        if (tunes && wifi.tunable === false) {
          const odd = radios.find((r) => r.band !== '2g' && r.band !== '5g');
          return fail('unsupported', `${odd?.device ?? 'wifi'}: a band Panama's rules do not cover would be switched off`);
        }
        const names = (p.radios ?? {}) as Record<string, { ssid?: unknown; key?: unknown }>;
        if (typeof names !== 'object' || Array.isArray(names)) return fail('invalid_params', 'radios');
        if (method === 'set_wifi' && !Object.keys(names).length) return fail('invalid_params', 'radios: name at least one');
        for (const dev of Object.keys(names)) {
          const r = radios.find((x) => x.device === dev);
          const n = names[dev];
          if (!r) return fail('invalid_params', `radios.${dev}: no such radio`);
          if (r.ap !== true) return fail('invalid_params', `radios.${dev}: no access point to name`);
          const bytes = typeof n.ssid === 'string' ? new TextEncoder().encode(n.ssid).length : 0;
          if (bytes < 1 || bytes > 32 || /\p{Cc}/u.test(String(n.ssid))) return fail('invalid_params', `radios.${dev}.ssid: 1-32 bytes of text`);
          if (n.key === undefined && r.secured !== true) return fail('invalid_params', `radios.${dev}.key: the network is open — give it a key`);
          if (n.key !== undefined && !(typeof n.key === 'string' && /^[\x20-\x7e]{8,63}$/.test(n.key))) return fail('invalid_params', `radios.${dev}.key: 8-63 printable ASCII characters`);
        }
        const channels = (p.channels ?? {}) as Record<string, unknown>;
        if (tunes) {
          // Tuning never leaves an open network on the air: it gets a key, or nothing happens.
          for (const r of radios) {
            if (r.enabled === true && r.ap === true && r.secured !== true && typeof names[r.device]?.key !== 'string') return fail('invalid_params', `radios.${r.device}.key: the network is open — give it a key`);
          }
          for (const dev of Object.keys(channels)) {
            const r = radios.find((x) => x.device === dev);
            const ch = channels[dev];
            if (!r) return fail('invalid_params', `channels.${dev}: no such radio`);
            if (r.mesh === true) return fail('invalid_params', `channels.${dev}: carries a mesh link; its channel stays`);
            if (typeof ch !== 'number' || allowed(r).indexOf(ch) < 0) return fail('invalid_params', `channels.${dev}: ${JSON.stringify(ch)} is not a channel for this band at ${r.width ?? 20} MHz`);
          }
        }
        const before = clone(radios);
        const tunedBefore = wifi.tuned;
        // The air, heard now — only through the radios that are up — for the channels not given.
        if (tunes) w.wifi_scan = { radios: radios.filter((r) => r.band === '2g' || r.band === '5g').map((r) => listen(r, FIXTURES.wifi_scan.radios)), scannedAt: iso(now()) };
        const left: string[] = [];
        for (const r of radios) {
          const n = names[r.device];
          // A key given to an open network switches it on; a secured one keeps its state.
          if (n) Object.assign(r, { ssid: n.ssid }, r.secured !== true ? { secured: true, enabled: true } : {});
          if (!tunes || (r.band !== '2g' && r.band !== '5g')) continue;
          const heard = w.wifi_scan.radios.find((x) => x.device === r.device);
          const ch = typeof channels[r.device] === 'number' ? (channels[r.device] as number) : heard?.recommended ?? r.channel;
          // A mesh radio keeps its channel, as the router does (on auto it stays on auto: not tuned).
          Object.assign(r, { country: 'PA', txpower: null, maxPower: true }, r.mesh === true ? {} : { auto: false, channel: ch });
          // Never an open network on the air: without a key it stays off.
          if (r.ap === true && r.secured !== true) left.push(`${r.device}: open network left disabled; give it a key`);
        }
        const on = radios.filter((r) => r.enabled === true && (r.band === '2g' || r.band === '5g'));
        // As the router counts it: every 2.4/5 GHz radio that is on (none on is vacuously tuned).
        if (tunes) wifi.tuned = on.every((r) => r.country === 'PA' && r.maxPower === true && r.auto === false && r.channel !== null);
        // The Wi-Fi restarts after the answer; the router then watches the radios come back.
        // Each change has its own `at` (the router's commit time), even on a stopped clock.
        const at = iso(now() + ++wifiChanges * 1000);
        wifi.apply = { state: 'applying', at, detail: null, radios: {} };
        for (const r of radios) if (r.enabled === true) r.up = false;
        later(applyMs, () => {
          const down = new Set(opts.wifiDown ?? []);
          const lost = before.filter((b) => b.up === true && down.has(b.device));
          if (lost.length) {
            // A radio that worked before did not come back: the old settings go back.
            wifi.radios = before;
            wifi.tuned = tunedBefore;
            wifi.apply = { state: 'rolled_back', at, detail: lost.map((b) => `${b.device}: did not come back; the old settings are back`).join('; '), radios: Object.fromEntries(before.map((b) => [b.device, b.up === true])) };
            return;
          }
          if (opts.wifiEnd === 'failed') {
            // The old settings were due back and could not be written: the radio stays down.
            for (const r of radios) r.up = r.enabled === true ? false : r.up;
            wifi.apply = { state: 'failed', at, detail: 'radio0: did not come back; the old settings could not be written back', radios: {} };
            return;
          }
          if (opts.wifiEnd === 'unverified') {
            // The router could not ask its network daemon: nothing to judge by.
            for (const r of radios) r.up = null;
            wifi.apply = { state: 'unverified', at, detail: 'network.wireless status unavailable', radios: {} };
            return;
          }
          for (const r of radios) r.up = r.enabled === true && r.ap === true && !down.has(r.device);
          const up = Object.fromEntries(radios.filter((r) => r.enabled === true).map((r) => [r.device, r.up === true]));
          const missing = Object.keys(up).filter((d) => !up[d]);
          wifi.apply = { state: missing.length ? 'partial' : 'ok', at, detail: missing.length ? missing.map((d) => `${d}: did not come up`).join('; ') : null, radios: up };
        });
        log(method === 'set_wifi' ? 'wifi renamed' : 'wifi at full power: ' + radios.map((r) => `${r.band} ch${r.channel}`).join(', '));
        return { ok: true, code: method === 'set_wifi' ? 'wifi_set' : 'wifi_optimized', detail: left.length ? left.join('; ') : null };
      }
      case 'finish_setup':
        w.setup.done = true;
        return ok('setup_finished');
      // Like `vctl rpcd`: already so answers at once; otherwise the change
      // runs on its own — the switch flips first, the stacks change hands
      // after it — and one runs at a time.
      case 'set_power': {
        const on = p.on;
        if (typeof on !== 'boolean' || Object.keys(p).some((k) => k !== 'on' && k !== 'force') || (p.force !== undefined && typeof p.force !== 'boolean'))
          return fail('invalid_params', 'params must be {"on": true} or {"on": false}');
        if (powerBusy) return fail('busy', 'another power change is still in flight');
        const pw = s.power;
        if (on && pw.wouldIdle === true && p.force !== true) return fail('would_idle', 'Vectra would carry no traffic yet');
        if (on && pw.enabled === true && pw.running === true) return ok('power_on');
        if (!on && pw.enabled === false && pw.running === false) return ok('power_off');
        powerBusy = true;
        pw.enabled = on;
        later(applyMs, () => {
          powerBusy = false;
          if (on) {
            const back = pw.holder;
            const was = beforeOff ?? clone(live ? shiftTimes(FIXTURES, born - FIXTURE_NOW) : FIXTURES);
            beforeOff = null;
            Object.assign(w, { balancers: was.balancers, nodes: was.nodes, entries: was.entries, diagnostics: was.diagnostics });
            w.status = { ...was.status, ui: s.ui, legacy: { ...was.status.legacy, agentEnabled: false, passwallRunning: false } };
            w.status.power = { enabled: true, running: true, holder: 'vectra', handBack: back === 'passwall2' || back === 'agent' ? back : 'direct', wouldIdle: false };
            // xray has just started.
            engineBase = 0;
            engineSince = now();
            log('vectra on: the router is taken');
          } else {
            beforeOff = clone({ status: s, balancers: w.balancers, nodes: w.nodes, entries: w.entries, diagnostics: w.diagnostics });
            off(w, pw.handBack ?? 'direct');
            log('vectra off: the router is given back');
          }
        });
        return ok('pending');
      }
      // Like `vctl rpcd`: uci set + commit, nothing restarts; the next job reads it.
      case 'set_remote_shell': {
        const on = p.on;
        if (typeof on !== 'boolean' || Object.keys(p).some((k) => k !== 'on')) return fail('invalid_params', 'params must be {"on": true} or {"on": false}');
        s.remoteShell = on;
        log(on ? 'support shell on' : 'support shell off');
        return ok('remote_shell_set');
      }
      case 'set_service': {
        if (typeof p.id !== 'string' || typeof p.country !== 'string') return fail('invalid_params', 'id and country are both required; "" is the entry\'s own path');
        const row = w.services.services.find((x) => x.id === p.id);
        if (!row) return fail('unknown_service', p.id);
        if (w.services.available === false) return fail('unavailable', 'this router routes by the operator\'s policy');
        const cc = (p.country as string).trim().toUpperCase();
        if (cc && row.countries.indexOf(cc) < 0) return fail('unknown_country', cc);
        // Like the router: the choice is kept once xray runs it.
        later(applyMs, () => {
          w.services = {
            ...w.services,
            services: w.services.services.map((x) => (x.id === row.id ? { ...x, choice: cc || null, active: !!cc, stale: false } : x)),
          };
          log(`service ${row.id}: ${cc || 'the entry\'s own path'}`);
          restartEngine();
        });
        return ok('pending');
      }
      case 'set_rules': {
        if (!Array.isArray(p.direct) || !Array.isArray(p.proxy)) return fail('invalid_params', 'direct and proxy are both required; [] empties a list');
        // Like the router: every entry normalized, the first bad one named,
        // repeats dropped, a site in both lists refused.
        const lists: Record<'direct' | 'proxy', string[]> = { direct: [], proxy: [] };
        for (const name of ['direct', 'proxy'] as const) {
          const raw = p[name] as unknown[];
          for (let i = 0; i < raw.length; i++) {
            const site = typeof raw[i] === 'string' ? normalizeSite(raw[i] as string) : ({ error: 'invalid' } as const);
            if ('error' in site) return fail('invalid_params', `${name}[${i}] ${JSON.stringify(raw[i])}: not a site`);
            if (lists[name].indexOf(site.value) < 0) lists[name].push(site.value);
          }
          if (lists[name].length > 100) return fail('invalid_params', `${name}: ${lists[name].length} sites; at most 100`);
        }
        const both = lists.proxy.find((x) => lists.direct.indexOf(x) >= 0);
        if (both) return fail('invalid_params', `proxy: ${JSON.stringify(both)} is in the direct list too; a site goes one way`);
        // Like the router: a service its geo file lacks is refused.
        const unknown = lists.direct.concat(lists.proxy).find((x) => x.startsWith('geosite:') && (w.rules.catalog || []).indexOf(x.slice(8)) < 0);
        if (unknown) return fail('unknown_service', unknown.slice(8));
        const land = () => {
          w.rules = { ...w.rules, ...lists };
          log(`rules set: ${lists.direct.length} direct, ${lists.proxy.length} proxy`);
          restartEngine();
        };
        // Usually the router answers once xray runs them; a slow start answers `pending`.
        if (!opts.rulesPending) {
          land();
          return ok('rules_set');
        }
        later(applyMs, land);
        return ok('pending');
      }
    }
    throw ubusError(method, 3, 'Method not found');
  }

  const call: CallFn = async (method, params) => {
    await wait();
    if (!world) throw ubusError(method, 4, 'Object not found');
    if (opts.locked && PRO_ONLY.indexOf(method) >= 0) return fail('locked');
    // Looking at the air takes a real router a few seconds.
    if ((READS as string[]).indexOf(method) >= 0) return snapshot(method as ReadMethod);
    return mutate(method, params || {});
  };

  // LuCI's, not vctl's: it works on a router whose Vectra plugin is missing too.
  const setPassword: SetPasswordFn = async (password) => {
    await wait();
    if (opts.passwordFails === 'denied') {
      const e = new Error('RPC call to luci/setPassword failed with ubus code 6: Permission denied');
      e.name = 'RPCError';
      throw e;
    }
    if (opts.passwordFails === 'offline') throw new Error('XHR request aborted by browser');
    if (opts.passwordFails === 'refused' || !password) return false;
    if (world) world.setup.passwordSet = true;
    return true;
  };

  return {
    call,
    setPassword,
    dispose() {
      timers.forEach((id) => clearTimeout(id));
      timers.clear();
    },
  };
}
