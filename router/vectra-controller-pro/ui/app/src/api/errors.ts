// A transport rejection, classified so the UI can say something useful in the
// operator's language while still showing the raw LuCI/ubus message verbatim.

export type ErrKind = 'not_found' | 'access' | 'method' | 'timeout' | 'network' | 'locked' | 'other';

export interface ErrInfo {
  kind: ErrKind;
  raw: string;
}

export function errText(e: unknown): string {
  if (typeof e === 'string') return e;
  if (e && typeof e === 'object') {
    const m = (e as { message?: unknown }).message;
    if (typeof m === 'string' && m) return m;
    const n = (e as { name?: unknown }).name;
    if (typeof n === 'string' && n) return n;
    try {
      return JSON.stringify(e);
    } catch {
      /* fall through */
    }
  }
  return String(e);
}

// LuCI raises e.g. "RPC call to vectra/status failed with ubus code 4: Resource not found",
// "... failed with error -32002: Access denied" or "... failed with HTTP error 0: ?".
const RULES: [RegExp, ErrKind][] = [
  // A read method the router refused with an action code (see refusal() in store.ts).
  [/^vectra\/\w+ refused: locked$/, 'locked'],
  [/object not found|ubus code 4\b/, 'not_found'],
  [/access denied|permission denied|ubus code 6\b|-32002|http error 40[13]/, 'access'],
  [/method not found|ubus code 3\b/, 'method'],
  [/timed out|timeout|ubus code 7\b/, 'timeout'],
  // LuCI 24.10 says "XHR request aborted by browser" when the connection drops.
  [/network|fetch|http error|connection|aborted|xhr request|ubus code 10\b|load failed/, 'network'],
];

export function describeError(e: unknown): ErrInfo {
  const raw = errText(e);
  const s = raw.toLowerCase();
  for (const [re, kind] of RULES) if (re.test(s)) return { kind, raw };
  return { kind: 'other', raw };
}
