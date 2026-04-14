import { describe, expect, it } from "vitest";

import {
  canRunDestructiveAction,
  canRunUpdateAction,
  describeEffectiveRouterSupport,
  describeRouterSupport,
  evaluateRouterSupport,
} from "./support";

describe("describeRouterSupport", () => {
  it("certifies only AX3000T stock-layout on OpenWrt 24.10.x", () => {
    const support = describeRouterSupport({
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily: "stock-layout",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
    });

    expect(support.state).toBe("certified");
    expect(canRunDestructiveAction(support.state)).toBe(true);
  });

  it("keeps other Filogic layouts in pilot mode but still allows operator actions", () => {
    const support = describeRouterSupport({
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily: "ubootmod",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
    });

    expect(support.state).toBe("pilot");
    expect(canRunDestructiveAction(support.state)).toBe(true);
    expect(canRunUpdateAction(support.state)).toBe(true);
  });

  it("treats other boards on the same Filogic family as pilot too", () => {
    const support = describeRouterSupport({
      boardName: "bananapi,bpi-r4",
      layoutFamily: "stock-layout",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
    });

    expect(support.state).toBe("pilot");
    expect(canRunDestructiveAction(support.state)).toBe(true);
    expect(canRunUpdateAction(support.state)).toBe(true);
  });
});

describe("evaluateRouterSupport", () => {
  it("enables both destructive and update actions for certified and pilot Filogic devices", () => {
    const certified = evaluateRouterSupport(
      "xiaomi,mi-router-ax3000t",
      "stock-layout",
      "mediatek/filogic",
      "aarch64_cortex-a53",
      "24.10.6"
    );
    expect(certified.destructiveActionsAllowed).toBe(true);
    expect(certified.updateActionsAllowed).toBe(true);

    const pilot = evaluateRouterSupport(
      "xiaomi,mi-router-ax3000t",
      "ubootmod",
      "mediatek/filogic",
      "aarch64_cortex-a53",
      "24.10.6"
    );
    expect(pilot.destructiveActionsAllowed).toBe(true);
    expect(pilot.updateActionsAllowed).toBe(true);
  });
});

describe("describeEffectiveRouterSupport", () => {
  it("keeps pilot status when the latest snapshot reports a non-certified Filogic layout", () => {
    const support = describeEffectiveRouterSupport({
      router: {
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
      },
      inventory: {
        boardName: "xiaomi,mi-router-ax3000t",
        layoutFamily: "ubootmod",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
      },
    });

    expect(support.state).toBe("pilot");
    expect(canRunDestructiveAction(support.state)).toBe(true);
    expect(canRunUpdateAction(support.state)).toBe(true);
  });
});
