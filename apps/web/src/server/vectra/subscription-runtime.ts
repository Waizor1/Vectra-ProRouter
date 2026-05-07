import { createHash } from "node:crypto";

import type {
  PasswallDesiredConfig,
  SubscriptionPreviewEntry,
} from "@vectra/contracts";

import { stableStringify } from "./secrets";

type SubscriptionItem = PasswallDesiredConfig["subscriptions"]["items"][number];
type NodeItem = PasswallDesiredConfig["nodes"][number];

type PayloadFingerprint = {
  fingerprint: string;
};

export type SubscriptionPreviewState =
  | "fresh"
  | "pending"
  | "stale"
  | "failed"
  | "missing"
  | "disabled";

export type SubscriptionRuntimeNodeView = {
  id: string;
  label: string;
  protocol: string;
  group: string;
  endpoint: string;
  enabled: boolean;
  selected: boolean;
  orphanReason: "group-only" | "cleanup-needed" | null;
  details: {
    address: string | null;
    port: number | null;
    transport: string | null;
    tls: boolean | null;
    usernamePresent: boolean;
    passwordPresent: boolean;
    realityEnabled: boolean;
    realityPublicKeyPresent: boolean;
    realityShortIdPresent: boolean;
    tlsServerName: string | null;
    grpcMode: string | null;
    flow: string | null;
    encryption: string | null;
    fingerprint: string | null;
    utls: string | null;
    mux: string | null;
    muxConcurrency: string | null;
    xudpConcurrency: string | null;
    packetEncoding: string | null;
    extraKeys: string[];
  };
};

export type SubscriptionRuntimePreviewSummary = {
  status: "in_sync" | "drift" | "unverifiable" | "disabled";
  previewState: SubscriptionPreviewState;
  remark: string;
  subscriptionKey: string;
  urlHash: string;
  enabled: boolean;
  accessMode: "auto" | "direct" | "proxy";
  userAgent: string | null;
  payloadMode:
    | "plain-lines"
    | "base64-lines"
    | "ssd-json"
    | "single-link"
    | "unknown";
  fetchState:
    | "ok"
    | "disabled"
    | "http_error"
    | "network_error"
    | "parse_error";
  httpStatus: number | null;
  checkedAt: string | null;
  payloadNodeCount: number | null;
  resolvedPayloadNodeCount: number | null;
  liveManagedNodeCount: number;
  panelDraftManagedNodeCount: number;
  panelOnlyNodeCount: number;
  cleanupNodeCount: number;
  orphanNodeCount: number;
};

export type SubscriptionRuntimeReadModel = {
  editableNodeIds: string[];
  editableSubscriptionIds: string[];
  manualNodes: SubscriptionRuntimeNodeView[];
  managedNodes: SubscriptionRuntimeNodeView[];
  panelOnlyNodes: SubscriptionRuntimeNodeView[];
  cleanupNodes: SubscriptionRuntimeNodeView[];
  orphanNodes: SubscriptionRuntimeNodeView[];
  previews: SubscriptionRuntimePreviewSummary[];
  previewState: {
    status: Exclude<SubscriptionPreviewState, "disabled">;
    checkedAt: string | null;
  };
};

export type SubscriptionPreviewLookupItem = {
  previewState: SubscriptionPreviewState;
  checkedAt: string | null;
  result: SubscriptionPreviewEntry | null;
};

function hashValue(value: string) {
  return createHash("sha256").update(value).digest("hex");
}

function normalizeText(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : null;
}

function normalizeLowerText(value: string | null | undefined) {
  return normalizeText(value)?.toLowerCase() ?? null;
}

function normalizeNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function normalizeBoolean(value: unknown) {
  return typeof value === "boolean" ? value : null;
}

function normalizeExtraValue(
  value: string | number | boolean | string[] | null | undefined,
) {
  if (Array.isArray(value)) {
    return value
      .map((entry) => entry.trim())
      .filter((entry) => entry.length > 0);
  }

  if (typeof value === "string") {
    return value.trim();
  }

  if (
    typeof value === "number" ||
    typeof value === "boolean" ||
    value === null
  ) {
    return value;
  }

  return undefined;
}

function normalizeNodeExtras(node: NodeItem) {
  const normalizedEntries = Object.entries(node.extras)
    .filter(([key]) => !["add_mode", "group"].includes(key))
    .map(([key, value]) => [key, normalizeExtraValue(value)] as const)
    .filter(([, value]) => value !== undefined)
    .sort(([left], [right]) => left.localeCompare(right));

  return Object.fromEntries(normalizedEntries);
}

function buildNodeFingerprintPayload(node: NodeItem) {
  return {
    label: normalizeText(node.label),
    protocol: normalizeLowerText(node.protocol),
    address: normalizeLowerText(node.address),
    port: normalizeNumber(node.port),
    username: normalizeText(node.username),
    password: normalizeText(node.password),
    transport: normalizeLowerText(node.transport),
    tls: normalizeBoolean(node.tls),
    extras: normalizeNodeExtras(node),
  };
}

function endpointLabel(node: NodeItem) {
  const address = normalizeText(node.address) ?? "n/a";
  return node.port ? `${address}:${node.port}` : address;
}

function extraString(extras: NodeItem["extras"], key: string): string | null {
  const value = extras[key];
  if (typeof value === "string") {
    return normalizeText(value);
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    const joined = value
      .map((entry) => entry.trim())
      .filter(Boolean)
      .join(", ");
    return joined.length > 0 ? joined : null;
  }
  return null;
}

function truthyExtra(extras: NodeItem["extras"], key: string) {
  const value = extraString(extras, key)?.toLowerCase();
  return value === "1" || value === "true" || value === "yes" || value === "on";
}

function normalizeSubscriptionRemark(value: string | null | undefined) {
  return normalizeLowerText(value);
}

export function buildSubscriptionUrlHash(url: string | null | undefined) {
  return hashValue(normalizeText(url) ?? "");
}

export function buildSubscriptionSemanticKey(
  subscription: Pick<SubscriptionItem, "remark" | "url">,
) {
  return `${normalizeSubscriptionRemark(subscription.remark) ?? "subscription"}::${buildSubscriptionUrlHash(subscription.url)}`;
}

function normalizeSubscriptionExtras(extras: SubscriptionItem["extras"]) {
  const normalizedEntries = Object.entries(extras)
    .map(([key, value]) => [key, normalizeExtraValue(value)] as const)
    .filter(([, value]) => value !== undefined)
    .sort(([left], [right]) => left.localeCompare(right));

  return Object.fromEntries(normalizedEntries);
}

export function buildSubscriptionPreviewDigest(
  subscriptions: PasswallDesiredConfig["subscriptions"],
) {
  const items = dedupeSubscriptions(subscriptions.items)
    .map((item) => ({
      subscriptionKey: buildSubscriptionSemanticKey(item),
      remark: normalizeText(item.remark),
      urlHash: buildSubscriptionUrlHash(item.url),
      enabled: item.enabled,
      addMode: normalizeText(item.addMode) ?? "2",
      extras: normalizeSubscriptionExtras(item.extras),
    }))
    .sort((left, right) =>
      left.subscriptionKey.localeCompare(right.subscriptionKey),
    );

  return hashValue(
    stableStringify({
      filterKeywordMode: normalizeText(subscriptions.filterKeywordMode) ?? "0",
      discardList: subscriptions.discardList.map((entry) => entry.trim()),
      keepList: subscriptions.keepList.map((entry) => entry.trim()),
      typePreferences: subscriptions.typePreferences,
      domainStrategy: normalizeText(subscriptions.domainStrategy) ?? "auto",
      items,
    }),
  );
}

export function isManagedSubscriptionNode(node: NodeItem) {
  return (
    node.extras.add_mode === "2" ||
    node.extras.add_mode === 2 ||
    node.extras.addMode === "2" ||
    node.extras.addMode === 2
  );
}

export function buildNodeSemanticFingerprint(node: NodeItem) {
  return hashValue(stableStringify(buildNodeFingerprintPayload(node)));
}

export function nodeMatchesSubscriptionGroup(
  node: Pick<NodeItem, "group">,
  subscription: Pick<SubscriptionItem, "remark">,
) {
  const nodeGroup = normalizeLowerText(node.group);
  const subscriptionRemark = normalizeSubscriptionRemark(subscription.remark);
  return (
    nodeGroup !== null &&
    subscriptionRemark !== null &&
    nodeGroup === subscriptionRemark
  );
}

function dedupeSubscriptions<T extends SubscriptionItem>(items: T[]) {
  const byKey = new Map<string, T>();

  for (const item of items) {
    const key = buildSubscriptionSemanticKey(item);
    if (!byKey.has(key)) {
      byKey.set(key, item);
    }
  }

  return [...byKey.values()];
}

export function mergeSubscriptionsBySemanticIdentity(args: {
  draftItems: SubscriptionItem[];
  liveItems: SubscriptionItem[];
}) {
  const liveById = new Map(
    args.liveItems.map((item) => [item.id, item] as const),
  );
  const liveByKey = new Map(
    dedupeSubscriptions(args.liveItems).map(
      (item) => [buildSubscriptionSemanticKey(item), item] as const,
    ),
  );
  const merged: SubscriptionItem[] = [];
  const seenKeys = new Set<string>();
  const seenIds = new Set<string>();

  for (const item of args.draftItems) {
    const key = buildSubscriptionSemanticKey(item);
    if (seenKeys.has(key)) {
      continue;
    }
    seenKeys.add(key);
    seenIds.add(item.id);

    const liveItem = liveById.get(item.id) ?? liveByKey.get(key);
    if (liveItem) {
      merged.push(
        liveById.has(item.id)
          ? item
          : {
              ...item,
              id: liveItem.id,
              remark: liveItem.remark,
              url: liveItem.url,
              metadata: liveItem.metadata,
            },
      );
      continue;
    }

    merged.push(item);
  }

  for (const item of args.liveItems) {
    const key = buildSubscriptionSemanticKey(item);
    if (!seenKeys.has(key) && !seenIds.has(item.id)) {
      seenKeys.add(key);
      seenIds.add(item.id);
      merged.push(item);
    }
  }

  return merged;
}

function mergeNonRuntimeNodesByFingerprint(args: {
  draftNodes: NodeItem[];
  liveNodes: NodeItem[];
}) {
  const draftIds = new Set(args.draftNodes.map((node) => node.id));
  const liveByFingerprint = new Map(
    args.liveNodes.map(
      (node) => [buildNodeSemanticFingerprint(node), node] as const,
    ),
  );
  const merged: NodeItem[] = [];
  const seenFingerprints = new Set<string>();

  for (const node of args.draftNodes) {
    const fingerprint = buildNodeSemanticFingerprint(node);
    if (seenFingerprints.has(fingerprint)) {
      continue;
    }
    seenFingerprints.add(fingerprint);

    const liveNode = liveByFingerprint.get(fingerprint);
    if (liveNode) {
      merged.push({
        ...node,
        id: liveNode.id,
      });
      continue;
    }

    merged.push(node);
  }

  for (const node of args.liveNodes) {
    if (draftIds.has(node.id)) {
      continue;
    }
    const fingerprint = buildNodeSemanticFingerprint(node);
    if (!seenFingerprints.has(fingerprint)) {
      seenFingerprints.add(fingerprint);
      merged.push(node);
    }
  }

  return merged;
}

export function mergeNodesWithCurrentRuntime(args: {
  draftNodes: NodeItem[];
  liveNodes: NodeItem[];
  liveSubscriptions: SubscriptionItem[];
}) {
  const liveSubscriptions = dedupeSubscriptions(args.liveSubscriptions);
  if (liveSubscriptions.length === 0) {
    return mergeNonRuntimeNodesByFingerprint({
      draftNodes: args.draftNodes,
      liveNodes: args.liveNodes,
    });
  }

  const belongsToLiveSubscriptionGroup = (node: NodeItem) =>
    liveSubscriptions.some((subscription) =>
      nodeMatchesSubscriptionGroup(node, subscription),
    );

  const draftManualNodes = args.draftNodes.filter(
    (node) => !belongsToLiveSubscriptionGroup(node),
  );
  const liveManualNodes = args.liveNodes.filter(
    (node) => !belongsToLiveSubscriptionGroup(node),
  );
  const liveRuntimeNodes = args.liveNodes.filter((node) =>
    belongsToLiveSubscriptionGroup(node),
  );

  return [
    ...mergeNonRuntimeNodesByFingerprint({
      draftNodes: draftManualNodes,
      liveNodes: liveManualNodes,
    }),
    ...liveRuntimeNodes,
  ];
}

function buildNodeView(
  node: NodeItem,
  selectedNodeId: string | null | undefined,
  orphanReason: SubscriptionRuntimeNodeView["orphanReason"],
): SubscriptionRuntimeNodeView {
  return {
    id: node.id,
    label: node.label,
    protocol: node.protocol,
    group: node.group,
    endpoint: endpointLabel(node),
    enabled: node.enabled,
    selected: selectedNodeId === node.id,
    orphanReason,
    details: {
      address: normalizeText(node.address),
      port: node.port ?? null,
      transport: normalizeText(node.transport),
      tls: typeof node.tls === "boolean" ? node.tls : null,
      usernamePresent:
        normalizeText(node.username) !== null ||
        extraString(node.extras, "uuid") !== null,
      passwordPresent: normalizeText(node.password) !== null,
      realityEnabled: truthyExtra(node.extras, "reality"),
      realityPublicKeyPresent:
        extraString(node.extras, "reality_publicKey") !== null,
      realityShortIdPresent:
        extraString(node.extras, "reality_shortId") !== null,
      tlsServerName: extraString(node.extras, "tls_serverName"),
      grpcMode: extraString(node.extras, "grpc_mode"),
      flow: extraString(node.extras, "flow"),
      encryption: extraString(node.extras, "encryption"),
      fingerprint: extraString(node.extras, "fingerprint"),
      utls: extraString(node.extras, "utls"),
      mux: extraString(node.extras, "mux"),
      muxConcurrency: extraString(node.extras, "mux_concurrency"),
      xudpConcurrency: extraString(node.extras, "xudp_concurrency"),
      packetEncoding: extraString(node.extras, "packet_encoding"),
      extraKeys: Object.keys(node.extras).sort((left, right) =>
        left.localeCompare(right),
      ),
    },
  };
}

function takeMatchedPayloadFingerprint(
  node: NodeItem,
  fingerprints: PayloadFingerprint[] | null,
) {
  if (!fingerprints || fingerprints.length === 0) {
    return false;
  }

  const fingerprint = buildNodeSemanticFingerprint(node);
  const matchedIndex = fingerprints.findIndex(
    (entry) => entry.fingerprint === fingerprint,
  );
  if (matchedIndex === -1) {
    return false;
  }

  fingerprints.splice(matchedIndex, 1);
  return true;
}

function buildNodeFingerprintCounts(nodes: NodeItem[]) {
  const counts = new Map<string, number>();

  for (const node of nodes) {
    const fingerprint = buildNodeSemanticFingerprint(node);
    counts.set(fingerprint, (counts.get(fingerprint) ?? 0) + 1);
  }

  return counts;
}

export function buildSubscriptionPreviewLookup(args: {
  subscriptions: PasswallDesiredConfig["subscriptions"];
  freshResult: {
    checkedAt: string;
    entries: SubscriptionPreviewEntry[];
  } | null;
  hasPendingJob: boolean;
  hasFailedJob: boolean;
  hasStaleResult: boolean;
}) {
  const currentSubscriptions = dedupeSubscriptions(args.subscriptions.items);
  const freshEntriesByKey = new Map(
    (args.freshResult?.entries ?? []).map(
      (entry) => [entry.subscriptionKey, entry] as const,
    ),
  );
  const stateByKey = new Map<string, SubscriptionPreviewLookupItem>();
  const overallStatus: Array<Exclude<SubscriptionPreviewState, "disabled">> =
    [];

  for (const subscription of currentSubscriptions) {
    const subscriptionKey = buildSubscriptionSemanticKey(subscription);
    if (!subscription.enabled) {
      stateByKey.set(subscriptionKey, {
        previewState: "disabled",
        checkedAt: args.freshResult?.checkedAt ?? null,
        result: freshEntriesByKey.get(subscriptionKey) ?? null,
      });
      continue;
    }

    const freshEntry = freshEntriesByKey.get(subscriptionKey) ?? null;
    if (freshEntry) {
      stateByKey.set(subscriptionKey, {
        previewState: "fresh",
        checkedAt: freshEntry.checkedAt,
        result: freshEntry,
      });
      overallStatus.push("fresh");
      continue;
    }

    if (args.hasPendingJob) {
      stateByKey.set(subscriptionKey, {
        previewState: "pending",
        checkedAt: null,
        result: null,
      });
      overallStatus.push("pending");
      continue;
    }

    if (args.hasStaleResult) {
      stateByKey.set(subscriptionKey, {
        previewState: "stale",
        checkedAt: null,
        result: null,
      });
      overallStatus.push("stale");
      continue;
    }

    if (args.hasFailedJob) {
      stateByKey.set(subscriptionKey, {
        previewState: "failed",
        checkedAt: null,
        result: null,
      });
      overallStatus.push("failed");
      continue;
    }

    stateByKey.set(subscriptionKey, {
      previewState: "missing",
      checkedAt: null,
      result: null,
    });
    overallStatus.push("missing");
  }

  let status: Exclude<SubscriptionPreviewState, "disabled"> = "missing";
  if (overallStatus.length === 0) {
    status = "missing";
  } else if (overallStatus.every((entry) => entry === "fresh")) {
    status = "fresh";
  } else if (overallStatus.includes("pending")) {
    status = "pending";
  } else if (overallStatus.includes("stale")) {
    status = "stale";
  } else if (overallStatus.includes("failed")) {
    status = "failed";
  } else if (overallStatus.includes("missing")) {
    status = "missing";
  }

  return {
    stateByKey,
    previewState: {
      status,
      checkedAt: args.freshResult?.checkedAt ?? null,
    },
  };
}

export function buildSubscriptionRuntimeReadModel(args: {
  runtimeConfig: PasswallDesiredConfig;
  draftConfig: PasswallDesiredConfig;
  latestPanelDraftConfig: PasswallDesiredConfig | null;
  previewLookup: Map<string, SubscriptionPreviewLookupItem>;
  previewState: SubscriptionRuntimeReadModel["previewState"];
  selectedNodeId: string | null | undefined;
}): SubscriptionRuntimeReadModel {
  const runtimeSubscriptions = dedupeSubscriptions(
    args.runtimeConfig.subscriptions.items,
  );
  const manualNodes: SubscriptionRuntimeNodeView[] = [];
  const managedNodes: SubscriptionRuntimeNodeView[] = [];
  const panelOnlyNodes: SubscriptionRuntimeNodeView[] = [];
  const cleanupNodes: SubscriptionRuntimeNodeView[] = [];
  const orphanNodes: SubscriptionRuntimeNodeView[] = [];
  const previews: SubscriptionRuntimePreviewSummary[] = [];

  for (const node of args.runtimeConfig.nodes) {
    const belongsToRuntimeSubscription = runtimeSubscriptions.some(
      (subscription) => nodeMatchesSubscriptionGroup(node, subscription),
    );

    if (!belongsToRuntimeSubscription) {
      manualNodes.push(buildNodeView(node, args.selectedNodeId, null));
    }
  }

  for (const subscription of runtimeSubscriptions) {
    const subscriptionKey = buildSubscriptionSemanticKey(subscription);
    const previewLookupItem = args.previewLookup.get(subscriptionKey) ?? null;
    const previewEntry = previewLookupItem?.result ?? null;
    const previewFingerprints =
      previewLookupItem?.previewState === "fresh" &&
      previewEntry?.payloadFingerprints
        ? [...previewEntry.payloadFingerprints]
        : null;
    const runtimeGroupNodes = args.runtimeConfig.nodes.filter((node) =>
      nodeMatchesSubscriptionGroup(node, subscription),
    );
    const liveManagedNodes = runtimeGroupNodes.filter((node) =>
      isManagedSubscriptionNode(node),
    );
    const panelDraftManagedNodes =
      args.latestPanelDraftConfig?.nodes.filter(
        (node) =>
          nodeMatchesSubscriptionGroup(node, subscription) &&
          isManagedSubscriptionNode(node),
      ) ?? [];
    const remainingLiveFingerprintCounts =
      buildNodeFingerprintCounts(liveManagedNodes);
    const subscriptionPanelOnlyNodes: SubscriptionRuntimeNodeView[] = [];
    const subscriptionCleanupNodes: SubscriptionRuntimeNodeView[] = [];

    for (const node of liveManagedNodes) {
      if (
        previewFingerprints !== null &&
        !takeMatchedPayloadFingerprint(node, previewFingerprints)
      ) {
        const cleanupNode = buildNodeView(
          node,
          args.selectedNodeId,
          "cleanup-needed",
        );
        subscriptionCleanupNodes.push(cleanupNode);
        cleanupNodes.push(cleanupNode);
      }

      managedNodes.push(buildNodeView(node, args.selectedNodeId, null));
    }

    for (const node of panelDraftManagedNodes) {
      const fingerprint = buildNodeSemanticFingerprint(node);
      const remainingCount =
        remainingLiveFingerprintCounts.get(fingerprint) ?? 0;

      if (remainingCount > 0) {
        remainingLiveFingerprintCounts.set(fingerprint, remainingCount - 1);
        continue;
      }

      const panelOnlyNode = buildNodeView(node, args.selectedNodeId, null);
      subscriptionPanelOnlyNodes.push(panelOnlyNode);
      panelOnlyNodes.push(panelOnlyNode);
    }

    const nonManagedGroupNodes = runtimeGroupNodes.filter(
      (node) => !isManagedSubscriptionNode(node),
    );
    for (const node of nonManagedGroupNodes) {
      orphanNodes.push(buildNodeView(node, args.selectedNodeId, "group-only"));
    }

    const latestPanelDraftManagedNodeCount = panelDraftManagedNodes.length;
    const panelOnlyNodeCount = subscriptionPanelOnlyNodes.length;
    const orphanNodeCount = nonManagedGroupNodes.length;
    const cleanupNodeCount = subscriptionCleanupNodes.length;
    const payloadNodeCount =
      previewLookupItem?.previewState === "fresh"
        ? (previewEntry?.payloadNodeCount ?? null)
        : null;
    const liveManagedNodeCount = liveManagedNodes.length;
    const previewMatchesLive =
      previewLookupItem?.previewState === "fresh" &&
      (previewEntry?.resolvedPayloadNodeCount ?? null) ===
        liveManagedNodeCount &&
      cleanupNodeCount === 0;
    const status = !subscription.enabled
      ? "disabled"
      : previewLookupItem?.previewState !== "fresh" ||
          previewEntry?.fetchState !== "ok" ||
          payloadNodeCount === null
        ? "unverifiable"
        : previewMatchesLive &&
            orphanNodeCount === 0 &&
            panelOnlyNodeCount === 0
          ? "in_sync"
          : "drift";

    previews.push({
      status,
      previewState: previewLookupItem?.previewState ?? "missing",
      remark: subscription.remark,
      subscriptionKey,
      urlHash:
        previewEntry?.urlHash ?? buildSubscriptionUrlHash(subscription.url),
      enabled: subscription.enabled,
      accessMode: previewEntry?.accessMode ?? "auto",
      userAgent: previewEntry?.userAgent ?? null,
      payloadMode: previewEntry?.payloadMode ?? "unknown",
      fetchState:
        previewLookupItem?.previewState === "fresh"
          ? (previewEntry?.fetchState ?? "network_error")
          : previewLookupItem?.previewState === "disabled"
            ? "disabled"
            : "network_error",
      httpStatus:
        previewLookupItem?.previewState === "fresh"
          ? (previewEntry?.httpStatus ?? null)
          : null,
      checkedAt: previewLookupItem?.checkedAt ?? null,
      payloadNodeCount,
      resolvedPayloadNodeCount:
        previewLookupItem?.previewState === "fresh"
          ? (previewEntry?.resolvedPayloadNodeCount ?? null)
          : null,
      liveManagedNodeCount,
      panelDraftManagedNodeCount: latestPanelDraftManagedNodeCount,
      panelOnlyNodeCount,
      cleanupNodeCount,
      orphanNodeCount,
    });
  }

  const runtimeSubscriptionRemarks = new Set(
    runtimeSubscriptions.map((subscription) =>
      normalizeSubscriptionRemark(subscription.remark),
    ),
  );
  const editableNodeIds = args.draftConfig.nodes
    .filter(
      (node) => !runtimeSubscriptionRemarks.has(normalizeLowerText(node.group)),
    )
    .map((node) => node.id);
  const editableSubscriptionIds = dedupeSubscriptions(
    args.draftConfig.subscriptions.items,
  ).map((item) => item.id);

  return {
    editableNodeIds,
    editableSubscriptionIds,
    manualNodes,
    managedNodes,
    panelOnlyNodes,
    cleanupNodes,
    orphanNodes,
    previews,
    previewState: args.previewState,
  };
}
