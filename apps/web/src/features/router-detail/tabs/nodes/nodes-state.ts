import type { PasswallDesiredConfig } from "@vectra/contracts";

type Node = PasswallDesiredConfig["nodes"][number];
type Subscription = PasswallDesiredConfig["subscriptions"]["items"][number];

export type NodeFieldPatch = Partial<
  Pick<
    Node,
    | "label"
    | "protocol"
    | "enabled"
    | "group"
    | "address"
    | "port"
    | "username"
    | "password"
    | "transport"
    | "tls"
    | "tags"
  >
>;

export function applyNodeFieldPatch(
  config: PasswallDesiredConfig,
  nodeId: string,
  patch: NodeFieldPatch,
): PasswallDesiredConfig {
  return {
    ...config,
    nodes: config.nodes.map((node) =>
      node.id === nodeId ? { ...node, ...patch } : node,
    ),
  };
}

export function toggleNodeEnabled(
  config: PasswallDesiredConfig,
  nodeId: string,
  enabled: boolean,
): PasswallDesiredConfig {
  return applyNodeFieldPatch(config, nodeId, { enabled });
}

export type SubscriptionFieldPatch = Partial<
  Pick<Subscription, "remark" | "url" | "enabled" | "addMode">
>;

export function applySubscriptionFieldPatch(
  config: PasswallDesiredConfig,
  subscriptionId: string,
  patch: SubscriptionFieldPatch,
): PasswallDesiredConfig {
  return {
    ...config,
    subscriptions: {
      ...config.subscriptions,
      items: config.subscriptions.items.map((item) =>
        item.id === subscriptionId ? { ...item, ...patch } : item,
      ),
    },
  };
}

// ── Shunt node bindings ────────────────────────────────────────────────────
// A shunt node carries per-rule routing on its extras: `{ruleId}` (outbound,
// mirrored from rule.outboundNodeId), `{ruleId}_fakedns`, `{ruleId}_proxy_tag`,
// plus node-level defaults (default_node, write_ipset_direct, domainStrategy…).
// Keys verified against apply.go (renderShuntNodeBindings) and shunt_options.lua.

type ExtrasValue = Node["extras"][string];

function isShuntNode(node: Node): boolean {
  if (node.protocol === "shunt") {
    return true;
  }
  const extras = node.extras ?? {};
  return (
    "default_node" in extras ||
    "default_fakedns" in extras ||
    "default_proxy_tag" in extras
  );
}

export function findShuntNode(config: PasswallDesiredConfig): Node | null {
  const selectedId = config.basicSettings.main.selectedNodeId;
  const selectedShunt = config.nodes.find(
    (node) => node.id === selectedId && isShuntNode(node),
  );
  if (selectedShunt) {
    return selectedShunt;
  }
  return config.nodes.find(isShuntNode) ?? null;
}

function writeExtra(
  extras: Node["extras"],
  key: string,
  value: ExtrasValue | undefined,
): Node["extras"] {
  const next = { ...extras };
  if (
    value === undefined ||
    value === null ||
    value === "" ||
    (Array.isArray(value) && value.length === 0)
  ) {
    delete next[key];
  } else {
    next[key] = value;
  }
  return next;
}

export function setShuntNodeExtra(
  config: PasswallDesiredConfig,
  key: string,
  value: ExtrasValue | undefined,
): PasswallDesiredConfig {
  const shunt = findShuntNode(config);
  if (!shunt) {
    return config;
  }
  return {
    ...config,
    nodes: config.nodes.map((node) =>
      node.id === shunt.id
        ? { ...node, extras: writeExtra(node.extras, key, value) }
        : node,
    ),
  };
}
