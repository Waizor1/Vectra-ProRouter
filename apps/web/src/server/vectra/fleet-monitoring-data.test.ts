import { describe, expect, it } from "vitest";

import {
  healthIncidents,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";

import { loadFleetMonitoringSnapshot } from "./fleet-monitoring-data";

function createMockDatabase(data: {
  routers: unknown[];
  snapshots: unknown[];
  incidents: unknown[];
  jobs: unknown[];
  executeSnapshots?: unknown[];
}) {
  return {
    select() {
      let currentTable: unknown;

      const chain = {
        from(table: unknown) {
          currentTable = table;
          return chain;
        },
        where() {
          return chain;
        },
        orderBy() {
          if (currentTable === routers) {
            return Promise.resolve(data.routers);
          }
          if (currentTable === routerInventorySnapshots) {
            return Promise.resolve(data.snapshots);
          }
          if (currentTable === healthIncidents) {
            return Promise.resolve(data.incidents);
          }
          if (currentTable === jobs) {
            return Promise.resolve(data.jobs);
          }
          return Promise.resolve([]);
        },
      };

      return chain;
    },
    execute() {
      return Promise.resolve(data.executeSnapshots ?? data.snapshots);
    },
  };
}

describe("loadFleetMonitoringSnapshot", () => {
  it("keeps telegram reachability from the latest inventory snapshot", async () => {
    const database = createMockDatabase({
      routers: [
        {
          id: "router-1",
          deviceIdentifier: "device-1",
          displayName: "1111111111",
          hostname: "1111111111",
          panelDomain: null,
          model: "Xiaomi Mi Router AX3000T",
          boardName: "xiaomi,mi-router-ax3000t",
          target: "mediatek/filogic",
          architecture: "aarch64_cortex-a53",
          openwrtRelease: "24.10.6",
          status: "active",
          importState: "approved",
          controllerChannel: "stable",
          pendingImportRevisionId: null,
          activeRevisionId: null,
          lastAppliedRevisionId: null,
          lastConfigDigest: null,
          approvedAt: new Date("2026-04-14T10:00:00.000Z"),
          lastSeenAt: new Date("2026-04-14T11:30:16.000Z"),
          lastCheckInAt: new Date("2026-04-14T11:30:16.000Z"),
          lastDirectModeAt: null,
          lastRescueReason: null,
          createdAt: new Date("2026-04-14T09:00:00.000Z"),
          updatedAt: new Date("2026-04-14T11:30:16.000Z"),
        },
      ],
      snapshots: [
        {
          id: "snapshot-new",
          routerId: "router-1",
          source: "check_in",
          payload: {
            hostname: "1111111111",
            boardName: "xiaomi,mi-router-ax3000t",
            layoutFamily: "stock-layout",
            target: "mediatek/filogic",
            architecture: "aarch64_cortex-a53",
            openwrtRelease: "24.10.6",
            selectedNodeLabel: "Маршрутизатор BloopCat",
            packageVersions: {
              "luci-app-passwall2": "26.4.5-r1",
            },
            binaryVersions: {
              geoview: "Geoview 0.2.5",
            },
            telegramReachability: {
              checkedAt: "2026-04-14T11:30:13Z",
              status: "partial",
              reachable: false,
              reachableCount: 3,
              totalCount: 4,
              checks: [
                {
                  id: "telegram-org",
                  label: "telegram.org",
                  reachable: true,
                  checkedAt: "2026-04-14T11:30:08Z",
                  targetUrl: "https://telegram.org/",
                  statusCode: 200,
                },
              ],
            },
          },
          passwallEnabled: true,
          selectedNodeId: "myshunt",
          nodeCount: 16,
          subscriptionCount: 1,
          controllerVersion: "0.1.12-r10",
          passwallAppVersion: "26.4.5-r1",
          createdAt: new Date("2026-04-14T11:30:16.731Z"),
        },
        {
          id: "snapshot-old",
          routerId: "router-1",
          source: "check_in",
          payload: {
            hostname: "1111111111",
            boardName: "xiaomi,mi-router-ax3000t",
            target: "mediatek/filogic",
            architecture: "aarch64_cortex-a53",
            openwrtRelease: "24.10.6",
            packageVersions: {},
            binaryVersions: {},
          },
          passwallEnabled: true,
          selectedNodeId: "myshunt",
          nodeCount: 15,
          subscriptionCount: 1,
          controllerVersion: "0.1.12-r9",
          passwallAppVersion: "26.4.5-r1",
          createdAt: new Date("2026-04-14T11:28:39.720Z"),
        },
      ],
      incidents: [],
      jobs: [],
    });

    const snapshot = await loadFleetMonitoringSnapshot(
      database as never,
      new Date("2026-04-14T11:31:00.000Z"),
    );

    expect(snapshot.routers).toHaveLength(1);
    expect(snapshot.routers[0]?.controllerVersion).toBe("0.1.12-r10");
    expect(snapshot.routers[0]?.telegramReachability?.status).toBe("partial");
    expect(snapshot.routers[0]?.telegramReachability?.reachableCount).toBe(3);
  });

  it("hydrates latest snapshots from execute() rows with raw snake_case fields", async () => {
    const database = createMockDatabase({
      routers: [
        {
          id: "router-1",
          deviceIdentifier: "device-1",
          displayName: "1111111111",
          hostname: "1111111111",
          panelDomain: null,
          model: "Xiaomi Mi Router AX3000T",
          boardName: "xiaomi,mi-router-ax3000t",
          target: "mediatek/filogic",
          architecture: "aarch64_cortex-a53",
          openwrtRelease: "24.10.6",
          status: "active",
          importState: "approved",
          controllerChannel: "stable",
          pendingImportRevisionId: null,
          activeRevisionId: null,
          lastAppliedRevisionId: null,
          lastConfigDigest: null,
          approvedAt: new Date("2026-04-14T10:00:00.000Z"),
          lastSeenAt: new Date("2026-04-14T11:30:16.000Z"),
          lastCheckInAt: new Date("2026-04-14T11:30:16.000Z"),
          lastDirectModeAt: null,
          lastRescueReason: null,
          createdAt: new Date("2026-04-14T09:00:00.000Z"),
          updatedAt: new Date("2026-04-14T11:30:16.000Z"),
        },
      ],
      snapshots: [],
      executeSnapshots: [
        {
          id: "snapshot-new",
          router_id: "router-1",
          source: "check_in",
          payload: JSON.stringify({
            hostname: "1111111111",
            boardName: "xiaomi,mi-router-ax3000t",
            layoutFamily: "stock-layout",
            target: "mediatek/filogic",
            architecture: "aarch64_cortex-a53",
            openwrtRelease: "24.10.6",
            selectedNodeLabel: "Маршрутизатор BloopCat",
            packageVersions: {
              "luci-app-passwall2": "26.4.10-r1",
            },
            binaryVersions: {
              geoview: "Geoview 0.2.5",
            },
            telegramReachability: {
              checkedAt: "2026-04-14T11:30:13Z",
              status: "ok",
              reachable: true,
              reachableCount: 4,
              totalCount: 4,
              checks: [],
            },
          }),
          passwall_enabled: true,
          selected_node_id: "myshunt",
          node_count: 16,
          subscription_count: 1,
          controller_version: "0.1.12-r13",
          passwall_app_version: "26.4.10-r1",
          created_at: "2026-04-14T11:30:16.731Z",
        },
      ],
      incidents: [],
      jobs: [],
    });

    const snapshot = await loadFleetMonitoringSnapshot(
      database as never,
      new Date("2026-04-14T11:31:00.000Z"),
    );

    expect(snapshot.routers).toHaveLength(1);
    expect(snapshot.routers[0]?.controllerVersion).toBe("0.1.12-r13");
    expect(snapshot.routers[0]?.passwallVersion).toBe("26.4.10-r1");
    expect(snapshot.routers[0]?.telegramReachability?.status).toBe("ok");
  });
});
