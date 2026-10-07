import { describe, expect, it } from "vitest";

import {
  ControllerUpdateRolloutRefusal,
  evaluateControllerUpdateRolloutHealth,
} from "~/lib/controller-update-rollout-guard";

const now = new Date("2026-10-07T12:00:00.000Z");
const minutesAgo = (minutes: number) =>
  new Date(now.getTime() - minutes * 60 * 1000);

describe("evaluateControllerUpdateRolloutHealth", () => {
  it("accepts an active router that checked in recently with no incidents", () => {
    expect(
      evaluateControllerUpdateRolloutHealth({
        router: { status: "active", lastCheckInAt: minutesAgo(1) },
        incidents: [],
        now,
      }),
    ).toEqual({ ok: true });
  });

  it("ignores a server_unreachable incident older than 24 h once resolved", () => {
    expect(
      evaluateControllerUpdateRolloutHealth({
        router: { status: "active", lastCheckInAt: minutesAgo(1) },
        incidents: [
          { type: "server_unreachable", state: "resolved", openedAt: minutesAgo(25 * 60) },
          { type: "proxy_outage", state: "open", openedAt: minutesAgo(5) },
        ],
        now,
      }),
    ).toEqual({ ok: true });
  });

  it.each([
    [{ status: "direct", lastCheckInAt: minutesAgo(1) }, [], "режиме direct"],
    [{ status: "rescue", lastCheckInAt: minutesAgo(1) }, [], "режиме rescue"],
    [{ status: "active", lastCheckInAt: minutesAgo(11) }, [], "11 мин назад"],
    [{ status: "active", lastCheckInAt: null }, [], "ни разу"],
    [
      { status: "active", lastCheckInAt: minutesAgo(1) },
      [{ type: "server_unreachable", state: "open", openedAt: minutesAgo(3 * 24 * 60) }],
      "открыт инцидент",
    ],
    [
      { status: "active", lastCheckInAt: minutesAgo(1) },
      [{ type: "server_unreachable", state: "resolved", openedAt: minutesAgo(23 * 60) }],
      "последние 24 ч",
    ],
  ] as const)("refuses %o", (router, incidents, reason) => {
    const result = evaluateControllerUpdateRolloutHealth({
      router,
      incidents,
      now,
    });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.reasons.join("; ")).toContain(reason);
    }
  });

  it("explains every reason and the force escape hatch", () => {
    const refusal = new ControllerUpdateRolloutRefusal(["a", "b"]);
    expect(refusal.message).toContain("a; b");
    expect(refusal.message).toContain("force: true");
  });
});
