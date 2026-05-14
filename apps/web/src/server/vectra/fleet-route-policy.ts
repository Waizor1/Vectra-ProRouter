import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

export const FLEET_ROUTE_POLICY_VERSION = "2026-05-12-v1" as const;

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

const exceptionIdentityValues = new Set(["hh"]);

export const canonicalFleetRoutePolicy = {
  version: FLEET_ROUTE_POLICY_VERSION,
  exceptions: [...exceptionIdentityValues],
  slots: [
    {
      id: "WorldProxy",
      label: "WorldProxy",
      expected: "RU-entry Germany",
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
      expected: "RU-entry Poland + UDP/mux/xudp tuning",
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
  const transport = normalizeText(node.transport);
  const ruEntry = hostLooksLikeRuEntry(address) || label.includes("🇷🇺");
  const isGrpc = transport === "grpc";

  switch (slot) {
    case "WorldProxy": {
      const germany = includesAny(label, [
        "германи",
        "germany",
        "deutsch",
        "🇩🇪",
      ]);
      if (!germany) {
        return 0;
      }
      let score = 60;
      if (ruEntry) score += 40;
      if (node.port === 50052) score += 30;
      if (isGrpc) score += 20;
      return ruEntry ? score : 0;
    }
    case "YouTube": {
      const russia = includesAny(label, ["росси", "russia", "🇷🇺"]);
      const ruRussiaPort = hostLooksLikeRuEntry(address) && node.port === 50051;
      if (!russia && !ruRussiaPort) {
        return 0;
      }
      let score = 60;
      if (ruEntry) score += 25;
      if (node.port === 50051) score += 35;
      if (isGrpc) score += 20;
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
      const poland = includesAny(label, ["польш", "poland", "🇵🇱"]);
      if (!poland) {
        return 0;
      }
      let score = 60;
      if (ruEntry) score += 35;
      if (node.port === 50053) score += 35;
      if (isGrpc) score += 20;
      return ruEntry ? score : 0;
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

    const actualMatches = semanticScore(slot.id, actualNode) >= 100;
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
