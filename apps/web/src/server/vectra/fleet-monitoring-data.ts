import {
  healthIncidents,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { and, desc, eq, inArray, sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";
import { formatControllerVersion } from "~/lib/controller-version";

import { buildFleetMonitoringSnapshot } from "./fleet-monitoring";
import { describeRouterSupport } from "./support";

type DatabaseClient = typeof appDb;
type SnapshotSelectClient = Pick<DatabaseClient, "select">;
type SnapshotExecuteClient = Pick<DatabaseClient, "execute">;
type FleetMonitoringDatabaseClient = SnapshotSelectClient &
  Partial<SnapshotExecuteClient>;

function supportsSnapshotExecute(
  database: FleetMonitoringDatabaseClient,
): database is SnapshotSelectClient & SnapshotExecuteClient {
  return typeof database.execute === "function";
}

async function getLatestSnapshots(
  database: FleetMonitoringDatabaseClient,
  routerIds: string[],
) {
  if (routerIds.length === 0) {
    return new Map<string, typeof routerInventorySnapshots.$inferSelect>();
  }

  const rows =
    supportsSnapshotExecute(database)
      ? await database.execute(sql`
          select distinct on (router_id)
            id,
            router_id,
            source,
            payload,
            passwall_enabled,
            selected_node_id,
            node_count,
            subscription_count,
            controller_version,
            passwall_app_version,
            created_at
          from vectra_router_inventory_snapshot
          where router_id in (
            ${sql.join(routerIds.map((routerId) => sql`${routerId}`), sql`, `)}
          )
          order by router_id, created_at desc
        `)
      : await database
          .select()
          .from(routerInventorySnapshots)
          .where(inArray(routerInventorySnapshots.routerId, routerIds))
          .orderBy(desc(routerInventorySnapshots.createdAt));

  const latest = new Map<
    string,
    typeof routerInventorySnapshots.$inferSelect
  >();
  for (const row of rows) {
    const snapshot = row as typeof routerInventorySnapshots.$inferSelect;
    if (!latest.has(snapshot.routerId)) {
      latest.set(snapshot.routerId, snapshot);
    }
  }

  return latest;
}

function pickComponentVersions(
  snapshot: typeof routerInventorySnapshots.$inferSelect | null,
) {
  const payload = snapshot?.payload;
  const binaryVersions = payload?.binaryVersions ?? {};
  const packageVersions = payload?.packageVersions ?? {};

  return Object.fromEntries(
    ["xray", "sing-box", "hysteria", "geoview"].flatMap((key) => {
      const version =
        binaryVersions[key] ??
        packageVersions[key] ??
        packageVersions[`${key}-core`] ??
        null;
      return version ? [[key, version]] : [];
    }),
  );
}

export async function loadFleetMonitoringSnapshot(
  database: FleetMonitoringDatabaseClient,
  now = new Date(),
) {
  const routerRows = await database
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));

  const routerIds = routerRows.map((router) => router.id);
  const [snapshots, incidentRows, queuedJobRows] = await Promise.all([
    getLatestSnapshots(database, routerIds),
    routerIds.length
      ? database
          .select()
          .from(healthIncidents)
          .where(and(inArray(healthIncidents.routerId, routerIds), eq(healthIncidents.state, "open")))
          .orderBy(desc(healthIncidents.openedAt))
      : Promise.resolve([]),
    routerIds.length
      ? database
          .select()
          .from(jobs)
          .where(
            and(
              inArray(jobs.routerId, routerIds),
              inArray(jobs.state, ["queued", "delivered", "running"]),
            ),
          )
          .orderBy(desc(jobs.createdAt))
      : Promise.resolve([]),
  ]);

  const incidentMap = new Map<string, typeof healthIncidents.$inferSelect>();
  const openIncidentCount = incidentRows.length;
  for (const incident of incidentRows) {
    if (!incidentMap.has(incident.routerId)) {
      incidentMap.set(incident.routerId, incident);
    }
  }

  const jobCountMap = new Map<string, number>();
  const queuedJobs = queuedJobRows.length;
  for (const job of queuedJobRows) {
    jobCountMap.set(job.routerId, (jobCountMap.get(job.routerId) ?? 0) + 1);
  }

  return buildFleetMonitoringSnapshot({
    now,
    openIncidentCount,
    queuedJobs,
    routers: routerRows.map((router) => {
      const snapshot = snapshots.get(router.id) ?? null;
      const payload = snapshot?.payload;
      const support = describeRouterSupport({
        boardName: payload?.boardName ?? router.boardName,
        layoutFamily:
          typeof payload?.layoutFamily === "string"
            ? payload.layoutFamily
            : null,
        target: payload?.target ?? router.target,
        architecture: payload?.architecture ?? router.architecture,
        openwrtRelease: payload?.openwrtRelease ?? router.openwrtRelease,
      });
      const incident = incidentMap.get(router.id) ?? null;

      return {
        id: router.id,
        name:
          router.displayName ??
          payload?.hostname ??
          router.hostname ??
          router.deviceIdentifier,
        status: router.status,
        importState: router.importState,
        supportState: support.state,
        lastSeenAt: router.lastSeenAt,
        selectedNode:
          payload?.selectedNodeLabel ?? snapshot?.selectedNodeId ?? "Не выбрана",
        passwallEnabled: snapshot?.passwallEnabled ?? false,
        nodeCount: snapshot?.nodeCount ?? 0,
        subscriptionCount: snapshot?.subscriptionCount ?? 0,
        controllerVersion: formatControllerVersion(snapshot?.controllerVersion),
        passwallVersion:
          snapshot?.passwallAppVersion ??
          payload?.packageVersions["luci-app-passwall2"] ??
          "неизвестно",
        components: pickComponentVersions(snapshot),
        telegramReachability: payload?.telegramReachability ?? null,
        queuedJobCount: jobCountMap.get(router.id) ?? 0,
        lastRescueReason: incident?.reason ?? router.lastRescueReason ?? null,
        openIncident: incident
          ? {
              type: incident.type,
              reason: incident.reason,
              openedAt: incident.openedAt ?? null,
            }
          : null,
      };
    }),
  });
}
