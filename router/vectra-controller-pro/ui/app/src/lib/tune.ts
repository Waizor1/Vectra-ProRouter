// The router's tune («Разгон роутера», contract: Tune) in plain words: what
// is in place — set by the tune, or so before it — for the wizard's last
// step and the Pro diagnostics' line.

import type { T } from '../i18n';
import type { Tune } from '../api/types';
import type { Fmt } from './format';

/**
 * One phrase per thing in place, in the router's order: the zram swap with its
 * size, the two memory settings as one, packet steering, flow offloading, the
 * quiet cron log. An id the UI does not know yet is shown raw. The memory
 * settings are there for the swap: they are named only beside it, and not at
 * all when `brief` (one line, the Pro diagnostics), where they go without
 * saying. vctl's leftovers in RAM are housekeeping, never named.
 */
export function tuneWords(t: T, f: Fmt, ids: readonly string[], zramMiB: number | null, brief = false): string[] {
  const out: string[] = [];
  const add = (w: string) => void (out.indexOf(w) < 0 && out.push(w));
  for (const id of ids) {
    switch (id) {
      case 'zram':
        add(zramMiB !== null ? t('tune.zram', { size: f.mib(zramMiB) }) : t('tune.zram0'));
        break;
      case 'swappiness':
      case 'vfs_cache_pressure':
        if (!brief && ids.indexOf('zram') >= 0) add(t('tune.memory'));
        break;
      case 'packet_steering':
        add(t('tune.packet_steering'));
        break;
      case 'flow_offloading':
        add(t('tune.flow_offloading'));
        break;
      case 'cron_loglevel':
        add(t('tune.cron_loglevel'));
        break;
      case 'tmp_leftovers':
        break;
      default:
        add(id);
    }
  }
  return out;
}

/** What of a status' tune is in place (`applied` or `already`), and the zram swap's size. */
export function tuneInPlace(tune: Tune): { ids: string[]; zramMiB: number | null } {
  const ids: string[] = [];
  let zramMiB: number | null = null;
  for (const it of tune.items) {
    if (it.state !== 'applied' && it.state !== 'already') continue;
    ids.push(it.id);
    if (it.id === 'zram' && it.value !== null && /^\d+$/.test(it.value)) zramMiB = +it.value;
  }
  return { ids, zramMiB };
}
