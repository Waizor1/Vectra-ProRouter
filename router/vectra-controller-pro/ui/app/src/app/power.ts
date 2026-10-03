// Turning Vectra on and off (set_power), the same from both views: a dialog
// that says where the internet goes, then the router's own `vectra on|off`,
// watched until status.power shows it.

import type { Status } from '../api/types';
import type { Key, T } from '../i18n';
import { own } from '../lib/own';
import type { RunOpts } from './ctx';

/** A change hands the router between two stacks: allow for a PassWall slow to stop. */
const POWER_WAIT_MS = 90_000;

// What the dialog says: who takes the traffic, by what the router reports.
const ON_BODY: Record<string, Key> = { passwall2: 's.pw.onBody.passwall2', agent: 's.pw.onBody.agent' };
const OFF_BODY: Record<string, Key> = { passwall2: 's.pw.offBody.passwall2', agent: 's.pw.offBody.agent' };
const pick = (m: Record<string, Key>, holder: string | null, other: Key): Key => (holder !== null && own(m, holder)) || other;

/**
 * set_power's params. A Vectra that would carry nothing yet (power.wouldIdle:
 * no operator config, no PassWall2 to route by) is turned on only with
 * `force` — sent after the dialog has said so (powerOpts), and only then.
 */
export function powerParams(s: Status, on: boolean): { on: boolean; force?: true } {
  return on && s.power.wouldIdle === true ? { on, force: true } : { on };
}

export function powerOpts(t: T, s: Status, on: boolean): RunOpts {
  const p = s.power;
  const idle = on && p.wouldIdle === true;
  return {
    key: on ? 'power:on' : 'power:off',
    confirm: on
      ? { title: t('s.pw.onQ'), body: t(idle ? 's.pw.onBody.idle' : pick(ON_BODY, p.holder, 's.pw.onBody')), ok: t('s.pw.on') }
      : { title: t('s.pw.offQ'), body: t(pick(OFF_BODY, p.handBack, 's.pw.offBody')), ok: t('s.pw.off') },
    landed: (n) => n.power.enabled === on && n.power.running === on,
    done: on ? 'power_on' : 'power_off',
    waitMs: POWER_WAIT_MS,
    fail: { busy: 's.pw.busy', would_idle: 's.pw.idle' },
    // would_idle where this page read wouldIdle false (its status older than
    // the router's answer): the router's word is taken at once — the dialog
    // that says so, and on its confirmation `force`, no status poll between.
    retry: on && !idle ? { code: 'would_idle', params: { on, force: true }, opts: powerOpts(t, { ...s, power: { ...p, wouldIdle: true } }, on) } : undefined,
  };
}
