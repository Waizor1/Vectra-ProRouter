import { createHash } from "node:crypto";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import {
  eventLog,
  healthIncidents,
  jobResults,
  jobs,
  passwallDesiredRevisions,
  routerInventorySnapshots,
  routerOnboardingProfiles,
  routerOnboardingRuns,
  routers,
  type RouterOnboardingRunStatus,
  type RouterOnboardingState,
} from "@vectra/db";
import { and, desc, eq, inArray, or } from "drizzle-orm";
import { z } from "zod";

import { env } from "~/env";
import {
  compareControllerVersions,
  resolveInstalledControllerVersion,
} from "~/lib/controller-version";
import {
  buildTerminalRouterHostnameUpdatePayload,
  normalizeRouterHostname,
} from "~/lib/router-hostname-jobs";
import { db } from "~/server/db";
import {
  evaluateFleetRoutePolicy,
  normalizeFleetRoutePolicy,
} from "~/server/vectra/fleet-route-policy";
import { isRouterReachable } from "~/server/vectra/router-presence";
import {
  createOperatorDraftRevisionWithDb,
  getFullConfigForRevisionWithDb,
} from "~/server/vectra/router-control";
import { describeEffectiveRouterSupport } from "~/server/vectra/support";

import { decryptJson, encryptJson } from "./secrets";

type DatabaseClient = typeof db;
type RouterRow = typeof routers.$inferSelect;
type SnapshotRow = typeof routerInventorySnapshots.$inferSelect;
type JobRow = typeof jobs.$inferSelect;
type JobResultRow = typeof jobResults.$inferSelect;
type IncidentRow = typeof healthIncidents.$inferSelect;
type ProfileRow = typeof routerOnboardingProfiles.$inferSelect;
type RunRow = typeof routerOnboardingRuns.$inferSelect;

type OnboardingJobState = "queued" | "delivered" | "running";

export type ProfileLike = Pick<
  ProfileRow,
  | "id"
  | "routerId"
  | "enabled"
  | "targetHostname"
  | "displayName"
  | "subscriptionSecretCiphertext"
  | "subscriptionUrlHash"
  | "subscriptionRemark"
  | "baseline"
  | "runtimePolicy"
  | "verifyPolicy"
  | "notes"
>;

export type RunLike = Pick<
  RunRow,
  | "id"
  | "routerId"
  | "profileId"
  | "state"
  | "status"
  | "attempt"
  | "lastJobId"
  | "activeRevisionId"
  | "lastError"
  | "nextRunAfter"
  | "completedAt"
>;

export type RouterLike = Pick<
  RouterRow,
  | "id"
  | "deviceIdentifier"
  | "displayName"
  | "hostname"
  | "boardName"
  | "target"
  | "architecture"
  | "openwrtRelease"
  | "status"
  | "importState"
  | "pendingImportRevisionId"
  | "activeRevisionId"
  | "approvedAt"
  | "lastSeenAt"
>;

export type SnapshotLike = Pick<SnapshotRow, "payload" | "createdAt">;

export type JobLike = Pick<
  JobRow,
  "id" | "type" | "state" | "dedupeKey" | "payload" | "desiredRevisionId"
>;

export type JobResultLike = Pick<
  JobResultRow,
  "status" | "payload" | "reportedAt"
>;

export type IncidentLike = Pick<
  IncidentRow,
  "id" | "type" | "state" | "reason"
>;

const activeJobStates: OnboardingJobState[] = [
  "queued",
  "delivered",
  "running",
];
export const onboardingRunResumeStatuses: RouterOnboardingRunStatus[] = [
  "running",
  "waiting",
  "blocked",
  "failed",
  "paused",
  "done",
];

const onboardingSubscriptionId = "vectra-onboarding-subscription";
const compactGeoipUrl =
  "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat";
const compactGeositeUrl =
  "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat";
const passwallAssetDirectory = "/usr/share/v2ray/";
const memoryAvailableFloorMb = 48;
const tmpFreeFloorMb = 8;
const overlayFreeFloorMb = 4;
const ensureRuntimeMemoryAvailableFloorMb = 64;
const ensureRuntimeTmpFreeFloorMb = 32;
const ensureRuntimeOverlayFreeFloorMb = 16;
const typedOnboardingJobMinControllerVersion = "0.1.13-r23";

const subscriptionSecretPayloadSchema = z.object({
  url: z.string().url(),
});

const onboardingAdvanceLocks = new Map<string, Promise<void>>();

async function withRouterOnboardingAdvanceLock<T>(
  routerId: string,
  run: () => Promise<T>,
) {
  const previous = onboardingAdvanceLocks.get(routerId) ?? Promise.resolve();
  let releaseCurrentLock: (() => void) | undefined;
  const current = new Promise<void>((resolve) => {
    releaseCurrentLock = resolve;
  });
  const next = previous.catch(() => undefined).then(() => current);
  onboardingAdvanceLocks.set(routerId, next);

  await previous.catch(() => undefined);
  try {
    return await run();
  } finally {
    releaseCurrentLock?.();
    if (onboardingAdvanceLocks.get(routerId) === next) {
      onboardingAdvanceLocks.delete(routerId);
    }
  }
}

export const routerOnboardingProfileInputSchema = z.object({
  routerId: z.string().uuid(),
  enabled: z.boolean().default(true),
  targetHostname: z.string().trim().min(1).max(63).nullable().optional(),
  displayName: z.string().trim().min(1).max(120).nullable().optional(),
  subscriptionUrl: z.string().trim().url().nullable().optional(),
  subscriptionRemark: z.string().trim().min(1).max(120).nullable().optional(),
  baseline: z
    .enum(["standard-non-hh", "hh-exempt", "subscription-only"])
    .default("standard-non-hh"),
  runtimePolicy: z
    .enum(["auto-minimal-passwall-xray", "controller-only"])
    .default("auto-minimal-passwall-xray"),
  verifyPolicy: z.enum(["route-smoke", "services-only"]).default("route-smoke"),
  notes: z.string().trim().max(1000).nullable().optional(),
});

export type RouterOnboardingProfileInput = z.infer<
  typeof routerOnboardingProfileInputSchema
>;

export type RouterOnboardingContext = {
  featureEnabled: boolean;
  router: RouterLike | null;
  profile: ProfileLike | null;
  run: RunLike | null;
  latestSnapshot: SnapshotLike | null;
  activeJobs: JobLike[];
  openIncidents: IncidentLike[];
  lastJob: JobLike | null;
  lastJobResult: JobResultLike | null;
  activeConfig: PasswallDesiredConfig | null;
  now: Date;
};

export type RouterOnboardingPlan =
  | { action: "skip"; reason: string }
  | { action: "create_run"; nextState: RouterOnboardingState; reason: string }
  | {
      action: "transition";
      nextState: RouterOnboardingState;
      status?: RouterOnboardingRunStatus;
      reason: string;
    }
  | {
      action: "wait";
      nextState: RouterOnboardingState;
      reason: string;
      lastJobId?: string | null;
    }
  | {
      action: "block";
      nextState: RouterOnboardingState;
      reason: string;
    }
  | { action: "fail"; nextState: RouterOnboardingState; reason: string }
  | {
      action: "request_import";
      nextState: RouterOnboardingState;
      reason: string;
    }
  | {
      action: "approve_import";
      revisionId: string;
      nextState: RouterOnboardingState;
      done?: boolean;
      reason: string;
    }
  | { action: "queue_hostname"; hostname: string; reason: string }
  | {
      action: "queue_ensure_runtime";
      actions: Array<"compact_geodata" | "dnsmasq_full">;
      reason: string;
    }
  | {
      action: "apply_subscription";
      config: PasswallDesiredConfig;
      reason: string;
    }
  | { action: "queue_refresh_subscriptions"; reason: string }
  | { action: "queue_route_verification"; reason: string }
  | {
      action: "apply_route_baseline";
      config: PasswallDesiredConfig;
      reason: string;
    }
  | { action: "mark_done"; reason: string };

export type RouterOnboardingAdvanceResult = {
  action: RouterOnboardingPlan["action"];
  routerId: string;
  runId: string | null;
  state: RouterOnboardingState | null;
  status: RouterOnboardingRunStatus | "skipped";
  reason: string;
  jobId?: string | null;
  revisionId?: string | null;
};

export function hashOnboardingSecret(value: string) {
  return createHash("sha256").update(value).digest("hex");
}

export function createSubscriptionSecretCiphertext(url: string) {
  return encryptJson(subscriptionSecretPayloadSchema.parse({ url }));
}

function readSubscriptionSecret(profile: ProfileLike) {
  if (!profile.subscriptionSecretCiphertext) {
    return null;
  }

  return subscriptionSecretPayloadSchema.parse(
    decryptJson(profile.subscriptionSecretCiphertext),
  );
}

export function sanitizeOnboardingProfileForClient(profile: ProfileRow | null) {
  if (!profile) {
    return null;
  }

  return {
    id: profile.id,
    routerId: profile.routerId,
    enabled: profile.enabled,
    targetHostname: profile.targetHostname,
    displayName: profile.displayName,
    hasSubscription: Boolean(profile.subscriptionSecretCiphertext),
    subscriptionUrlHash: profile.subscriptionUrlHash,
    subscriptionRemark: profile.subscriptionRemark,
    baseline: profile.baseline,
    runtimePolicy: profile.runtimePolicy,
    verifyPolicy: profile.verifyPolicy,
    notes: profile.notes,
    createdAt: profile.createdAt,
    updatedAt: profile.updatedAt,
  };
}

export function sanitizeOnboardingRunForClient(run: RunRow | null) {
  if (!run) {
    return null;
  }

  return {
    id: run.id,
    routerId: run.routerId,
    profileId: run.profileId,
    state: run.state,
    status: run.status,
    attempt: run.attempt,
    lastJobId: run.lastJobId,
    activeRevisionId: run.activeRevisionId,
    lastError: run.lastError,
    nextRunAfter: run.nextRunAfter,
    completedAt: run.completedAt,
    createdAt: run.createdAt,
    updatedAt: run.updatedAt,
  };
}

function onboardingDedupePrefix(runId: string) {
  return `onboarding:${runId}:`;
}

export function onboardingAttemptDedupePrefix(runId: string, attempt: number) {
  return `onboarding:${runId}:attempt:${attempt}:`;
}

function isOnboardingJobForRun(job: JobLike, runId: string) {
  return job.dedupeKey?.startsWith(onboardingDedupePrefix(runId)) ?? false;
}

function installedControllerVersion(ctx: RouterOnboardingContext) {
  return resolveInstalledControllerVersion({
    controllerVersion: ctx.latestSnapshot?.payload?.controllerVersion ?? null,
    payload: ctx.latestSnapshot?.payload ?? null,
  });
}

function controllerMeetsMinimumVersion(
  ctx: RouterOnboardingContext,
  minimumVersion: string,
) {
  const currentVersion = installedControllerVersion(ctx);
  const comparison = compareControllerVersions(currentVersion, minimumVersion);
  return comparison !== null && comparison >= 0;
}

function typedOnboardingJobBlockReason(
  ctx: RouterOnboardingContext,
  jobLabel: string,
) {
  const currentVersion = installedControllerVersion(ctx) ?? "unknown";
  return `controller ${currentVersion} does not support ${jobLabel}; update vectra-controller-agent/LuCI to ${typedOnboardingJobMinControllerVersion}+ before retrying`;
}

function readRouterHostname(router: RouterLike, snapshot: SnapshotLike | null) {
  return snapshot?.payload.hostname ?? router.hostname ?? "";
}

function resourceBlockReason(snapshot: SnapshotLike | null) {
  const resources = snapshot?.payload.resources;
  if (!resources) {
    return "latest router resources are unknown";
  }

  if (resources.memoryAvailableMb < memoryAvailableFloorMb) {
    return `available RAM ${resources.memoryAvailableMb} MB is below ${memoryAvailableFloorMb} MB floor`;
  }

  if (resources.tmpFreeMb < tmpFreeFloorMb) {
    return `/tmp free ${resources.tmpFreeMb} MB is below ${tmpFreeFloorMb} MB floor`;
  }

  if (resources.overlayFreeMb < overlayFreeFloorMb) {
    return `/overlay free ${resources.overlayFreeMb} MB is below ${overlayFreeFloorMb} MB floor`;
  }

  const criticalSafetyEvent = snapshot.payload.safetyEvents?.find(
    (event) =>
      event.severity === "critical" &&
      ["low_memory", "low_overlay", "low_tmp", "oom_kill"].includes(event.type),
  );
  if (criticalSafetyEvent) {
    return `latest safety event ${criticalSafetyEvent.type} is critical`;
  }

  return null;
}

function runtimeIsHealthy(snapshot: SnapshotLike | null) {
  const health = snapshot?.payload.serviceHealth;
  if (!health) {
    return false;
  }

  return (
    health.controller === "running" &&
    health.passwall === "running" &&
    health.passwallServer === "running" &&
    health.dnsmasq === "running"
  );
}

function hasSubscriptionInConfig(
  config: PasswallDesiredConfig | null,
  profile: ProfileLike,
) {
  if (!config || !profile.subscriptionUrlHash) {
    return false;
  }

  return config.subscriptions.items.some(
    (item) => hashOnboardingSecret(item.url) === profile.subscriptionUrlHash,
  );
}

function withOnboardingSubscription(
  config: PasswallDesiredConfig,
  profile: ProfileLike,
) {
  const secret = readSubscriptionSecret(profile);
  if (!secret) {
    return null;
  }

  const existing = config.subscriptions.items.find(
    (item) => item.id === onboardingSubscriptionId,
  );
  const nextItems = config.subscriptions.items.filter(
    (item) => item.id !== onboardingSubscriptionId,
  );

  return passwallDesiredConfigSchema.parse({
    ...config,
    subscriptions: {
      ...config.subscriptions,
      items: [
        {
          id: onboardingSubscriptionId,
          remark:
            profile.subscriptionRemark ??
            existing?.remark ??
            "Vectra onboarding subscription",
          url: secret.url,
          enabled: true,
          addMode: "2",
          metadata: existing?.metadata ?? {},
          extras: {
            ...(existing?.extras ?? {}),
            auto_update: "1",
            vectra_onboarding: "1",
          },
        },
        ...nextItems,
      ],
    },
  });
}

function routePolicyIdentity(router: RouterLike, profile: ProfileLike) {
  return {
    id: router.id,
    name: profile.displayName ?? router.displayName ?? router.hostname,
    displayName: profile.displayName ?? router.displayName,
    hostname: router.hostname,
    deviceIdentifier: router.deviceIdentifier,
  };
}

function planCompletedLastJob(ctx: RouterOnboardingContext) {
  const run = ctx.run;
  if (!run?.lastJobId || ctx.lastJob?.id !== run.lastJobId) {
    return null;
  }

  if (activeJobStates.includes(ctx.lastJob.state as OnboardingJobState)) {
    return {
      action: "wait" as const,
      nextState: run.state,
      reason: `waiting for onboarding job ${ctx.lastJob.type}`,
      lastJobId: ctx.lastJob.id,
    };
  }

  if (ctx.lastJob.state === "failed" || ctx.lastJob.state === "cancelled") {
    return {
      action: "fail" as const,
      nextState: run.state,
      reason: `onboarding job ${ctx.lastJob.type} ${ctx.lastJob.state}`,
    };
  }

  if (ctx.lastJob.state !== "succeeded") {
    return null;
  }

  switch (run.state) {
    case "rename_router":
      return {
        action: "transition" as const,
        nextState: "ensure_runtime" as const,
        reason: "hostname job succeeded",
      };
    case "ensure_runtime":
      if (ctx.lastJob.type !== "ensure_passwall_runtime") {
        return null;
      }
      if (!runtimeEnsureSucceeded(ctx.lastJobResult)) {
        return {
          action: "block" as const,
          nextState: "ensure_runtime" as const,
          reason: "typed runtime repair finished without green proof",
        };
      }
      return {
        action: "transition" as const,
        nextState: "apply_subscription" as const,
        reason: "typed runtime repair succeeded",
      };
    case "apply_subscription":
      return {
        action: "transition" as const,
        nextState: "refresh_subscription" as const,
        reason: "subscription apply job succeeded",
      };
    case "refresh_subscription":
      return {
        action: "request_import" as const,
        nextState: "resolve_route_baseline" as const,
        reason: "subscription refresh succeeded; requesting live import",
      };
    case "apply_route_baseline":
      return {
        action: "transition" as const,
        nextState: "verify_runtime" as const,
        reason: "route baseline apply job succeeded",
      };
    case "verify_runtime":
      if (ctx.lastJob.type !== "verify_passwall_routes") {
        return null;
      }
      if (!routeVerificationSucceeded(ctx.lastJobResult)) {
        return {
          action: "block" as const,
          nextState: "verify_runtime" as const,
          reason:
            "typed route verifier finished without green route-smoke proof",
        };
      }
      return {
        action: "request_import" as const,
        nextState: "final_reimport" as const,
        reason: "typed route verifier returned green route-smoke proof",
      };
    default:
      return null;
  }
}

function routeVerificationSucceeded(result: JobResultLike | null) {
  if (result?.status !== "success") {
    return false;
  }
  const payload = result?.payload;
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return false;
  }
  if (payload.ok !== true) {
    return false;
  }
  const slots = payload.slots;
  return (
    Array.isArray(slots) &&
    slots.length >= 5 &&
    slots.every((slot) => {
      if (!slot || typeof slot !== "object" || Array.isArray(slot)) {
        return false;
      }
      const slotRecord = slot as Record<string, unknown>;
      return (
        slotRecord.bindingOk === true &&
        slotRecord.ruleExtrasOk === true &&
        slotRecord.nodeExtrasOk === true &&
        slotRecord.smokeOk === true &&
        slotRecord.statusCode === 204
      );
    })
  );
}

function runtimeEnsureSucceeded(result: JobResultLike | null) {
  if (result?.status !== "success") {
    return false;
  }
  const payload = result?.payload;
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return false;
  }
  const record = payload;
  if (record.ok !== true) {
    return false;
  }

  const actions = record.actions;
  if (
    !Array.isArray(actions) ||
    actions.length === 0 ||
    !actions.every((action) => {
      if (!action || typeof action !== "object" || Array.isArray(action)) {
        return false;
      }
      return (action as Record<string, unknown>).status === "success";
    })
  ) {
    return false;
  }

  const services = record.services;
  if (!services || typeof services !== "object" || Array.isArray(services)) {
    return false;
  }
  const serviceRecord = services as Record<string, unknown>;
  if (
    serviceRecord.controller !== "running" ||
    serviceRecord.passwall !== "running" ||
    serviceRecord.passwallServer !== "running" ||
    serviceRecord.dnsmasq !== "running"
  ) {
    return false;
  }

  const resources = record.resources;
  if (!resources || typeof resources !== "object" || Array.isArray(resources)) {
    return false;
  }
  const resourceRecord = resources as Record<string, unknown>;
  return (
    Number(resourceRecord.memoryAvailableMb) >=
      ensureRuntimeMemoryAvailableFloorMb &&
    Number(resourceRecord.overlayFreeMb) >= ensureRuntimeOverlayFreeFloorMb &&
    Number(resourceRecord.tmpFreeMb) >= ensureRuntimeTmpFreeFloorMb
  );
}

function planRuntimeEnsureAction(ctx: RouterOnboardingContext):
  | {
      action: "queue_ensure_runtime";
      actions: Array<"compact_geodata" | "dnsmasq_full">;
      reason: string;
    }
  | { action: "block"; nextState: RouterOnboardingState; reason: string }
  | { action: "transition"; nextState: RouterOnboardingState; reason: string } {
  if (ctx.profile?.runtimePolicy === "controller-only") {
    return {
      action: "block",
      nextState: "ensure_runtime",
      reason:
        "runtime policy is controller-only; PassWall/Xray convergence requires manual takeover",
    };
  }

  const snapshot = ctx.latestSnapshot?.payload;
  if (!snapshot) {
    return {
      action: "block",
      nextState: "ensure_runtime",
      reason: "latest router inventory is missing before runtime repair",
    };
  }

  const packageVersions = snapshot.packageVersions ?? {};
  const binaryVersions = snapshot.binaryVersions ?? {};
  const serviceHealth = snapshot.serviceHealth;
  const passwallKnown =
    Boolean(packageVersions["luci-app-passwall2"]) ||
    (serviceHealth?.passwall && serviceHealth.passwall !== "unknown");
  const xrayKnown =
    Boolean(packageVersions["xray-core"]) || Boolean(binaryVersions.xray);

  if (!passwallKnown || !xrayKnown) {
    return {
      action: "block",
      nextState: "ensure_runtime",
      reason:
        "core PassWall/Xray runtime is absent; refusing blind full-stack install during onboarding",
    };
  }

  const actions: Array<"compact_geodata" | "dnsmasq_full"> = [];
  const rulesAssets = snapshot.rulesAssets ?? {};
  if (!rulesAssets.geoipVersion || !rulesAssets.geositeVersion) {
    actions.push("compact_geodata");
  }
  if (!packageVersions["dnsmasq-full"]) {
    actions.push("dnsmasq_full");
  }

  if (actions.length === 0) {
    return {
      action: "transition",
      nextState: "apply_subscription",
      reason: "minimal PassWall/Xray runtime is already present",
    };
  }

  if (
    !controllerMeetsMinimumVersion(
      ctx,
      typedOnboardingJobMinControllerVersion,
    )
  ) {
    return {
      action: "block",
      nextState: "ensure_runtime",
      reason: typedOnboardingJobBlockReason(
        ctx,
        "ensure_passwall_runtime",
      ),
    };
  }

  return {
    action: "queue_ensure_runtime",
    actions,
    reason: `queueing typed runtime repair: ${actions.join(", ")}`,
  };
}

export function planNextOnboardingAction(
  ctx: RouterOnboardingContext,
): RouterOnboardingPlan {
  if (!ctx.featureEnabled) {
    return { action: "skip", reason: "feature flag disabled" };
  }

  if (!ctx.router) {
    return { action: "skip", reason: "router not found" };
  }

  if (!ctx.profile?.enabled) {
    return { action: "skip", reason: "onboarding profile disabled or absent" };
  }

  if (!ctx.run) {
    return {
      action: "create_run",
      nextState: "created",
      reason: "profile is enabled and no active run exists",
    };
  }

  if (ctx.run.status === "done" || ctx.run.status === "paused") {
    return {
      action: "skip",
      reason: `onboarding run is ${ctx.run.status}`,
    };
  }

  const completedLastJobPlan = planCompletedLastJob(ctx);
  if (completedLastJobPlan) {
    return completedLastJobPlan;
  }

  const activeOwnJob = ctx.activeJobs.find((job) =>
    isOnboardingJobForRun(job, ctx.run!.id),
  );
  if (activeOwnJob) {
    return {
      action: "wait",
      nextState: ctx.run.state,
      reason: `waiting for onboarding job ${activeOwnJob.type}`,
      lastJobId: activeOwnJob.id,
    };
  }

  const unrelatedActiveJob = ctx.activeJobs.find(
    (job) => !isOnboardingJobForRun(job, ctx.run!.id),
  );
  if (unrelatedActiveJob) {
    return {
      action: "wait",
      nextState: ctx.run.state,
      reason: `waiting for unrelated active job ${unrelatedActiveJob.type}`,
      lastJobId: unrelatedActiveJob.id,
    };
  }

  if (!isRouterReachable(ctx.router.lastSeenAt, ctx.now)) {
    return {
      action: "wait",
      nextState: ctx.run.state,
      reason: "router is offline or has no recent check-in",
    };
  }

  const support = describeEffectiveRouterSupport({
    router: ctx.router,
    inventory: ctx.latestSnapshot?.payload ?? null,
  });
  if (support.state === "blocked") {
    return {
      action: "block",
      nextState: ctx.run.state,
      reason: `unsupported router: ${support.reason}`,
    };
  }

  if (ctx.router.status === "direct" || ctx.router.status === "rescue") {
    return {
      action: "block",
      nextState: ctx.run.state,
      reason: `router is in ${ctx.router.status} mode`,
    };
  }

  if (ctx.openIncidents.length > 0) {
    return {
      action: "block",
      nextState: ctx.run.state,
      reason: `router has open incident ${ctx.openIncidents[0]!.type}`,
    };
  }

  const resourceReason = resourceBlockReason(ctx.latestSnapshot);
  if (resourceReason) {
    return {
      action: "block",
      nextState: ctx.run.state,
      reason: resourceReason,
    };
  }

  switch (ctx.run.state) {
    case "created":
      return {
        action: "transition",
        nextState: "preflight",
        reason: "starting preflight",
      };
    case "preflight":
    case "request_initial_import": {
      if (
        ctx.run.state === "request_initial_import" &&
        ctx.router.importState === "awaiting_import"
      ) {
        return {
          action: "wait",
          nextState: ctx.run.state,
          reason: "waiting for initial live PassWall import",
        };
      }

      if (
        ctx.router.importState === "import_review" &&
        ctx.router.pendingImportRevisionId
      ) {
        return {
          action: "approve_import",
          revisionId: ctx.router.pendingImportRevisionId,
          nextState: "rename_router",
          reason: "approving first live import owned by onboarding",
        };
      }

      if (ctx.router.importState === "out_of_sync") {
        return {
          action: "block",
          nextState: ctx.run.state,
          reason: "router has conflicting out-of-sync import",
        };
      }

      if (
        !ctx.router.activeRevisionId ||
        ctx.router.importState === "awaiting_import"
      ) {
        return {
          action: "request_import",
          nextState: "request_initial_import",
          reason: "requesting initial live PassWall import",
        };
      }

      return {
        action: "transition",
        nextState: "rename_router",
        reason: "initial import is already approved",
      };
    }
    case "approve_initial_import":
      if (ctx.router.pendingImportRevisionId) {
        return {
          action: "approve_import",
          revisionId: ctx.router.pendingImportRevisionId,
          nextState: "rename_router",
          reason: "approving pending import",
        };
      }
      return {
        action: "transition",
        nextState: "rename_router",
        reason: "no pending import remains",
      };
    case "rename_router": {
      const targetHostname = ctx.profile.targetHostname
        ? normalizeRouterHostname(ctx.profile.targetHostname)
        : null;
      const currentHostname = normalizeRouterHostname(
        readRouterHostname(ctx.router, ctx.latestSnapshot),
      );
      if (targetHostname && targetHostname !== currentHostname) {
        return {
          action: "queue_hostname",
          hostname: targetHostname,
          reason: `queueing hostname update to ${targetHostname}`,
        };
      }
      return {
        action: "transition",
        nextState: "ensure_runtime",
        reason: "hostname is already aligned or not requested",
      };
    }
    case "ensure_runtime":
      if (ctx.profile.baseline === "hh-exempt") {
        return {
          action: "transition",
          nextState: "verify_runtime",
          reason: "hh-exempt profile skips subscription and route baseline",
        };
      }
      return planRuntimeEnsureAction(ctx);
    case "apply_subscription": {
      if (!ctx.profile.subscriptionSecretCiphertext) {
        return {
          action: "block",
          nextState: ctx.run.state,
          reason: "onboarding profile has no subscription secret",
        };
      }
      if (!ctx.activeConfig) {
        return {
          action: "request_import",
          nextState: "request_initial_import",
          reason: "active PassWall config is missing before subscription apply",
        };
      }
      if (hasSubscriptionInConfig(ctx.activeConfig, ctx.profile)) {
        return {
          action: "transition",
          nextState: "refresh_subscription",
          reason: "subscription is already present in authoritative config",
        };
      }
      const config = withOnboardingSubscription(ctx.activeConfig, ctx.profile);
      if (!config) {
        return {
          action: "block",
          nextState: ctx.run.state,
          reason: "onboarding profile has no readable subscription secret",
        };
      }
      return {
        action: "apply_subscription",
        config,
        reason: "creating subscription-only desired revision",
      };
    }
    case "refresh_subscription":
      return {
        action: "queue_refresh_subscriptions",
        reason: "queueing native PassWall subscription refresh",
      };
    case "resolve_route_baseline": {
      if (
        (ctx.router.importState === "import_review" ||
          ctx.router.importState === "out_of_sync") &&
        ctx.router.pendingImportRevisionId
      ) {
        return {
          action: "approve_import",
          revisionId: ctx.router.pendingImportRevisionId,
          nextState: "apply_route_baseline",
          reason: "approving post-subscription live import owned by onboarding",
        };
      }

      if (ctx.router.importState === "awaiting_import") {
        return {
          action: "wait",
          nextState: ctx.run.state,
          reason: "waiting for post-subscription live import",
        };
      }

      return {
        action: "transition",
        nextState: "apply_route_baseline",
        reason: "post-subscription import is already authoritative",
      };
    }
    case "apply_route_baseline": {
      if (!ctx.activeConfig) {
        return {
          action: "wait",
          nextState: ctx.run.state,
          reason: "waiting for active config before route normalization",
        };
      }

      if (ctx.profile.baseline === "subscription-only") {
        return {
          action: "transition",
          nextState: "verify_runtime",
          reason: "subscription-only profile skips route normalization",
        };
      }

      const normalized = normalizeFleetRoutePolicy(
        ctx.activeConfig,
        routePolicyIdentity(ctx.router, ctx.profile),
      );
      if (normalized.before.status === "exempt") {
        return {
          action: "transition",
          nextState: "verify_runtime",
          reason: "router is exempt from standard route policy",
        };
      }
      if (
        normalized.before.status === "violation" &&
        !normalized.before.canNormalize
      ) {
        return {
          action: "block",
          nextState: ctx.run.state,
          reason: "route baseline cannot resolve enough live nodes",
        };
      }
      if (!normalized.changed) {
        return {
          action: "transition",
          nextState: "verify_runtime",
          reason: "route baseline is already aligned",
        };
      }
      return {
        action: "apply_route_baseline",
        config: normalized.config,
        reason: "creating normalized fleet route baseline revision",
      };
    }
    case "verify_runtime": {
      if (!runtimeIsHealthy(ctx.latestSnapshot)) {
        return {
          action: "block",
          nextState: ctx.run.state,
          reason: "latest service-health snapshot is not fully running",
        };
      }

      if (ctx.profile.baseline === "standard-non-hh") {
        const compliance = evaluateFleetRoutePolicy(
          ctx.activeConfig,
          routePolicyIdentity(ctx.router, ctx.profile),
        );
        if (compliance.status !== "compliant") {
          return {
            action: "block",
            nextState: ctx.run.state,
            reason: `route policy is ${compliance.status}: ${compliance.summary}`,
          };
        }
      }

      if (ctx.profile.verifyPolicy === "route-smoke") {
        if (ctx.profile.baseline !== "standard-non-hh") {
          return {
            action: "block",
            nextState: ctx.run.state,
            reason:
              "typed route-smoke verifier currently supports only the standard non-hh baseline",
          };
        }
        if (
          !controllerMeetsMinimumVersion(
            ctx,
            typedOnboardingJobMinControllerVersion,
          )
        ) {
          return {
            action: "block",
            nextState: ctx.run.state,
            reason: typedOnboardingJobBlockReason(
              ctx,
              "verify_passwall_routes",
            ),
          };
        }
        return {
          action: "queue_route_verification",
          reason: "queueing typed PassWall route-smoke verifier",
        };
      }

      return {
        action: "request_import",
        nextState: "final_reimport",
        reason: "runtime services are green; requesting final live import",
      };
    }
    case "final_reimport": {
      if (
        (ctx.router.importState === "import_review" ||
          ctx.router.importState === "out_of_sync") &&
        ctx.router.pendingImportRevisionId
      ) {
        return {
          action: "approve_import",
          revisionId: ctx.router.pendingImportRevisionId,
          nextState: "done",
          done: true,
          reason: "approving final live import owned by onboarding",
        };
      }
      if (ctx.router.importState === "awaiting_import") {
        return {
          action: "wait",
          nextState: ctx.run.state,
          reason: "waiting for final live import",
        };
      }
      return {
        action: "mark_done",
        reason: "final live state is already approved",
      };
    }
    case "repair_runtime":
      return {
        action: "block",
        nextState: ctx.run.state,
        reason: "typed runtime repair job is not implemented in this MVP",
      };
    case "done":
      return { action: "skip", reason: "onboarding already done" };
  }
}

async function insertEvent(
  client: DatabaseClient,
  input: {
    routerId: string;
    type: string;
    severity?: "info" | "warning" | "critical";
    message: string;
    metadata?: Record<string, unknown>;
  },
) {
  await client.insert(eventLog).values({
    routerId: input.routerId,
    type: input.type,
    severity: input.severity ?? "info",
    message: input.message,
    metadata: input.metadata ?? {},
  });
}

async function updateRun(
  client: DatabaseClient,
  run: RunLike,
  patch: Partial<typeof routerOnboardingRuns.$inferInsert>,
) {
  const [updatedRun] = await client
    .update(routerOnboardingRuns)
    .set({ ...patch, updatedAt: new Date() })
    .where(eq(routerOnboardingRuns.id, run.id))
    .returning();

  return updatedRun ?? null;
}

async function approveImportWithDb(
  client: DatabaseClient,
  router: RouterLike,
  revisionId: string,
) {
  const [revision] = await client
    .select()
    .from(passwallDesiredRevisions)
    .where(eq(passwallDesiredRevisions.id, revisionId))
    .limit(1);

  if (revision?.routerId !== router.id) {
    throw new Error(
      "Imported baseline revision was not found for this router.",
    );
  }

  const approvedAt = new Date();
  await client
    .update(passwallDesiredRevisions)
    .set({ status: "approved", approvedAt })
    .where(eq(passwallDesiredRevisions.id, revision.id));

  await client
    .update(routers)
    .set({
      approvedAt: router.approvedAt ?? approvedAt,
      importState: "approved",
      activeRevisionId: revision.id,
      pendingImportRevisionId: null,
      lastConfigDigest: revision.configDigest,
      status: router.status === "direct" ? "direct" : "active",
    })
    .where(eq(routers.id, router.id));

  await insertEvent(client, {
    routerId: router.id,
    type: "router.onboarding.import.approved",
    message: "Auto-onboarding approved a run-owned live PassWall2 import.",
    metadata: { revisionId: revision.id },
  });

  return revision;
}

async function queueOnboardingApplyJob(
  client: DatabaseClient,
  input: {
    routerId: string;
    runId: string;
    attempt: number;
    revisionId: string;
    purpose: string;
  },
) {
  const dedupeKey = `${onboardingAttemptDedupePrefix(input.runId, input.attempt)}apply:${input.purpose}:${input.revisionId}`;
  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, activeJobStates),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await client
    .insert(jobs)
    .values({
      routerId: input.routerId,
      type: "apply_passwall_config",
      state: "queued",
      dedupeKey,
      desiredRevisionId: input.revisionId,
      payload: {
        desiredRevisionId: input.revisionId,
        onboardingRunId: input.runId,
        purpose: input.purpose,
      },
    })
    .returning();

  await client
    .update(passwallDesiredRevisions)
    .set({ status: "queued" })
    .where(eq(passwallDesiredRevisions.id, input.revisionId));

  return job ?? null;
}

async function queueHostnameJob(
  client: DatabaseClient,
  input: { routerId: string; runId: string; attempt: number; hostname: string },
) {
  const dedupeKey = `${onboardingAttemptDedupePrefix(input.runId, input.attempt)}hostname:${input.hostname}`;
  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, activeJobStates),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await client
    .insert(jobs)
    .values({
      routerId: input.routerId,
      type: "run_terminal_command",
      state: "queued",
      dedupeKey,
      payload: {
        ...buildTerminalRouterHostnameUpdatePayload(input.hostname),
        onboardingRunId: input.runId,
      },
    })
    .returning();

  return job ?? null;
}

async function queueEnsureRuntimeJob(
  client: DatabaseClient,
  input: {
    routerId: string;
    runId: string;
    attempt: number;
    actions: Array<"compact_geodata" | "dnsmasq_full">;
  },
) {
  const dedupeKey = `${onboardingAttemptDedupePrefix(input.runId, input.attempt)}ensure_passwall_runtime:${input.actions.join("+")}`;
  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, activeJobStates),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await client
    .insert(jobs)
    .values({
      routerId: input.routerId,
      type: "ensure_passwall_runtime",
      state: "queued",
      dedupeKey,
      payload: {
        onboardingRunId: input.runId,
        actions: input.actions,
        assetDirectory: passwallAssetDirectory,
        geoipUrl: compactGeoipUrl,
        geositeUrl: compactGeositeUrl,
        resourceFloors: {
          memoryAvailableMb: ensureRuntimeMemoryAvailableFloorMb,
          overlayFreeMb: ensureRuntimeOverlayFreeFloorMb,
          tmpFreeMb: ensureRuntimeTmpFreeFloorMb,
        },
      },
    })
    .returning();

  return job ?? null;
}

async function queueRefreshSubscriptionsJob(
  client: DatabaseClient,
  input: { routerId: string; runId: string; attempt: number },
) {
  const dedupeKey = `${onboardingAttemptDedupePrefix(input.runId, input.attempt)}refresh_subscriptions`;
  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, activeJobStates),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await client
    .insert(jobs)
    .values({
      routerId: input.routerId,
      type: "refresh_subscriptions",
      state: "queued",
      dedupeKey,
      payload: { onboardingRunId: input.runId },
    })
    .returning();

  return job ?? null;
}

async function queueRouteVerificationJob(
  client: DatabaseClient,
  input: { routerId: string; runId: string; attempt: number },
) {
  const dedupeKey = `${onboardingAttemptDedupePrefix(input.runId, input.attempt)}verify_passwall_routes`;
  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, activeJobStates),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await client
    .insert(jobs)
    .values({
      routerId: input.routerId,
      type: "verify_passwall_routes",
      state: "queued",
      dedupeKey,
      payload: {
        onboardingRunId: input.runId,
        expectedPolicy: "standard-non-hh",
      },
    })
    .returning();

  return job ?? null;
}

async function loadOnboardingContext(
  client: DatabaseClient,
  routerId: string,
  featureEnabled: boolean,
  now: Date,
): Promise<RouterOnboardingContext> {
  const [
    [router],
    [profile],
    [run],
    [latestSnapshot],
    activeJobs,
    openIncidents,
  ] = await Promise.all([
    client.select().from(routers).where(eq(routers.id, routerId)).limit(1),
    client
      .select()
      .from(routerOnboardingProfiles)
      .where(eq(routerOnboardingProfiles.routerId, routerId))
      .limit(1),
    client
      .select()
      .from(routerOnboardingRuns)
      .where(
        and(
          eq(routerOnboardingRuns.routerId, routerId),
          inArray(routerOnboardingRuns.status, onboardingRunResumeStatuses),
        ),
      )
      .orderBy(desc(routerOnboardingRuns.createdAt))
      .limit(1),
    client
      .select()
      .from(routerInventorySnapshots)
      .where(eq(routerInventorySnapshots.routerId, routerId))
      .orderBy(desc(routerInventorySnapshots.createdAt))
      .limit(1),
    client
      .select()
      .from(jobs)
      .where(
        and(eq(jobs.routerId, routerId), inArray(jobs.state, activeJobStates)),
      )
      .orderBy(desc(jobs.createdAt))
      .limit(10),
    client
      .select()
      .from(healthIncidents)
      .where(
        and(
          eq(healthIncidents.routerId, routerId),
          eq(healthIncidents.state, "open"),
        ),
      )
      .orderBy(desc(healthIncidents.openedAt))
      .limit(10),
  ]);

  const [lastJob, lastJobResult] = run?.lastJobId
    ? await Promise.all([
        client
          .select()
          .from(jobs)
          .where(eq(jobs.id, run.lastJobId))
          .limit(1)
          .then((rows) => rows[0] ?? null),
        client
          .select()
          .from(jobResults)
          .where(eq(jobResults.jobId, run.lastJobId))
          .orderBy(desc(jobResults.reportedAt))
          .limit(1)
          .then((rows) => rows[0] ?? null),
      ])
    : [null, null];

  const activeConfig = router?.activeRevisionId
    ? await getFullConfigForRevisionWithDb(client, router.activeRevisionId)
    : null;

  return {
    featureEnabled,
    router: router ?? null,
    profile: profile ?? null,
    run: run ?? null,
    latestSnapshot: latestSnapshot ?? null,
    activeJobs,
    openIncidents,
    lastJob,
    lastJobResult,
    activeConfig,
    now,
  };
}

async function executePlan(
  client: DatabaseClient,
  ctx: RouterOnboardingContext,
  plan: RouterOnboardingPlan,
): Promise<RouterOnboardingAdvanceResult & { continue: boolean }> {
  const routerId = ctx.router?.id ?? ctx.profile?.routerId ?? "unknown";
  const runId = ctx.run?.id ?? null;

  if (plan.action === "skip") {
    return {
      action: plan.action,
      routerId,
      runId,
      state: ctx.run?.state ?? null,
      status: "skipped",
      reason: plan.reason,
      continue: false,
    };
  }

  if (!ctx.router || !ctx.profile) {
    return {
      action: "skip",
      routerId,
      runId,
      state: ctx.run?.state ?? null,
      status: "skipped",
      reason: "router/profile context is missing",
      continue: false,
    };
  }

  if (plan.action === "create_run") {
    const [run] = await client
      .insert(routerOnboardingRuns)
      .values({
        routerId: ctx.router.id,
        profileId: ctx.profile.id,
        state: plan.nextState,
        status: "running",
      })
      .returning();

    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.started",
      message: "Auto-onboarding run started.",
      metadata: {
        runId: run?.id ?? null,
        profileId: ctx.profile.id,
        baseline: ctx.profile.baseline,
      },
    });

    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: run?.id ?? null,
      state: run?.state ?? plan.nextState,
      status: run?.status ?? "running",
      reason: plan.reason,
      continue: true,
    };
  }

  if (!ctx.run) {
    return {
      action: "skip",
      routerId: ctx.router.id,
      runId: null,
      state: null,
      status: "skipped",
      reason: "active run is missing",
      continue: false,
    };
  }

  if (plan.action === "wait") {
    await updateRun(client, ctx.run, {
      state: plan.nextState,
      status: "waiting",
      lastError: plan.reason,
      lastJobId: plan.lastJobId ?? ctx.run.lastJobId,
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: plan.nextState,
      status: "waiting",
      reason: plan.reason,
      jobId: plan.lastJobId ?? ctx.run.lastJobId,
      continue: false,
    };
  }

  if (plan.action === "block" || plan.action === "fail") {
    const status = plan.action === "block" ? "blocked" : "failed";
    await updateRun(client, ctx.run, {
      state: plan.nextState,
      status,
      lastError: plan.reason,
      attempt: ctx.run.attempt + 1,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type:
        plan.action === "block"
          ? "router.onboarding.blocked"
          : "router.onboarding.failed",
      severity: "warning",
      message: plan.reason,
      metadata: { runId: ctx.run.id, state: plan.nextState },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: plan.nextState,
      status,
      reason: plan.reason,
      continue: false,
    };
  }

  if (plan.action === "transition") {
    await updateRun(client, ctx.run, {
      state: plan.nextState,
      status: plan.status ?? "running",
      lastError: null,
      lastJobId: null,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.transitioned",
      message: `Auto-onboarding moved to ${plan.nextState}.`,
      metadata: {
        runId: ctx.run.id,
        state: plan.nextState,
        reason: plan.reason,
      },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: plan.nextState,
      status: plan.status ?? "running",
      reason: plan.reason,
      continue: true,
    };
  }

  if (plan.action === "request_import") {
    await client
      .update(routers)
      .set({ importState: "awaiting_import" })
      .where(eq(routers.id, ctx.router.id));
    await updateRun(client, ctx.run, {
      state: plan.nextState,
      status: "waiting",
      lastError: plan.reason,
      lastJobId: null,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.import_requested",
      message: "Auto-onboarding requested a fresh live PassWall import.",
      metadata: { runId: ctx.run.id, state: plan.nextState },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: plan.nextState,
      status: "waiting",
      reason: plan.reason,
      continue: false,
    };
  }

  if (plan.action === "approve_import") {
    const revision = await approveImportWithDb(
      client,
      ctx.router,
      plan.revisionId,
    );
    const status = plan.done ? "done" : "running";
    await updateRun(client, ctx.run, {
      state: plan.nextState,
      status,
      activeRevisionId: revision.id,
      lastError: null,
      lastJobId: null,
      completedAt: plan.done ? new Date() : null,
    });
    if (plan.done) {
      await insertEvent(client, {
        routerId: ctx.router.id,
        type: "router.onboarding.completed",
        message: "Auto-onboarding completed after final live import approval.",
        metadata: { runId: ctx.run.id, revisionId: revision.id },
      });
    }
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: plan.nextState,
      status,
      reason: plan.reason,
      revisionId: revision.id,
      continue: !plan.done,
    };
  }

  if (plan.action === "queue_hostname") {
    const job = await queueHostnameJob(client, {
      routerId: ctx.router.id,
      runId: ctx.run.id,
      attempt: ctx.run.attempt,
      hostname: plan.hostname,
    });
    await updateRun(client, ctx.run, {
      state: "rename_router",
      status: "waiting",
      lastJobId: job?.id ?? null,
      lastError: plan.reason,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.hostname_queued",
      message: `Auto-onboarding queued hostname update to "${plan.hostname}".`,
      metadata: { runId: ctx.run.id, jobId: job?.id ?? null },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: "rename_router",
      status: "waiting",
      reason: plan.reason,
      jobId: job?.id ?? null,
      continue: false,
    };
  }

  if (plan.action === "queue_ensure_runtime") {
    const job = await queueEnsureRuntimeJob(client, {
      routerId: ctx.router.id,
      runId: ctx.run.id,
      attempt: ctx.run.attempt,
      actions: plan.actions,
    });
    await updateRun(client, ctx.run, {
      state: "ensure_runtime",
      status: "waiting",
      lastJobId: job?.id ?? null,
      lastError: plan.reason,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.runtime_repair_queued",
      message: "Auto-onboarding queued typed PassWall runtime repair.",
      metadata: {
        runId: ctx.run.id,
        jobId: job?.id ?? null,
        actions: plan.actions,
      },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: "ensure_runtime",
      status: "waiting",
      reason: plan.reason,
      jobId: job?.id ?? null,
      continue: false,
    };
  }

  if (
    plan.action === "apply_subscription" ||
    plan.action === "apply_route_baseline"
  ) {
    const note =
      plan.action === "apply_subscription"
        ? "Auto-onboarding: subscription profile revision."
        : "Auto-onboarding: standard fleet route baseline.";
    const revision = await createOperatorDraftRevisionWithDb(client, {
      routerId: ctx.router.id,
      note,
      config: plan.config,
    });
    const job = await queueOnboardingApplyJob(client, {
      routerId: ctx.router.id,
      runId: ctx.run.id,
      attempt: ctx.run.attempt,
      revisionId: revision.id,
      purpose:
        plan.action === "apply_subscription"
          ? "apply_subscription"
          : "apply_route_baseline",
    });
    const state =
      plan.action === "apply_subscription"
        ? "apply_subscription"
        : "apply_route_baseline";
    await updateRun(client, ctx.run, {
      state,
      status: "waiting",
      activeRevisionId: revision.id,
      lastJobId: job?.id ?? null,
      lastError: plan.reason,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type:
        plan.action === "apply_subscription"
          ? "router.onboarding.subscription_queued"
          : "router.onboarding.route_baseline_queued",
      message:
        plan.action === "apply_subscription"
          ? "Auto-onboarding queued subscription revision apply."
          : "Auto-onboarding queued route baseline revision apply.",
      metadata: {
        runId: ctx.run.id,
        revisionId: revision.id,
        jobId: job?.id ?? null,
      },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state,
      status: "waiting",
      reason: plan.reason,
      revisionId: revision.id,
      jobId: job?.id ?? null,
      continue: false,
    };
  }

  if (plan.action === "queue_refresh_subscriptions") {
    const job = await queueRefreshSubscriptionsJob(client, {
      routerId: ctx.router.id,
      runId: ctx.run.id,
      attempt: ctx.run.attempt,
    });
    await updateRun(client, ctx.run, {
      state: "refresh_subscription",
      status: "waiting",
      lastJobId: job?.id ?? null,
      lastError: plan.reason,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.subscription_refresh_queued",
      message: "Auto-onboarding queued native PassWall subscription refresh.",
      metadata: { runId: ctx.run.id, jobId: job?.id ?? null },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: "refresh_subscription",
      status: "waiting",
      reason: plan.reason,
      jobId: job?.id ?? null,
      continue: false,
    };
  }

  if (plan.action === "queue_route_verification") {
    const job = await queueRouteVerificationJob(client, {
      routerId: ctx.router.id,
      runId: ctx.run.id,
      attempt: ctx.run.attempt,
    });
    await updateRun(client, ctx.run, {
      state: "verify_runtime",
      status: "waiting",
      lastJobId: job?.id ?? null,
      lastError: plan.reason,
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.route_verification_queued",
      message:
        "Auto-onboarding queued typed PassWall route-smoke verification.",
      metadata: { runId: ctx.run.id, jobId: job?.id ?? null },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: "verify_runtime",
      status: "waiting",
      reason: plan.reason,
      jobId: job?.id ?? null,
      continue: false,
    };
  }

  if (plan.action === "mark_done") {
    await updateRun(client, ctx.run, {
      state: "done",
      status: "done",
      lastError: null,
      lastJobId: null,
      completedAt: new Date(),
    });
    await insertEvent(client, {
      routerId: ctx.router.id,
      type: "router.onboarding.completed",
      message: "Auto-onboarding completed.",
      metadata: { runId: ctx.run.id },
    });
    return {
      action: plan.action,
      routerId: ctx.router.id,
      runId: ctx.run.id,
      state: "done",
      status: "done",
      reason: plan.reason,
      continue: false,
    };
  }

  throw new Error("Unsupported onboarding plan action.");
}

export async function advanceRouterOnboardingWithDb(
  client: DatabaseClient,
  routerId: string,
  options: { featureEnabled?: boolean; now?: Date; maxSteps?: number } = {},
): Promise<RouterOnboardingAdvanceResult> {
  return withRouterOnboardingAdvanceLock(routerId, () =>
    advanceRouterOnboardingWithDbUnlocked(client, routerId, options),
  );
}

async function advanceRouterOnboardingWithDbUnlocked(
  client: DatabaseClient,
  routerId: string,
  options: { featureEnabled?: boolean; now?: Date; maxSteps?: number } = {},
): Promise<RouterOnboardingAdvanceResult> {
  const featureEnabled =
    options.featureEnabled ?? Boolean(env.VECTRA_AUTO_ONBOARDING_ENABLED);
  const now = options.now ?? new Date();
  const maxSteps = options.maxSteps ?? 8;
  let latestResult: RouterOnboardingAdvanceResult | null = null;

  for (let step = 0; step < maxSteps; step += 1) {
    const ctx = await loadOnboardingContext(
      client,
      routerId,
      featureEnabled,
      now,
    );
    const plan = planNextOnboardingAction(ctx);
    const result = await executePlan(client, ctx, plan);
    latestResult = result;
    if (!result.continue) {
      return result;
    }
  }

  return (
    latestResult ?? {
      action: "skip",
      routerId,
      runId: null,
      state: null,
      status: "skipped",
      reason: "no onboarding action was planned",
    }
  );
}

export async function advanceRouterOnboarding(routerId: string) {
  return advanceRouterOnboardingWithDb(db, routerId);
}

export async function maybeAdvanceRouterOnboarding(routerId: string) {
  if (!env.VECTRA_AUTO_ONBOARDING_ENABLED) {
    return null;
  }

  return advanceRouterOnboarding(routerId);
}

export async function safelyMaybeAdvanceRouterOnboarding(routerId: string) {
  if (!env.VECTRA_AUTO_ONBOARDING_ENABLED) {
    return null;
  }

  try {
    return await advanceRouterOnboarding(routerId);
  } catch (error) {
    await db.insert(eventLog).values({
      routerId,
      type: "router.onboarding.advance_error",
      severity: "warning",
      message: "Auto-onboarding advance failed after router event.",
      metadata: {
        error: error instanceof Error ? error.message : String(error),
      },
    });
    return null;
  }
}

export async function upsertRouterOnboardingProfileWithDb(
  client: DatabaseClient,
  input: RouterOnboardingProfileInput,
) {
  const parsed = routerOnboardingProfileInputSchema.parse(input);
  const subscriptionPatch = parsed.subscriptionUrl
    ? {
        subscriptionSecretCiphertext: createSubscriptionSecretCiphertext(
          parsed.subscriptionUrl,
        ),
        subscriptionUrlHash: hashOnboardingSecret(parsed.subscriptionUrl),
      }
    : {};

  const values = {
    routerId: parsed.routerId,
    enabled: parsed.enabled,
    targetHostname: parsed.targetHostname
      ? normalizeRouterHostname(parsed.targetHostname)
      : null,
    displayName: parsed.displayName ?? null,
    subscriptionRemark: parsed.subscriptionRemark ?? null,
    baseline: parsed.baseline,
    runtimePolicy: parsed.runtimePolicy,
    verifyPolicy: parsed.verifyPolicy,
    notes: parsed.notes ?? null,
    ...subscriptionPatch,
  } satisfies Partial<typeof routerOnboardingProfiles.$inferInsert> & {
    routerId: string;
  };

  const [profile] = await client
    .insert(routerOnboardingProfiles)
    .values(values)
    .onConflictDoUpdate({
      target: routerOnboardingProfiles.routerId,
      set: { ...values, updatedAt: new Date() },
    })
    .returning();

  if (!profile) {
    throw new Error("Failed to upsert router onboarding profile.");
  }

  await insertEvent(client, {
    routerId: parsed.routerId,
    type: "router.onboarding.profile_saved",
    message: "Operator saved an auto-onboarding profile.",
    metadata: {
      profileId: profile.id,
      enabled: profile.enabled,
      baseline: profile.baseline,
      hasSubscription: Boolean(profile.subscriptionSecretCiphertext),
      subscriptionUrlHash: profile.subscriptionUrlHash,
    },
  });

  return profile;
}

export async function getRouterOnboardingStateWithDb(
  client: DatabaseClient,
  routerId: string,
) {
  const [[profile], [run]] = await Promise.all([
    client
      .select()
      .from(routerOnboardingProfiles)
      .where(eq(routerOnboardingProfiles.routerId, routerId))
      .limit(1),
    client
      .select()
      .from(routerOnboardingRuns)
      .where(eq(routerOnboardingRuns.routerId, routerId))
      .orderBy(desc(routerOnboardingRuns.createdAt))
      .limit(1),
  ]);

  return {
    featureEnabled: Boolean(env.VECTRA_AUTO_ONBOARDING_ENABLED),
    profile: sanitizeOnboardingProfileForClient(profile ?? null),
    run: sanitizeOnboardingRunForClient(run ?? null),
  };
}

export async function pauseRouterOnboardingWithDb(
  client: DatabaseClient,
  routerId: string,
) {
  await client
    .update(routerOnboardingProfiles)
    .set({ enabled: false, updatedAt: new Date() })
    .where(eq(routerOnboardingProfiles.routerId, routerId));

  const [run] = await client
    .update(routerOnboardingRuns)
    .set({ status: "paused", updatedAt: new Date() })
    .where(
      and(
        eq(routerOnboardingRuns.routerId, routerId),
        inArray(routerOnboardingRuns.status, [
          "running",
          "waiting",
          "blocked",
          "failed",
        ]),
      ),
    )
    .returning();

  await insertEvent(client, {
    routerId,
    type: "router.onboarding.paused",
    message: "Operator paused auto-onboarding.",
    metadata: { runId: run?.id ?? null },
  });

  return run ?? null;
}

export async function retryRouterOnboardingWithDb(
  client: DatabaseClient,
  routerId: string,
) {
  await client
    .update(routerOnboardingProfiles)
    .set({ enabled: true, updatedAt: new Date() })
    .where(eq(routerOnboardingProfiles.routerId, routerId));

  await client
    .update(routerOnboardingRuns)
    .set({
      status: "running",
      lastError: null,
      lastJobId: null,
      updatedAt: new Date(),
    })
    .where(
      and(
        eq(routerOnboardingRuns.routerId, routerId),
        or(
          eq(routerOnboardingRuns.status, "blocked"),
          eq(routerOnboardingRuns.status, "failed"),
          eq(routerOnboardingRuns.status, "paused"),
        ),
      ),
    );

  await insertEvent(client, {
    routerId,
    type: "router.onboarding.retry_requested",
    message: "Operator requested auto-onboarding retry.",
    metadata: {},
  });

  return advanceRouterOnboardingWithDb(client, routerId, {
    featureEnabled: true,
  });
}

export async function listRouterOnboardingRunsWithDb(
  client: DatabaseClient,
  routerId: string,
) {
  return client
    .select()
    .from(routerOnboardingRuns)
    .where(eq(routerOnboardingRuns.routerId, routerId))
    .orderBy(desc(routerOnboardingRuns.createdAt))
    .limit(20);
}
