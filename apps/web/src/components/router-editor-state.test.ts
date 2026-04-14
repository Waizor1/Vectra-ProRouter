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
    const reorderedSubscription = moveSubscriptionToTop(withSecondSubscription, 1);
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
    const withSubscriptions = addSubscription(addSubscription(createDraftFixture()));
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
    expect(next.nodes[0]?.extras.world_fakedns).toBe("1");
    expect(next.nodes[0]?.extras.world_proxy_tag).toBe("node-b");
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
});
