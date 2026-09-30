// Two verdicts from the same status + diagnostics.
//
// health(): the Pro view's — every failed or warned check counts.
// simpleVerdict(): the simple view's — only what changes the owner's internet,
// and what they can do about it. Low memory, a slow panel link or one dead node
// the balancer already routes around are real, but nothing the owner can act
// on: they stay in Pro and in the support report.

import type { Check, Diagnostics, Status } from '../api/types';
import type { Key } from '../i18n';
import { own } from './own';

/** `off`: Vectra is switched off on purpose (status.power.enabled). */
export type Level = 'ok' | 'warn' | 'fail' | 'setup' | 'unknown' | 'off';

export interface Health {
  level: Level;
  fails: Check[];
  warns: Check[];
}

const DOWN_STATES = ['backoff', 'exited', 'stopped', 'failed'];
const SHAKY_STATES = ['starting', 'reloading', 'unknown'];

export function hasSubscription(s: Status): boolean {
  return s.subscription.entryIndex !== null || (s.subscription.entryCount ?? 0) > 0;
}

export function health(status: Status | null, diags: Diagnostics | null): Health {
  const checks = diags ? diags.checks : [];
  const fails = checks.filter((c) => c.status === 'fail');
  const warns = checks.filter((c) => c.status === 'warn');
  if (!status) return { level: 'unknown', fails, warns };
  // Off is not down: nothing is supposed to run.
  if (status.power.enabled === false) return { level: 'off', fails, warns };

  const e = status.engine.state;
  const down =
    status.controller.running === false ||
    fails.length > 0 ||
    (e !== null && DOWN_STATES.indexOf(e) >= 0) ||
    (status.dataplane.loaded === false && hasSubscription(status));
  if (down) return { level: 'fail', fails, warns };

  // A fresh router: no subscription yet, so there is nothing to run.
  if (!hasSubscription(status)) return { level: 'setup', fails, warns };

  const shaky =
    warns.length > 0 ||
    (e !== null && SHAKY_STATES.indexOf(e) >= 0) ||
    e === 'idle' ||
    status.api.reachable === false ||
    status.controlPlane.reachable === false ||
    status.subscription.overrideStale === true;
  return { level: shaky ? 'warn' : 'ok', fails, warns };
}

// ── the simple view ─────────────────────────────────────────────────────────

export type SimpleKind =
  | 'ok'
  | 'checking'
  | 'connecting'
  | 'reserve'
  | 'noroute'
  | 'direct'
  | 'partial'
  | 'pinDown'
  | 'bypass'
  | 'cut'
  | 'down'
  | 'off'
  | 'conflict'
  | 'link'
  | 'setup'
  | 'poweroff'
  | 'switching';
/** The one thing to offer first. */
export type SimpleAct = 'restart' | 'location' | 'unpin' | 'report' | 'link' | 'power' | null;
/**
 * Where the router is in being linked to a Vectra account (ADR-0006): not
 * linked; confirmed (or linked) with the settings on their way; still waiting
 * for them after minutes. null: linked and set up, or not known.
 */
export type LinkState = 'unlinked' | 'claimed' | 'late' | 'recheck' | null;
export type SimpleTone = 'ok' | 'info' | 'warn' | 'fail' | 'mute';

export interface SimpleVerdict {
  kind: SimpleKind;
  tone: SimpleTone;
  title: Key;
  /** Whole sentences, shown in order. */
  lines: Key[];
  act: SimpleAct;
}

const verdict = (kind: SimpleKind, tone: SimpleTone, title: Key, lines: Key[], act: SimpleAct): SimpleVerdict => ({ kind, tone, title, lines, act });

const VIA: Record<string, Key> = { passwall2: 's.pw.via.passwall2', agent: 's.pw.via.agent', direct: 's.pw.via.direct' };
/** Where the internet goes while Vectra is off, in one sentence; a holder the UI does not know yet is "without Vectra". */
export const viaKey = (holder: string | null): Key => (holder !== null && own(VIA, holder)) || 's.pw.via.other';

/** Vectra being turned on or off: the stacks change hands, the internet may drop. */
export const powerSwitching = (on: boolean): SimpleVerdict =>
  verdict('switching', 'info', on ? 's.pw.starting.t' : 's.pw.stopping.t', ['s.pw.wait'], null);

/**
 * The owner's verdict. Every "works" is backed by evidence: without the
 * diagnostics it says "checking", and "servers respond" is said only when the
 * router has walked the traffic's chain (balancer_fallback) and found it
 * carried. `failed`: the diagnostics could not be read at all.
 */
export function simpleVerdict(s: Status, d: Diagnostics | null, failed = false, link: LinkState = null): SimpleVerdict {
  const check = (id: string) => (d ? d.checks.find((c) => c.id === id) : undefined);

  // Switched off on purpose: not a failure — where the internet goes instead,
  // and the one way back. Still running: it is being turned off.
  const pw = s.power;
  if (pw.enabled === false) return pw.running === true ? powerSwitching(false) : verdict('poweroff', 'mute', 's.pw.off.t', [viaKey(pw.holder)], 'power');
  if (s.controller.running === false) return verdict('off', 'fail', 's.off.t', ['s.reboot'], 'report');
  // Two proxy stacks fight over the same packets: nothing here fixes that.
  if (check('single_stack')?.status === 'fail') return verdict('conflict', 'fail', 's.conflict.t', ['s.conflict.d'], 'report');
  if (!hasSubscription(s)) {
    // The settings just went and what the router says about its link is older
    // than that: do not guess between "released" and "on their way".
    if (link === 'recheck') return verdict('checking', 'info', 's.checking.t', ['s.checking.d'], null);
    // Not linked yet: the one thing that sets the VPN up is linking it.
    if (link === 'unlinked') return verdict('link', 'info', 's.link.t', ['s.link.d'], 'link');
    if (link === 'claimed') return verdict('connecting', 'info', 's.claimed.t', ['s.claimed.d'], null);
    if (link === 'late') return verdict('setup', 'warn', 's.cfgSlow.t', ['s.cfgSlow.d'], 'report');
    return verdict('setup', 'info', 's.setup.t', ['s.setup.d'], 'report');
  }

  const e = s.engine.state;
  // No interception rules: traffic is not even offered to xray. A restart of
  // xray does not load them — the controller does, at start.
  if (s.dataplane.loaded === false) return verdict('down', 'fail', 's.down.t', ['s.down.direct', 's.reboot'], 'report');
  if (e !== null && DOWN_STATES.indexOf(e) >= 0) {
    // With the kill switch on, forwarded traffic fails closed while xray is down.
    const lines: Key[] = [s.dataplane.killSwitch ? 's.down.blocked' : 's.down.direct'];
    if (e === 'backoff') lines.push('s.down.auto');
    return verdict('down', 'fail', 's.down.t', lines, 'restart');
  }
  if (e !== 'running') return verdict('connecting', 'info', 's.connecting.t', ['s.connecting.d'], null);
  if (!d) return failed ? verdict('ok', 'ok', 's.ok.t', ['s.ok.d0'], null) : verdict('checking', 'info', 's.checking.t', ['s.checking.d'], null);

  // xray runs. Does the traffic reach it (no_leak), and is it carried (the chains)?
  const leak = check('no_leak');
  if (leak?.status === 'fail') {
    return leak.params.killSwitch === true
      ? verdict('cut', 'fail', 's.cut.t', ['s.cut.d', 's.reboot'], 'report')
      : verdict('bypass', 'fail', 's.bypass.t', ['s.bypass.d', 's.reboot'], 'report');
  }
  const pin = check('pinned_node_dead');
  const pinBad = !!pin && (pin.status === 'warn' || pin.status === 'fail');
  // xray sends everything to a pinned target, dead or alive.
  if (pinBad && (pin!.status === 'fail' || pin!.params.main === true)) return verdict('pinDown', 'fail', 's.pinDown.t', ['s.pinDown.d'], 'unpin');

  const bf = check('balancer_fallback');
  const p = bf ? bf.params : {};
  // Right after an xray start nothing is probed yet: do not guess.
  if (bf?.status === 'unknown' && p.warming === true) return verdict('connecting', 'info', 's.connecting.t', ['s.connecting.d'], null);
  if (p.mainDirect === true) return verdict('direct', 'fail', 's.direct.t', ['s.direct.d'], 'location');
  // vctl 0.4 names the main chain's dead end; 0.3 only said "blocked" and "main".
  const mainBlocked = p.mainBlocked === true || (p.mainBlocked === undefined && bf?.status === 'fail' && p.main === true && p.blocked === true);
  if (mainBlocked) return verdict('noroute', 'fail', 's.noroute.t', ['s.noroute.d'], 'location');
  if (bf?.status === 'warn' && p.main === true) return verdict('reserve', 'warn', 's.reserve.t', ['s.reserve.d'], 'location');
  if (pinBad) return verdict('partial', 'warn', 's.partial.t', ['s.partial.pin'], 'unpin');
  // A routed chain with nowhere to go: some sites fail, the rest work.
  if (bf?.status === 'fail') return verdict('partial', 'warn', 's.partial.t', ['s.partial.route'], 'location');
  return verdict('ok', 'ok', 's.ok.t', [bf?.status === 'ok' ? 's.ok.d' : 's.ok.d0'], null);
}

/**
 * The router's memory, judged where the kernel starts reclaiming the pages
 * of running programs — 5% of RAM, at least 12 MiB (the router's
 * memguard.CriticalKB; the diagnostics' memory check uses the same lines) —
 * and warned at twice that. A 234 MB router lives at 40-65 MB free under
 * load, as the fleet did under PassWall2.
 */
export function memTone(free: number | null, total: number | null): 'ok' | 'warn' | 'fail' | 'mute' {
  if (free === null) return 'mute';
  const critical = Math.max(Math.floor(((total ?? 234) * 5) / 100), 12);
  return free < critical ? 'fail' : free < 2 * critical ? 'warn' : 'ok';
}
