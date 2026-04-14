import { describe, expect, it } from "vitest";

import {
  resolveRescueReason,
  shouldPromotePostApplyImport,
} from "./router-control";

describe("resolveRescueReason", () => {
  it("clears stale rescue reason after proxy recovery", () => {
    expect(
      resolveRescueReason(
        "proxy",
        undefined,
        "Subscription expired or upstream proxy unavailable",
      ),
    ).toBeNull();
  });

  it("keeps the reported direct-mode reason when controller is in direct mode", () => {
    expect(
      resolveRescueReason(
        "direct",
        "Оператор принудительно включил прямой режим из LuCI",
        null,
      ),
    ).toBe("Оператор принудительно включил прямой режим из LuCI");
  });

  it("keeps previous direct-mode reason until a new direct reason arrives", () => {
    expect(
      resolveRescueReason(
        "direct",
        undefined,
        "Subscription expired or upstream proxy unavailable",
      ),
    ).toBe("Subscription expired or upstream proxy unavailable");
  });
});

describe("shouldPromotePostApplyImport", () => {
  it("promotes check-in imports that confirm a server-applied revision", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "check_in",
        reportedAppliedRevisionId: "revision-applied",
        activeRevisionId: "revision-applied",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(true);
  });

  it("does not promote arbitrary live drift after approval", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "check_in",
        reportedAppliedRevisionId: null,
        activeRevisionId: "revision-active",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(false);
  });

  it("keeps operator-requested re-imports in the review lane", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "operator_reimport",
        reportedAppliedRevisionId: "revision-applied",
        activeRevisionId: "revision-applied",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(false);
  });
});
