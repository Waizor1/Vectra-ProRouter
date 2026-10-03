// Router states the UI must render gracefully. `healthy` is the contract
// fixtures as-is; the others are derived from them.

import type { Check, Holder, ReadData } from '../api/types';
import { FIXTURES, FIXTURE_NOW, radio } from './fixtures';

export type Scenario = 'healthy' | 'reserve' | 'degraded' | 'down' | 'empty' | 'unboxed' | 'boxed' | 'off' | 'unfit';
export const SCENARIOS: readonly Scenario[] = ['healthy', 'reserve', 'degraded', 'down', 'empty', 'unboxed', 'boxed', 'off', 'unfit'];

export const clone = <T>(x: T): T => JSON.parse(JSON.stringify(x)) as T;
const at = (secondsBeforeFixtureNow: number) => new Date(FIXTURE_NOW - secondsBeforeFixtureNow * 1000).toISOString().replace('.000Z', 'Z');

function setCheck(w: ReadData, id: string, status: Check['status'], params: Record<string, unknown>): void {
  const c = w.diagnostics.checks.find((x) => x.id === id);
  if (c) {
    c.status = status;
    c.params = params;
  } else {
    w.diagnostics.checks.push({ id, status, params });
  }
}

function degraded(w: ReadData): void {
  const s = w.status;
  s.engine.state = 'backoff';
  s.engine.pid = null;
  s.engine.uptimeSec = null;
  s.engine.rssMiB = null;
  s.engine.restarts = 7;
  s.engine.lastExit = { at: at(42), code: 23, error: 'exit status 23' };
  s.api.reachable = false;
  s.metrics.reachable = false;
  s.controlPlane.reachable = false;
  s.controlPlane.lastCheckIn = at(1116);
  s.router.memAvailableMiB = 41;
  s.dataplane.counters = { ...s.dataplane.counters, vctl_local_guard: 3 };
  w.balancers.apiReachable = false;
  // selected is null everywhere; BL-MAIN keeps its pin (re-applied at xray start).
  for (const b of w.balancers.balancers) b.selected = null;
  w.balancers.balancers[0].matchers.push({ kind: 'other', network: null, sample: ['port:25', 'protocol:bittorrent'], total: 2 });
  for (const n of w.nodes.nodes) {
    n.alive = null;
    n.delayMs = null;
  }
  setCheck(w, 'xray_running', 'fail', { state: 'backoff' });
  setCheck(w, 'xray_api', 'fail', { error: 'dial tcp 127.0.0.1:10085: connect: connection refused' });
  setCheck(w, 'panel_link', 'warn', { lastCheckInAgoSec: 1116 });
  setCheck(w, 'subscription_ua', 'fail', { reason: 'malformed_happ' });
  setCheck(w, 'memory', 'fail', { availableMiB: 41, totalMiB: 234, xrayMiB: null });
  // The zram swap did not come up at the tune's last run: a small router without it.
  const zram = s.tune?.items.find((x) => x.id === 'zram');
  if (zram) Object.assign(zram, { state: 'pending', value: null, reason: 'failed' });
  setCheck(w, 'tune', 'warn', { enabled: true, profile: 'lowmem', items: ['swappiness', 'vfs_cache_pressure', 'packet_steering', 'flow_offloading'], zramMiB: null });
  setCheck(w, 'dead_nodes', 'unknown', {});
  setCheck(w, 'pinned_node_dead', 'unknown', {});
  // A real router omits checks whose inputs it cannot read.
  w.diagnostics.checks = w.diagnostics.checks.filter((c) => c.id !== 'no_leak' && c.id !== 'balancer_fallback');
  w.logs.lines.push(
    { time: at(44), level: 'error', source: 'xray', message: 'Failed to start: main: failed to load config files > infra/conf: failed to build outbound handler' },
    { time: at(42), level: 'error', source: 'vctl', message: 'xray exited: exit status 23' },
    { time: at(41), level: 'warn', source: 'vctl', message: 'supervisor: backing off 30s before restart (attempt 7)' },
    { time: at(12), level: 'warn', source: 'vctl', message: 'check-in failed: context deadline exceeded' },
  );
}

/** Every node of the location's main balancer is down: its traffic runs on the backup balancer. */
function reserve(w: ReadData): void {
  const main = w.balancers.balancers.find((b) => b.role === 'main')!;
  main.selected = [];
  main.pinned = null;
  w.status.pins = {};
  for (const n of w.nodes.nodes) {
    if (main.members.indexOf(n.tag) >= 0) {
      n.alive = false;
      n.delayMs = null;
    }
  }
  setCheck(w, 'dead_nodes', 'warn', { count: main.members.length, tags: main.members.slice(0, 10) });
  setCheck(w, 'balancer_fallback', 'warn', { balancers: [main.tag], main: true, blocked: false });
  setCheck(w, 'pinned_node_dead', 'ok', {});
}

// 1111, 2026-09-30: the router's own check found the American exit carrying
// no blocked site and left it out; the card names it.
function unfit(w: ReadData): void {
  if (w.status.route) {
    w.status.route.unfit = [{ tag: 'bridge-us5', country: 'US', egress: null }];
    // «ОАЭ» leaving in Poland, as 1111's did.
    w.status.route.nodes = [...w.status.route.nodes, { tag: 'bridge-ae5', country: 'AE', egress: 'PL' }];
  }
  const tk = w.services.services.find((s) => s.id === 'tiktok');
  if (tk) tk.egress = { TR: 'PL', AE: 'PL' };
}

function empty(w: ReadData): void {
  const s = w.status;
  s.controller.uptimeSec = 1180;
  s.engine = { state: 'idle', xrayVersion: s.engine.xrayVersion, pid: null, uptimeSec: null, rssMiB: null, memoryLimitMiB: s.engine.memoryLimitMiB, restarts: 0, lastExit: null };
  s.api.reachable = false;
  s.metrics.reachable = false;
  s.dataplane = { loaded: false, killSwitch: false, counters: {} };
  s.subscription = { entryIndex: null, entryRemark: null, entryCount: null, fetchedAt: null, source: null, overrideStale: false };
  s.pins = {};
  s.router.memAvailableMiB = 131;
  s.router.uptimeSec = 1260;
  // A router Vectra holds from the start: nothing to give back but the plain
  // internet. With no data plane yet vctl carries nothing: the holder is direct.
  s.power = { enabled: true, running: true, holder: 'direct', handBack: 'direct', wouldIdle: false };
  // A fleet router the operator has not given a subscription yet: no owner.
  w.setup.vectra.owner = null;
  w.balancers = { ...w.balancers, apiReachable: false, balancers: [] };
  w.nodes = { observedAt: null, nodes: [] };
  w.entries = { active: null, panelIndex: null, source: null, overrideStale: false, fetchedAt: null, cached: false, entries: [] };
  const pid = s.controller.pid;
  w.diagnostics.checks = [
    { id: 'controller_running', status: 'ok', params: { pid } },
    { id: 'xray_running', status: 'unknown', params: { state: 'idle' } },
    { id: 'xray_api', status: 'unknown', params: { error: 'xray is not running' } },
    { id: 'dataplane_loaded', status: 'unknown', params: { tproxyHits: null } },
    { id: 'no_leak', status: 'ok', params: { wouldLeak: 0, sinceStart: null, killSwitch: false } },
    { id: 'single_stack', status: 'ok', params: { passwallRunning: false, agentEnabled: false } },
    { id: 'panel_link', status: 'ok', params: { lastCheckInAgoSec: 7 } },
    { id: 'subscription_ua', status: 'unknown', params: {} },
    { id: 'memory', status: 'ok', params: { availableMiB: 131, totalMiB: 234, xrayMiB: null } },
    { id: 'dead_nodes', status: 'ok', params: { count: 0, tags: [] } },
    { id: 'balancer_fallback', status: 'ok', params: { balancers: [], main: false, blocked: false } },
    { id: 'pinned_node_dead', status: 'ok', params: {} },
  ];
  w.logs.lines = [
    { time: at(1180), level: 'info', source: 'vctl', message: 'vctl 0.3.0-r1 starting' },
    { time: at(1170), level: 'info', source: 'vctl', message: 'check-in ok' },
    { time: at(1169), level: 'info', source: 'vctl', message: 'no subscription assigned yet; xray stays idle' },
    { time: at(7), level: 'info', source: 'vctl', message: 'check-in ok' },
  ];
}

/** Straight out of the box: still coming online, Wi-Fi off and open, not linked — the wizard's case. */
function unboxed(w: ReadData): void {
  empty(w);
  w.setup = {
    done: false,
    // OpenWrt ships without a root password: anyone on the LAN can sign in.
    passwordSet: false,
    // The cable is in and the router is still getting its address: the mock
    // brings the internet up by itself a few seconds later, as a router does.
    wan: { proto: 'dhcp', link: true, ipv4: null, gateway: null, dns: [] },
    lan: { ipv4: '192.168.1.1' },
    // OpenWrt's defaults: both radios off, open, channel picked by the radio, the regulatory default.
    wifi: {
      radios: [
        radio('2g', { channel: null, auto: true, country: null, maxPower: true, ssid: 'OpenWrt', secured: false, enabled: false, up: false }),
        radio('5g', { channel: null, auto: true, country: null, maxPower: true, ssid: 'OpenWrt', secured: false, enabled: false, up: false }),
      ],
      tuned: false,
      tunable: true,
      // Nothing on the air judges as fine: the open networks are what the wizard asks about.
      verdict: 'fine',
      suggested: 'Vectra-4E2A',
      apply: null,
    },
    // Never online yet: the code is the router's own, but the bot, the key for
    // the QR and so the Telegram link come with the panel's first answer.
    vectra: {
      linked: false,
      botUsername: null,
      owner: null,
      claim: { state: 'unclaimed', code: '7KQ4M9XD', qr: null, expiresAt: at(-540), botUrl: null, owner: null },
    },
  };
  w.wan_check = { link: true, ipv4: null, dns: false, internet: false, panel: false, checkedAt: at(0) };
  // Never boosted: the router has not listened to the air yet.
  w.wifi_scan = { radios: [], scannedAt: null };
  w.rules = { direct: [], proxy: [], max: 100, catalog: w.rules.catalog, missing: [] };
  w.services = { available: true, services: [] };
}

/**
 * A box as it ships (docs/LAUNCH.md): the Wi-Fi and the router's password
 * from the card, the Wi-Fi on the air and secured, each radio picking its own
 * channel, the regulatory default — the person joins it and opens the wizard
 * over Wi-Fi.
 */
function boxed(w: ReadData): void {
  unboxed(w);
  w.setup.passwordSet = true;
  w.setup.wifi.radios = [
    radio('2g', { channel: null, auto: true, country: null }),
    radio('5g', { channel: null, auto: true, country: null }),
  ];
}

/** The Wi-Fi as an owner may have it (`wifiAs`). */
export type WifiAs = 'manual' | 'overlap' | 'down';

/**
 * The Wi-Fi as an owner may have it, on any scenario (dev: `&wifi=`), for the
 * wizard's verdicts (contract: setup, `wifi.verdict`; the mock judges them at
 * every read): `manual` — a country of their own, 5 GHz on a radar channel at
 * a power they set, left alone; `overlap` — 2.4 GHz on channel 3, between the
 * channels that do not overlap; `down` — 5 GHz on, and not up. The last two
 * get the boost suggested.
 */
export function wifiAs(w: ReadData, as: WifiAs): void {
  const wifi = w.setup.wifi;
  const r2 = wifi.radios.find((r) => r.band === '2g');
  const r5 = wifi.radios.find((r) => r.band === '5g');
  if (as === 'manual') {
    if (r2) Object.assign(r2, { channel: 6, auto: false, country: 'RU', txpower: null, maxPower: true });
    if (r5) Object.assign(r5, { channel: 100, auto: false, country: 'RU', txpower: 14, maxPower: false });
  }
  if (as === 'overlap' && r2) Object.assign(r2, { channel: 3, auto: false });
  if (as === 'down' && r5) r5.up = false;
  // The tuning's recipe, as the router counts it: every 2.4/5 GHz radio that is on.
  const on = wifi.radios.filter((r) => r.enabled === true && (r.band === '2g' || r.band === '5g'));
  wifi.tuned = on.every((r) => r.country === 'PA' && r.maxPower === true && r.auto === false && r.channel !== null);
}

/**
 * Vectra switched off (`vectra off`, set_power): vctl does not run, and what
 * the router answers is what it answers then — no controller, no data plane,
 * the rendered config and the locations cache still on disk. `holder` carries
 * the traffic.
 */
export function off(w: ReadData, holder: Holder): void {
  const s = w.status;
  s.power = { enabled: false, running: false, holder, handBack: null, wouldIdle: false };
  s.controller = { running: false, pid: null, uptimeSec: null };
  s.engine = { state: 'unknown', xrayVersion: null, pid: null, uptimeSec: null, rssMiB: null, memoryLimitMiB: null, restarts: 0, lastExit: null };
  s.api.reachable = false;
  s.metrics.reachable = false;
  s.dataplane = { loaded: false, killSwitch: false, counters: null };
  s.controlPlane = { reachable: null, lastCheckIn: null, routerId: null };
  s.subscription = { ...s.subscription, entryIndex: null, entryRemark: null, source: 'panel', overrideStale: false };
  s.probe = { intervalSec: s.probe.intervalSec, source: 'provider' };
  s.legacy = { ...s.legacy, agentEnabled: holder !== 'direct', passwallRunning: holder === 'passwall2' };
  s.router.memAvailableMiB = 102;
  w.balancers = { ...w.balancers, apiReachable: false, balancers: w.balancers.balancers.map((b) => ({ ...b, selected: null })) };
  for (const n of w.nodes.nodes) Object.assign(n, { alive: null, delayMs: null, lastSeen: null, lastTry: null, traffic: null });
  w.nodes.observedAt = null;
  w.entries = { ...w.entries, active: null };
  w.diagnostics.checks = [
    { id: 'single_stack', status: holder === 'passwall2' ? 'fail' : holder === 'agent' ? 'warn' : 'ok', params: { passwallRunning: holder === 'passwall2', agentEnabled: holder !== 'direct' } },
    { id: 'controller_running', status: 'fail', params: {} },
    { id: 'dataplane_loaded', status: 'fail', params: {} },
    { id: 'xray_running', status: 'unknown', params: {} },
    { id: 'xray_api', status: 'fail', params: { listen: '127.0.0.1:10085', error: 'not answering' } },
    { id: 'panel_link', status: 'unknown', params: {} },
    { id: 'subscription_ua', status: 'ok', params: {} },
    { id: 'memory', status: 'ok', params: { availableMiB: 102, totalMiB: 234 } },
  ];
  // As the router orders them: fail, warn, unknown, ok.
  const rank: Record<string, number> = { fail: 0, warn: 1, unknown: 2, ok: 3 };
  w.diagnostics.checks.sort((a, b) => rank[a.status] - rank[b.status]);
}

/** A fresh copy of the router's data for a scenario; `down` has none. */
export function buildWorld(scenario: Scenario, holder: Holder = 'passwall2'): ReadData | null {
  if (scenario === 'down') return null;
  const w = clone(FIXTURES);
  if (scenario === 'degraded') degraded(w);
  if (scenario === 'reserve') reserve(w);
  if (scenario === 'empty') empty(w);
  if (scenario === 'unboxed') unboxed(w);
  if (scenario === 'boxed') boxed(w);
  if (scenario === 'off') off(w, holder);
  if (scenario === 'unfit') unfit(w);
  return w;
}
