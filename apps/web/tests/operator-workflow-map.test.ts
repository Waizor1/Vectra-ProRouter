import { describe, expect, it } from "vitest";

import { buildOperatorWorkflowMapItems } from "../src/components/operator-workflow-map";

describe("buildOperatorWorkflowMapItems", () => {
  it("marks the current screen active", () => {
    const items = buildOperatorWorkflowMapItems("router");
    const routerItem = items.find((item) => item.id === "router");
    const fleetItem = items.find((item) => item.id === "fleet");

    expect(routerItem?.active).toBe(true);
    expect(fleetItem?.active).toBe(false);
  });

  it("keeps updates as the emphasized fleet-wide destination", () => {
    const items = buildOperatorWorkflowMapItems("drafts");
    const emphasized = items.filter((item) => item.emphasized);
    const updatesItem = items.find((item) => item.id === "updates");

    expect(emphasized).toHaveLength(1);
    expect(updatesItem?.emphasized).toBe(true);
  });

  it("preserves the operator workflow order", () => {
    const items = buildOperatorWorkflowMapItems("updates");

    expect(items.map((item) => item.id)).toEqual([
      "fleet",
      "router",
      "drafts",
      "rescue",
      "updates",
      "enrollment",
    ]);
  });
});
