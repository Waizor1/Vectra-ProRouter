import {
  healthIncidents,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { desc, inArray } from "drizzle-orm";

import type { db as appDb } from "~/server/db";
import { formatControllerVersion } from "~/lib/controller-version";

import { buildFleetMonitoringSnapshot } from "./fleet-monitoring";
import { describeRouterSupport } from "./support";

type DatabaseClient = typeof appDb;

async function getLatestSnapshots(
  database: DatabaseClient,
  routerIds: string[],
) {
  if (routerIds.length === 0) {
    return new Map<string, typeof routerInventorySnapshots.$inferSelect>();
  }

  const rows = await database
    .select()
    .from(routerInventorySnapshots)
    .where(inArray(routerInventorySnapshots.routerId, routerIds))
    .orderBy(desc(routerInventorySnapshots.createdAt));

  const latest = new Map<
    string,
    typeof routerInventorySnapshots.$inferSelect
  >();
  for (const row of rows) {
    if (!latest.has(row.routerId)) {
      latest.set(row.routerId, row);
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
  database: DatabaseClient,
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
          .where(inArray(healthIncidents.routerId, routerIds))
          .orderBy(desc(healthIncidents.openedAt))
      : Promise.resolve([]),
    routerIds.length
      ? database
          .select()
          .from(jobs)
          .where(inArray(jobs.routerId, routerIds))
          .orderBy(desc(jobs.createdAt))
      : Promise.resolve([]),
  ]);

  const incidentMap = new Map<string, typeof healthIncidents.$inferSelect>();
  let openIncidentCount = 0;
  for (const incident of incidentRows) {
    if (incident.state !== "open") {
      continue;
    }

    openIncidentCount += 1;
    if (!incidentMap.has(incident.routerId)) {
      incidentMap.set(incident.routerId, incident);
    }
  }

  const jobCountMap = new Map<string, number>();
  let queuedJobs = 0;
  for (const job of queuedJobRows) {
    if (!["queued", "delivered", "running"].includes(job.state)) {
      continue;
    }

    queuedJobs += 1;
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
