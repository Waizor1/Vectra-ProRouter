// Shapes of the `vectra` ubus object, mirrored from ../../contract/*.json.
// The contract README is the source of truth. Enumerations are open-ended
// (`| (string & {})`): the router may grow a value before the UI learns it, and
// the UI must show it raw instead of breaking.

type Open<T extends string> = T | (string & {});

export type Method =
  | 'status'
  | 'balancers'
  | 'nodes'
  | 'entries'
  | 'diagnostics'
  | 'logs'
  | 'select_entry'
  | 'reset_entry'
  | 'pin_balancer'
  | 'unpin_balancer'
  | 'set_probe_interval'
  | 'restart_xray'
  | 'setup'
  | 'wan_check'
  | 'rules'
  | 'wifi_scan'
  | 'set_wifi'
  | 'optimize_wifi'
  | 'finish_setup'
  | 'set_rules'
  | 'services'
  | 'set_service'
  | 'set_power'
  | 'set_remote_shell';

export type ReadMethod = 'status' | 'balancers' | 'nodes' | 'entries' | 'diagnostics' | 'logs' | 'setup' | 'wan_check' | 'rules' | 'wifi_scan' | 'services';
export type ActionMethod = Exclude<Method, ReadMethod>;

/** The transport the host page hands to mount(): LuCI rpc in production, fixtures in dev. */
export type CallFn = (method: string, params?: Record<string, unknown>) => Promise<unknown>;

/**
 * LuCI's own change of root's password (`luci.setPassword`), as the host page
 * hands it to mount(): true when LuCI took the password, false when it did
 * not; a transport failure rejects. vctl never sees a password.
 */
export type SetPasswordFn = (password: string) => Promise<boolean>;

export type EngineState = Open<
  'running' | 'starting' | 'reloading' | 'backoff' | 'exited' | 'stopped' | 'failed' | 'idle' | 'unknown'
>;
export type ChoiceSource = Open<'panel' | 'local'>;
export type ProbeSource = Open<'default' | 'local' | 'provider'>;
export type BalancerRole = Open<'main' | 'routed' | 'reserve' | 'unused'>;
export type Strategy = Open<'leastLoad' | 'leastPing' | 'random' | 'roundRobin'>;
export type MatcherKind = Open<'domain' | 'ip' | 'all'>;
export type CheckStatus = Open<'ok' | 'warn' | 'fail' | 'unknown'>;
export type LogLevel = Open<'debug' | 'info' | 'warn' | 'error' | 'unknown'>;
/** Who carries the LAN's traffic (contract: Power). */
export type Holder = Open<'vectra' | 'passwall2' | 'agent' | 'direct'>;
/** What became of PassWall2 on the router (contract: Power, `legacy.passwall`). */
export type PassWallState = Open<'installed' | 'retired' | 'absent'>;

export interface Status {
  version: string | null;
  /**
   * Vectra's own switch: `enabled` switched on (a trial counts), `running`
   * procd runs it, `holder` who carries the traffic now, `handBack` who takes
   * it when Vectra is turned off (null while it is off), `wouldIdle` that
   * switched on now it would carry nothing — the LAN without a VPN until the
   * router is linked (set_power then wants `force`). All null on a vctl
   * older than the switch.
   */
  power: { enabled: boolean | null; running: boolean | null; holder: Holder | null; handBack: Holder | null; wouldIdle?: boolean | null };
  controller: { running: boolean | null; pid: number | null; uptimeSec: number | null };
  engine: {
    state: EngineState | null;
    xrayVersion: string | null;
    pid: number | null;
    uptimeSec: number | null;
    rssMiB: number | null;
    memoryLimitMiB: number | null;
    restarts: number | null;
    lastExit: { at: string | null; code: number | null; error: string | null } | null;
  };
  api: { listen: string | null; reachable: boolean | null };
  metrics: { listen: string | null; reachable: boolean | null };
  dataplane: { loaded: boolean | null; killSwitch: boolean | null; counters: Record<string, number> | null };
  controlPlane: { reachable: boolean | null; lastCheckIn: string | null; routerId: string | null };
  subscription: {
    entryIndex: number | null;
    entryRemark: string | null;
    entryCount: number | null;
    fetchedAt: string | null;
    source: ChoiceSource | null;
    overrideStale: boolean | null;
  };
  probe: { intervalSec: number | null; source: ProbeSource | null };
  pins: Record<string, string> | null;
  legacy: {
    agentEnabled: boolean | null;
    passwallRunning: boolean | null;
    /** installed (the takeover's way back), retired (vctl removed it after a day: `passwallRetiredAt`), absent. */
    passwall: PassWallState | null;
    passwallRetiredAt: string | null;
  };
  /** `locked`: the operator allows only the simple view here; the router refuses the Pro methods itself. */
  ui: { locked: boolean | null };
  /** The panel's support may run commands on the router (the owner's switch); null: a router without the switch. */
  remoteShell: boolean | null;
  router: {
    hostname: string | null;
    model: string | null;
    release: string | null;
    memTotalMiB: number | null;
    memAvailableMiB: number | null;
    overlayFreeMiB: number | null;
    tmpFreeMiB: number | null;
    load: number[] | null;
    uptimeSec: number | null;
  };
  /** Where the main traffic goes now and what the router's watchdog moved it off; null before its first look. */
  route: RouteView | null;
  /** The router's tune («Разгон роутера»): what it set on this router; null on a vctl without it. */
  tune: Tune | null;
}

export type TuneState = Open<'applied' | 'already' | 'pending' | 'user_set' | 'skipped'>;

/**
 * One thing the tune sets: `zram` (compressed swap; `value` its size in MiB),
 * `swappiness`, `vfs_cache_pressure`, `packet_steering`, `flow_offloading`,
 * `cron_loglevel`, `tmp_leftovers` (vctl's own leftovers in RAM removed;
 * `value` the MiB found, null when none).
 * `applied`: the tune set it; `already`: it was so; `user_set`: the owner's
 * own choice, left alone; `skipped`: not for this router (`reason`).
 */
export interface TuneItem {
  id: string;
  state: TuneState | null;
  value: string | null;
  target: string | null;
  reason: string | null;
}

export interface Tune {
  enabled: boolean | null;
  /** `lowmem` (under 384 MiB of RAM) or `standard`. */
  profile: Open<'lowmem' | 'standard'> | null;
  items: TuneItem[];
}

/** A node and the country its tag names (null when none). */
export interface NodeRef {
  tag: string;
  country: string | null;
  /** Where the router saw it really leave, when not where its name says. */
  egress: string | null;
}

/** The server card's line: the nodes the main traffic goes through, and the node the watchdog moved it off. */
export interface RouteView {
  nodes: NodeRef[];
  movedFrom: NodeRef | null;
  movedAt: string | null;
  /** failing | fallback | borrowed */
  reason: string | null;
  /** Servers the router's own check found unable to open blocked sites, left out until they can. */
  unfit: NodeRef[];
  /** Such servers the main traffic still goes through: there is no other. */
  unfitKept: NodeRef[];
}

export interface Matcher {
  kind: MatcherKind;
  network: string | null;
  sample: string[];
  total: number;
}

export interface Balancer {
  tag: string;
  role: BalancerRole;
  strategy: Strategy;
  expected: number | null;
  selector: string[];
  members: string[];
  fallback: { tag: string; balancer: string | null } | null;
  selected: string[] | null;
  pinned: string | null;
  matchers: Matcher[];
}

export interface Probe {
  intervalSec: number | null;
  providerIntervalSec: number | null;
  source: ProbeSource | null;
  sampling: number | null;
  timeoutSec: number | null;
  destination: string | null;
}

export interface Balancers {
  apiReachable: boolean | null;
  probe: Probe;
  balancers: Balancer[];
  /** xray's default outbound (the config's first): where a balancer with no target and no fallback sends its traffic. */
  defaultOutbound: string | null;
}

export interface Node {
  tag: string;
  protocol: string | null;
  transport: string | null;
  security: string | null;
  address: string | null;
  port: number | null;
  countryHint: string | null;
  alive: boolean | null;
  delayMs: number | null;
  lastSeen: string | null;
  lastTry: string | null;
  traffic: { upBytes: number | null; downBytes: number | null } | null;
  balancers: string[];
}

export interface Nodes {
  observedAt: string | null;
  nodes: Node[];
}

export interface Entry {
  index: number;
  remark: string;
  nodeCount: number | null;
  balancerCount: number | null;
}

export interface Entries {
  active: number | null;
  panelIndex: number | null;
  source: ChoiceSource | null;
  overrideStale: boolean | null;
  fetchedAt: string | null;
  cached: boolean | null;
  entries: Entry[];
}

export interface Check {
  id: string;
  status: CheckStatus;
  params: Record<string, unknown>;
}

export interface Diagnostics {
  checkedAt: string | null;
  checks: Check[];
}

export interface LogLine {
  time: string | null;
  level: LogLevel;
  source: string | null;
  message: string;
}

export interface Logs {
  lines: LogLine[];
}

export interface Action {
  ok: boolean;
  code: string;
  detail: string | null;
}

// ── setup wizard, linking and the owner's own rules ─────────────────────────

export type WanProto = Open<'dhcp' | 'pppoe' | 'static' | 'other'>;
export type ClaimState = Open<'unclaimed' | 'pending' | 'claimed'>;

export interface Claim {
  state: ClaimState;
  /** 8 characters; shown to people as XXXX-XXXX. */
  code: string | null;
  /** "VECTRA:R1:…" for the Vectra app's scanner; null when the router has no key to seal it with. */
  qr: string | null;
  expiresAt: string | null;
  /** https://t.me/<bot>?start=rt_<code>, or null when the bot is not known. */
  botUrl: string | null;
  /** The account that claimed it (masked), once the panel reports one. */
  owner: Owner | null;
}

/** A Vectra account as the router may show it: a masked label, never an id. */
export interface Owner {
  label: string | null;
}

export interface Setup {
  done: boolean | null;
  /** Root has a password, so LuCI's login asks for one; null: the router cannot tell. */
  passwordSet: boolean | null;
  /** Read-only: the router sets its internet connection up itself. */
  wan: {
    proto: WanProto | null;
    link: boolean | null;
    ipv4: string | null;
    gateway: string | null;
    dns: string[];
  };
  /** How the home network reaches the router: `ipv4` is where this page always opens. */
  lan: { ipv4: string | null };
  /**
   * One radio per band. `tuned`: every enabled 2.4/5 GHz radio runs at full
   * power on a fixed channel (null when the router cannot be tuned) — the
   * tuning's recipe, not the verdict (`verdict`).
   * `tunable`: false on a router with a band Panama's rules do not cover
   * (6 GHz): the wizard only names its networks there. `suggested`: a unique
   * default name (Vectra-XXXX from the MAC), offered instead of OpenWrt's.
   * `apply`: how the last Wi-Fi change went once the Wi-Fi restarted.
   */
  wifi: {
    radios: WifiRadio[];
    tuned: boolean | null;
    tunable: boolean | null;
    /**
     * How the Wi-Fi is as it is: `fine` (it works — the tuning's own or the
     * owner's), `boost` (more to get: a band down, or 2.4 GHz on a channel
     * that overlaps), `manual` (the owner's own choice the tuning would undo:
     * left alone). null when the router cannot be tuned, or on an older vctl.
     */
    verdict: WifiVerdict | null;
    suggested: string | null;
    apply: WifiApply | null;
  };
  /** `linked`: the router has an operator config. `owner`: the account it belongs to, when the panel named one. */
  vectra: { linked: boolean | null; botUsername: string | null; owner: Owner | null; claim: Claim | null };
}

export type Band = Open<'2g' | '5g' | '6g' | '60g'>;
export type WifiVerdict = Open<'fine' | 'boost' | 'manual'>;

/** A Wi-Fi radio and the network on it (its first access point). Never the key. */
export interface WifiRadio {
  /** The UCI wifi-device, e.g. "radio0": what changes name. */
  device: string;
  band: Band | null;
  /** null while the radio picks its channel itself (`auto`). */
  channel: number | null;
  auto: boolean | null;
  htmode: string | null;
  country: string | null;
  /** dBm set in UCI; null: the driver's maximum. */
  txpower: number | null;
  maxPower: boolean | null;
  ssid: string | null;
  secured: boolean | null;
  enabled: boolean | null;
  /** The channel width htmode gives, MHz (20, 40, 80, 160). */
  width: number | null;
  /** It carries an access point network: the one a name and a password are for. */
  ap: boolean | null;
  /** It carries a mesh (or ad-hoc) link: its channel stays where the link is. */
  mesh: boolean | null;
  /** Up right now, as the router's network daemon says; null: unknown. */
  up: boolean | null;
}

/**
 * The last Wi-Fi change, after the restart: `applying` until the router has
 * seen the radios come back; `ok`; `partial` (a radio that was off did not come
 * up); `rolled_back` (a radio that worked before did not come back, so the
 * router put the old settings back); `unverified` (the router could not tell);
 * `failed` (it had to put the old settings back and could not).
 */
export interface WifiApply {
  state: Open<'applying' | 'ok' | 'partial' | 'rolled_back' | 'unverified' | 'failed'> | null;
  at: string | null;
  detail: string | null;
  /** Per radio: up after the change. */
  radios: Record<string, boolean> | null;
}

/** What is on the air around one radio, and the channel to use. */
export interface WifiScanRadio {
  device: string;
  band: Band | null;
  current: number | null;
  recommended: number | null;
  networks: number | null;
  channels: { channel: number; networks: number; strongest: number | null }[];
  /**
   * Why this radio was not heard: `off` (it was switched off), `empty`
   * (nothing came back), `failed`. It then keeps a channel that works, or
   * gets the default.
   */
  error: Open<'off' | 'empty' | 'failed'> | null;
}

/** The air as the router heard it during the last Wi-Fi boost; it does not listen by itself. */
export interface WifiScan {
  radios: WifiScanRadio[];
  scannedAt: string | null;
}

export interface WanCheck {
  link: boolean | null;
  ipv4: string | null;
  dns: boolean | null;
  internet: boolean | null;
  panel: boolean | null;
  checkedAt: string | null;
}

export interface Rules {
  direct: string[];
  proxy: string[];
  max: number | null;
  /** The services «+ сервис» offers: categories of the router's geo file, kept as "geosite:<id>". */
  catalog: string[];
  /** Services in the lists the router's geo file no longer has: kept, not routed. */
  missing: string[];
}

/** One service with a country of its own (contract: Services). */
export interface ServiceInfo {
  /** youtube | tiktok | telegram — a newer router may add one. */
  id: string;
  /** The owner's country (ISO); null = the subscription entry's own path. */
  choice: string | null;
  /** The country of the entry's own path; null when it names none or several. */
  defaultCountry: string | null;
  /** The ISO codes the running entry's outbounds name. */
  countries: string[];
  /** The choice runs now. */
  active: boolean | null;
  /** Chosen, but the entry has no outbound of that country any more. */
  stale: boolean | null;
  /** Offered countries whose every exit the router saw leave in one other country: that country. */
  egress: Record<string, string>;
}

export interface Services {
  /** false where the operator's policy routes the router: nothing to choose. */
  available: boolean | null;
  services: ServiceInfo[];
}

export interface ReadData {
  status: Status;
  balancers: Balancers;
  nodes: Nodes;
  entries: Entries;
  diagnostics: Diagnostics;
  logs: Logs;
  setup: Setup;
  wan_check: WanCheck;
  rules: Rules;
  wifi_scan: WifiScan;
  services: Services;
}
