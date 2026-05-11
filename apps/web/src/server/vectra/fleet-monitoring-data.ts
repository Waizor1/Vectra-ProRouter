import {
  healthIncidents,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { and, desc, eq, inArray, sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";
import { formatControllerVersion } from "~/lib/controller-version";

import { buildConfigTrustState } from "./config-trust";
import { buildFleetMonitoringSnapshot } from "./fleet-monitoring";
import { loadRevisionMetadata } from "./revision-metadata";
import { isRouterReachable } from "./router-presence";
import { describeRouterSupport } from "./support";

type DatabaseClient = typeof appDb;
type SnapshotSelectClient = Pick<DatabaseClient, "select">;
type SnapshotExecuteClient = Pick<DatabaseClient, "execute">;
type FleetMonitoringDatabaseClient = SnapshotSelectClient &
  Partial<SnapshotExecuteClient>;
type RouterInventorySnapshotRow = typeof routerInventorySnapshots.$inferSelect;

function supportsSnapshotExecute(
  database: FleetMonitoringDatabaseClient,
): database is SnapshotSelectClient & SnapshotExecuteClient {
  return typeof database.execute === "function";
}

function readStringField(
  record: Record<string, unknown>,
  ...keys: string[]
): string | null {
  for (const key of keys) {
    if (typeof record[key] === "string") {
      return record[key];
    }
  }

  return null;
}

function readNullableStringField(
  record: Record<string, unknown>,
  ...keys: string[]
) {
  for (const key of keys) {
    const value = record[key];
    if (value === null) {
      return null;
    }
    if (typeof value === "string") {
      return value;
    }
  }

  return null;
}

function readNumberField(
  record: Record<string, unknown>,
  ...keys: string[]
): number {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
  }

  return 0;
}

function readBooleanField(
  record: Record<string, unknown>,
  ...keys: string[]
): boolean {
  for (const key of keys) {
    if (typeof record[key] === "boolean") {
      return record[key];
    }
  }

  return false;
}

function readDateField(
  record: Record<string, unknown>,
  ...keys: string[]
): Date {
  for (const key of keys) {
    const value = record[key];
    if (value instanceof Date) {
      return value;
    }
    if (typeof value === "string") {
      const parsed = new Date(value);
      if (!Number.isNaN(parsed.getTime())) {
        return parsed;
      }
    }
  }

  return new Date(0);
}

function normalizeSnapshotPayload(
  payload: unknown,
): RouterInventorySnapshotRow["payload"] {
  if (payload && typeof payload === "object") {
    return payload as RouterInventorySnapshotRow["payload"];
  }

  if (typeof payload === "string") {
    try {
      const parsed = JSON.parse(payload) as unknown;
      if (parsed && typeof parsed === "object") {
        return parsed as RouterInventorySnapshotRow["payload"];
      }
    } catch {
      return {} as RouterInventorySnapshotRow["payload"];
    }
  }

  return {} as RouterInventorySnapshotRow["payload"];
}

function normalizeSnapshotRow(row: unknown): RouterInventorySnapshotRow | null {
  if (!row || typeof row !== "object") {
    return null;
  }

  const record = row as Record<string, unknown>;
  const routerId = readStringField(record, "routerId", "router_id");
  if (!routerId) {
    return null;
  }

  return {
    id: readStringField(record, "id") ?? "",
    routerId,
    source: readStringField(record, "source") ?? "check_in",
    payload: normalizeSnapshotPayload(record.payload),
    passwallEnabled: readBooleanField(
      record,
      "passwallEnabled",
      "passwall_enabled",
    ),
    selectedNodeId: readNullableStringField(
      record,
      "selectedNodeId",
      "selected_node_id",
    ),
    nodeCount: readNumberField(record, "nodeCount", "node_count"),
    subscriptionCount: readNumberField(
      record,
      "subscriptionCount",
      "subscription_count",
    ),
    controllerVersion: readNullableStringField(
      record,
      "controllerVersion",
      "controller_version",
    ),
    passwallAppVersion: readNullableStringField(
      record,
      "passwallAppVersion",
      "passwall_app_version",
    ),
    createdAt: readDateField(record, "createdAt", "created_at"),
  };
}

export async function loadLatestSnapshots(
  database: FleetMonitoringDatabaseClient,
  routerIds: string[],
) {
  if (routerIds.length === 0) {
    return new Map<string, typeof routerInventorySnapshots.$inferSelect>();
  }

  const rows = supportsSnapshotExecute(database)
    ? await database.execute(sql`
          select distinct on (router_id)
            id,
            router_id as "routerId",
            source,
            payload,
            passwall_enabled as "passwallEnabled",
            selected_node_id as "selectedNodeId",
            node_count as "nodeCount",
            subscription_count as "subscriptionCount",
            controller_version as "controllerVersion",
            passwall_app_version as "passwallAppVersion",
            created_at as "createdAt"
          from vectra_router_inventory_snapshot
          where router_id in (
            ${sql.join(
              routerIds.map((routerId) => sql`${routerId}`),
              sql`, `,
            )}
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
    const snapshot = normalizeSnapshotRow(row);
    if (snapshot && !latest.has(snapshot.routerId)) {
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
  const [snapshots, incidentRows, queuedJobRows, revisionRows] =
    await Promise.all([
      loadLatestSnapshots(database, routerIds),
      routerIds.length
        ? database
            .select()
            .from(healthIncidents)
            .where(
              and(
                inArray(healthIncidents.routerId, routerIds),
                eq(healthIncidents.state, "open"),
              ),
            )
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
      routerIds.length
        ? loadRevisionMetadata(database, routerIds, {
            origins: ["router_import", "operator_reimport"],
          })
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

  const revisionsByRouter = new Map<string, typeof revisionRows>();
  for (const revision of revisionRows) {
    revisionsByRouter.set(revision.routerId, [
      ...(revisionsByRouter.get(revision.routerId) ?? []),
      revision,
    ]);
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
      const configTrust = buildConfigTrustState({
        routerReachable: isRouterReachable(router.lastSeenAt, now),
        lastCheckInAt: router.lastCheckInAt ?? router.lastSeenAt,
        authoritativeDigest: router.lastConfigDigest,
        snapshotDigest: payload?.configDigest ?? null,
        revisions: revisionsByRouter.get(router.id) ?? [],
        hasAuthoritativeConfig: Boolean(router.activeRevisionId),
      });

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
          payload?.selectedNodeLabel ??
          snapshot?.selectedNodeId ??
          "Не выбрана",
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
        youtubeReachability: payload?.youtubeReachability ?? null,
        safetyEvents: payload?.safetyEvents ?? [],
        resources: payload?.resources ?? null,
        queuedJobCount: jobCountMap.get(router.id) ?? 0,
        lastRescueReason: incident?.reason ?? router.lastRescueReason ?? null,
        configTrust: {
          ...configTrust,
          lastLiveImportAt: configTrust.lastLiveImportAt?.toISOString() ?? null,
          lastCheckInAt: configTrust.lastCheckInAt?.toISOString() ?? null,
        },
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
