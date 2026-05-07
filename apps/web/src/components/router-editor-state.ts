import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";

function cloneConfig<T>(config: T) {
  return JSON.parse(JSON.stringify(config)) as T;
}

function syncShuntRules(config: PasswallDesiredConfig) {
  config.ruleManage.shuntRules = cloneConfig(config.basicSettings.shuntRules);
}

type ShuntRuleExtraValue =
  PasswallDesiredConfig["basicSettings"]["shuntRules"][number]["extras"][string];
type NodeExtraValue = PasswallDesiredConfig["nodes"][number]["extras"][string];

function nextId(prefix: string) {
  return `${prefix}-${Math.random().toString(36).slice(2, 8)}`;
}

export function createDraftFixture(overrides?: Partial<PasswallDesiredConfig>) {
  return passwallDesiredConfigSchema.parse({
    schemaVersion: 1,
    basicSettings: {
      main: {},
      dns: {},
      log: {},
      maintenance: {},
      socks: [],
      shuntRules: [],
    },
    nodes: [],
    subscriptions: {
      items: [],
    },
    appUpdate: {},
    ruleManage: {
      geoipUrl: "https://example.com/geoip.dat",
      geositeUrl: "https://example.com/geosite.dat",
    },
    ...overrides,
  });
}

export function addNode(config: PasswallDesiredConfig) {
  const next = cloneConfig(config);
  next.nodes.push({
    id: nextId("node"),
    label: "Новая нода",
    protocol: "xray",
    enabled: true,
    group: "default",
    tags: [],
    extras: {},
  });
  return next;
}

export function duplicateNode(config: PasswallDesiredConfig, index: number) {
  const next = cloneConfig(config);
  const node = next.nodes[index];
  if (!node) {
    return next;
  }

  next.nodes.splice(index + 1, 0, {
    ...cloneConfig(node),
    id: `${node.id}-copy`,
    label: `${node.label} копия`,
  });
  return next;
}

export function deleteNode(config: PasswallDesiredConfig, index: number) {
  const next = cloneConfig(config);
  const [removed] = next.nodes.splice(index, 1);
  if (
    removed &&
    next.basicSettings.main.selectedNodeId &&
    next.basicSettings.main.selectedNodeId === removed.id
  ) {
    next.basicSettings.main.selectedNodeId = next.nodes[0]?.id ?? undefined;
  }
  return next;
}

function pruneNodeReferenceExtras(
  extras: Record<string, string | number | boolean | string[] | null>,
  removedNodeIds: Set<string>,
) {
  const nextExtras = { ...extras };

  for (const [key, value] of Object.entries(nextExtras)) {
    if (typeof value !== "string") {
      continue;
    }

    const targetsNodeReference =
      key === "default_node" || key === "to_node" || key.endsWith("_proxy_tag");
    if (!targetsNodeReference) {
      continue;
    }

    if (removedNodeIds.has(value)) {
      delete nextExtras[key];
    }
  }

  return nextExtras;
}

export function pruneNodes(config: PasswallDesiredConfig, nodeIds: string[]) {
  const removedNodeIds = new Set(nodeIds);
  if (removedNodeIds.size === 0) {
    return cloneConfig(config);
  }

  const next = cloneConfig(config);
  next.nodes = next.nodes
    .filter((node) => !removedNodeIds.has(node.id))
    .map((node) => ({
      ...node,
      extras: pruneNodeReferenceExtras(node.extras, removedNodeIds),
    }));

  const fallbackNodeId = next.nodes[0]?.id;

  if (
    next.basicSettings.main.selectedNodeId &&
    removedNodeIds.has(next.basicSettings.main.selectedNodeId)
  ) {
    next.basicSettings.main.selectedNodeId = fallbackNodeId;
  }

  next.basicSettings.socks = next.basicSettings.socks
    .map((entry) => {
      const nextBackupNodeIds = [
        ...new Set(
          entry.autoswitchBackupNodeIds.filter(
            (nodeId) =>
              !removedNodeIds.has(nodeId) && nodeId !== fallbackNodeId,
          ),
        ),
      ];

      if (!removedNodeIds.has(entry.nodeId)) {
        return {
          ...entry,
          autoswitchBackupNodeIds: nextBackupNodeIds,
        };
      }

      if (!fallbackNodeId) {
        return null;
      }

      return {
        ...entry,
        nodeId: fallbackNodeId,
        autoswitchBackupNodeIds: nextBackupNodeIds,
      };
    })
    .filter(
      (
        entry,
      ): entry is PasswallDesiredConfig["basicSettings"]["socks"][number] =>
        entry !== null,
    );

  next.basicSettings.shuntRules = next.basicSettings.shuntRules.map((rule) => ({
    ...rule,
    outboundNodeId:
      rule.outboundNodeId && removedNodeIds.has(rule.outboundNodeId)
        ? undefined
        : rule.outboundNodeId,
  }));
  syncShuntRules(next);

  next.subscriptions.items = next.subscriptions.items.map((item) => ({
    ...item,
    extras: pruneNodeReferenceExtras(item.extras, removedNodeIds),
  }));

  return next;
}

export function moveNodeToTop(config: PasswallDesiredConfig, index: number) {
  const next = cloneConfig(config);
  const [node] = next.nodes.splice(index, 1);
  if (!node) {
    return next;
  }

  next.nodes.unshift(node);
  return next;
}

export function selectNode(config: PasswallDesiredConfig, nodeId: string) {
  const next = cloneConfig(config);
  next.basicSettings.main.selectedNodeId = nodeId;
  return next;
}

export function addSubscription(config: PasswallDesiredConfig) {
  const next = cloneConfig(config);
  next.subscriptions.items.push({
    id: nextId("subscription"),
    remark: "Новая подписка",
    url: "https://",
    enabled: true,
    addMode: "2",
    metadata: {},
    extras: {},
  });
  return next;
}

export function deleteSubscription(
  config: PasswallDesiredConfig,
  index: number,
) {
  const next = cloneConfig(config);
  next.subscriptions.items.splice(index, 1);
  return next;
}

export function moveSubscriptionToTop(
  config: PasswallDesiredConfig,
  index: number,
) {
  const next = cloneConfig(config);
  const [subscription] = next.subscriptions.items.splice(index, 1);
  if (!subscription) {
    return next;
  }

  next.subscriptions.items.unshift(subscription);
  return next;
}

export function addShuntRule(config: PasswallDesiredConfig) {
  const next = cloneConfig(config);
  next.basicSettings.shuntRules.push({
    id: nextId("shunt"),
    label: "Новое правило",
    domainRules: [],
    ipRules: [],
    extras: {},
  });
  syncShuntRules(next);
  return next;
}

export function deleteShuntRule(config: PasswallDesiredConfig, index: number) {
  const next = cloneConfig(config);
  const [removed] = next.basicSettings.shuntRules.splice(index, 1);
  if (removed) {
    for (const node of next.nodes) {
      delete node.extras[removed.id];
      delete node.extras[`${removed.id}_fakedns`];
      delete node.extras[`${removed.id}_proxy_tag`];
    }
  }
  syncShuntRules(next);
  return next;
}

export function moveShuntRuleToTop(
  config: PasswallDesiredConfig,
  index: number,
) {
  const next = cloneConfig(config);
  const [rule] = next.basicSettings.shuntRules.splice(index, 1);
  if (!rule) {
    return next;
  }

  next.basicSettings.shuntRules.unshift(rule);
  syncShuntRules(next);
  return next;
}

export function renameShuntRule(
  config: PasswallDesiredConfig,
  ruleId: string,
  nextRuleId: string,
) {
  const next = cloneConfig(config);
  const normalized = nextRuleId.trim();
  if (!normalized) {
    return next;
  }

  const target = next.basicSettings.shuntRules.find(
    (rule) => rule.id === ruleId,
  );
  if (!target) {
    return next;
  }

  const duplicate = next.basicSettings.shuntRules.some(
    (rule) => rule.id === normalized && rule.id !== ruleId,
  );
  if (duplicate) {
    return next;
  }

  target.id = normalized;
  for (const node of next.nodes) {
    renameExtraKey(node.extras, ruleId, normalized);
    renameExtraKey(node.extras, `${ruleId}_fakedns`, `${normalized}_fakedns`);
    renameExtraKey(
      node.extras,
      `${ruleId}_proxy_tag`,
      `${normalized}_proxy_tag`,
    );
  }
  syncShuntRules(next);
  return next;
}

export function updateShuntRuleExtra(
  config: PasswallDesiredConfig,
  ruleId: string,
  key: string,
  value: ShuntRuleExtraValue | undefined,
) {
  const next = cloneConfig(config);
  const target = next.basicSettings.shuntRules.find(
    (rule) => rule.id === ruleId,
  );
  if (!target) {
    return next;
  }

  setExtra(target.extras, key, value);
  syncShuntRules(next);
  return next;
}

export function normalizeShuntRuleBindings(config: PasswallDesiredConfig) {
  const next = cloneConfig(config);
  syncShuntRules(next);

  const bindings = new Map(
    next.basicSettings.shuntRules.map((rule) => [
      rule.id,
      rule.outboundNodeId?.trim() ? rule.outboundNodeId : undefined,
    ]),
  );

  for (const node of next.nodes) {
    if (node.protocol !== "shunt") {
      continue;
    }

    for (const [ruleId, outboundNodeId] of bindings) {
      if (outboundNodeId) {
        node.extras[ruleId] = outboundNodeId;
      } else {
        delete node.extras[ruleId];
      }
    }
  }

  return next;
}

function renameExtraKey(
  extras: Record<string, NodeExtraValue>,
  previousKey: string,
  nextKey: string,
) {
  if (!(previousKey in extras) || previousKey === nextKey) {
    return;
  }

  const current = extras[previousKey];
  if (current === undefined) {
    return;
  }

  extras[nextKey] = current;
  delete extras[previousKey];
}

function setExtra(
  extras: Record<string, ShuntRuleExtraValue>,
  key: string,
  value: ShuntRuleExtraValue | undefined,
) {
  if (value === undefined || value === null) {
    delete extras[key];
    return;
  }

  if (Array.isArray(value) && value.length === 0) {
    delete extras[key];
    return;
  }

  extras[key] = value;
}
