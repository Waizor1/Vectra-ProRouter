import { describe, expect, it } from "vitest";

import {
  addNode,
  addSubscription,
  createDraftFixture,
} from "~/components/router-editor-state";

import {
  applyNodeFieldPatch,
  applySubscriptionFieldPatch,
  findShuntNode,
  setShuntNodeExtra,
  toggleNodeEnabled,
} from "./nodes-state";

describe("nodes-state", () => {
  it("applyNodeFieldPatch edits node fields without mutating the source", () => {
    const config = addNode(createDraftFixture());
    const nodeId = config.nodes[0]!.id;

    const next = applyNodeFieldPatch(config, nodeId, {
      label: "DE · Frankfurt",
      address: "203.0.113.10",
      port: 443,
      tls: true,
    });

    const edited = next.nodes[0]!;
    expect(edited.label).toBe("DE · Frankfurt");
    expect(edited.address).toBe("203.0.113.10");
    expect(edited.port).toBe(443);
    expect(edited.tls).toBe(true);
    expect(config.nodes[0]!.label).toBe("Новая нода");
    expect(next).not.toBe(config);
  });

  it("toggleNodeEnabled flips only the targeted node", () => {
    let config = addNode(createDraftFixture());
    config = addNode(config);
    const firstId = config.nodes[0]!.id;

    const next = toggleNodeEnabled(config, firstId, false);

    expect(next.nodes[0]!.enabled).toBe(false);
    expect(next.nodes[1]!.enabled).toBe(true);
  });

  it("applySubscriptionFieldPatch edits subscription fields immutably", () => {
    const config = addSubscription(createDraftFixture());
    const subId = config.subscriptions.items[0]!.id;

    const next = applySubscriptionFieldPatch(config, subId, {
      remark: "Main feed",
      url: "https://example.com/sub",
      enabled: false,
    });

    const edited = next.subscriptions.items[0]!;
    expect(edited.remark).toBe("Main feed");
    expect(edited.url).toBe("https://example.com/sub");
    expect(edited.enabled).toBe(false);
    expect(config.subscriptions.items[0]!.remark).toBe("Новая подписка");
  });

  it("findShuntNode locates the shunt node by protocol", () => {
    let config = addNode(createDraftFixture());
    config = addNode(config);
    const shuntId = config.nodes[1]!.id;
    config = applyNodeFieldPatch(config, shuntId, { protocol: "shunt" });

    const shunt = findShuntNode(config);
    expect(shunt?.id).toBe(shuntId);
  });

  it("setShuntNodeExtra writes per-rule bindings on the shunt node only", () => {
    let config = addNode(createDraftFixture());
    config = applyNodeFieldPatch(config, config.nodes[0]!.id, {
      protocol: "shunt",
    });

    const next = setShuntNodeExtra(config, "YouTube_fakedns", "1");
    expect(next.nodes[0]!.extras.YouTube_fakedns).toBe("1");

    // empty/undefined removes the key
    const cleared = setShuntNodeExtra(next, "YouTube_fakedns", undefined);
    expect("YouTube_fakedns" in cleared.nodes[0]!.extras).toBe(false);
  });

  it("setShuntNodeExtra is a no-op when there is no shunt node", () => {
    const config = addNode(createDraftFixture());
    const next = setShuntNodeExtra(config, "default_node", "_direct");
    expect(next).toBe(config);
  });
});
