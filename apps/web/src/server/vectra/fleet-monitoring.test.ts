import { describe, expect, it } from "vitest";

import { pickFreshAlertsForBrowser } from "~/lib/fleet-browser-alerts";

import {
  buildFleetMonitoringSnapshot,
  computeConnectivityVerdict,
} from "./fleet-monitoring";

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
          youtubeReachability: {
            checkedAt: "2026-04-09T08:59:11.000Z",
            status: "reachable",
            reachable: true,
            reachableCount: 3,
            totalCount: 3,
            checks: [
              {
                id: "youtube-main",
                label: "youtube.com",
                reachable: true,
                checkedAt: "2026-04-09T08:59:09.000Z",
                targetUrl: "https://www.youtube.com/generate_204",
                statusCode: 204,
              },
            ],
          },
          resources: {
            memoryTotalMb: 234,
            memoryAvailableMb: 57,
          },
          queuedJobCount: 1,
          lastRescueReason: null,
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-04-09T08:59:00.000Z",
            lastCheckInAt: "2026-04-09T08:59:20.000Z",
          },
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
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-04-09T08:58:00.000Z",
            lastCheckInAt: "2026-04-09T08:58:40.000Z",
          },
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
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-04-09T08:49:00.000Z",
            lastCheckInAt: "2026-04-09T08:50:00.000Z",
          },
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
          configTrust: {
            liveConfigAvailable: false,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "authoritative",
            lastLiveImportAt: "2026-04-09T08:57:00.000Z",
            lastCheckInAt: "2026-04-09T08:57:20.000Z",
          },
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
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-04-09T08:58:00.000Z",
            lastCheckInAt: "2026-04-09T08:59:00.000Z",
          },
          openIncident: null,
        },
      ],
    });

    expect(snapshot.stats.map((item) => [item.label, item.value])).toEqual([
      ["Всего устройств", "5"],
      ["В строю", "1"],
      ["Проблемные", "3"],
      ["Импорт / drift", "1"],
      ["Policy drift", "0"],
      ["Открытые инциденты", "1"],
      ["RAM риск", "1"],
      ["Мин. RAM", "57 МБ"],
      ["Задания в очереди", "6"],
    ]);

    expect(
      snapshot.charts[0]?.slices.map((slice) => [slice.key, slice.count]),
    ).toEqual([
      ["stable", 1],
      ["recovery", 1],
      ["offline", 1],
      ["review", 1],
      ["blocked", 1],
    ]);

    expect(
      snapshot.charts[1]?.slices.map((slice) => [slice.key, slice.count]),
    ).toEqual([
      ["fresh", 3],
      ["watch", 1],
      ["offline", 1],
      ["never", 0],
    ]);

    expect(
      snapshot.charts[2]?.slices.map((slice) => [slice.key, slice.count]),
    ).toEqual([
      ["good", 0],
      ["warning", 1],
      ["critical", 0],
      ["unknown", 4],
    ]);
    expect(
      snapshot.charts[3]?.slices.map((slice) => [slice.key, slice.count]),
    ).toEqual([
      ["compliant", 0],
      ["violation", 0],
      ["exempt", 0],
      ["unknown", 5],
    ]);
    expect(
      snapshot.charts[4]?.slices.map((slice) => [slice.key, slice.count]),
    ).toEqual([
      ["telegram_degraded", 1],
      ["youtube_degraded", 0],
      ["instagram_degraded", 0],
      ["service_unknown", 4],
    ]);

    expect(snapshot.routers[0]?.id).toBe("direct-1");
    expect(
      snapshot.routers.find((router) => router.id === "stable-1")?.memory,
    ).toMatchObject({
      level: "warning",
      availableMb: 57,
      availablePercent: 24,
    });
    expect(
      snapshot.routers.find((router) => router.id === "stable-1")
        ?.telegramReachability?.status,
    ).toBe("partial");
    expect(
      snapshot.routers.find((router) => router.id === "stable-1")
        ?.youtubeReachability?.status,
    ).toBe("reachable");
    expect(
      snapshot.alerts.slice(0, 6).map((alert) => [alert.kind, alert.routerId]),
    ).toEqual([
      ["direct_mode", "direct-1"],
      ["offline", "offline-1"],
      ["low_memory", "stable-1"],
      ["telegram_degraded", "stable-1"],
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

  it("treats approved routers with digest mismatch as review and emits reimport alert", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-04-09T09:00:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 0,
      queuedJobs: 0,
      routers: [
        {
          id: "approved-drift",
          name: "Approved Drift",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-04-09T08:59:20.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 3,
          subscriptionCount: 1,
          controllerVersion: "0.1.12-r2",
          passwallVersion: "26.4.10-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          configTrust: {
            liveConfigAvailable: false,
            requiresReimport: true,
            digestMismatch: true,
            configSourceMode: "stale-authoritative",
            lastLiveImportAt: "2026-04-09T08:50:00.000Z",
            lastCheckInAt: "2026-04-09T08:59:20.000Z",
          },
          openIncident: null,
        },
      ],
    });

    expect(snapshot.routers[0]?.operationalState).toBe("review");
    expect(snapshot.routers[0]?.configTrust.requiresReimport).toBe(true);
    expect(snapshot.alerts[0]?.kind).toBe("reimport_needed");
  });

  it("surfaces fleet policy violation even when live import trust is green", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-05-12T10:10:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 0,
      queuedJobs: 0,
      routers: [
        {
          id: "policy-drift",
          name: "Policy Drift",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-05-12T10:09:40.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 8,
          subscriptionCount: 1,
          controllerVersion: "0.1.13-r20",
          passwallVersion: "26.5.1-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-05-12T10:08:00.000Z",
            lastCheckInAt: "2026-05-12T10:09:40.000Z",
          },
          fleetPolicyCompliance: {
            policyVersion: "2026-05-12-v1",
            status: "violation",
            checked: true,
            exempt: false,
            exceptionReason: null,
            canNormalize: true,
            matchedSlots: [],
            mismatches: [
              {
                slot: "WorldProxy",
                expected: "RU-entry Germany",
                actual: "🇷🇺🇺🇸 США | ru3.nfnpx.online:50058 | grpc",
                reason: "wrong_target",
              },
            ],
            summary: "1 fleet route policy mismatch(es): WorldProxy.",
          },
          openIncident: null,
        },
      ],
    });

    expect(snapshot.routers[0]?.operationalState).toBe("review");
    expect(snapshot.routers[0]?.needsImportReview).toBe(true);
    expect(snapshot.routers[0]?.configTrust.requiresReimport).toBe(false);
    expect(snapshot.routers[0]?.alertKinds).toContain(
      "fleet_policy_violation",
    );
    expect(snapshot.alerts[0]).toMatchObject({
      kind: "fleet_policy_violation",
      routerId: "policy-drift",
      severity: "warning",
    });
  });

  it("surfaces router-side safety events as fleet alerts", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-05-12T10:10:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 0,
      queuedJobs: 0,
      routers: [
        {
          id: "oom-router",
          name: "OOM Router",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-05-12T10:09:40.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 3,
          subscriptionCount: 1,
          controllerVersion: "0.1.13-r18",
          passwallVersion: "26.5.1-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          configTrust: {
            liveConfigAvailable: true,
            requiresReimport: false,
            digestMismatch: false,
            configSourceMode: "live-import",
            lastLiveImportAt: "2026-05-12T10:08:00.000Z",
            lastCheckInAt: "2026-05-12T10:09:40.000Z",
          },
          safetyEvents: [
            {
              type: "oom_kill",
              severity: "critical",
              component: "xray",
              source: "dmesg",
              message: "OOM pressure mentioned xray",
              observedAt: "2026-05-12T10:09:00.000Z",
              evidence: "Out of memory: Killed process 2699 (xray)",
            },
          ],
          openIncident: null,
        },
      ],
    });

    expect(snapshot.alerts[0]).toMatchObject({
      kind: "router_safety",
      severity: "critical",
      routerId: "oom-router",
      title: "Критическое событие на роутере",
    });
    expect(snapshot.alerts[0]?.description).toContain("xray");
    expect(snapshot.routers[0]?.alertKinds).toContain("router_safety");
  });

  it("drops the duplicate low_memory safety event when the dedicated RAM alert covers it", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-05-17T10:10:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 0,
      queuedJobs: 0,
      routers: [
        {
          id: "low-ram",
          name: "Low RAM AX3000T",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-05-17T10:09:40.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 3,
          subscriptionCount: 1,
          controllerVersion: "0.1.13-r24",
          passwallVersion: "26.5.1-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          resources: { memoryTotalMb: 234, memoryAvailableMb: 46 },
          safetyEvents: [
            {
              type: "low_memory",
              severity: "critical",
              component: "memory",
              source: "resources",
              message: "available RAM is low: 46 MB available (19% of 234 MB)",
              observedAt: "2026-05-17T10:09:00.000Z",
            },
          ],
          openIncident: null,
        },
      ],
    });

    const lowMemoryAlerts = snapshot.alerts.filter(
      (alert) => alert.kind === "low_memory",
    );
    expect(lowMemoryAlerts).toHaveLength(1);
    expect(
      snapshot.alerts.some((alert) => alert.kind === "router_safety"),
    ).toBe(false);
    expect(snapshot.routers[0]?.alertKinds).not.toContain("router_safety");
  });

  it("keeps a low_memory safety event when no dedicated RAM alert is present", () => {
    const snapshot = buildFleetMonitoringSnapshot({
      now: new Date("2026-05-17T10:10:00.000Z"),
      offlineThresholdMs: 3 * 60 * 1000,
      openIncidentCount: 0,
      queuedJobs: 0,
      routers: [
        {
          id: "ok-ram",
          name: "Healthy AX3000T",
          status: "active",
          importState: "approved",
          supportState: "pilot",
          lastSeenAt: new Date("2026-05-17T10:09:40.000Z"),
          selectedNode: "WorldProxy",
          passwallEnabled: true,
          nodeCount: 3,
          subscriptionCount: 1,
          controllerVersion: "0.1.13-r24",
          passwallVersion: "26.5.1-r1",
          components: {},
          queuedJobCount: 0,
          lastRescueReason: null,
          resources: { memoryTotalMb: 234, memoryAvailableMb: 180 },
          safetyEvents: [
            {
              type: "low_memory",
              severity: "warning",
              component: "memory",
              source: "resources",
              message: "controller low-memory guard tripped early",
              observedAt: "2026-05-17T10:09:00.000Z",
            },
          ],
          openIncident: null,
        },
      ],
    });

    expect(snapshot.alerts.some((alert) => alert.kind === "low_memory")).toBe(
      false,
    );
    expect(
      snapshot.alerts.some((alert) => alert.kind === "router_safety"),
    ).toBe(true);
  });
});

describe("computeConnectivityVerdict", () => {
  it("returns 'unknown' when router is offline", () => {
    expect(
      computeConnectivityVerdict({
        reachable: false,
        passwallEnabled: true,
        telegramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 4,
          totalCount: 4,
          checks: [],
        },
      }),
    ).toBe("unknown");
  });

  it("returns 'unknown' when passwall is disabled", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: false,
      }),
    ).toBe("unknown");
  });

  it("returns 'unknown' when no probes have data", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: true,
        telegramReachability: null,
        youtubeReachability: null,
        instagramReachability: null,
      }),
    ).toBe("unknown");
  });

  it("returns 'ok' when all services are reachable", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: true,
        telegramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 4,
          totalCount: 4,
          checks: [],
        },
        youtubeReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 3,
          totalCount: 3,
          checks: [],
        },
        instagramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 2,
          totalCount: 2,
          checks: [],
        },
      }),
    ).toBe("ok");
  });

  it("returns 'partial' when some services degraded", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: true,
        telegramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 4,
          totalCount: 4,
          checks: [],
        },
        youtubeReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "blocked",
          reachable: false,
          reachableCount: 0,
          totalCount: 3,
          checks: [],
        },
        instagramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 2,
          totalCount: 2,
          checks: [],
        },
      }),
    ).toBe("partial");
  });

  it("returns 'down' when all services are blocked", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: true,
        telegramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "blocked",
          reachable: false,
          reachableCount: 0,
          totalCount: 4,
          checks: [],
        },
        youtubeReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "blocked",
          reachable: false,
          reachableCount: 0,
          totalCount: 3,
          checks: [],
        },
        instagramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "blocked",
          reachable: false,
          reachableCount: 0,
          totalCount: 2,
          checks: [],
        },
      }),
    ).toBe("down");
  });

  it("ignores unknown services when computing verdict", () => {
    expect(
      computeConnectivityVerdict({
        reachable: true,
        passwallEnabled: true,
        telegramReachability: {
          checkedAt: "2026-05-26T12:00:00Z",
          status: "reachable",
          reachable: true,
          reachableCount: 4,
          totalCount: 4,
          checks: [],
        },
        youtubeReachability: null,
        instagramReachability: null,
      }),
    ).toBe("ok");
  });
});
