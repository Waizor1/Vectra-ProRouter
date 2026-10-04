// The journal as an owner reads it: each line at the level it really has, the
// same line said again and again shown once with a count.

import type { LogLine } from '../api/types';
import { own } from './own';

/**
 * OpenWrt's own chatter that syslog files under an alarming priority: busybox
 * crond's start banner (cron.err), a LuCI sign-in (daemon.err), procd's
 * SIGHUP note, dnsmasq at boot before the WAN has a resolver. Nothing is wrong.
 */
const BENIGN = [
  /crond \(busybox [^)]*\) started, log level \d+/,
  /accepted login on .* for root/,
  /Got unexpected signal 1\b/,
  /no servers found in \/tmp\/resolv\.conf\.d\/resolv\.conf\.auto, will retry/,
];

// vctl logs through slog: `time=… level=WARN msg="…" key=value`, whatever the syslog priority.
const SLOG_LEVEL = /(?:^|\s)level=(\w+)/;
const LEVELS: Record<string, string> = { debug: 'debug', info: 'info', notice: 'info', warn: 'warn', warning: 'warn', err: 'error', error: 'error' };

export interface LogRow {
  time: string | null;
  level: string;
  source: string | null;
  /** The message as a person reads it: slog's time and level taken out, its msg unquoted. */
  text: string;
  /** How many times in a row it was logged. */
  count: number;
}

export function lineLevel(l: LogLine): string {
  if (BENIGN.some((re) => re.test(l.message))) return 'info';
  const m = SLOG_LEVEL.exec(l.message);
  return (m && own(LEVELS, m[1].toLowerCase())) || l.level;
}

export function lineText(message: string): string {
  if (!SLOG_LEVEL.test(message)) return message;
  return message
    .replace(/(^|\s)time=\S+/, '')
    .replace(SLOG_LEVEL, '')
    .trim()
    .replace(/^msg=("((?:[^"\\]|\\.)*)"|\S+)/, (_, raw: string, quoted?: string) => (quoted !== undefined ? quoted.replace(/\\(.)/g, '$1') : raw));
}

/** The lines as rows, a line repeated straight after itself folded into the first with a count; `keep` filters by level. */
export function logRows(lines: LogLine[], keep: (level: string) => boolean): LogRow[] {
  const out: LogRow[] = [];
  for (const l of lines) {
    const level = lineLevel(l);
    if (!keep(level)) continue;
    const text = lineText(l.message);
    const last = out[out.length - 1];
    if (last && last.level === level && last.source === l.source && last.text === text) {
      last.count++;
      if (l.time) last.time = l.time;
    } else {
      out.push({ time: l.time, level, source: l.source, text, count: 1 });
    }
  }
  return out;
}
