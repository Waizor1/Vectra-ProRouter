import { describe, expect, it } from "vitest";

import {
  basicSettingsSecondaryTabs,
  buildRouterConsoleQuery,
  describeDisabledTabs,
  normalizeRouterConsoleSelection,
  normalizeRouterPrimaryTab,
  normalizeRouterSecondaryTab,
  routerPrimaryTabs,
} from "~/components/router-console";

describe("router console tab helpers", () => {
  it("falls back to Basic Settings > Main for invalid query params", () => {
    const primary = normalizeRouterPrimaryTab("server-side");
    const secondary = normalizeRouterSecondaryTab(primary, "anything");

    expect(primary).toBe("basic-settings");
    expect(secondary).toBe("main");
  });

  it("keeps DNS section only for basic settings", () => {
    expect(normalizeRouterSecondaryTab("basic-settings", "dns")).toBe("dns");
    expect(normalizeRouterSecondaryTab("geo-view", "dns")).toBeNull();
  });

  it("normalizes router console selection as a single unit", () => {
    expect(
      normalizeRouterConsoleSelection("app-update", "dns"),
    ).toStrictEqual({
      primaryTab: "app-update",
      secondaryTab: null,
    });

    expect(
      normalizeRouterConsoleSelection("basic-settings", "dns"),
    ).toStrictEqual({
      primaryTab: "basic-settings",
      secondaryTab: "dns",
    });
  });

  it("allows Watch Logs as a real primary tab", () => {
    expect(normalizeRouterPrimaryTab("watch-logs")).toBe("watch-logs");
  });

  it("rewrites query params for deep links", () => {
    const existing = new URLSearchParams("foo=1&section=log");
    const next = buildRouterConsoleQuery({
      existing,
      primaryTab: "rule-manage",
    });

    expect(next.get("foo")).toBe("1");
    expect(next.get("tab")).toBe("rule-manage");
    expect(next.get("section")).toBeNull();
  });

  it("describes disabled tabs for user-facing notices", () => {
    const descriptions = describeDisabledTabs();
    expect(descriptions).toHaveLength(3);
    expect(descriptions[0]).toContain("Other Settings");
  });

  it("keeps the PassWall-recognizable tab order", () => {
    expect(routerPrimaryTabs.map((tab) => tab.label)).toEqual([
      "Basic Settings",
      "Node List",
      "Node Subscribe",
      "Other Settings",
      "App Update",
      "Rule Manage",
      "Geo View",
      "Access Control",
      "Server-Side",
      "Watch Logs",
    ]);
    expect(basicSettingsSecondaryTabs.map((tab) => tab.label)).toEqual([
      "Main",
      "Shunt Rule",
      "DNS",
      "Log",
      "Maintain",
    ]);
  });
});
