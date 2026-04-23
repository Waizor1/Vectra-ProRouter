import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import { analyzeSubscriptionPayload } from "./subscription-audit";
import {
  buildSubscriptionPreviewLookup,
  buildSubscriptionRuntimeReadModel,
  buildSubscriptionSemanticKey,
  buildSubscriptionUrlHash,
  mergeNodesWithCurrentRuntime,
  mergeSubscriptionsBySemanticIdentity,
} from "./subscription-runtime";

const runtimeConfig = passwallDesiredConfigSchema.parse({
  basicSettings: {
    main: {
      mainSwitch: true,
      selectedNodeId: "myshunt",
      localhostProxy: true,
      clientProxy: true,
      nodeSocksPort: 1070,
      nodeSocksBindLocal: true,
      socksMainSwitch: false,
      extras: {},
    },
    dns: {
      directQueryStrategy: "UseIP",
      remoteDnsProtocol: "tcp",
      remoteDns: "1.1.1.1",
      remoteDnsDoh: "https://1.1.1.1/dns-query",
      remoteDnsDetour: "remote",
      remoteFakeDns: false,
      remoteDnsQueryStrategy: "UseIPv4",
      dnsHosts: [],
      dnsRedirect: true,
      extras: {},
    },
    log: {
      enableNodeLog: false,
      level: "error",
      extras: {},
    },
    maintenance: {
      backupPaths: [],
      extras: {},
    },
    socks: [],
    shuntRules: [],
  },
  nodes: [
    {
      id: "managed-node",
      label: "BloopCat 1",
      protocol: "vless",
      enabled: true,
      group: "BloopCat",
      address: "example.com",
      port: 443,
      username: "uuid",
      transport: "ws",
      tls: true,
      tags: [],
      extras: {
        add_mode: "2",
        host: "edge.example.com",
        path: "/ws",
      },
    },
    {
      id: "orphan-node",
      label: "BloopCat local",
      protocol: "vless",
      enabled: true,
      group: "BloopCat",
      address: "local.example.com",
      port: 443,
      transport: "ws",
      tls: true,
      tags: [],
      extras: {
        host: "edge.example.com",
        path: "/local",
      },
    },
    {
      id: "payload-mismatch-managed-node",
      label: "BloopCat old",
      protocol: "vless",
      enabled: true,
      group: "BloopCat",
      address: "old.example.com",
      port: 443,
      transport: "ws",
      tls: true,
      tags: [],
      extras: {
        add_mode: "2",
        host: "edge.example.com",
        path: "/old",
      },
    },
    {
      id: "myshunt",
      label: "myshunt",
      protocol: "shunt",
      enabled: true,
      group: "default",
      tags: [],
      extras: {},
    },
  ],
  subscriptions: {
    filterKeywordMode: "0",
    discardList: [],
    keepList: [],
    typePreferences: {},
    domainStrategy: "auto",
    items: [
      {
        id: "vectra_sub_subscribe_list_0",
        remark: "BloopCat",
        url: "https://example.com/subscription",
        enabled: true,
        addMode: "2",
        metadata: {},
        extras: {},
      },
    ],
  },
  appUpdate: {
    binaryPaths: {},
    updateStrategy: "package-preferred",
    targetVersions: {},
    extras: {},
  },
  ruleManage: {
    geoipUrl: "https://example.com/geoip.dat",
    geositeUrl: "https://example.com/geosite.dat",
    assetDirectory: "/usr/share/v2ray/",
    autoUpdate: true,
    scheduleMode: "daily",
    enabledAssets: ["geoip", "geosite"],
    shuntRules: [],
    extras: {},
  },
});

describe("mergeSubscriptionsBySemanticIdentity", () => {
  it("deduplicates one subscription with different internal ids", () => {
    const draftItems = [
      {
        ...runtimeConfig.subscriptions.items[0]!,
        id: "@subscribe_list[0]",
      },
    ];

    const merged = mergeSubscriptionsBySemanticIdentity({
      draftItems,
      liveItems: runtimeConfig.subscriptions.items,
    });

    expect(merged).toHaveLength(1);
    expect(merged[0]?.id).toBe("vectra_sub_subscribe_list_0");
  });
});

describe("mergeNodesWithCurrentRuntime", () => {
  it("replaces stale subscription-group nodes with current live runtime nodes", () => {
    const draftNodes = [
      {
        ...runtimeConfig.nodes[0]!,
        id: "old-managed-node",
      },
      runtimeConfig.nodes[3]!,
    ];

    const merged = mergeNodesWithCurrentRuntime({
      draftNodes,
      liveNodes: runtimeConfig.nodes,
      liveSubscriptions: runtimeConfig.subscriptions.items,
    });

    expect(merged.map((node) => node.id)).toEqual([
      "myshunt",
      "managed-node",
      "orphan-node",
      "payload-mismatch-managed-node",
    ]);
  });
});

describe("buildSubscriptionRuntimeReadModel", () => {
  it("splits manual, managed and orphan nodes and compares with router preview counts", () => {
    const subscription = runtimeConfig.subscriptions.items[0]!;
    const checkedAt = new Date("2026-04-19T12:00:00Z").toISOString();
    const payloadAnalysis = analyzeSubscriptionPayload(
      "vless://uuid@example.com:443?type=ws&security=tls&host=edge.example.com&path=%2Fws#BloopCat%201",
    );
    const previewLookup = buildSubscriptionPreviewLookup({
      subscriptions: runtimeConfig.subscriptions,
      freshResult: {
        checkedAt,
        entries: [
          {
            remark: subscription.remark,
            subscriptionKey: buildSubscriptionSemanticKey(subscription),
            subscriptionId: subscription.id,
            urlHash: buildSubscriptionUrlHash(subscription.url),
            enabled: true,
            accessMode: "auto",
            userAgent: null,
            fetchState: "ok",
            httpStatus: 200,
            payloadMode: "single-link",
            payloadNodeCount: 1,
            resolvedPayloadNodeCount: 1,
            payloadFingerprints: payloadAnalysis.payloadFingerprints ?? [],
            checkedAt,
          },
        ],
      },
      hasPendingJob: false,
      hasFailedJob: false,
      hasStaleResult: false,
    });
    const readModel = buildSubscriptionRuntimeReadModel({
      runtimeConfig,
      draftConfig: runtimeConfig,
      latestPanelDraftConfig: passwallDesiredConfigSchema.parse({
        ...runtimeConfig,
        nodes: [
          runtimeConfig.nodes[0]!,
          {
            ...runtimeConfig.nodes[0]!,
            id: "panel-only-node",
            label: "BloopCat panel",
            address: "panel.example.com",
            extras: {
              ...runtimeConfig.nodes[0]!.extras,
              path: "/panel",
            },
          },
          runtimeConfig.nodes[3]!,
        ],
      }),
      previewLookup: previewLookup.stateByKey,
      previewState: previewLookup.previewState,
      selectedNodeId: "myshunt",
    });

    expect(readModel.manualNodes.map((node) => node.id)).toEqual(["myshunt"]);
    expect(readModel.managedNodes.map((node) => node.id)).toEqual([
      "managed-node",
      "payload-mismatch-managed-node",
    ]);
    expect(readModel.panelOnlyNodes.map((node) => node.id)).toEqual([
      "panel-only-node",
    ]);
    expect(readModel.cleanupNodes.map((node) => node.id)).toEqual([
      "payload-mismatch-managed-node",
    ]);
    expect(readModel.orphanNodes.map((node) => node.id)).toEqual(["orphan-node"]);
    expect(readModel.previews[0]).toMatchObject({
      previewState: "fresh",
      status: "drift",
      liveManagedNodeCount: 2,
      panelDraftManagedNodeCount: 2,
      panelOnlyNodeCount: 1,
      orphanNodeCount: 1,
      cleanupNodeCount: 1,
      payloadNodeCount: 1,
      resolvedPayloadNodeCount: 1,
    });
    expect(readModel.previewState).toMatchObject({
      status: "fresh",
      checkedAt,
    });
    expect(readModel.editableNodeIds).toEqual(["myshunt"]);
  });
});

describe("buildSubscriptionPreviewLookup", () => {
  it("marks subscriptions as missing when no router preview exists", () => {
    const lookup = buildSubscriptionPreviewLookup({
      subscriptions: runtimeConfig.subscriptions,
      freshResult: null,
      hasPendingJob: false,
      hasFailedJob: false,
      hasStaleResult: false,
    });
    const key = buildSubscriptionSemanticKey(runtimeConfig.subscriptions.items[0]!);

    expect(lookup.previewState).toEqual({
      status: "missing",
      checkedAt: null,
    });
    expect(lookup.stateByKey.get(key)).toMatchObject({
      previewState: "missing",
      checkedAt: null,
      result: null,
    });
  });

  it("marks subscriptions as stale when only a preview for another digest exists", () => {
    const lookup = buildSubscriptionPreviewLookup({
      subscriptions: runtimeConfig.subscriptions,
      freshResult: null,
      hasPendingJob: false,
      hasFailedJob: false,
      hasStaleResult: true,
    });
    const key = buildSubscriptionSemanticKey(runtimeConfig.subscriptions.items[0]!);

    expect(lookup.previewState).toEqual({
      status: "stale",
      checkedAt: null,
    });
    expect(lookup.stateByKey.get(key)).toMatchObject({
      previewState: "stale",
      checkedAt: null,
      result: null,
    });
  });
});

describe("analyzeSubscriptionPayload", () => {
  it("counts plain link payloads without exposing raw urls", () => {
    const analysis = analyzeSubscriptionPayload(
      "vless://uuid@example.com:443?type=ws&security=tls#Node%201\n\n# comment",
    );

    expect(analysis.payloadMode).toBe("single-link");
    expect(analysis.payloadNodeCount).toBe(1);
    expect(analysis.resolvedPayloadNodeCount).toBe(1);
  });

  it("counts base64-wrapped link payloads", () => {
    const encoded = Buffer.from(
      "vless://uuid@example.com:443?type=ws&security=tls#Node%201\nvless://uuid@example.org:443?type=ws&security=tls#Node%202",
      "utf8",
    ).toString("base64");

    const analysis = analyzeSubscriptionPayload(encoded);

    expect(analysis.payloadMode).toBe("base64-lines");
    expect(analysis.payloadNodeCount).toBe(2);
    expect(analysis.resolvedPayloadNodeCount).toBe(2);
  });

  it("counts ssd payload servers", () => {
    const encoded = Buffer.from(
      JSON.stringify({
        airport: "Demo",
        port: 443,
        encryption: "aes-256-gcm",
        password: "secret",
        servers: [
          { server: "one.example.com", remarks: "One" },
          { server: "two.example.com", remarks: "Two" },
        ],
      }),
      "utf8",
    ).toString("base64");

    const analysis = analyzeSubscriptionPayload(`ssd://${encoded}`);

    expect(analysis.payloadMode).toBe("ssd-json");
    expect(analysis.payloadNodeCount).toBe(2);
    expect(analysis.resolvedPayloadNodeCount).toBe(2);
  });
});
