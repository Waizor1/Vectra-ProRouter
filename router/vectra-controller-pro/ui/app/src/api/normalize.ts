// Coerce whatever the transport returned into the contract shapes.
// LuCI's `expect: {'': {}}` turns an empty ubus answer into `{}`, an older vctl
// may miss a field, a newer one may add one. Views read normalized data only: a
// missing field is `null` ("unknown") — never `undefined`, never a crash.
//
// Each shape is described once, as a spec mirroring ./types.ts:
//   s string|null · n number|null · b boolean|null · o object (default {})
//   S string[] · L string[]|null · N number[]|null
//   m {k: number}|null · M {k: string}|null · B {k: boolean}|null
//   T required string, I required number (a list item without it is dropped)
//   'x:d' = x with default d · ['?', spec] = object or null · [spec] = list

import type { Action, ReadData, ReadMethod } from './types';

type Obj = Record<string, unknown>;
type Spec = string | { [k: string]: Spec } | Spec[];

const DROP = {};
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);
const num = (v: unknown) => (typeof v === 'number' && isFinite(v) ? v : null);
const str = (v: unknown) => (typeof v === 'string' ? v : null);
const strs = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);

function dict(v: unknown, pick: (x: unknown) => unknown): Obj | null {
  if (!isObj(v)) return null;
  const out: Obj = Object.create(null);
  for (const k in v) {
    const x = pick(v[k]);
    if (x !== null) out[k] = x;
  }
  return out;
}

function norm(spec: Spec, v: unknown): unknown {
  if (Array.isArray(spec)) {
    if (spec[0] === '?') {
      const x = isObj(v) ? norm(spec[1], v) : null;
      return x === DROP ? null : x;
    }
    return (Array.isArray(v) ? v : []).map((x) => norm(spec[0], x)).filter((x) => x !== DROP);
  }
  if (typeof spec === 'object') {
    const o = isObj(v) ? v : {};
    const out: Obj = {};
    for (const k in spec) {
      const x = norm(spec[k], o[k]);
      if (x === DROP) return DROP;
      out[k] = x;
    }
    return out;
  }
  const [t, d] = spec.split(':');
  switch (t) {
    case 's':
      return str(v) ?? d ?? null;
    case 'n':
      return num(v) ?? (d ? +d : null);
    case 'b':
      return typeof v === 'boolean' ? v : null;
    case 'o':
      return isObj(v) ? v : {};
    case 'S':
      return strs(v);
    case 'L':
      // xray reports a leastPing balancer with no live node as [""]: no target.
      return Array.isArray(v) ? strs(v).filter(Boolean) : null;
    case 'N':
      return Array.isArray(v) ? v.map(num).filter((x) => x !== null) : null;
    case 'm':
      return dict(v, num);
    case 'M':
      return dict(v, (x) => str(x) || null);
    case 'B':
      return dict(v, (x) => (typeof x === 'boolean' ? x : null));
    case 'T':
      return str(v) || DROP;
  }
  return num(v) ?? DROP; // 'I'
}

const SPECS: Record<ReadMethod, Spec> = {
  status: {
    version: 's',
    power: { enabled: 'b', running: 'b', holder: 's', handBack: 's', wouldIdle: 'b' },
    controller: { running: 'b', pid: 'n', uptimeSec: 'n' },
    engine: {
      state: 's',
      xrayVersion: 's',
      pid: 'n',
      uptimeSec: 'n',
      rssMiB: 'n',
      memoryLimitMiB: 'n',
      restarts: 'n',
      lastExit: ['?', { at: 's', code: 'n', error: 's' }],
    },
    api: { listen: 's', reachable: 'b' },
    metrics: { listen: 's', reachable: 'b' },
    dataplane: { loaded: 'b', killSwitch: 'b', counters: 'm' },
    controlPlane: { reachable: 'b', lastCheckIn: 's', routerId: 's' },
    subscription: { entryIndex: 'n', entryRemark: 's', entryCount: 'n', fetchedAt: 's', source: 's', overrideStale: 'b' },
    probe: { intervalSec: 'n', source: 's' },
    pins: 'M',
    legacy: { agentEnabled: 'b', passwallRunning: 'b', passwall: 's', passwallRetiredAt: 's' },
    ui: { locked: 'b' },
    remoteShell: 'b',
    brand: { id: 's', name: 's', bot: 's', support: 's', lanName: 's', site: 's' },
    router: {
      hostname: 's',
      model: 's',
      release: 's',
      memTotalMiB: 'n',
      memAvailableMiB: 'n',
      overlayFreeMiB: 'n',
      tmpFreeMiB: 'n',
      load: 'N',
      uptimeSec: 'n',
    },
    route: [
      '?',
      {
        nodes: [{ tag: 'T', country: 's', egress: 's' }],
        movedFrom: ['?', { tag: 'T', country: 's', egress: 's' }],
        movedAt: 's',
        reason: 's',
        unfit: [{ tag: 'T', country: 's', egress: 's' }],
        unfitKept: [{ tag: 'T', country: 's', egress: 's' }],
      },
    ],
    tune: ['?', { enabled: 'b', profile: 's', items: [{ id: 'T', state: 's', value: 's', target: 's', reason: 's' }] }],
  },
  balancers: {
    apiReachable: 'b',
    defaultOutbound: 's',
    probe: { intervalSec: 'n', providerIntervalSec: 'n', source: 's', sampling: 'n', timeoutSec: 'n', destination: 's' },
    balancers: [
      {
        tag: 'T',
        role: 's:unused',
        strategy: 's:',
        expected: 'n',
        selector: 'S',
        members: 'S',
        fallback: ['?', { tag: 'T', balancer: 's' }],
        selected: 'L',
        pinned: 's',
        matchers: [{ kind: 's:domain', network: 's', sample: 'S', total: 'n:0' }],
      },
    ],
  },
  nodes: {
    observedAt: 's',
    nodes: [
      {
        tag: 'T',
        protocol: 's',
        transport: 's',
        security: 's',
        address: 's',
        port: 'n',
        countryHint: 's',
        alive: 'b',
        delayMs: 'n',
        lastSeen: 's',
        lastTry: 's',
        traffic: ['?', { upBytes: 'n', downBytes: 'n' }],
        balancers: 'S',
      },
    ],
  },
  entries: {
    active: 'n',
    panelIndex: 'n',
    source: 's',
    overrideStale: 'b',
    fetchedAt: 's',
    cached: 'b',
    entries: [{ index: 'I', remark: 's:', nodeCount: 'n', balancerCount: 'n' }],
  },
  diagnostics: { checkedAt: 's', checks: [{ id: 'T', status: 's:unknown', params: 'o' }] },
  logs: { lines: [{ time: 's', level: 's:unknown', source: 's', message: 's:' }] },
  setup: {
    done: 'b',
    passwordSet: 'b',
    wan: { proto: 's', link: 'b', ipv4: 's', gateway: 's', dns: 'S' },
    lan: { ipv4: 's' },
    wifi: {
      radios: [
        {
          device: 'T',
          band: 's',
          channel: 'n',
          auto: 'b',
          htmode: 's',
          country: 's',
          txpower: 'n',
          maxPower: 'b',
          ssid: 's',
          secured: 'b',
          enabled: 'b',
          width: 'n',
          ap: 'b',
          mesh: 'b',
          up: 'b',
        },
      ],
      tuned: 'b',
      tunable: 'b',
      verdict: 's',
      suggested: 's',
      rename: 's',
      apply: ['?', { state: 's', at: 's', detail: 's', radios: 'B' }],
    },
    vectra: {
      linked: 'b',
      botUsername: 's',
      owner: ['?', { label: 's' }],
      claim: ['?', { state: 's:unclaimed', code: 's', qr: 's', expiresAt: 's', botUrl: 's', owner: ['?', { label: 's' }] }],
    },
  },
  wan_check: { link: 'b', ipv4: 's', dns: 'b', internet: 'b', panel: 'b', checkedAt: 's' },
  wifi_scan: {
    radios: [
      {
        device: 'T',
        band: 's',
        current: 'n',
        recommended: 'n',
        networks: 'n',
        channels: [{ channel: 'I', networks: 'n:0', strongest: 'n' }],
        error: 's',
      },
    ],
    scannedAt: 's',
  },
  rules: { direct: 'S', proxy: 'S', max: 'n', catalog: 'S', missing: 'S' },
  services: { available: 'b', services: [{ id: 'T', choice: 's', defaultCountry: 's', countries: 'S', active: 'b', stale: 'b', egress: 'M' }] },
  // A rule without an id is kept, never dropped: the whole list goes back on
  // every save, and a dropped rule would be deleted from the router.
  port_forwards: {
    rules: [{ id: 's', preset: 's', destIp: 's:', deviceName: 's', port: 's:', proto: 's:tcp', direct: 'b', enabled: 'b' }],
    devices: [{ name: 's', ip: 'T' }],
    cgnat: 'b',
    directActive: 'b',
    max: 'n',
  },
};

export const normalize = <M extends ReadMethod>(m: M, raw: unknown): ReadData[M] => norm(SPECS[m], raw) as ReadData[M];

export function normAction(raw: unknown): Action {
  const r = isObj(raw) ? raw : {};
  const ok = r.ok === true;
  return { ok, code: str(r.code) || (ok ? 'ok' : 'internal'), detail: str(r.detail) };
}
