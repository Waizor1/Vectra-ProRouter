import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import {
  healthIncidents,
  jobs,
  passwallDesiredRevisions,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { and, desc, eq, inArray, sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";
import { formatControllerVersion } from "~/lib/controller-version";

import { buildConfigTrustState } from "./config-trust";
import { evaluateFleetRoutePolicy } from "./fleet-route-policy";
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
type FleetPolicyConfigRow = Pick<
  typeof passwallDesiredRevisions.$inferSelect,
  "id" | "routerId" | "origin" | "config" | "createdAt"
>;

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

function readUnknownField(record: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    if (key in record) {
      return record[key];
    }
  }

  return undefined;
}

function normalizePasswallConfig(
  value: unknown,
): PasswallDesiredConfig | null {
  const raw =
    typeof value === "string"
      ? (() => {
          try {
            return JSON.parse(value) as unknown;
          } catch {
            return null;
          }
        })()
      : value;
  const parsed = passwallDesiredConfigSchema.safeParse(raw);
  return parsed.success ? parsed.data : null;
}

function normalizeFleetPolicyConfigRow(row: unknown): FleetPolicyConfigRow | null {
  if (!row || typeof row !== "object") {
    return null;
  }

  const record = row as Record<string, unknown>;
  const id = readStringField(record, "id");
  const routerId = readStringField(record, "routerId", "router_id");
  const origin = readStringField(record, "origin");
  const config = normalizePasswallConfig(readUnknownField(record, "config"));
  if (!id || !routerId || !origin || !config) {
    return null;
  }

  return {
    id,
    routerId,
    origin,
    config,
    createdAt: readDateField(record, "createdAt", "created_at"),
  };
}

export async function loadLatestFleetPolicyConfigRows(
  database: FleetMonitoringDatabaseClient,
  routerIds: string[],
) {
  if (routerIds.length === 0) {
    return new Map<string, FleetPolicyConfigRow>();
  }

  const rows = supportsSnapshotExecute(database)
    ? await database.execute(sql`
          select
            s.id,
            s.router_id as "routerId",
            s.origin,
            s.config,
            s.created_at as "createdAt"
          from (
            values ${sql.join(
              routerIds.map((routerId) => sql`(${routerId})`),
              sql`, `,
            )}
          ) as r(router_id)
          join lateral (
            select *
            from vectra_passwall_desired_revision rev
            where rev.router_id = r.router_id
              and rev.origin in ('router_import', 'operator_reimport')
            order by rev.created_at desc
            limit 1
          ) s on true
        `)
    : await database
        .select()
        .from(passwallDesiredRevisions)
        .where(inArray(passwallDesiredRevisions.routerId, routerIds))
        .orderBy(desc(passwallDesiredRevisions.createdAt));

  const latest = new Map<string, FleetPolicyConfigRow>();
  for (const row of rows) {
    const revision = normalizeFleetPolicyConfigRow(row);
    if (
      revision &&
      (revision.origin === "router_import" ||
        revision.origin === "operator_reimport") &&
      !latest.has(revision.routerId)
    ) {
      latest.set(revision.routerId, revision);
    }
  }

  return latest;
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
          select
            s.id,
            s.router_id as "routerId",
            s.source,
            s.payload,
            s.passwall_enabled as "passwallEnabled",
            s.selected_node_id as "selectedNodeId",
            s.node_count as "nodeCount",
            s.subscription_count as "subscriptionCount",
            s.controller_version as "controllerVersion",
            s.passwall_app_version as "passwallAppVersion",
            s.created_at as "createdAt"
          from (
            values ${sql.join(
              routerIds.map((routerId) => sql`(${routerId})`),
              sql`, `,
            )}
          ) as r(router_id)
          join lateral (
            select *
            from vectra_router_inventory_snapshot snap
            where snap.router_id = r.router_id
            order by snap.created_at desc
            limit 1
          ) s on true
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
  const [
    snapshots,
    incidentRows,
    queuedJobRows,
    revisionRows,
    policyConfigRows,
  ] =
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
      loadLatestFleetPolicyConfigRows(database, routerIds),
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
      const routerName =
        router.displayName ??
        payload?.hostname ??
        router.hostname ??
        router.deviceIdentifier;
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
        name: routerName,
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
        instagramReachability: payload?.instagramReachability ?? null,
        safetyEvents: payload?.safetyEvents ?? [],
        resources: payload?.resources ?? null,
        queuedJobCount: jobCountMap.get(router.id) ?? 0,
        lastRescueReason: incident?.reason ?? router.lastRescueReason ?? null,
        configTrust: {
          ...configTrust,
          lastLiveImportAt: configTrust.lastLiveImportAt?.toISOString() ?? null,
          lastCheckInAt: configTrust.lastCheckInAt?.toISOString() ?? null,
        },
        fleetPolicyCompliance: evaluateFleetRoutePolicy(
          policyConfigRows.get(router.id)?.config ?? null,
          {
            id: router.id,
            name: routerName,
            displayName: router.displayName,
            hostname: payload?.hostname ?? router.hostname,
            deviceIdentifier: router.deviceIdentifier,
          },
        ),
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
