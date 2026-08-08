import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

export const FLEET_ROUTE_POLICY_VERSION = "2026-08-03-v4" as const;

export type FleetRoutePolicySlotId =
  | "WorldProxy"
  | "YouTube"
  | "Special"
  | "Tiktok"
  | "DiscordVoiceUdp";

export type FleetRoutePolicyStatus =
  | "compliant"
  | "violation"
  | "exempt"
  | "unknown";

export type FleetRoutePolicyRouterIdentity = {
  id?: string | null;
  name?: string | null;
  displayName?: string | null;
  hostname?: string | null;
  deviceIdentifier?: string | null;
  // Operator-controlled exemption, stored per router in the database. This is
  // the supported way to add or retire an exemption: it takes effect on the
  // router's next check-in with no code change, no controller rebuild and no
  // fleet rollout. `true`/`false` both override the seed list below, so an
  // operator can also un-exempt a router the seed list still names.
  routePolicyExempt?: boolean | null;
  routePolicyExemptReason?: string | null;
};

type PasswallNode = PasswallDesiredConfig["nodes"][number];
type PasswallShuntRule =
  PasswallDesiredConfig["basicSettings"]["shuntRules"][number];

type PolicySlot = {
  id: FleetRoutePolicySlotId;
  label: string;
  expected: string;
  requiredRuleExtras?: Record<string, string>;
  requiredNodeExtras?: Record<string, string>;
  // When set, "compliant" means the slot is bound to the single best-scoring
  // candidate, not merely to one that clears the 100-point bar. Without it a
  // slot with a scored fallback shape (WorldProxy: direct Poland 140 vs
  // RU-entry 120) would report compliant while parked on the fallback, and
  // buildFleetRoutePolicyDirective — which echoes matchedSlots — would then pin
  // the router to that fallback forever, overriding the controller's own
  // scorer. That is exactly how the 2026-08-02 Telegram/Netflix outage would
  // have survived the fix.
  strictPreferred?: boolean;
};

export type FleetRoutePolicyMismatch = {
  slot: FleetRoutePolicySlotId;
  expected: string;
  actual: string;
  reason:
    | "missing_rule"
    | "missing_binding"
    | "missing_node"
    | "wrong_target"
    | "missing_target_candidate"
    | "rule_extra_mismatch"
    | "node_extra_mismatch";
  actualNodeId?: string | null;
  actualFingerprint?: string | null;
  expectedNodeId?: string | null;
  expectedFingerprint?: string | null;
  detail?: string;
};

export type FleetRoutePolicySlotMatch = {
  slot: FleetRoutePolicySlotId;
  targetNodeId: string;
  targetFingerprint: string;
};

export type FleetRoutePolicyCompliance = {
  policyVersion: typeof FLEET_ROUTE_POLICY_VERSION;
  status: FleetRoutePolicyStatus;
  checked: boolean;
  exempt: boolean;
  exceptionReason: string | null;
  canNormalize: boolean;
  matchedSlots: FleetRoutePolicySlotMatch[];
  mismatches: FleetRoutePolicyMismatch[];
  summary: string;
};

export type FleetRoutePolicyNormalizationChange = {
  slot: FleetRoutePolicySlotId;
  previousNodeId: string | null;
  nextNodeId: string | null;
  previousFingerprint: string | null;
  nextFingerprint: string | null;
  ruleExtrasChanged: string[];
  nodeExtrasChanged: string[];
};

export type FleetRoutePolicyNormalizationResult = {
  policyVersion: typeof FLEET_ROUTE_POLICY_VERSION;
  changed: boolean;
  config: PasswallDesiredConfig;
  before: FleetRoutePolicyCompliance;
  after: FleetRoutePolicyCompliance;
  changes: FleetRoutePolicyNormalizationChange[];
};

// Routers excluded from fleet package normalization. Keep aligned with
// fleetRoutePolicyExceptionValues in
// router/vectra-controller-agent/internal/passwall/fleet_policy.go — the panel
// and the on-router self-heal must agree, or one will keep undoing the other
// every check-in.
//
// - hh: operator-designated no-touch router.
//
// vagrandrouter was listed here from 2026-07-29 because its ISP filtered the
// non-standard high ports (50051-50061) that every RU-entry gRPC node uses, so
// the canonical targets were unreachable from that line while port 443 worked
// fine. Reachability is not part of the scorer, so normalization kept rebinding
// its slots to a dead RU-entry node once a minute. That filtering was confirmed
// gone on 2026-08-05 and the router now runs its Special slot on
// ru11.nfnpx.online:50055, a high port, so the exemption no longer has a cause
// and was removed. Per-router exemptions that are not permanent operator policy
// belong in routers.routePolicyExempt, which the panel already reads and which
// needs no controller rebuild.
const exceptionIdentityValues = new Set(["hh"]);

export const canonicalFleetRoutePolicy = {
  version: FLEET_ROUTE_POLICY_VERSION,
  exceptions: [...exceptionIdentityValues],
  slots: [
    {
      id: "WorldProxy",
      label: "WorldProxy",
      expected: "Poland direct :443 (RU-entry Poland fallback)",
      strictPreferred: true,
    },
    {
      id: "YouTube",
      label: "YouTube",
      expected: "RU Russia",
    },
    {
      id: "Special",
      label: "Special",
      expected: "Netherlands",
    },
    {
      id: "Tiktok",
      label: "Tiktok",
      expected: "Belarus",
    },
    {
      id: "DiscordVoiceUdp",
      label: "DiscordVoiceUdp",
      expected: "same node as WorldProxy + UDP/mux/xudp tuning",
      strictPreferred: true,
      requiredRuleExtras: {
        network: "udp",
        port: "19294-19344,50000-50100",
      },
      requiredNodeExtras: {
        mux: "1",
        mux_concurrency: "-1",
        xudp_concurrency: "16",
      },
    },
  ] satisfies PolicySlot[],
} as const;

function cloneConfig(config: PasswallDesiredConfig): PasswallDesiredConfig {
  return passwallDesiredConfigSchema.parse(
    JSON.parse(JSON.stringify(config)) as unknown,
  );
}

function normalizeText(value: string | null | undefined) {
  return (value ?? "")
    .toLowerCase()
    .replace(/[ё]/g, "е")
    .replace(/[_|()[\]{}:;,.]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function normalizeHost(value: string | null | undefined) {
  return (value ?? "").toLowerCase().replace(/[ё]/g, "е").trim();
}

function normalizeIdentity(value: string | null | undefined) {
  return normalizeText(value).replace(/[^a-zа-я0-9-]+/g, "");
}

function identityValues(identity?: FleetRoutePolicyRouterIdentity | null) {
  if (!identity) {
    return [];
  }
  return [
    identity.id,
    identity.name,
    identity.displayName,
    identity.hostname,
    identity.deviceIdentifier,
  ]
    .map(normalizeIdentity)
    .filter((value) => value.length > 0);
}

// Build the identity every policy call site must pass.
//
// Why a shared builder rather than an object literal per call site: the panel
// surfaces each hand-rolled their own literal and every one of them silently
// dropped routePolicyExempt. Only the check-in path in router-control.ts
// carried it, so the CONTROLLER honoured an operator's exemption while the
// PANEL did not — kirill-msk read `status: "violation"`, `canNormalize: true`,
// and one press of the normalize button would have rebound the slots away from
// the hand-tuned node that took his handshake failures from 78/150 to 0/150.
// The flag protected him from the automation but not from the operator.
//
// Fields are optional so partially-populated callers (onboarding fixtures) keep
// working; what matters is that the exemption travels by construction.
export function buildFleetRoutePolicyIdentity(
  row: {
    id?: string | null;
    displayName?: string | null;
    hostname?: string | null;
    deviceIdentifier?: string | null;
    routePolicyExempt?: boolean | null;
    routePolicyExemptReason?: string | null;
  },
  overrides?: { name?: string | null; snapshotHostname?: string | null },
): FleetRoutePolicyRouterIdentity {
  return {
    id: row.id ?? undefined,
    name:
      overrides?.name ??
      row.displayName ??
      overrides?.snapshotHostname ??
      row.hostname ??
      row.deviceIdentifier,
    displayName: row.displayName,
    hostname: overrides?.snapshotHostname ?? row.hostname,
    deviceIdentifier: row.deviceIdentifier,
    routePolicyExempt: row.routePolicyExempt,
    routePolicyExemptReason: row.routePolicyExemptReason,
  };
}

export function getFleetRoutePolicyExceptionReason(
  identity?: FleetRoutePolicyRouterIdentity | null,
) {
  // The per-router database flag wins over the seed list in both directions:
  // it can exempt a router the list never named, and it can un-exempt one the
  // list still carries. Only fall through to the seed list when the flag is
  // unset (null/undefined), which is the state of every router until an
  // operator touches it.
  const override = identity?.routePolicyExempt;
  if (typeof override === "boolean") {
    if (!override) {
      return null;
    }
    return (
      identity?.routePolicyExemptReason?.trim() ??
      "router is exempted from fleet package normalization by operator"
    );
  }

  const values = identityValues(identity);
  const matched = values.find((value) => exceptionIdentityValues.has(value));
  return matched
    ? `router ${matched} is explicitly excluded from fleet package normalization`
    : null;
}

function includesAny(value: string, needles: string[]) {
  return needles.some((needle) => value.includes(needle));
}

function hostLooksLikeRuEntry(host: string) {
  return (
    /^ru\d*\./.test(host) ||
    host.includes("ru-entry") ||
    host.includes("ru entry")
  );
}

// The provider's exit hosts are named by country: pl1/pl2 are the Poland
// exits, exactly like ru* marks an RU entry above.
function hostLooksLikePolandExit(host: string) {
  return /^pl\d*\./.test(host);
}

function semanticScore(slot: FleetRoutePolicySlotId, node: PasswallNode) {
  if (!node.enabled || node.protocol === "shunt") {
    return 0;
  }

  const label = normalizeText(node.label);
  const address = normalizeHost(node.address);
  const transport = normalizeText(node.transport);
  const ruEntry = hostLooksLikeRuEntry(address) || label.includes("🇷🇺");
  const isGrpc = transport === "grpc";

  switch (slot) {
    case "WorldProxy": {
      // History: RU-entry Germany (ru*:50052) -> RU-entry Poland (ru*:50053)
      // on 2026-07-02 because the shared German exit was overloaded.
      //
      // Moved again on 2026-08-02 to the DIRECT Poland exit (pl*:443). The
      // provider blackholed a subset of prefixes — Telegram DCs and the
      // Netflix OCA CDN — on part of its RU-entry fleet (ru3/ru4/ru5:50053
      // dead, ru7-ru12 fine), which killed WorldProxy for 12 of 25 routers.
      // Measured on one router at one moment: pl2:443 reached
      // web.telegram.org 200 and pulled 200 KB of OCA video at ~583 KB/s while
      // ru3:50053 returned 000 for both, with the SAME egress IP. The RU-entry
      // hop, not the exit, was the fault domain.
      //
      // RU-entry Poland is kept as a scored fallback (120 < 140) so a router
      // whose subscription carries no direct pl*:443 node is not stranded with
      // an unbound slot.
      //
      // DiscordVoiceUdp deliberately resolves to this same node — see that
      // slot's case below for why splitting them broke Discord voice. Keep
      // aligned with the controller scorer in
      // router/vectra-controller-agent/internal/passwall/fleet_policy.go.
      // Match the exit by HOST as well as by label. The provider re-maps its
      // human labels per router: on artem-lutfulin "⚡Extreme Польша 🇵🇱" is
      // pl2, on AlekseyHorev the same label is pl1 while pl2 arrives as
      // "⚡Extreme Авто EU 🇪🇺" — no Poland marker at all. Label-only matching
      // therefore made a perfectly good pl2 node invisible and dropped those
      // routers to the RU-entry fallback tier; measured 2026-08-08 on 2 of the
      // 26 routers that carry a pl2 host. The host is the routing fact, the
      // label is provider ad copy.
      const poland =
        includesAny(label, ["польш", "poland", "🇵🇱"]) ||
        (!ruEntry && hostLooksLikePolandExit(address));
      if (!poland) {
        return 0;
      }
      let score = 60;
      if (!ruEntry && node.port === 443) {
        // Canonical shape: direct foreign Poland exit on :443.
        //
        // Every such exit scores identically ON PURPOSE. This used to carry a
        // label tie-break (+5 when the label lacked "extreme") to make the
        // winner independent of node order, which the subscription re-mints on
        // every refresh. That tie-break also ranked pl1 at 145 against pl2's
        // 140 fleet-wide, so every router that could see pl1 took pl1 — the
        // concentration findBestTarget now exists to break. Determinism is
        // handled there instead, by hashing the router identity over the
        // sorted host list, which is both order- and refresh-independent.
        score += 80;
        return score;
      }
      if (ruEntry) {
        score += 40;
        if (node.port === 50053) score += 15;
        if (isGrpc) score += 5;
        return score;
      }
      return 0;
    }
    case "YouTube": {
      // Subscription labels are an entry/exit flag pair: "🇷🇺🇩🇪 Германия
      // YouTube" is RU-entry / DE-exit, "🇷🇺🇦🇪 ОАЭ" is RU-entry / UAE-exit. The
      // leading 🇷🇺 is the ENTRY marker and must NOT, on its own, qualify a node
      // for this slot — that is how the dead generic "🇷🇺🇦🇪 ОАЭ" node (port
      // 50061, passes a trivial google-204 healthcheck but fails real
      // youtube.com) used to win YouTube while a working "...Германия YouTube"
      // node sat unused. Require an explicit YouTube purpose or a genuine Russia
      // destination (the fleet provides no pure Russia-exit node, so
      // YouTube-purposed RU-entry nodes are the real targets). Keep aligned with
      // the controller scorer in
      // router/vectra-controller-agent/internal/passwall/fleet_policy.go.
      const youTubePurposed = includesAny(label, ["youtube"]);
      const ruRussiaPort = hostLooksLikeRuEntry(address) && node.port === 50051;
      const russiaExit =
        includesAny(label, ["росси", "russia"]) || ruRussiaPort;
      if (!youTubePurposed && !russiaExit) {
        return 0;
      }
      let score = 60;
      if (ruEntry) score += 25;
      if (node.port === 50051) score += 35;
      if (isGrpc) score += 20;
      if (youTubePurposed) score += 30;
      return score;
    }
    case "Special": {
      const nl = includesAny(label, [
        "нидерланд",
        "netherlands",
        "holland",
        "🇳🇱",
      ]);
      const nlHost = /^nl\d*\./.test(address);
      const ruNlPort = hostLooksLikeRuEntry(address) && node.port === 50055;
      if (!nl && !nlHost && !ruNlPort) {
        return 0;
      }
      let score = 60;
      // Prefer the RU-entry Netherlands subscription slot when it is present:
      // plain NL nodes have repeatedly passed semantic matching while failing
      // live router probes, whereas the RU-entry 50055 path is the proven
      // fleet fallback for Special.
      if (ruEntry) score += 20;
      if (isGrpc) score += 15;
      if (ruNlPort) score += 65;
      if (nlHost) score += 25;
      if (node.port === 443) score += 15;
      return score;
    }
    case "Tiktok": {
      const by = includesAny(label, ["беларус", "belarus", "🇧🇾"]);
      const byHost = /^by\d*\./.test(address);
      if (!by && !byHost) {
        return 0;
      }
      let score = 70;
      if (byHost) score += 25;
      if (node.port === 443) score += 10;
      return score;
    }
    case "DiscordVoiceUdp": {
      // This slot MUST land on the same node as WorldProxy, so it scores with
      // the WorldProxy scorer verbatim.
      //
      // Why: the generated Xray routing chain puts the WorldProxy rule ABOVE
      // this one, and that rule already carries the Discord prefixes
      // (66.22.192.0/18, 66.22.176.0/24, 66.22.188.0/22) with
      // network=tcp,udp. Xray takes the first matching rule, so every Discord
      // voice packet leaves through the WorldProxy node — this rule (network
      // =udp, port=19294-19344,50000-50100, no domain/ip of its own) never
      // sees them. The slot's only real effect is the mux/xudp tuning it
      // stamps onto whichever node it resolves to.
      //
      // That held silently until 2026-08-02: WorldProxy and this slot both
      // pointed at ru*:50053, so the tuning landed where the traffic actually
      // went. Moving WorldProxy to the direct pl*:443 exit left this slot
      // pinned to RU-entry, so the mux/xudp settings decorated a node no
      // Discord packet reached, and the traffic ran over a node with no XUDP.
      // Voice died fleet-wide the next day (operator report 2026-08-03).
      //
      // Delegating keeps the two in lockstep through any future canon move,
      // including onto the RU-entry fallback. `mux_concurrency: -1` disables
      // TCP mux, so this costs the WorldProxy TCP path nothing; XTLS Vision
      // rejects UDP/443 on its own, so QUIC behaviour is unchanged too.
      return semanticScore("WorldProxy", node);
    }
  }
}

function slotRules(config: PasswallDesiredConfig) {
  return config.basicSettings.shuntRules;
}

function findRule(
  config: PasswallDesiredConfig,
  slot: PolicySlot,
): PasswallShuntRule | null {
  const normalizedId = normalizeText(slot.id);
  const normalizedLabel = normalizeText(slot.label);
  return (
    slotRules(config).find(
      (rule) =>
        normalizeText(rule.id) === normalizedId ||
        normalizeText(rule.label) === normalizedLabel,
    ) ?? null
  );
}

function findRuleIndex(rules: PasswallShuntRule[], slot: PolicySlot): number {
  const normalizedId = normalizeText(slot.id);
  const normalizedLabel = normalizeText(slot.label);
  return rules.findIndex(
    (rule) =>
      normalizeText(rule.id) === normalizedId ||
      normalizeText(rule.label) === normalizedLabel,
  );
}

function findNodeById(config: PasswallDesiredConfig, nodeId?: string | null) {
  if (!nodeId) {
    return null;
  }
  return config.nodes.find((node) => node.id === nodeId) ?? null;
}

function readShuntBinding(config: PasswallDesiredConfig, slot: PolicySlot) {
  const keys = [slot.id, slot.label].filter(Boolean);
  for (const shunt of config.nodes) {
    if (shunt.protocol !== "shunt") {
      continue;
    }
    for (const key of keys) {
      const value = shunt.extras[key];
      if (typeof value === "string" && value.trim().length > 0) {
        return value.trim();
      }
    }
  }
  return null;
}

// Stable 32-bit FNV-1a. Used only to spread routers over equally-good exits,
// never for anything security-sensitive.
function stableHash(value: string) {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash >>> 0;
}

// The spread key must be stable for the lifetime of the router, or a router
// would hop between exits on every refresh. deviceIdentifier is the most
// durable of the identity fields (it survives an agent token loss and a
// rename); id and hostname are fallbacks for callers that pass a partial
// identity.
function spreadKey(identity?: FleetRoutePolicyRouterIdentity | null) {
  const raw =
    identity?.deviceIdentifier ??
    identity?.id ??
    identity?.hostname ??
    identity?.name ??
    "";
  return normalizeIdentity(raw);
}

function findBestTarget(
  config: PasswallDesiredConfig,
  slot: PolicySlot,
  identity?: FleetRoutePolicyRouterIdentity | null,
  currentNodeId?: string | null,
) {
  let bestScore = 0;
  const scored: { node: PasswallNode; score: number }[] = [];
  for (const node of config.nodes) {
    const score = semanticScore(slot.id, node);
    if (score <= 0) {
      continue;
    }
    scored.push({ node, score });
    if (score > bestScore) {
      bestScore = score;
    }
  }
  if (bestScore < 100) {
    return null;
  }

  // Spread the fleet across every exit that scores equally well instead of
  // handing all 31 routers the single highest-scoring host.
  //
  // Why: a deterministic single winner is how the German exit ended up
  // carrying 22 of 24 routers on 2026-07-31 and took Instagram down for all of
  // them at once. The same shape reappeared on 2026-08-08 — 14 routers pinned
  // to pl1 and 9 to pl2 — and a blanket "prefer pl2" would merely have moved
  // the concentration, not removed it. Both Poland exits measured healthy from
  // three client routers that day (5/5 TCP connects at 40-50ms, Telegram 200,
  // YouTube 204), so the fleet has no reason to crowd onto either one.
  //
  // Grouping is by HOST, not by node: a subscription commonly carries the same
  // host twice under two labels, and spreading between those two would split
  // the label while leaving every router on one exit — no blast-radius gain.
  //
  // The pick is a pure function of (router identity, sorted host list), so it
  // is stable across check-ins and identical for WorldProxy and
  // DiscordVoiceUdp — that pair MUST resolve to the same node, and does,
  // because DiscordVoiceUdp scores with the WorldProxy scorer verbatim.
  const byHost = new Map<string, PasswallNode[]>();
  for (const candidate of scored) {
    if (candidate.score !== bestScore) {
      continue;
    }
    const host = normalizeHost(candidate.node.address);
    const nodes = byHost.get(host);
    if (nodes) {
      nodes.push(candidate.node);
    } else {
      byHost.set(host, [candidate.node]);
    }
  }

  const hosts = [...byHost.keys()].sort();
  const host = hosts[stableHash(spreadKey(identity)) % hosts.length]!;
  const nodes = byHost.get(host)!;
  if (nodes.length === 1) {
    return nodes[0]!;
  }

  // Already parked on an equally-good node of the chosen host? Stay there.
  //
  // A subscription routinely carries one host twice under two labels
  // ("🇵🇱 ⚡️Польша YouTube 🚫Ad🚫" and "⚡Extreme Польша 🇵🇱" are both pl2), and
  // without this the policy demanded whichever label sorted first — a rebind
  // to the same host, same port, same protocol that changes no traffic
  // whatsoever. It also never converges in practice: the node ids are re-minted
  // on every subscription refresh, so the router is flagged non-compliant again
  // the next night. Observed 2026-08-08 on 1111111111 and AndreyVK, which sat
  // at "violation" while already on the correct exit.
  const keep = currentNodeId
    ? nodes.find((node) => node.id === currentNodeId)
    : undefined;
  if (keep) {
    return keep;
  }

  // Otherwise order by label, so a router arriving fresh gets a deterministic
  // node rather than one that depends on subscription ordering.
  return [...nodes].sort((left, right) =>
    normalizeText(left.label).localeCompare(normalizeText(right.label)),
  )[0]!;
}

function fingerprint(node: PasswallNode | null | undefined) {
  if (!node) {
    return null;
  }
  const parts = [
    node.label,
    node.address ? `${node.address}${node.port ? `:${node.port}` : ""}` : null,
    node.transport,
    node.protocol,
  ].filter((part): part is string => Boolean(part));
  return parts.join(" | ");
}

function formatActual(
  rule: PasswallShuntRule | null,
  node: PasswallNode | null,
  bindingId?: string | null,
) {
  if (!rule) {
    return "slot is absent";
  }
  if (!bindingId) {
    return "slot has no outbound binding";
  }
  return fingerprint(node) ?? `node ${bindingId} is absent`;
}

function extraValueToString(value: unknown) {
  if (value === null || value === undefined) {
    return "";
  }
  if (
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean"
  ) {
    return String(value);
  }
  if (Array.isArray(value)) {
    return value.join("\n");
  }
  return "";
}

function extraMismatch(
  extras: Record<string, unknown>,
  expected: Record<string, string> | undefined,
) {
  if (!expected) {
    return [];
  }
  return Object.entries(expected).flatMap(([key, value]) =>
    extraValueToString(extras[key]) === value ? [] : [key],
  );
}

function readRuleBindingId(
  config: PasswallDesiredConfig,
  rule: PasswallShuntRule,
  slot: PolicySlot,
) {
  const shuntBindingId = readShuntBinding(config, slot);
  if (shuntBindingId) {
    return shuntBindingId;
  }
  const outboundNodeId = rule.outboundNodeId?.trim();
  return outboundNodeId && outboundNodeId.length > 0 ? outboundNodeId : null;
}

function summarizeCompliance(
  compliance: Omit<FleetRoutePolicyCompliance, "summary">,
) {
  if (compliance.status === "exempt") {
    return "Router is explicitly excluded from the fleet route policy.";
  }
  if (compliance.status === "unknown") {
    return "No full live PassWall import is available for fleet policy matching.";
  }
  if (compliance.mismatches.length === 0) {
    return "Route bindings match the canonical fleet server package.";
  }
  return `${compliance.mismatches.length} fleet route policy mismatch(es): ${compliance.mismatches
    .map((mismatch) => mismatch.slot)
    .join(", ")}.`;
}

export function evaluateFleetRoutePolicy(
  config: PasswallDesiredConfig | null | undefined,
  identity?: FleetRoutePolicyRouterIdentity | null,
): FleetRoutePolicyCompliance {
  const exceptionReason = getFleetRoutePolicyExceptionReason(identity);
  if (exceptionReason) {
    const base = {
      policyVersion: FLEET_ROUTE_POLICY_VERSION,
      status: "exempt" as const,
      checked: false,
      exempt: true,
      exceptionReason,
      canNormalize: false,
      matchedSlots: [],
      mismatches: [],
    };
    return { ...base, summary: summarizeCompliance(base) };
  }

  if (!config) {
    const base = {
      policyVersion: FLEET_ROUTE_POLICY_VERSION,
      status: "unknown" as const,
      checked: false,
      exempt: false,
      exceptionReason: null,
      canNormalize: false,
      matchedSlots: [],
      mismatches: [],
    };
    return { ...base, summary: summarizeCompliance(base) };
  }

  const mismatches: FleetRoutePolicyMismatch[] = [];
  const matchedSlots: FleetRoutePolicySlotMatch[] = [];

  for (const slot of canonicalFleetRoutePolicy.slots) {
    const rule = findRule(config, slot);
    const preferredTarget = findBestTarget(
      config,
      slot,
      identity,
      rule ? readRuleBindingId(config, rule, slot) : null,
    );
    if (!rule) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: "missing ShuntRule",
        reason: "missing_rule",
        expectedNodeId: preferredTarget?.id ?? null,
        expectedFingerprint: fingerprint(preferredTarget),
      });
      continue;
    }

    const bindingId = readRuleBindingId(config, rule, slot);
    if (!bindingId) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: "missing outbound node binding",
        reason: "missing_binding",
        expectedNodeId: preferredTarget?.id ?? null,
        expectedFingerprint: fingerprint(preferredTarget),
      });
      continue;
    }

    const actualNode = findNodeById(config, bindingId);
    if (!actualNode) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: `missing node ${bindingId}`,
        reason: "missing_node",
        actualNodeId: bindingId,
        expectedNodeId: preferredTarget?.id ?? null,
        expectedFingerprint: fingerprint(preferredTarget),
      });
      continue;
    }

    const strictPreferred =
      "strictPreferred" in slot && slot.strictPreferred === true;
    const actualMatches =
      strictPreferred && preferredTarget
        ? actualNode.id === preferredTarget.id
        : semanticScore(slot.id, actualNode) >= 100;
    if (!actualMatches) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: formatActual(rule, actualNode, bindingId),
        reason: preferredTarget ? "wrong_target" : "missing_target_candidate",
        actualNodeId: actualNode.id,
        actualFingerprint: fingerprint(actualNode),
        expectedNodeId: preferredTarget?.id ?? null,
        expectedFingerprint: fingerprint(preferredTarget),
      });
      continue;
    }

    const ruleExtraDiff = extraMismatch(rule.extras, slot.requiredRuleExtras);
    if (ruleExtraDiff.length > 0) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: formatActual(rule, actualNode, bindingId),
        reason: "rule_extra_mismatch",
        actualNodeId: actualNode.id,
        actualFingerprint: fingerprint(actualNode),
        expectedNodeId: actualNode.id,
        expectedFingerprint: fingerprint(actualNode),
        detail: `rule extras differ: ${ruleExtraDiff.join(", ")}`,
      });
      continue;
    }

    const nodeExtraDiff = extraMismatch(
      actualNode.extras,
      slot.requiredNodeExtras,
    );
    if (nodeExtraDiff.length > 0) {
      mismatches.push({
        slot: slot.id,
        expected: slot.expected,
        actual: formatActual(rule, actualNode, bindingId),
        reason: "node_extra_mismatch",
        actualNodeId: actualNode.id,
        actualFingerprint: fingerprint(actualNode),
        expectedNodeId: actualNode.id,
        expectedFingerprint: fingerprint(actualNode),
        detail: `node extras differ: ${nodeExtraDiff.join(", ")}`,
      });
      continue;
    }

    matchedSlots.push({
      slot: slot.id,
      targetNodeId: actualNode.id,
      targetFingerprint: fingerprint(actualNode) ?? actualNode.id,
    });
  }

  const canNormalize = mismatches.every((mismatch) => {
    if (
      mismatch.reason === "wrong_target" ||
      mismatch.reason === "missing_binding" ||
      mismatch.reason === "missing_node" ||
      mismatch.reason === "missing_rule" ||
      mismatch.reason === "missing_target_candidate"
    ) {
      return Boolean(mismatch.expectedNodeId);
    }
    return true;
  });
  const base = {
    policyVersion: FLEET_ROUTE_POLICY_VERSION,
    status:
      mismatches.length > 0 ? ("violation" as const) : ("compliant" as const),
    checked: true,
    exempt: false,
    exceptionReason: null,
    canNormalize: mismatches.length > 0 && canNormalize,
    matchedSlots,
    mismatches,
  };
  return { ...base, summary: summarizeCompliance(base) };
}

function syncRuleManageShuntRules(config: PasswallDesiredConfig) {
  config.ruleManage.shuntRules = JSON.parse(
    JSON.stringify(config.basicSettings.shuntRules),
  ) as PasswallDesiredConfig["ruleManage"]["shuntRules"];
}

function syncShuntNodeBinding(
  config: PasswallDesiredConfig,
  slot: PolicySlot,
  targetNodeId: string | null,
) {
  for (const node of config.nodes) {
    if (node.protocol !== "shunt") {
      continue;
    }
    if (targetNodeId) {
      node.extras[slot.id] = targetNodeId;
    } else {
      delete node.extras[slot.id];
    }
  }
}

export function normalizeFleetRoutePolicy(
  config: PasswallDesiredConfig,
  identity?: FleetRoutePolicyRouterIdentity | null,
): FleetRoutePolicyNormalizationResult {
  const before = evaluateFleetRoutePolicy(config, identity);
  const next = cloneConfig(config);
  const changes: FleetRoutePolicyNormalizationChange[] = [];

  if (before.status === "exempt" || before.status === "unknown") {
    return {
      policyVersion: FLEET_ROUTE_POLICY_VERSION,
      changed: false,
      config: next,
      before,
      after: before,
      changes,
    };
  }

  for (const slot of canonicalFleetRoutePolicy.slots) {
    const basicIndex = findRuleIndex(next.basicSettings.shuntRules, slot);
    if (basicIndex < 0) {
      continue;
    }

    const rule = next.basicSettings.shuntRules[basicIndex]!;
    const previousBindingId = readRuleBindingId(next, rule, slot);
    const target = findBestTarget(next, slot, identity, previousBindingId);
    if (!target) {
      continue;
    }
    const previousNode = findNodeById(next, previousBindingId);
    const previousNodeId = previousNode?.id ?? previousBindingId ?? null;
    const previousFingerprint = fingerprint(previousNode);
    const ruleExtrasChanged: string[] = [];
    const nodeExtrasChanged: string[] = [];

    if (rule.outboundNodeId !== target.id) {
      rule.outboundNodeId = target.id;
    }
    for (const [key, value] of Object.entries(slot.requiredRuleExtras ?? {})) {
      if (String(rule.extras[key] ?? "") !== value) {
        rule.extras[key] = value;
        ruleExtrasChanged.push(key);
      }
    }
    for (const [key, value] of Object.entries(slot.requiredNodeExtras ?? {})) {
      if (String(target.extras[key] ?? "") !== value) {
        target.extras[key] = value;
        nodeExtrasChanged.push(key);
      }
    }

    syncShuntNodeBinding(next, slot, target.id);

    if (
      previousNodeId !== target.id ||
      ruleExtrasChanged.length > 0 ||
      nodeExtrasChanged.length > 0
    ) {
      changes.push({
        slot: slot.id,
        previousNodeId,
        nextNodeId: target.id,
        previousFingerprint,
        nextFingerprint: fingerprint(target),
        ruleExtrasChanged,
        nodeExtrasChanged,
      });
    }
  }

  syncRuleManageShuntRules(next);
  const parsed = passwallDesiredConfigSchema.parse(next);
  const after = evaluateFleetRoutePolicy(parsed, identity);

  return {
    policyVersion: FLEET_ROUTE_POLICY_VERSION,
    changed: changes.length > 0,
    config: parsed,
    before,
    after,
    changes,
  };
}

/**
 * Builds the route-policy directive handed to the controller on check-in.
 *
 * This is the mechanism that keeps route policy a panel concern. The controller
 * binds exactly the node IDs named here rather than re-deriving them from its
 * own compiled-in scorer, so retargeting a slot or exempting a router ships as a
 * panel deploy instead of a controller rebuild plus a fleet-wide rollout.
 *
 * Returns null when there is nothing useful to say — no live config yet, or no
 * slot resolved. Null (and an omitted field on the wire) means "panel has no
 * instruction"; the controller then falls back to its built-in scorer, which
 * remains the safety net for routers that cannot reach the panel.
 *
 * An exempt router still gets a directive, with `exempt: true` and no slots.
 * That is deliberate: the exemption itself is the instruction, and sending it
 * is how an operator-set exemption reaches a controller whose compiled-in list
 * does not contain that router.
 */
export function buildFleetRoutePolicyDirective(
  config: PasswallDesiredConfig | null | undefined,
  identity?: FleetRoutePolicyRouterIdentity | null,
): {
  version: string;
  exempt: boolean;
  reason?: string;
  slots: {
    id: string;
    nodeId: string;
    fingerprint?: string;
    ruleExtras?: Record<string, string>;
    nodeExtras?: Record<string, string>;
  }[];
} | null {
  const exceptionReason = getFleetRoutePolicyExceptionReason(identity);
  if (exceptionReason) {
    return {
      version: FLEET_ROUTE_POLICY_VERSION,
      exempt: true,
      reason: exceptionReason,
      slots: [],
    };
  }

  const compliance = evaluateFleetRoutePolicy(config, identity);
  if (!compliance.checked || compliance.matchedSlots.length === 0) {
    return null;
  }

  const extrasBySlot = new Map(
    canonicalFleetRoutePolicy.slots.map((slot) => [slot.id, slot]),
  );

  return {
    version: FLEET_ROUTE_POLICY_VERSION,
    exempt: false,
    slots: compliance.matchedSlots.map((match) => {
      const canonical = extrasBySlot.get(match.slot);
      return {
        id: match.slot,
        nodeId: match.targetNodeId,
        fingerprint: match.targetFingerprint,
        ...(canonical && "requiredRuleExtras" in canonical
          ? { ruleExtras: { ...canonical.requiredRuleExtras } }
          : {}),
        ...(canonical && "requiredNodeExtras" in canonical
          ? { nodeExtras: { ...canonical.requiredNodeExtras } }
          : {}),
      };
    }),
  };
}
