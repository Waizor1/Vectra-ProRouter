import { describe, expect, it } from "vitest";

import {
  ControllerUpdateRolloutRefusal,
  evaluateControllerUpdateRolloutHealth,
  findRecentControllerRollbackRefusal,
  readControllerUpdateRolloutRefusal,
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

  it("explains every reason and names the real ways to force", () => {
    const refusal = new ControllerUpdateRolloutRefusal(["a", "b"]);
    expect(refusal.message).toContain("a; b");
    expect(refusal.message).toContain("«Всё равно обновить»");
    expect(refusal.message).toContain("update controller <роутер> --force");
    expect(refusal.message).not.toContain("force: true");
  });

  it("reads the reasons back from an error that crossed tRPC", () => {
    const refusal = new ControllerUpdateRolloutRefusal([
      "роутер в режиме direct",
      "открыт инцидент server_unreachable",
    ]);
    expect(readControllerUpdateRolloutRefusal(new Error(refusal.message))).toBe(
      "роутер в режиме direct; открыт инцидент server_unreachable",
    );
    expect(readControllerUpdateRolloutRefusal(new Error("boom"))).toBeNull();
    expect(readControllerUpdateRolloutRefusal(undefined)).toBeNull();
  });
});

describe("findRecentControllerRollbackRefusal", () => {
  const refusedAt = new Date("2026-10-07T10:00:00.000Z");
  const refused = {
    kind: "controller-self-update",
    artifactVersion: "0.1.13-r47",
    createdAt: refusedAt,
    error: "terminal command failed with exit code 75",
    stderr:
      "controller self-update refused (75): 0.1.13-r47 was rolled back on this router 600s ago (crash loop); to try it again force it: router Updates tab, or VectraPanelCli.sh update controller <router> --force",
  };
  const inAnHour = new Date(refusedAt.getTime() + 3600 * 1000);

  it("offers a force when the newest controller update hit the 75 refusal for the current target", () => {
    expect(
      findRecentControllerRollbackRefusal(
        [{ kind: "router-reboot", artifactVersion: null, stderr: null }, refused],
        { targetVersion: "0.1.13-r47", now: inAnHour },
      ),
    ).toEqual({
      artifactVersion: "0.1.13-r47",
      detail: expect.stringContaining("was rolled back on this router") as unknown,
    });
  });

  it("not once the refusal is older than 24 h", () => {
    expect(
      findRecentControllerRollbackRefusal([refused], {
        targetVersion: "0.1.13-r47",
        now: new Date(refusedAt.getTime() + 25 * 3600 * 1000),
      }),
    ).toBeNull();
  });

  it("not when the update would now install another version, or the target is unknown", () => {
    expect(
      findRecentControllerRollbackRefusal([refused], {
        targetVersion: "0.1.13-r48",
        now: inAnHour,
      }),
    ).toBeNull();
    expect(
      findRecentControllerRollbackRefusal([refused], { targetVersion: null, now: inAnHour }),
    ).toBeNull();
  });

  it("not when a newer controller update came after it, or nothing was refused", () => {
    expect(
      findRecentControllerRollbackRefusal(
        [
          { kind: "controller-self-update", artifactVersion: "0.1.13-r47", createdAt: inAnHour, stderr: null },
          refused,
        ],
        { targetVersion: "0.1.13-r47", now: inAnHour },
      ),
    ).toBeNull();
    expect(
      findRecentControllerRollbackRefusal([], { targetVersion: "0.1.13-r47", now: inAnHour }),
    ).toBeNull();
  });
});
