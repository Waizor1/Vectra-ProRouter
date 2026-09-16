import { describe, expect, it } from "vitest";

import {
  describeSubscriptionRisk,
  hasSubscriptionGateRisk,
  hasWipedNodeList,
} from "./subscription-health";

describe("subscription health", () => {
  // The gate is what stands between a router and losing every node at midnight.
  // A router reporting it off is not yet broken, which is exactly why the alert
  // has to fire now rather than after the cron runs.
  it("flags a router whose HWID gate is off", () => {
    expect(
      hasSubscriptionGateRisk({
        hwidEnabled: false,
        scheduleEnabled: true,
        placeholderNodes: 0,
      }),
    ).toBe(true);
  });

  it("does not flag a router whose gate is on", () => {
    expect(
      hasSubscriptionGateRisk({
        hwidEnabled: true,
        scheduleEnabled: true,
        placeholderNodes: 0,
      }),
    ).toBe(false);
  });

  // Older controllers send nothing. Absence of a report is not evidence the gate
  // is open, and manufacturing an alert from silence would cry wolf across every
  // router that has not been updated yet.
  it("treats a missing report as unknown rather than as risk", () => {
    expect(hasSubscriptionGateRisk(undefined)).toBe(false);
    expect(hasSubscriptionGateRisk(null)).toBe(false);
  });

  // A placeholder node is not a warning about the future: it means the node list
  // has already been replaced and the router is running on nothing.
  it("reports an already-wiped node list", () => {
    expect(
      hasWipedNodeList({
        hwidEnabled: false,
        scheduleEnabled: true,
        placeholderNodes: 1,
      }),
    ).toBe(true);
    expect(
      hasWipedNodeList({
        hwidEnabled: true,
        scheduleEnabled: true,
        placeholderNodes: 0,
      }),
    ).toBe(false);
    expect(hasWipedNodeList(undefined)).toBe(false);
  });

  it("describes the risk in terms an operator can act on", () => {
    expect(
      describeSubscriptionRisk({
        hwidEnabled: false,
        scheduleEnabled: true,
        placeholderNodes: 0,
      }),
    ).toContain("hwid");
    expect(
      describeSubscriptionRisk({
        hwidEnabled: true,
        scheduleEnabled: false,
        placeholderNodes: 0,
      }),
    ).toContain("расписание");
  });
});
