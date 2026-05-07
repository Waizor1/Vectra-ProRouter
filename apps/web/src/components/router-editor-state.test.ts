import { describe, expect, it } from "vitest";

import {
  addNode,
  addShuntRule,
  addSubscription,
  createDraftFixture,
  deleteNode,
  deleteShuntRule,
  deleteSubscription,
  duplicateNode,
  moveNodeToTop,
  moveShuntRuleToTop,
  moveSubscriptionToTop,
  normalizeShuntRuleBindings,
  pruneNodes,
  renameShuntRule,
  selectNode,
  updateShuntRuleExtra,
} from "~/components/router-editor-state";

describe("router editor state helpers", () => {
  it("duplicates a node and keeps the original in place", () => {
    const config = createDraftFixture({
      nodes: [
        {
          id: "node-main",
          label: "Основная нода",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
      ],
    });

    const next = duplicateNode(config, 0);

    expect(next.nodes).toHaveLength(2);
    expect(next.nodes[1]?.id).toBe("node-main-copy");
    expect(next.nodes[1]?.label).toBe("Основная нода копия");
  });

  it("moves a node to top and can mark it selected", () => {
    const config = createDraftFixture({
      nodes: [
        {
          id: "node-a",
          label: "A",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
        {
          id: "node-b",
          label: "B",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
      ],
    });

    const reordered = moveNodeToTop(config, 1);
    const selected = selectNode(reordered, "node-b");

    expect(selected.nodes[0]?.id).toBe("node-b");
    expect(selected.basicSettings.main.selectedNodeId).toBe("node-b");
  });

  it("reassigns selected node when the active one is deleted", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      ...base,
      basicSettings: {
        ...base.basicSettings,
        main: {
          ...base.basicSettings.main,
          selectedNodeId: "node-a",
        },
      },
      nodes: [
        {
          id: "node-a",
          label: "A",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
        {
          id: "node-b",
          label: "B",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
      ],
    });

    const next = deleteNode(config, 0);

    expect(next.nodes).toHaveLength(1);
    expect(next.basicSettings.main.selectedNodeId).toBe("node-b");
  });

  it("keeps subscriptions and shunt rules reorderable through pure helpers", () => {
    const withSubscription = addSubscription(createDraftFixture());
    const withSecondSubscription = addSubscription(withSubscription);
    const reorderedSubscription = moveSubscriptionToTop(
      withSecondSubscription,
      1,
    );
    const withRule = addShuntRule(reorderedSubscription);
    const withSecondRule = addShuntRule(withRule);
    const reorderedRule = moveShuntRuleToTop(withSecondRule, 1);

    expect(reorderedSubscription.subscriptions.items).toHaveLength(2);
    expect(reorderedSubscription.subscriptions.items[0]?.id).toBe(
      withSecondSubscription.subscriptions.items[1]?.id,
    );
    expect(reorderedRule.basicSettings.shuntRules).toHaveLength(2);
    expect(reorderedRule.basicSettings.shuntRules[0]?.id).toBe(
      withSecondRule.basicSettings.shuntRules[1]?.id,
    );
    expect(reorderedRule.ruleManage.shuntRules).toHaveLength(2);
  });

  it("adds new nodes with sane defaults", () => {
    const next = addNode(createDraftFixture());
    expect(next.nodes).toHaveLength(1);
    expect(next.nodes[0]?.protocol).toBe("xray");
    expect(next.nodes[0]?.enabled).toBe(true);
  });

  it("deletes subscriptions and shunt rules without leaving stale mirrored state", () => {
    const withSubscriptions = addSubscription(
      addSubscription(createDraftFixture()),
    );
    const withRules = addShuntRule(addShuntRule(withSubscriptions));

    const nextSubscriptions = deleteSubscription(withRules, 0);
    const nextRules = deleteShuntRule(nextSubscriptions, 0);

    expect(nextSubscriptions.subscriptions.items).toHaveLength(1);
    expect(nextRules.basicSettings.shuntRules).toHaveLength(1);
    expect(nextRules.ruleManage.shuntRules).toHaveLength(1);
  });

  it("renames shunt rules and preserves node-bound FakeDNS/preproxy extras", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      ...base,
      nodes: [
        {
          id: "shunt-main",
          label: "Shunt",
          protocol: "shunt",
          enabled: true,
          group: "default",
          tags: [],
          extras: {
            direct: "node-a",
            direct_fakedns: "1",
            direct_proxy_tag: "node-b",
          },
        },
      ],
      basicSettings: {
        ...base.basicSettings,
        socks: [],
        shuntRules: [
          {
            id: "direct",
            label: "Direct",
            outboundNodeId: "_direct",
            domainRules: ["domain:example.com"],
            ipRules: [],
            extras: {},
          },
        ],
      },
      ruleManage: {
        ...base.ruleManage,
        shuntRules: [
          {
            id: "direct",
            label: "Direct",
            outboundNodeId: "_direct",
            domainRules: ["domain:example.com"],
            ipRules: [],
            extras: {},
          },
        ],
      },
    });

    const next = renameShuntRule(config, "direct", "world");

    expect(next.basicSettings.shuntRules[0]?.id).toBe("world");
    expect(next.ruleManage.shuntRules[0]?.id).toBe("world");
    expect(next.nodes[0]?.extras.world).toBe("node-a");
    expect(next.nodes[0]?.extras.world_fakedns).toBe("1");
    expect(next.nodes[0]?.extras.world_proxy_tag).toBe("node-b");
    expect(next.nodes[0]?.extras.direct).toBeUndefined();
    expect(next.nodes[0]?.extras.direct_fakedns).toBeUndefined();
    expect(next.nodes[0]?.extras.direct_proxy_tag).toBeUndefined();
  });

  it("updates shunt extras and keeps mirrored rule-manage state in sync", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      ...base,
      basicSettings: {
        ...base.basicSettings,
        socks: [],
        shuntRules: [
          {
            id: "direct",
            label: "Direct",
            outboundNodeId: "_direct",
            domainRules: [],
            ipRules: [],
            extras: {},
          },
        ],
      },
      ruleManage: {
        ...base.ruleManage,
        shuntRules: [
          {
            id: "direct",
            label: "Direct",
            outboundNodeId: "_direct",
            domainRules: [],
            ipRules: [],
            extras: {},
          },
        ],
      },
    });

    const next = updateShuntRuleExtra(config, "direct", "protocol", "http tls");

    expect(next.basicSettings.shuntRules[0]?.extras.protocol).toBe("http tls");
    expect(next.ruleManage.shuntRules[0]?.extras.protocol).toBe("http tls");
  });

  it("normalizes shunt target bindings into shunt node extras before save/apply", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      ...base,
      basicSettings: {
        ...base.basicSettings,
        main: {
          ...base.basicSettings.main,
          selectedNodeId: "myshunt",
        },
        socks: [],
        shuntRules: [
          {
            id: "WorldProxy",
            label: "WorldProxy",
            outboundNodeId: "node-new",
            domainRules: [],
            ipRules: [],
            extras: {},
          },
        ],
      },
      nodes: [
        {
          id: "myshunt",
          label: "Main shunt",
          protocol: "shunt",
          enabled: true,
          group: "default",
          tags: [],
          extras: {
            WorldProxy: "node-old",
            China: "_direct",
          },
        },
        {
          id: "node-old",
          label: "Old",
          protocol: "vless",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
        {
          id: "node-new",
          label: "New",
          protocol: "vless",
          enabled: true,
          group: "default",
          tags: [],
          extras: {},
        },
      ],
      ruleManage: {
        ...base.ruleManage,
        shuntRules: [],
      },
    });

    const next = normalizeShuntRuleBindings(config);

    expect(next.nodes[0]?.extras.WorldProxy).toBe("node-new");
    expect(next.nodes[0]?.extras.China).toBe("_direct");
    expect(next.ruleManage.shuntRules[0]?.outboundNodeId).toBe("node-new");
  });

  it("prunes orphan nodes and repairs dependent references", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      ...base,
      basicSettings: {
        ...base.basicSettings,
        main: {
          ...base.basicSettings.main,
          selectedNodeId: "orphan-node",
        },
        socks: [
          {
            id: "socks-1",
            enabled: true,
            nodeId: "orphan-node",
            port: 1080,
            bindLocal: true,
            autoswitchBackupNodeIds: ["orphan-node", "node-main"],
            extras: {},
          },
        ],
        shuntRules: [
          {
            id: "route-us",
            label: "Route US",
            outboundNodeId: "orphan-node",
            domainRules: [],
            ipRules: [],
            extras: {},
          },
        ],
      },
      nodes: [
        {
          id: "node-main",
          label: "Main",
          protocol: "xray",
          enabled: true,
          group: "default",
          tags: [],
          extras: {
            default_node: "orphan-node",
            "route-us_proxy_tag": "orphan-node",
          },
        },
        {
          id: "orphan-node",
          label: "Orphan",
          protocol: "xray",
          enabled: true,
          group: "Managed",
          tags: [],
          extras: {
            add_mode: "1",
          },
        },
      ],
      subscriptions: {
        ...base.subscriptions,
        items: [
          {
            id: "subscription-1",
            remark: "Managed",
            url: "https://example.invalid/sub",
            enabled: true,
            addMode: "2",
            metadata: {},
            extras: {
              to_node: "orphan-node",
            },
          },
        ],
      },
      ruleManage: {
        ...base.ruleManage,
        shuntRules: [
          {
            id: "route-us",
            label: "Route US",
            outboundNodeId: "orphan-node",
            domainRules: [],
            ipRules: [],
            extras: {},
          },
        ],
      },
    });

    const next = pruneNodes(config, ["orphan-node"]);

    expect(next.nodes.map((node) => node.id)).toEqual(["node-main"]);
    expect(next.basicSettings.main.selectedNodeId).toBe("node-main");
    expect(next.basicSettings.socks[0]).toMatchObject({
      nodeId: "node-main",
      autoswitchBackupNodeIds: [],
    });
    expect(next.basicSettings.shuntRules[0]?.outboundNodeId).toBeUndefined();
    expect(next.ruleManage.shuntRules[0]?.outboundNodeId).toBeUndefined();
    expect(next.nodes[0]?.extras.default_node).toBeUndefined();
    expect(next.nodes[0]?.extras["route-us_proxy_tag"]).toBeUndefined();
    expect(next.subscriptions.items[0]?.extras.to_node).toBeUndefined();
  });

  it("drops socks entries that lose their node and have no fallback", () => {
    const base = createDraftFixture();
    const config = createDraftFixture({
      basicSettings: {
        ...base.basicSettings,
        main: {
          ...base.basicSettings.main,
          mainSwitch: true,
          localhostProxy: true,
          clientProxy: true,
          nodeSocksPort: 1070,
          nodeSocksBindLocal: true,
          socksMainSwitch: false,
          extras: {},
          selectedNodeId: "orphan-node",
        },
        socks: [
          {
            id: "socks-1",
            enabled: true,
            nodeId: "orphan-node",
            port: 1080,
            bindLocal: true,
            autoswitchBackupNodeIds: [],
            extras: {},
          },
        ],
        shuntRules: [],
      },
      nodes: [
        {
          id: "orphan-node",
          label: "Orphan",
          protocol: "xray",
          enabled: true,
          group: "Managed",
          tags: [],
          extras: {},
        },
      ],
    });

    const next = pruneNodes(config, ["orphan-node"]);

    expect(next.nodes).toHaveLength(0);
    expect(next.basicSettings.main.selectedNodeId).toBeUndefined();
    expect(next.basicSettings.socks).toHaveLength(0);
  });
});
