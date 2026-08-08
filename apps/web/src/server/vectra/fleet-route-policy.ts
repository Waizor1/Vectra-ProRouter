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
  // slot with a scored fallback shape (WorldProxy: direct Poland 145 vs
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

// Provider nodes the operator has pulled out of rotation. Every slot scores
// them 0, so the policy picks its next-best candidate instead of pinning the
// fleet to a node that cannot carry traffic.
//
// Matched by HOST, never by node id: the subscription re-mints node ids on
// every refresh, so an id-based list would silently stop matching overnight.
//
// Why this exists rather than a liveness probe: evaluateFleetRoutePolicy only
// receives the PassWall config and the router identity — it has no node-health
// signal to consult, and plumbing one through every call site is a much larger
// change. A short operator-owned list is the honest version of "this node is
// known bad"; remove the entry to put the node back in rotation.
//
// pl1.nfnpx.online (2026-08-08): flapped all day — dead in the morning, alive
// at 20:00, dead at 21:30, alive again at 21:50. Each outage took Telegram,
// Instagram and Discord down for the 12 routers pinned to it, because the
// WorldProxy tie-break below (`!extreme` => +5) scores pl1 at 145 against
// pl2's 140 and therefore always prefers it. Manual rebinds did not survive:
// the controller re-applies the panel directive on the next check-in.
const quarantinedNodeHosts = new Set(["pl1.nfnpx.online"]);

export const canonicalFleetRoutePolicy = {
  version: FLEET_ROUTE_POLICY_VERSION,
  exceptions: [...exceptionIdentityValues],
  quarantinedNodeHosts: [...quarantinedNodeHosts],
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

function semanticScore(slot: FleetRoutePolicySlotId, node: PasswallNode) {
  if (!node.enabled || node.protocol === "shunt") {
    return 0;
  }

  const label = normalizeText(node.label);
  const address = normalizeHost(node.address);

  // Quarantined hosts are out of rotation for every slot, not just the one
  // that exposed the problem: a node that cannot carry Telegram is not a
  // better YouTube or Tiktok target either.
  if (quarantinedNodeHosts.has(address)) {
    return 0;
  }

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
      const poland = includesAny(label, ["польш", "poland", "🇵🇱"]);
      if (!poland) {
        return 0;
      }
      let score = 60;
      if (!ruEntry && node.port === 443) {
        // Canonical shape: direct foreign Poland exit on :443.
        score += 80;
        // Deterministic tie-break. Subscriptions carry two direct Poland :443
        // nodes ("🇵🇱 ⚡️Польша YouTube 🚫Ad🚫" and "⚡Extreme Польша 🇵🇱");
        // without this they score equal and the winner depends on node order,
        // which the subscription re-mints on every refresh.
        if (!includesAny(label, ["extreme"])) score += 5;
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

function findBestTarget(config: PasswallDesiredConfig, slot: PolicySlot) {
  let best: { node: PasswallNode; score: number } | null = null;
  for (const node of config.nodes) {
    const score = semanticScore(slot.id, node);
    if (score > (best?.score ?? 0)) {
      best = { node, score };
    }
  }
  return best && best.score >= 100 ? best.node : null;
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
    const preferredTarget = findBestTarget(config, slot);
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
    const target = findBestTarget(next, slot);
    if (!target) {
      continue;
    }

    const basicIndex = findRuleIndex(next.basicSettings.shuntRules, slot);
    if (basicIndex < 0) {
      continue;
    }

    const rule = next.basicSettings.shuntRules[basicIndex]!;
    const previousBindingId = readRuleBindingId(next, rule, slot);
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
