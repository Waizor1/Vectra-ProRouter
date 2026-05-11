import { describe, expect, it } from "vitest";

import { describeRouterMemory } from "./router-memory";

describe("describeRouterMemory", () => {
  it("marks missing zero-default resources as unknown", () => {
    expect(
      describeRouterMemory({
        memoryTotalMb: 0,
        memoryAvailableMb: 0,
      }),
    ).toMatchObject({
      level: "unknown",
      availableMb: null,
      summary: "RAM нет данных",
    });
  });

  it("warns before the controller reaches the low-memory defer zone", () => {
    expect(
      describeRouterMemory({
        memoryTotalMb: 234,
        memoryAvailableMb: 57,
      }),
    ).toMatchObject({
      level: "warning",
      availablePercent: 24,
      summary: "57 МБ свободно из 234 МБ (24%)",
    });
  });

  it("marks very low free RAM as critical even without percent pressure", () => {
    expect(
      describeRouterMemory({
        memoryTotalMb: 512,
        memoryAvailableMb: 47,
      }),
    ).toMatchObject({
      level: "critical",
      label: "RAM критично",
    });
  });
});
