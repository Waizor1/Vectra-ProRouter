import { describe, expect, it } from "vitest";

import { pickFreshAlertsForBrowser } from "~/lib/fleet-browser-alerts";

import { buildFleetMonitoringSnapshot } from "./fleet-monitoring";

describe("buildFleetMonitoringSnapshot", () => {
  it("builds operational charts and prioritized alerts from the current fleet state", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-04-09T09:00:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 1,
      queuedJobs: 6,
      routers: [
        {
          id: "stable-1",
          name: "Stable AX3000T",
          status: "active",
          importState: "approved",
          supportState: "certified",
          lastSeenAt: new Date("2026-04-09T08:59:20.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 12,
          subscriptionCount: 2,
          controllerVersion: "0.1.12-r2",
          passwallVersion: "26.4.5-r1",
          components: { xray: "26.3.27-r1" },
          telegramReachability: {
            checkedAt: "2026-04-09T08:59:10.000Z",
            status: "partial",
            reachable: false,
            reachableCount: 3,
            totalCount: 4,
            checks: [
              {
                id: "telegram-org",
                label: "telegram.org",
                reachable: true,
                checkedAt: "2026-04-09T08:59:08.000Z",
                targetUrl: "https://telegram.org/",
                statusCode: 200,
              },
            ],
          },
          queuedJobCount: 1,
          lastRescueReason: null,
          openIncident: null,
        },
        {
          id: "direct-1",
          name: "Remote NX31",
          status: "direct",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-04-09T08:58:40.000Z"),
          selectedNode: "YouTube",
          passwallEnabled: false,
          nodeCount: 7,
          subscriptionCount: 1,
          controllerVersion: "0.1.11-r1",
          passwallVersion: "26.4.5-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: "Proxy health failed repeatedly.",
          openIncident: {
            type: "entered_direct_mode",
            reason: "Proxy health failed repeatedly.",
            openedAt: new Date("2026-04-09T08:58:35.000Z"),
          },
        },
        {
          id: "offline-1",
          name: "Warehouse R4",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-04-09T08:50:00.000Z"),
          selectedNode: "Default",
          passwallEnabled: true,
          nodeCount: 5,
          subscriptionCount: 1,
          controllerVersion: "0.1.10-r1",
          passwallVersion: "26.4.5-r1",
          components: {},
          queuedJobCount: 3,
          lastRescueReason: "Route unstable",
          openIncident: null,
        },
        {
          id: "review-1",
          name: "Import Review",
          status: "active",
          importState: "import_review",
          supportState: "pilot",
          lastSeenAt: new Date("2026-04-09T08:57:20.000Z"),
          selectedNode: "Default",
          passwallEnabled: true,
          nodeCount: 4,
          subscriptionCount: 1,
          controllerVersion: "0.1.11-r1",
          passwallVersion: "26.4.5-r1",
          components: {},
          queuedJobCount: 2,
          lastRescueReason: null,
          openIncident: null,
        },
        {
          id: "blocked-1",
          name: "Legacy TP-Link",
          status: "active",
          importState: "approved",
          supportState: "blocked",
          lastSeenAt: new Date("2026-04-09T08:59:00.000Z"),
          selectedNode: "Default",
          passwallEnabled: true,
          nodeCount: 2,
          subscriptionCount: 0,
          controllerVersion: "0.1.8-r1",
          passwallVersion: "26.2.0-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          openIncident: null,
        },
      ],
    });

    expect(snapshot.stats.map((item) => [item.label, item.value])).toEqual([
      ["Всего устройств", "5"],
      ["В строю", "1"],
      ["Проблемные", "3"],
      ["Импорт / drift", "1"],
      ["Открытые инциденты", "1"],
      ["Задания в очереди", "6"],
    ]);

    expect(snapshot.charts[0]?.slices.map((slice) => [slice.key, slice.count])).toEqual([
      ["stable", 1],
      ["recovery", 1],
      ["offline", 1],
      ["review", 1],
      ["blocked", 1],
    ]);

    expect(snapshot.charts[1]?.slices.map((slice) => [slice.key, slice.count])).toEqual([
      ["fresh", 3],
      ["watch", 1],
      ["offline", 1],
      ["never", 0],
    ]);

    expect(snapshot.routers[0]?.id).toBe("direct-1");
    expect(
      snapshot.routers.find((router) => router.id === "stable-1")
        ?.telegramReachability?.status,
    ).toBe("partial");
    expect(snapshot.alerts.slice(0, 4).map((alert) => [alert.kind, alert.routerId])).toEqual([
      ["direct_mode", "direct-1"],
      ["offline", "offline-1"],
      ["blocked_support", "blocked-1"],
      ["import_review", "review-1"],
    ]);
  });
});

describe("pickFreshAlertsForBrowser", () => {
  it("keeps only new non-info alerts for browser notifications", () => {
    const alerts = [
      {
        id: "offline:router-1",
        severity: "critical",
      },
      {
        id: "import_review:router-2",
        severity: "warning",
      },
      {
        id: "awaiting_import:router-3",
        severity: "info",
      },
    ] as Parameters<typeof pickFreshAlertsForBrowser>[0];

    const fresh = pickFreshAlertsForBrowser(
      alerts,
      new Set(["offline:router-1"]),
    );

    expect(fresh).toEqual([
      {
        id: "import_review:router-2",
        severity: "warning",
      },
    ]);
  });
});
