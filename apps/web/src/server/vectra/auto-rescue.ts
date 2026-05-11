import {
  healthIncidents,
  jobResults,
  jobs,
  rescueCases,
  routerInventorySnapshots,
  routers,
  type RescueCaseTrigger,
} from "@vectra/db";
import {
  collectRouterLogsJobPayloadSchema,
  runRescueRepairJobPayloadSchema,
} from "@vectra/contracts";
import { and, desc, eq, inArray, isNull, lte } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";
import { buildRouterManagementTaskLog } from "~/server/vectra/editor-surface";
import { loadFleetMonitoringSnapshot } from "~/server/vectra/fleet-monitoring-data";
import { isRouterReachable } from "~/server/vectra/router-presence";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

import { sendTelegramRescueMessage } from "./telegram-rescue";

type DatabaseClient = typeof db;
type RescueCaseRow = typeof rescueCases.$inferSelect;
type RouterRow = typeof routers.$inferSelect;
type SnapshotRow = typeof routerInventorySnapshots.$inferSelect;
type JobRow = typeof jobs.$inferSelect;

type CriticalTrigger = {
  routerId: string;
  trigger: RescueCaseTrigger;
  title: string;
  reason: string;
  openedAt: Date;
  details: Record<string, unknown>;
};

const activeRescueStates = [
  "open",
  "repairing",
  "escalated",
  "silenced",
] as const;
const repairJobStates = ["queued", "delivered", "running"] as const;
const blockedSnapshotWindow = 3;
const heavyRescueRepairActions = [
  "refresh_rules",
  "refresh_subscriptions",
] as const;
const proxyRuntimeRepairActions = ["restart_passwall", "reconnect_proxy"] as const;
const heavyRepairMemoryFloorMb = 64;
const heavyRepairOverlayFloorMb = 8;
const heavyRepairTmpFloorMb = 16;
const diagnosticMemoryFloorMb = 48;
const diagnosticTmpFloorMb = 8;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function numberField(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function latestResourceRecord(snapshotPayload: unknown) {
  return asRecord(asRecord(snapshotPayload).resources);
}

function latestSafetyEventRecords(snapshotPayload: unknown) {
  const events = asRecord(snapshotPayload).safetyEvents;
  return Array.isArray(events) ? events.map(asRecord) : [];
}

function resourceSafetyEventReasons(
  snapshotPayload: unknown,
  eventTypes: readonly string[],
) {
  const allowed = new Set(eventTypes);
  return latestSafetyEventRecords(snapshotPayload).flatMap((event) => {
    const type = typeof event.type === "string" ? event.type : null;
    const severity =
      typeof event.severity === "string" ? event.severity : null;
    if (
      !type ||
      !allowed.has(type) ||
      (severity !== "critical" && severity !== "warning")
    ) {
      return [];
    }
    const component =
      typeof event.component === "string" ? `/${event.component}` : "";
    return [`latest safety event ${type}${component} is ${severity}`];
  });
}

function resourceFloorReasons(
  resources: Record<string, unknown>,
  floors: {
    memoryAvailableMb: number;
    tmpFreeMb: number;
    overlayFreeMb?: number;
  },
) {
  const reasons: string[] = [];
  const memoryAvailableMb = numberField(resources, "memoryAvailableMb");
  if (!memoryAvailableMb || memoryAvailableMb <= 0) {
    reasons.push("available RAM is unknown");
  } else if (memoryAvailableMb < floors.memoryAvailableMb) {
    reasons.push(
      `available RAM ${memoryAvailableMb} MB is below ${floors.memoryAvailableMb} MB floor`,
    );
  }

  const tmpFreeMb = numberField(resources, "tmpFreeMb");
  if (!tmpFreeMb || tmpFreeMb <= 0) {
    reasons.push("/tmp free space is unknown");
  } else if (tmpFreeMb < floors.tmpFreeMb) {
    reasons.push(
      `/tmp free ${tmpFreeMb} MB is below ${floors.tmpFreeMb} MB floor`,
    );
  }

  if (floors.overlayFreeMb !== undefined) {
    const overlayFreeMb = numberField(resources, "overlayFreeMb");
    if (!overlayFreeMb || overlayFreeMb <= 0) {
      reasons.push("/overlay free space is unknown");
    } else if (overlayFreeMb < floors.overlayFreeMb) {
      reasons.push(
        `/overlay free ${overlayFreeMb} MB is below ${floors.overlayFreeMb} MB floor`,
      );
    }
  }

  return reasons;
}

export function resourceGuardReasonsForHeavyRescueRepair(
  snapshotPayload: unknown,
) {
  return [
    ...resourceFloorReasons(latestResourceRecord(snapshotPayload), {
      memoryAvailableMb: heavyRepairMemoryFloorMb,
      overlayFreeMb: heavyRepairOverlayFloorMb,
      tmpFreeMb: heavyRepairTmpFloorMb,
    }),
    ...resourceSafetyEventReasons(snapshotPayload, [
      "low_memory",
      "low_overlay",
      "low_tmp",
      "oom_kill",
    ]),
  ];
}

export function resourceGuardReasonsForLogCollection(snapshotPayload: unknown) {
  return [
    ...resourceFloorReasons(latestResourceRecord(snapshotPayload), {
      memoryAvailableMb: diagnosticMemoryFloorMb,
      tmpFreeMb: diagnosticTmpFloorMb,
    }),
    ...resourceSafetyEventReasons(snapshotPayload, [
      "low_memory",
      "low_tmp",
      "oom_kill",
    ]),
  ];
}

export function planRepairActionsForRouterSafety(
  actions: readonly string[],
  snapshotPayload: unknown,
  trigger?: RescueCaseTrigger,
) {
  const heavyReasons =
    resourceGuardReasonsForHeavyRescueRepair(snapshotPayload);
  if (heavyReasons.length === 0) {
    return {
      actions: [...actions],
      droppedActions: [] as string[],
      reasons: [] as string[],
    };
  }

  const blocked = new Set<string>(heavyRescueRepairActions);
  if (
    trigger === "foreign_reachability_blocked" ||
    trigger === "telegram_blocked"
  ) {
    for (const action of proxyRuntimeRepairActions) {
      blocked.add(action);
    }
  }

  return {
    actions: actions.filter((action) => !blocked.has(action)),
    droppedActions: actions.filter((action) => blocked.has(action)),
    reasons: heavyReasons,
  };
}

function readStatus(value: unknown) {
  const record = asRecord(value);
  const status = record.status;
  return typeof status === "string" ? status : null;
}

function isBlockedReachability(value: unknown) {
  const record = asRecord(value);
  if (record.reachable === false) {
    return true;
  }
  const status = readStatus(value);
  return status === "blocked" || status === "partial" || status === "failed";
}

type ReachabilitySnapshotLike = {
  payload: unknown;
};

function reachabilityCheckedAt(value: unknown) {
  const record = asRecord(value);
  const checkedAt = record.checkedAt;
  return typeof checkedAt === "string" && checkedAt.trim().length > 0
    ? checkedAt
    : null;
}

export function hasDistinctBlockedReachabilityEvidence(
  snapshots: readonly ReachabilitySnapshotLike[],
  field: "foreignReachability" | "telegramReachability",
  required = blockedSnapshotWindow,
) {
  if (snapshots.length < required) {
    return false;
  }

  const checkedAtValues = new Set<string>();
  for (const snapshot of snapshots.slice(0, required)) {
    const reachability = asRecord(snapshot.payload)[field];
    if (!isBlockedReachability(reachability)) {
      return false;
    }

    const checkedAt = reachabilityCheckedAt(reachability);
    if (!checkedAt) {
      return false;
    }
    checkedAtValues.add(checkedAt);
  }

  return checkedAtValues.size >= required;
}

function compactReachability(value: unknown) {
  const record = asRecord(value);
  return {
    status: typeof record.status === "string" ? record.status : null,
    reachable: typeof record.reachable === "boolean" ? record.reachable : null,
    reachableCount:
      typeof record.reachableCount === "number" ? record.reachableCount : null,
    totalCount:
      typeof record.totalCount === "number" ? record.totalCount : null,
    checkedAt: typeof record.checkedAt === "string" ? record.checkedAt : null,
  };
}

function compactSnapshot(snapshot: SnapshotRow) {
  const payload = snapshot.payload;
  return {
    id: snapshot.id,
    createdAt: snapshot.createdAt.toISOString(),
    passwallEnabled: snapshot.passwallEnabled,
    selectedNodeId: snapshot.selectedNodeId,
    selectedNodeLabel: payload.selectedNodeLabel ?? null,
    controllerVersion: snapshot.controllerVersion,
    passwallAppVersion: snapshot.passwallAppVersion,
    serviceHealth: payload.serviceHealth ?? null,
    lastRescue: payload.lastRescue ?? null,
    panelReachability: compactReachability(payload.panelReachability),
    foreignReachability: compactReachability(payload.foreignReachability),
    telegramReachability: compactReachability(payload.telegramReachability),
    safetyEvents: payload.safetyEvents ?? [],
    resources: payload.resources ?? null,
    rulesAssets: payload.rulesAssets ?? null,
  };
}

function compactJob(job: JobRow) {
  return {
    id: job.id,
    type: job.type,
    state: job.state,
    createdAt: job.createdAt.toISOString(),
    deliveredAt: job.deliveredAt?.toISOString() ?? null,
    completedAt: job.completedAt?.toISOString() ?? null,
  };
}

function normalizeIncidentTrigger(type: string): RescueCaseTrigger | null {
  if (type === "proxy_outage") {
    return "proxy_outage";
  }
  if (type === "server_unreachable") {
    return "server_unreachable";
  }
  return null;
}

export function repairActionsForTrigger(trigger: RescueCaseTrigger) {
  switch (trigger) {
    case "direct_mode":
    case "proxy_outage":
      return [
        "restart_passwall",
        "restart_dnsmasq",
        "refresh_rules",
        "refresh_subscriptions",
        "reconnect_proxy",
      ] as const;
    case "server_unreachable":
      return ["restart_controller", "restart_dnsmasq"] as const;
    case "foreign_reachability_blocked":
    case "telegram_blocked":
      return [
        "restart_passwall",
        "restart_dnsmasq",
        "refresh_rules",
        "refresh_subscriptions",
      ] as const;
    case "stale_check_in":
      return [] as const;
  }
}

function isCaseActive(candidate: RescueCaseRow, now: Date) {
  if (!activeRescueStates.some((state) => state === candidate.state)) {
    return false;
  }

  if (candidate.state !== "silenced") {
    return true;
  }

  return !candidate.silencedUntil || candidate.silencedUntil > now;
}

async function loadRecentSnapshots(
  database: DatabaseClient,
  routerId: string,
  limit = blockedSnapshotWindow,
) {
  return database
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(limit);
}

async function loadLatestSnapshot(database: DatabaseClient, routerId: string) {
  const [snapshot] = await database
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);
  return snapshot ?? null;
}

async function buildCompactEvidence(
  database: DatabaseClient,
  routerId: string,
  now = new Date(),
) {
  const [snapshotRows, incidentRows, jobRows] = await Promise.all([
    loadRecentSnapshots(database, routerId, 3),
    database
      .select()
      .from(healthIncidents)
      .where(
        and(
          eq(healthIncidents.routerId, routerId),
          eq(healthIncidents.state, "open"),
        ),
      )
      .orderBy(desc(healthIncidents.openedAt))
      .limit(3),
    database
      .select()
      .from(jobs)
      .where(eq(jobs.routerId, routerId))
      .orderBy(desc(jobs.createdAt))
      .limit(8),
  ]);

  return {
    collectedAt: now.toISOString(),
    snapshots: snapshotRows.map(compactSnapshot),
    incidents: incidentRows.map((incident) => ({
      id: incident.id,
      type: incident.type,
      state: incident.state,
      reason: incident.reason,
      openedAt: incident.openedAt.toISOString(),
    })),
    recentJobs: jobRows.map(compactJob),
  };
}

async function loadActiveCaseForRouter(
  database: DatabaseClient,
  routerId: string,
  now = new Date(),
) {
  const [activeCase] = await database
    .select()
    .from(rescueCases)
    .where(
      and(
        eq(rescueCases.routerId, routerId),
        inArray(rescueCases.state, [...activeRescueStates]),
      ),
    )
    .orderBy(desc(rescueCases.startedAt))
    .limit(1);

  if (!activeCase) {
    return null;
  }

  if (
    activeCase.state === "silenced" &&
    activeCase.silencedUntil &&
    activeCase.silencedUntil <= now
  ) {
    const [opened] = await database
      .update(rescueCases)
      .set({
        state: "open",
        silencedUntil: null,
      })
      .where(eq(rescueCases.id, activeCase.id))
      .returning();
    return opened ?? activeCase;
  }

  return activeCase;
}

async function detectBlockedReachabilityTriggers(
  database: DatabaseClient,
  now: Date,
): Promise<CriticalTrigger[]> {
  const routerRows = await database.select().from(routers);
  const triggers: CriticalTrigger[] = [];

  for (const router of routerRows) {
    const recentSnapshots = await loadRecentSnapshots(database, router.id);
    if (recentSnapshots.length < blockedSnapshotWindow) {
      continue;
    }

    const foreignBlocked = hasDistinctBlockedReachabilityEvidence(
      recentSnapshots,
      "foreignReachability",
    );
    if (foreignBlocked) {
      triggers.push({
        routerId: router.id,
        trigger: "foreign_reachability_blocked",
        title: "Foreign reachability blocked",
        reason:
          "Foreign reachability failed for several consecutive snapshots.",
        openedAt: recentSnapshots[0]?.createdAt ?? now,
        details: {
          snapshotIds: recentSnapshots.map((snapshot) => snapshot.id),
        },
      });
    }

    const telegramBlocked = hasDistinctBlockedReachabilityEvidence(
      recentSnapshots,
      "telegramReachability",
    );
    if (telegramBlocked) {
      triggers.push({
        routerId: router.id,
        trigger: "telegram_blocked",
        title: "Telegram reachability blocked",
        reason: "Telegram endpoints failed for several consecutive snapshots.",
        openedAt: recentSnapshots[0]?.createdAt ?? now,
        details: {
          snapshotIds: recentSnapshots.map((snapshot) => snapshot.id),
        },
      });
    }
  }

  return triggers;
}

async function detectFleetCriticalTriggers(
  database: DatabaseClient,
  now: Date,
): Promise<CriticalTrigger[]> {
  const snapshot = await loadFleetMonitoringSnapshot(database, now);
  return snapshot.alerts.flatMap((alert) => {
    if (alert.severity !== "critical") {
      return [];
    }

    const openedAt = alert.openedAt ? new Date(alert.openedAt) : now;
    if (alert.kind === "direct_mode") {
      return [
        {
          routerId: alert.routerId,
          trigger: "direct_mode",
          title: alert.title,
          reason: alert.description,
          openedAt,
          details: {
            alertId: alert.id,
            href: alert.href,
          },
        } satisfies CriticalTrigger,
      ];
    }

    if (alert.kind === "offline") {
      const lastSeenAt = alert.openedAt ? new Date(alert.openedAt) : null;
      const staleEnough =
        !lastSeenAt ||
        now.getTime() - lastSeenAt.getTime() >=
          env.VECTRA_AUTO_RESCUE_STALE_SECONDS * 1000;
      if (!staleEnough) {
        return [];
      }
      return [
        {
          routerId: alert.routerId,
          trigger: "stale_check_in",
          title: alert.title,
          reason: alert.description,
          openedAt,
          details: {
            alertId: alert.id,
            href: alert.href,
          },
        } satisfies CriticalTrigger,
      ];
    }

    if (alert.kind === "incident") {
      const match = /^incident:[^:]+:(.+)$/.exec(alert.id);
      const trigger = normalizeIncidentTrigger(match?.[1] ?? "");
      if (!trigger) {
        return [];
      }
      return [
        {
          routerId: alert.routerId,
          trigger,
          title: alert.title,
          reason: alert.description,
          openedAt,
          details: {
            alertId: alert.id,
            href: alert.href,
          },
        } satisfies CriticalTrigger,
      ];
    }

    return [];
  });
}

async function detectCriticalTriggers(database: DatabaseClient, now: Date) {
  const [fleetTriggers, blockedTriggers] = await Promise.all([
    detectFleetCriticalTriggers(database, now),
    detectBlockedReachabilityTriggers(database, now),
  ]);
  return [...fleetTriggers, ...blockedTriggers];
}

async function canAutoRepairRouter(
  database: DatabaseClient,
  router: RouterRow,
) {
  const [snapshot] = await database
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, router.id))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);

  const support = describeEffectiveRouterSupport({
    router: {
      boardName: router.boardName,
      target: router.target,
      architecture: router.architecture,
      openwrtRelease: router.openwrtRelease,
    },
    inventory: snapshot?.payload ?? null,
  });

  return canRunDestructiveAction(support.state);
}

async function hasActiveRepairJob(
  database: DatabaseClient,
  routerId: string,
  caseId: string,
) {
  const [job] = await database
    .select({ id: jobs.id })
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, routerId),
        eq(jobs.type, "run_rescue_repair"),
        eq(jobs.dedupeKey, `auto_rescue_repair:${caseId}`),
        inArray(jobs.state, [...repairJobStates]),
      ),
    )
    .limit(1);
  return Boolean(job);
}

async function ensureRescueCase(
  database: DatabaseClient,
  trigger: CriticalTrigger,
  now: Date,
) {
  const activeCase = await loadActiveCaseForRouter(
    database,
    trigger.routerId,
    now,
  );
  const evidence = await buildCompactEvidence(database, trigger.routerId, now);

  if (activeCase) {
    await database
      .update(rescueCases)
      .set({
        evidence,
        triggerDetails: {
          ...asRecord(activeCase.triggerDetails),
          latestTrigger: trigger.details,
          latestReason: trigger.reason,
        },
      })
      .where(eq(rescueCases.id, activeCase.id));
    return activeCase;
  }

  const [created] = await database
    .insert(rescueCases)
    .values({
      routerId: trigger.routerId,
      trigger: trigger.trigger,
      state: "open",
      startedAt: now,
      triggerDetails: {
        title: trigger.title,
        reason: trigger.reason,
        openedAt: trigger.openedAt.toISOString(),
        ...trigger.details,
      },
      evidence,
      diagnosis: {
        summary: "Auto-rescue case opened from critical monitoring trigger.",
      },
    })
    .onConflictDoNothing()
    .returning();

  return (
    created ?? (await loadActiveCaseForRouter(database, trigger.routerId, now))
  );
}

export async function queueRescueCaseLogCollection(
  caseId: string,
  database: DatabaseClient = db,
) {
  const rescueCase = await getRescueCaseOrThrow(caseId, database);
  const [existingJob] = await database
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, rescueCase.routerId),
        eq(jobs.type, "collect_router_logs"),
        inArray(jobs.state, [...repairJobStates]),
      ),
    )
    .orderBy(desc(jobs.createdAt))
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const latestSnapshot = await loadLatestSnapshot(database, rescueCase.routerId);
  const guardReasons = latestSnapshot
    ? resourceGuardReasonsForLogCollection(latestSnapshot.payload)
    : ["latest router resource snapshot is unavailable"];
  if (guardReasons.length > 0) {
    throw new Error(
      `Router resource guard blocked log collection: ${guardReasons.join("; ")}`,
    );
  }

  const payload = collectRouterLogsJobPayloadSchema.parse({
    source: "all",
    lines: 200,
  });
  const [job] = await database
    .insert(jobs)
    .values({
      routerId: rescueCase.routerId,
      type: "collect_router_logs",
      state: "queued",
      dedupeKey: `auto_rescue_logs:${caseId}`,
      payload,
    })
    .returning();

  return job;
}

export async function queueRescueCaseSafeRepair(
  args: {
    caseId: string;
    actions?: readonly string[];
    requestedBy: "auto_rescue" | "operator" | "telegram";
  },
  database: DatabaseClient = db,
) {
  const rescueCase = await getRescueCaseOrThrow(args.caseId, database);
  const [router] = await database
    .select()
    .from(routers)
    .where(eq(routers.id, rescueCase.routerId))
    .limit(1);

  if (!router) {
    throw new Error("Router for rescue case was not found.");
  }
  if (!isRouterReachable(router.lastSeenAt)) {
    throw new Error(
      "Router is offline; panel cannot deliver a repair job until the next check-in.",
    );
  }
  if (!(await canAutoRepairRouter(database, router))) {
    throw new Error("Safe repair is allowed only for pilot/certified routers.");
  }

  const requestedActions =
    args.actions ?? repairActionsForTrigger(rescueCase.trigger);
  const latestSnapshot = await loadLatestSnapshot(database, rescueCase.routerId);
  const actionPlan = planRepairActionsForRouterSafety(
    requestedActions,
    latestSnapshot?.payload ?? null,
    rescueCase.trigger,
  );
  const actions = actionPlan.actions;
  if (actions.length === 0) {
    throw new Error(
      actionPlan.droppedActions.length > 0
        ? `Router resource guard blocked all requested repair actions: ${actionPlan.reasons.join("; ")}`
        : "This rescue trigger has no remote repair sequence.",
    );
  }
  if (await hasActiveRepairJob(database, rescueCase.routerId, rescueCase.id)) {
    const [existingJob] = await database
      .select()
      .from(jobs)
      .where(
        and(
          eq(jobs.routerId, rescueCase.routerId),
          eq(jobs.type, "run_rescue_repair"),
          eq(jobs.dedupeKey, `auto_rescue_repair:${rescueCase.id}`),
          inArray(jobs.state, [...repairJobStates]),
        ),
      )
      .orderBy(desc(jobs.createdAt))
      .limit(1);
    return existingJob;
  }

  const payload = runRescueRepairJobPayloadSchema.parse({
    actions,
    caseId: rescueCase.id,
    reason:
      typeof rescueCase.triggerDetails.reason === "string"
        ? rescueCase.triggerDetails.reason
        : rescueCase.trigger,
    requestedBy: args.requestedBy,
    timeoutSeconds: 90,
  });

  const [job] = await database
    .insert(jobs)
    .values({
      routerId: rescueCase.routerId,
      type: "run_rescue_repair",
      state: "queued",
      dedupeKey: `auto_rescue_repair:${rescueCase.id}`,
      payload,
    })
    .returning();

  await database
    .update(rescueCases)
    .set({
      state: "repairing",
      lastAttemptAt: new Date(),
      diagnosis: {
        ...asRecord(rescueCase.diagnosis),
        lastQueuedJobId: job?.id ?? null,
        lastQueuedActions: payload.actions,
        resourceGuard:
          actionPlan.droppedActions.length > 0
            ? {
                droppedActions: actionPlan.droppedActions,
                reasons: actionPlan.reasons,
              }
            : null,
      },
    })
    .where(eq(rescueCases.id, rescueCase.id));

  return job;
}

export async function queueRescueCaseReconnectProxy(
  caseId: string,
  requestedBy: "operator" | "telegram" = "operator",
  database: DatabaseClient = db,
) {
  return queueRescueCaseSafeRepair(
    {
      caseId,
      actions: ["reconnect_proxy"],
      requestedBy,
    },
    database,
  );
}

export async function silenceRescueCase(
  caseId: string,
  durationSeconds: number,
  database: DatabaseClient = db,
) {
  const until = new Date(Date.now() + durationSeconds * 1000);
  const [updated] = await database
    .update(rescueCases)
    .set({
      state: "silenced",
      silencedUntil: until,
    })
    .where(eq(rescueCases.id, caseId))
    .returning();
  if (!updated) {
    throw new Error("Rescue case not found.");
  }
  return updated;
}

async function getRescueCaseOrThrow(caseId: string, database: DatabaseClient) {
  const [rescueCase] = await database
    .select()
    .from(rescueCases)
    .where(eq(rescueCases.id, caseId))
    .limit(1);
  if (!rescueCase) {
    throw new Error("Rescue case not found.");
  }
  return rescueCase;
}

async function queueInitialCaseWork(
  database: DatabaseClient,
  rescueCase: RescueCaseRow,
  now: Date,
) {
  await queueRescueCaseLogCollection(rescueCase.id, database).catch(() => null);

  if (
    rescueCase.state === "silenced" &&
    rescueCase.silencedUntil &&
    rescueCase.silencedUntil > now
  ) {
    return;
  }
  if (rescueCase.lastAttemptAt) {
    return;
  }

  const actions = repairActionsForTrigger(rescueCase.trigger);
  if (actions.length === 0) {
    await escalateRescueCase(database, rescueCase, now, {
      reason: "Router is offline/stale; remote repair cannot be delivered.",
    });
    return;
  }

  await queueRescueCaseSafeRepair(
    {
      caseId: rescueCase.id,
      requestedBy: "auto_rescue",
    },
    database,
  ).catch(async (error: unknown) => {
    await database
      .update(rescueCases)
      .set({
        diagnosis: {
          ...asRecord(rescueCase.diagnosis),
          autoRepairBlocked:
            error instanceof Error ? error.message : "repair blocked",
        },
      })
      .where(eq(rescueCases.id, rescueCase.id));
  });
}

function caseEscalationText(rescueCase: RescueCaseRow) {
  const details = asRecord(rescueCase.triggerDetails);
  const reason =
    typeof details.reason === "string" ? details.reason : rescueCase.trigger;
  return [
    "Vectra Auto-Rescue escalation",
    `Case: ${rescueCase.id}`,
    `Router: ${rescueCase.routerId}`,
    `Trigger: ${rescueCase.trigger}`,
    `Reason: ${reason}`,
    "Automation did not converge within the fast rescue window. Open the guided cockpit before using terminal/manual lanes.",
  ].join("\n");
}

function caseRecoveredText(rescueCase: RescueCaseRow) {
  return [
    "Vectra Auto-Rescue recovered",
    `Case: ${rescueCase.id}`,
    `Router: ${rescueCase.routerId}`,
    `Trigger: ${rescueCase.trigger}`,
    "Router returned to healthy proxy/passwall state.",
  ].join("\n");
}

async function escalateRescueCase(
  database: DatabaseClient,
  rescueCase: RescueCaseRow,
  now: Date,
  options: { reason?: string } = {},
) {
  if (rescueCase.escalatedAt || rescueCase.resolvedAt) {
    return rescueCase;
  }

  const sendResult = await sendTelegramRescueMessage({
    caseId: rescueCase.id,
    text: caseEscalationText(rescueCase),
    includeButtons: true,
  }).catch((error: unknown) => ({
    attempted: false,
    delivered: 0,
    dryRun: false,
    reason: error instanceof Error ? error.message : "Telegram send failed",
  }));

  const [updated] = await database
    .update(rescueCases)
    .set({
      state: "escalated",
      escalatedAt: now,
      diagnosis: {
        ...asRecord(rescueCase.diagnosis),
        escalationReason: options.reason ?? "fast window expired",
        telegram: sendResult,
      },
    })
    .where(eq(rescueCases.id, rescueCase.id))
    .returning();

  return updated ?? rescueCase;
}

async function resolveRecoveredCases(database: DatabaseClient, now: Date) {
  const activeCases = await database
    .select()
    .from(rescueCases)
    .where(
      inArray(rescueCases.state, [
        "open",
        "repairing",
        "escalated",
        "silenced",
      ]),
    );

  let resolved = 0;
  for (const rescueCase of activeCases) {
    if (!isCaseActive(rescueCase, now)) {
      continue;
    }

    const [[router], [snapshot], openIncidentRows] = await Promise.all([
      database
        .select()
        .from(routers)
        .where(eq(routers.id, rescueCase.routerId))
        .limit(1),
      database
        .select()
        .from(routerInventorySnapshots)
        .where(eq(routerInventorySnapshots.routerId, rescueCase.routerId))
        .orderBy(desc(routerInventorySnapshots.createdAt))
        .limit(1),
      database
        .select()
        .from(healthIncidents)
        .where(
          and(
            eq(healthIncidents.routerId, rescueCase.routerId),
            eq(healthIncidents.state, "open"),
          ),
        ),
    ]);

    if (!router || !snapshot) {
      continue;
    }

    const hasBlockingIncident = openIncidentRows.some((incident) =>
      ["proxy_outage", "server_unreachable", "entered_direct_mode"].includes(
        incident.type,
      ),
    );
    const healthy =
      isRouterReachable(router.lastSeenAt, now) &&
      router.status === "active" &&
      snapshot.passwallEnabled &&
      !hasBlockingIncident;

    if (!healthy) {
      continue;
    }

    const [updated] = await database
      .update(rescueCases)
      .set({
        state: "resolved",
        resolvedAt: now,
        diagnosis: {
          ...asRecord(rescueCase.diagnosis),
          summary: "Router returned to healthy proxy/passwall state.",
          resolvedBy: "auto_rescue_monitor",
        },
      })
      .where(eq(rescueCases.id, rescueCase.id))
      .returning();

    if (updated) {
      resolved += 1;
      await sendTelegramRescueMessage({
        caseId: rescueCase.id,
        text: caseRecoveredText(rescueCase),
        includeButtons: false,
      }).catch(() => null);
    }
  }

  return resolved;
}

async function escalateExpiredCases(database: DatabaseClient, now: Date) {
  const cutoff = new Date(
    now.getTime() - env.VECTRA_AUTO_RESCUE_ESCALATION_SECONDS * 1000,
  );
  const cases = await database
    .select()
    .from(rescueCases)
    .where(
      and(
        inArray(rescueCases.state, ["open", "repairing"]),
        lte(rescueCases.startedAt, cutoff),
        isNull(rescueCases.resolvedAt),
      ),
    );

  let escalated = 0;
  for (const rescueCase of cases) {
    await escalateRescueCase(database, rescueCase, now);
    escalated += 1;
  }

  return escalated;
}

export async function runAutoRescueMonitorTick(
  now = new Date(),
  database: DatabaseClient = db,
) {
  if (!env.VECTRA_AUTO_RESCUE_ENABLED) {
    return {
      enabled: false,
      created: 0,
      queued: 0,
      resolved: 0,
      escalated: 0,
    };
  }

  const triggers = await detectCriticalTriggers(database, now);
  let created = 0;
  let queued = 0;
  for (const trigger of triggers) {
    const rescueCase = await ensureRescueCase(database, trigger, now);
    if (!rescueCase) {
      continue;
    }
    if (rescueCase.startedAt.getTime() === now.getTime()) {
      created += 1;
    }
    await queueInitialCaseWork(database, rescueCase, now);
    queued += 1;
  }

  const resolved = await resolveRecoveredCases(database, now);
  const escalated = await escalateExpiredCases(database, now);

  return {
    enabled: true,
    created,
    queued,
    resolved,
    escalated,
  };
}

const globalForAutoRescue = globalThis as typeof globalThis & {
  __vectraAutoRescueMonitorTimer?: ReturnType<typeof setInterval>;
  __vectraAutoRescueMonitorRunning?: boolean;
};

export function startAutoRescueMonitor() {
  if (env.NODE_ENV === "test" || !env.VECTRA_AUTO_RESCUE_ENABLED) {
    return;
  }

  if (globalForAutoRescue.__vectraAutoRescueMonitorTimer) {
    return;
  }

  const run = async () => {
    if (globalForAutoRescue.__vectraAutoRescueMonitorRunning) {
      return;
    }

    globalForAutoRescue.__vectraAutoRescueMonitorRunning = true;
    try {
      await runAutoRescueMonitorTick(new Date(), db);
    } catch (error) {
      console.error("[auto-rescue-monitor]", error);
    } finally {
      globalForAutoRescue.__vectraAutoRescueMonitorRunning = false;
    }
  };

  void run();

  globalForAutoRescue.__vectraAutoRescueMonitorTimer = setInterval(
    () => void run(),
    env.VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS * 1000,
  );
  globalForAutoRescue.__vectraAutoRescueMonitorTimer.unref?.();
}

export async function listRescueCases(database: DatabaseClient = db) {
  return database
    .select()
    .from(rescueCases)
    .orderBy(desc(rescueCases.startedAt))
    .limit(50);
}

export async function getActiveRescueCaseForRouter(
  routerId: string,
  database: DatabaseClient = db,
) {
  return loadActiveCaseForRouter(database, routerId);
}

export async function loadRescueCaseDetails(
  caseId: string,
  database: DatabaseClient = db,
) {
  const rescueCase = await getRescueCaseOrThrow(caseId, database);
  const [[router], jobRows, resultRows] = await Promise.all([
    database
      .select()
      .from(routers)
      .where(eq(routers.id, rescueCase.routerId))
      .limit(1),
    database
      .select()
      .from(jobs)
      .where(eq(jobs.routerId, rescueCase.routerId))
      .orderBy(desc(jobs.createdAt))
      .limit(16),
    database
      .select()
      .from(jobResults)
      .where(eq(jobResults.routerId, rescueCase.routerId))
      .orderBy(desc(jobResults.reportedAt))
      .limit(16),
  ]);

  return {
    case: rescueCase,
    router: router ?? null,
    jobs: jobRows,
    results: resultRows,
    managementTaskLog: buildRouterManagementTaskLog({
      jobs: jobRows,
      results: resultRows,
      installedControllerVersion: null,
    }),
  };
}

export async function appendRescueRepairAttemptFromJobResult(args: {
  routerId: string;
  caseId: string | null;
  resultPayload: Record<string, unknown>;
  status: "accepted" | "success" | "failure";
  database?: DatabaseClient;
}) {
  const database = args.database ?? db;
  const rescueCase = args.caseId
    ? await getRescueCaseOrThrow(args.caseId, database).catch(() => null)
    : await loadActiveCaseForRouter(database, args.routerId);
  if (!rescueCase) {
    return null;
  }

  const nextAttempts = [
    ...((Array.isArray(rescueCase.repairAttempts)
      ? rescueCase.repairAttempts
      : []) as Record<string, unknown>[]),
    {
      ...args.resultPayload,
      status: args.status,
      recordedAt: new Date().toISOString(),
    },
  ];

  const [updated] = await database
    .update(rescueCases)
    .set({
      repairAttempts: nextAttempts,
      lastAttemptAt: new Date(),
      diagnosis: {
        ...asRecord(rescueCase.diagnosis),
        lastRepairStatus: args.status,
        lastRepairError:
          typeof args.resultPayload.error === "string"
            ? args.resultPayload.error
            : null,
      },
    })
    .where(eq(rescueCases.id, rescueCase.id))
    .returning();

  return updated ?? rescueCase;
}
