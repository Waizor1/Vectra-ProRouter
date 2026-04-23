import {
  artifactMetadataSchema,
  createDefaultRescuePolicy,
  createDefaultUpdatePolicy,
  desiredRevisionSummarySchema,
  firmwareManifestSchema,
  jobResultRequestSchema,
  jobResultResponseSchema,
  type PasswallImportedState,
  type RouterConfigSyncState,
  type RouterInventory,
  routerCheckInRequestSchema,
  routerCheckInResponseSchema,
  routerJobSchema,
  routerRegisterRequestSchema,
  routerRegisterResponseSchema,
  summarizePasswallRevisionDiff,
} from "@vectra/contracts";
import {
  artifacts,
  eventLog,
  firmwareManifests,
  healthIncidents,
  jobResults,
  jobs,
  passwallAppliedRevisions,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { and, desc, eq, inArray, isNull, lte, or } from "drizzle-orm";

import { env } from "~/env";
import { isControllerUpdateJob } from "~/lib/controller-update-jobs";
import { isRouterHostnameUpdateTerminalPayload } from "~/lib/router-hostname-jobs";
import { isRouterRebootJob } from "~/lib/router-reboot-jobs";
import { db } from "~/server/db";
import { issueRouterCredential } from "~/server/vectra/auth";
import {
  resolveImportedConfigDigest,
  resolvePersistedConfigDigest,
  shouldRequestImportOnCheckIn,
} from "~/server/vectra/config-sync";
import {
  computeConfigDigest,
  createSecretPayload,
  hydratePasswallConfig,
  restoreMaskedPasswallConfig,
  sanitizePasswallConfig,
  sanitizePasswallRawSnapshot,
} from "~/server/vectra/secrets";

type RouterRow = typeof routers.$inferSelect;
type RevisionRow = typeof passwallDesiredRevisions.$inferSelect;
type JobRow = typeof jobs.$inferSelect;
type DatabaseClient = typeof db;

export type ClientRevisionRow = Omit<RevisionRow, "rawImportedSnapshot"> & {
  hasRawImportedSnapshot: boolean;
};

function deriveRouterStatus(
  currentStatus: RouterRow["status"],
  approvedAt: Date | null,
  mode: "proxy" | "direct" | null,
) {
  if (currentStatus === "disabled") {
    return currentStatus;
  }

  if (mode === "direct") {
    return "direct";
  }

  return approvedAt ? "active" : "pending";
}

export function resolveRescueReason(
  mode: "proxy" | "direct" | null,
  reportedReason: string | null | undefined,
  previousReason: string | null,
) {
  if (reportedReason) {
    return reportedReason;
  }

  if (mode === "proxy") {
    return null;
  }

  return previousReason;
}

export function shouldPromotePostApplyImport(args: {
  approvedAt: Date | null;
  importSource: PasswallImportedState["source"];
  reportedAppliedRevisionId: string | null | undefined;
  activeRevisionId: string | null;
  lastAppliedRevisionId: string | null;
}) {
  if (!args.approvedAt || args.importSource !== "check_in") {
    return false;
  }

  if (!args.reportedAppliedRevisionId) {
    return false;
  }

  return (
    args.reportedAppliedRevisionId === args.activeRevisionId ||
    args.reportedAppliedRevisionId === args.lastAppliedRevisionId
  );
}

function buildConfigSyncState(
  router: RouterRow,
  options: { requestImport?: boolean } = {},
): RouterConfigSyncState {
  return {
    importState: router.importState,
    pendingImportRevisionId: router.pendingImportRevisionId,
    activeRevisionId: router.activeRevisionId,
    lastAppliedRevisionId: router.lastAppliedRevisionId,
    lastConfigDigest: router.lastConfigDigest,
    requestImport:
      options.requestImport ?? router.importState === "awaiting_import",
  };
}

function serializeJob(job: JobRow) {
  return routerJobSchema.parse({
    id: job.id,
    type: job.type,
    state: job.state,
    createdAt: job.createdAt.toISOString(),
    desiredRevisionId: job.desiredRevisionId,
    payload: job.payload,
  });
}

export function selectDeliverableJobsForCheckIn(
  importState: RouterRow["importState"],
  queuedCandidates: JobRow[],
) {
  const allowedJobs =
    importState === "approved"
      ? queuedCandidates
      : queuedCandidates.filter((job) => job.type !== "apply_passwall_config");

  const exclusiveJob = allowedJobs.find((job) =>
    isControllerUpdateJob(job) ||
    isRouterRebootJob(job) ||
    job.type === "validate_firmware",
  );

  if (exclusiveJob) {
    return [exclusiveJob];
  }

  return allowedJobs;
}

export function sanitizeRevisionForClient(
  revision: RevisionRow | null | undefined,
): ClientRevisionRow | null {
  if (!revision) {
    return null;
  }

  const safeRevision = Object.fromEntries(
    Object.entries(revision).filter(([key]) => key !== "rawImportedSnapshot"),
  ) as Omit<RevisionRow, "rawImportedSnapshot">;
  return {
    ...safeRevision,
    hasRawImportedSnapshot: Boolean(revision.rawImportedSnapshot),
  };
}

export function resolveReportedRouterHostname(args: {
  jobType: string;
  jobPayload: Record<string, unknown> | null | undefined;
  resultPayload: Record<string, unknown> | null | undefined;
}) {
  if (
    args.jobType !== "run_terminal_command" ||
    !isRouterHostnameUpdateTerminalPayload(args.jobPayload ?? null)
  ) {
    return null;
  }

  const hostnameAfter =
    typeof args.resultPayload?.hostnameAfter === "string"
      ? args.resultPayload.hostnameAfter.trim()
      : "";
  if (hostnameAfter.length > 0) {
    return hostnameAfter;
  }

  const requestedHostname =
    typeof args.jobPayload?.hostname === "string"
      ? args.jobPayload.hostname.trim()
      : "";
  return requestedHostname.length > 0 ? requestedHostname : null;
}

type RecoveryHealthPayload = ReturnType<typeof routerCheckInRequestSchema.parse>["health"];
type RecoveryIncidentTransition = ReturnType<
  typeof jobResultRequestSchema.parse
>["incidentTransitions"][number];
type OpenIncidentRow = typeof healthIncidents.$inferSelect | null;

const unresolvedProxyRecoveryPhases = new Set([
  "direct_settle",
  "reboot_wait",
  "post_reboot_check",
  "passwall_retry_wait",
  "operator_attention",
]);
const controlPlaneRecoveryIncidentOrigin = "control-plane-recovery";

function hasControlPlaneRecoveryIncidentOrigin(
  metadata: Record<string, unknown> | null | undefined,
) {
  return metadata?.origin === controlPlaneRecoveryIncidentOrigin;
}

export function isControlPlaneRecoveryIncident(
  incident:
    | Pick<NonNullable<OpenIncidentRow>, "type" | "metadata">
    | null
    | undefined,
) {
  if (!incident) {
    return false;
  }

  if (
    incident.type !== "server_unreachable" &&
    incident.type !== "proxy_outage"
  ) {
    return false;
  }

  return hasControlPlaneRecoveryIncidentOrigin(incident.metadata ?? {});
}

function isControlPlaneRecoveryTransition(
  transition: RecoveryIncidentTransition,
) {
  return hasControlPlaneRecoveryIncidentOrigin(transition.metadata);
}

export function buildSyntheticRecoveryTransitions(args: {
  health: RecoveryHealthPayload;
  inventory: RouterInventory;
  openIncident: OpenIncidentRow;
}): RecoveryIncidentTransition[] {
  const metadata = {
    origin: controlPlaneRecoveryIncidentOrigin,
    recoveryPhase: args.health.recoveryPhase,
    awaitingOperator: args.health.awaitingOperator,
    panelStatus: args.inventory.panelReachability?.status ?? null,
    ruStatus: args.inventory.ruReachability?.status ?? null,
    foreignStatus: args.inventory.foreignReachability?.status ?? null,
  };
  const hasUnrelatedOpenIncident =
    args.openIncident?.state === "open" &&
    !isControlPlaneRecoveryIncident(args.openIncident);

  let desiredOpen:
    | {
        type: "server_unreachable" | "proxy_outage";
        reason: string;
      }
    | null = null;

  if (args.health.recoveryPhase === "controller_restart_wait") {
    desiredOpen = {
      type: "server_unreachable",
      reason:
        args.health.lastRecoveryAction ??
        "Control plane stayed unreachable long enough to trigger local controller recovery.",
    };
  } else if (
    args.health.awaitingOperator ||
    (unresolvedProxyRecoveryPhases.has(args.health.recoveryPhase) &&
      args.inventory.foreignReachability?.status !== "healthy")
  ) {
    desiredOpen = {
      type: "proxy_outage",
      reason:
        args.health.lastRecoveryAction ??
        "Proxy-path recovery did not converge; the router requires operator review.",
    };
  }

  if (desiredOpen) {
    if (hasUnrelatedOpenIncident) {
      return [];
    }

    if (
      args.openIncident?.state === "open" &&
      isControlPlaneRecoveryIncident(args.openIncident) &&
      args.openIncident.type === desiredOpen.type &&
      args.openIncident.reason === desiredOpen.reason
    ) {
      return [];
    }

    return [
      {
        type: desiredOpen.type,
        state: "open",
        reason: desiredOpen.reason,
        metadata,
      },
    ];
  }

  if (
    args.openIncident?.state === "open" &&
    isControlPlaneRecoveryIncident(args.openIncident)
  ) {
    return [
      {
        type: "recovered",
        state: "resolved",
        reason:
          args.health.lastRecoveryAction ??
          "Router resumed normal control-plane contact and cleared the active recovery incident.",
        metadata,
      },
    ];
  }

  return [];
}

async function insertInventorySnapshot(
  routerId: string,
  inventory: RouterInventory,
  source: string,
) {
  return db.insert(routerInventorySnapshots).values({
    routerId,
    source,
    payload: inventory,
    passwallEnabled: inventory.passwallEnabled,
    selectedNodeId: inventory.selectedNodeId ?? null,
    nodeCount: inventory.nodeCount,
    subscriptionCount: inventory.subscriptionCount,
    controllerVersion: inventory.controllerVersion,
    passwallAppVersion: inventory.packageVersions["luci-app-passwall2"] ?? null,
  });
}

async function getSecretCiphertextForRevisionWithDb(
  client: DatabaseClient,
  revisionId: string,
) {
  const [secret] = await client
    .select()
    .from(passwallSecretBlobs)
    .where(eq(passwallSecretBlobs.desiredRevisionId, revisionId))
    .orderBy(desc(passwallSecretBlobs.createdAt))
    .limit(1);

  return secret?.ciphertext ?? null;
}

async function upsertRevisionSecretBlobWithDb(
  client: DatabaseClient,
  routerId: string,
  revisionId: string,
  scope: "router_import" | "desired_revision",
  ciphertext: string,
) {
  await client
    .delete(passwallSecretBlobs)
    .where(
      and(
        eq(passwallSecretBlobs.routerId, routerId),
        eq(passwallSecretBlobs.desiredRevisionId, revisionId),
        eq(passwallSecretBlobs.scope, scope),
      ),
    );

  await client.insert(passwallSecretBlobs).values({
    routerId,
    desiredRevisionId: revisionId,
    scope,
    ciphertext,
  });
}

async function upsertRevisionSecretBlob(
  routerId: string,
  revisionId: string,
  scope: "router_import" | "desired_revision",
  ciphertext: string,
) {
  await upsertRevisionSecretBlobWithDb(
    db,
    routerId,
    revisionId,
    scope,
    ciphertext,
  );
}

async function hydrateRevisionConfigWithDb(
  client: DatabaseClient,
  revision: RevisionRow,
) {
  const ciphertext = await getSecretCiphertextForRevisionWithDb(
    client,
    revision.id,
  );
  return hydratePasswallConfig(revision.config, ciphertext);
}

async function getRevisionSummaryWithDb(
  client: DatabaseClient,
  routerId: string,
  revisionId: string | null | undefined,
) {
  if (!revisionId) {
    return null;
  }

  const revisions = await client
    .select()
    .from(passwallDesiredRevisions)
    .where(eq(passwallDesiredRevisions.routerId, routerId))
    .orderBy(desc(passwallDesiredRevisions.revisionNumber));

  const currentIndex = revisions.findIndex(
    (revision) => revision.id === revisionId,
  );
  if (currentIndex === -1) {
    return null;
  }

  const current = revisions[currentIndex];
  if (!current) {
    return null;
  }

  const previous = revisions[currentIndex + 1] ?? null;
  const [currentConfig, previousConfig] = await Promise.all([
    hydrateRevisionConfigWithDb(client, current),
    previous ? hydrateRevisionConfigWithDb(client, previous) : Promise.resolve(null),
  ]);

  return desiredRevisionSummarySchema.parse({
    id: current.id,
    revisionNumber: current.revisionNumber,
    status: current.status,
    origin: current.origin,
    configDigest: current.configDigest,
    config: currentConfig,
    impact: summarizePasswallRevisionDiff(previousConfig, currentConfig),
  });
}

async function getRevisionSummary(
  routerId: string,
  revisionId: string | null | undefined,
) {
  return getRevisionSummaryWithDb(db, routerId, revisionId);
}

async function createImportedBaselineRevision(
  router: RouterRow,
  importedState: PasswallImportedState,
  options: {
    reportedAppliedRevisionId?: string | null;
  } = {},
) {
  const configDigest = resolveImportedConfigDigest({
    importedDigest: importedState.configDigest,
    fallbackDigest: computeConfigDigest(importedState.config),
  });
  const promoteAsApproved = shouldPromotePostApplyImport({
    approvedAt: router.approvedAt,
    importSource: importedState.source,
    reportedAppliedRevisionId: options.reportedAppliedRevisionId,
    activeRevisionId: router.activeRevisionId,
    lastAppliedRevisionId: router.lastAppliedRevisionId,
  });
  const [activeRevision, pendingRevision, latestRevision] = await Promise.all([
    router.activeRevisionId
      ? db
          .select()
          .from(passwallDesiredRevisions)
          .where(eq(passwallDesiredRevisions.id, router.activeRevisionId))
          .limit(1)
          .then((rows) => rows[0] ?? null)
      : Promise.resolve(null),
    router.pendingImportRevisionId
      ? db
          .select()
          .from(passwallDesiredRevisions)
          .where(
            eq(passwallDesiredRevisions.id, router.pendingImportRevisionId),
          )
          .limit(1)
          .then((rows) => rows[0] ?? null)
      : Promise.resolve(null),
    db
      .select({
        revisionNumber: passwallDesiredRevisions.revisionNumber,
      })
      .from(passwallDesiredRevisions)
      .where(eq(passwallDesiredRevisions.routerId, router.id))
      .orderBy(desc(passwallDesiredRevisions.revisionNumber))
      .limit(1)
      .then((rows) => rows[0] ?? null),
  ]);

  if (activeRevision?.configDigest === configDigest) {
    const [updatedRouter] = await db
      .update(routers)
      .set({
        importState: "approved",
        pendingImportRevisionId: null,
        lastConfigDigest: configDigest,
      })
      .where(eq(routers.id, router.id))
      .returning();

    return updatedRouter ?? router;
  }

  let revision =
    pendingRevision?.configDigest === configDigest ? pendingRevision : null;
  let created = false;

  if (!revision) {
    const [createdRevision] = await db
      .insert(passwallDesiredRevisions)
      .values({
        routerId: router.id,
        revisionNumber: (latestRevision?.revisionNumber ?? 0) + 1,
        status: promoteAsApproved ? "approved" : "import_review",
        origin:
          importedState.source === "operator_reimport"
            ? "operator_reimport"
            : "router_import",
        configDigest,
        config: sanitizePasswallConfig(importedState.config),
        rawImportedSnapshot: sanitizePasswallRawSnapshot(
          importedState.rawSnapshot,
        ),
        createdBy:
          importedState.source === "operator_reimport" ? "operator" : "router",
        note:
          promoteAsApproved
            ? "Imported from live router after authoritative apply."
            : importedState.source === "operator_reimport"
            ? "Operator requested re-import from live router state."
            : "Imported from live router PassWall2 state.",
        approvedAt: promoteAsApproved ? new Date() : null,
      })
      .returning();

    revision = createdRevision ?? null;
    created = Boolean(createdRevision);
  }

  if (!revision) {
    return router;
  }

  await upsertRevisionSecretBlob(
    router.id,
    revision.id,
    "router_import",
    createSecretPayload(importedState.config),
  );

  if (promoteAsApproved && revision.status !== "approved") {
    const [updatedRevision] = await db
      .update(passwallDesiredRevisions)
      .set({
        status: "approved",
        approvedAt: new Date(),
      })
      .where(eq(passwallDesiredRevisions.id, revision.id))
      .returning();

    revision = updatedRevision ?? revision;
  }

  const nextImportState = promoteAsApproved
    ? "approved"
    : router.approvedAt
      ? "out_of_sync"
      : "import_review";
  const [updatedRouter] = await db
    .update(routers)
    .set({
      importState: nextImportState,
      pendingImportRevisionId: promoteAsApproved ? null : revision.id,
      activeRevisionId: promoteAsApproved ? revision.id : router.activeRevisionId,
      lastConfigDigest: configDigest,
    })
    .where(eq(routers.id, router.id))
    .returning();

  if (created) {
    await db.insert(eventLog).values({
      routerId: router.id,
      type:
        importedState.source === "operator_reimport"
          ? "router.import.reimported"
          : "router.import.created",
      severity:
        nextImportState === "out_of_sync" ? "warning" : "info",
      message:
        promoteAsApproved
          ? "Router reported a post-apply live PassWall2 baseline and it was promoted back to authoritative state."
          : nextImportState === "out_of_sync"
          ? "Router reported a new live PassWall2 baseline that differs from the authoritative revision."
          : "Router imported its live PassWall2 baseline and is awaiting operator review.",
      metadata: {
        configDigest,
        revisionId: revision.id,
        source: importedState.source,
        reportedAppliedRevisionId: options.reportedAppliedRevisionId ?? null,
      },
    });
  }

  return updatedRouter ?? router;
}

async function resolveDesiredRevision(router: RouterRow, queuedJobs: JobRow[]) {
  if (router.importState !== "approved") {
    return null;
  }

  const jobRevisionId = queuedJobs.find(
    (job) => job.desiredRevisionId,
  )?.desiredRevisionId;
  const preferredRevisionId =
    jobRevisionId ??
    router.activeRevisionId ??
    router.lastAppliedRevisionId ??
    null;

  return getRevisionSummary(router.id, preferredRevisionId);
}

function buildRegisterMessage(router: RouterRow) {
  if (router.importState === "import_review") {
    return "Роутер зарегистрирован. Новый импортированный baseline PassWall2 ожидает проверки оператора.";
  }

  if (!router.approvedAt) {
    return "Роутер зарегистрирован и ожидает review оператора.";
  }

  return "Учётные данные роутера обновлены.";
}

function buildCheckInMessage(
  router: RouterRow,
  currentMode: "proxy" | "direct",
) {
  if (router.importState === "import_review") {
    return "Импортированный baseline PassWall2 ожидает проверки оператора.";
  }

  if (router.importState === "out_of_sync") {
    return "Обнаружено расхождение с роутером. Проверьте новый импортированный baseline, прежде чем сервер снова станет authoritative.";
  }

  if (currentMode === "direct") {
    return "Роутер сейчас в прямом режиме из-за локальной аварийной политики.";
  }

  return null;
}

function normalizeJobResultPayload(
  parsed: ReturnType<typeof jobResultRequestSchema.parse>,
) {
  return {
    ...parsed.result,
    appliedRevisionId:
      parsed.appliedRevisionId ?? parsed.result.appliedRevisionId ?? null,
    configDigest: parsed.configDigest ?? parsed.result.configDigest ?? null,
    stdout: parsed.stdout ?? parsed.result.stdout ?? null,
    stderr: parsed.stderr ?? parsed.result.stderr ?? null,
    uciCommands:
      parsed.result.uciCommands && Array.isArray(parsed.result.uciCommands)
        ? parsed.result.uciCommands
        : [],
    operationResults:
      parsed.result.operationResults &&
      Array.isArray(parsed.result.operationResults)
        ? parsed.result.operationResults
        : [],
    commandResults:
      parsed.result.commandResults &&
      Array.isArray(parsed.result.commandResults)
        ? parsed.result.commandResults
        : [],
    postApplyImportDigest:
      typeof parsed.result.postApplyImportDigest === "string"
        ? parsed.result.postApplyImportDigest
        : null,
    incidentTransitions: parsed.incidentTransitions,
  };
}

export async function applyIncidentTransitions(
  router: RouterRow,
  transitions: ReturnType<
    typeof jobResultRequestSchema.parse
  >["incidentTransitions"],
) {
  if (transitions.length === 0) {
    return router;
  }

  let currentRouter = router;

  for (const transition of transitions) {
    const happenedAt = transition.happenedAt
      ? new Date(transition.happenedAt)
      : new Date();
    const controlPlaneRecoveryTransition =
      isControlPlaneRecoveryTransition(transition);
    const openIncidents = await db
      .select()
      .from(healthIncidents)
      .where(
        and(
          eq(healthIncidents.routerId, router.id),
          eq(healthIncidents.state, "open"),
        ),
      )
      .orderBy(desc(healthIncidents.openedAt));

    if (transition.state === "open") {
      const unrelatedOpenIncident = openIncidents.find(
        (incident) => !isControlPlaneRecoveryIncident(incident),
      );
      if (controlPlaneRecoveryTransition && unrelatedOpenIncident) {
        continue;
      }
      const existingOpen = controlPlaneRecoveryTransition
        ? openIncidents.find((incident) => isControlPlaneRecoveryIncident(incident))
        : openIncidents[0];

      if (existingOpen?.type === transition.type) {
        await db
          .update(healthIncidents)
          .set({
            reason: transition.reason,
            metadata: transition.metadata,
            openedAt: happenedAt,
            resolvedAt: null,
          })
          .where(eq(healthIncidents.id, existingOpen.id));
      } else {
        const incidentIdsToResolve = controlPlaneRecoveryTransition
          ? openIncidents
              .filter((incident) => isControlPlaneRecoveryIncident(incident))
              .map((incident) => incident.id)
          : openIncidents.map((incident) => incident.id);
        if (incidentIdsToResolve.length > 0) {
          await db
            .update(healthIncidents)
            .set({
              state: "resolved",
              resolvedAt: happenedAt,
            })
            .where(
              and(
                eq(healthIncidents.routerId, router.id),
                eq(healthIncidents.state, "open"),
                inArray(healthIncidents.id, incidentIdsToResolve),
              ),
            );
        }

        await db.insert(healthIncidents).values({
          routerId: router.id,
          type: transition.type,
          state: "open",
          reason: transition.reason,
          metadata: transition.metadata,
          openedAt: happenedAt,
        });
      }

      if (transition.type === "entered_direct_mode") {
        const [updatedRouter] = await db
          .update(routers)
          .set({
            status: "direct",
            lastDirectModeAt: happenedAt,
            lastRescueReason: transition.reason,
          })
          .where(eq(routers.id, router.id))
          .returning();

        currentRouter = updatedRouter ?? currentRouter;
      }
    } else {
      const incidentIdsToResolve = controlPlaneRecoveryTransition
        ? openIncidents
            .filter((incident) => isControlPlaneRecoveryIncident(incident))
            .map((incident) => incident.id)
        : openIncidents.map((incident) => incident.id);
      if (incidentIdsToResolve.length === 0) {
        continue;
      }

      await db
        .update(healthIncidents)
        .set({
          state: "resolved",
          resolvedAt: happenedAt,
        })
        .where(
          and(
            eq(healthIncidents.routerId, router.id),
            eq(healthIncidents.state, "open"),
            inArray(healthIncidents.id, incidentIdsToResolve),
          ),
        );

      if (controlPlaneRecoveryTransition) {
        continue;
      }

      const [updatedRouter] = await db
        .update(routers)
        .set({
          status: currentRouter.approvedAt ? "active" : "pending",
        })
        .where(eq(routers.id, router.id))
        .returning();

      currentRouter = updatedRouter ?? currentRouter;
    }
  }

  return currentRouter;
}

export async function registerRouter(input: unknown) {
  const parsed = routerRegisterRequestSchema.parse(input);
  const now = new Date();

  const [existingRouter] = await db
    .select()
    .from(routers)
    .where(eq(routers.deviceIdentifier, parsed.inventory.deviceIdentifier))
    .limit(1);

  const nextStatus = deriveRouterStatus(
    existingRouter?.status ?? "pending",
    existingRouter?.approvedAt ?? null,
    parsed.inventory.lastRescue?.mode ?? null,
  );

  const [persistedRouter] = existingRouter
    ? await db
        .update(routers)
        .set({
          hostname: parsed.inventory.hostname,
          panelDomain:
            parsed.inventory.panelDomain ?? env.VECTRA_DEFAULT_CONTROL_DOMAIN,
          model: parsed.inventory.model,
          boardName: parsed.inventory.boardName,
          target: parsed.inventory.target,
          architecture: parsed.inventory.architecture,
          openwrtRelease: parsed.inventory.openwrtRelease,
          status: nextStatus,
          lastSeenAt: now,
          lastCheckInAt: now,
          lastDirectModeAt:
            parsed.inventory.lastRescue?.mode === "direct"
              ? now
              : existingRouter.lastDirectModeAt,
          lastRescueReason: resolveRescueReason(
            parsed.inventory.lastRescue?.mode ?? null,
            parsed.inventory.lastRescue?.reason,
            existingRouter.lastRescueReason,
          ),
          lastAppliedRevisionId:
            parsed.inventory.appliedRevisionId ??
            existingRouter.lastAppliedRevisionId,
          lastConfigDigest: resolvePersistedConfigDigest({
            previousDigest: existingRouter.lastConfigDigest,
            reportedDigest: parsed.inventory.configDigest,
            hasPasswallImport: Boolean(parsed.passwallImport),
          }),
        })
        .where(eq(routers.id, existingRouter.id))
        .returning()
    : await db
        .insert(routers)
        .values({
          deviceIdentifier: parsed.inventory.deviceIdentifier,
          hostname: parsed.inventory.hostname,
          panelDomain:
            parsed.inventory.panelDomain ?? env.VECTRA_DEFAULT_CONTROL_DOMAIN,
          model: parsed.inventory.model,
          boardName: parsed.inventory.boardName,
          target: parsed.inventory.target,
          architecture: parsed.inventory.architecture,
          openwrtRelease: parsed.inventory.openwrtRelease,
          status: nextStatus,
          lastSeenAt: now,
          lastCheckInAt: now,
          lastDirectModeAt:
            parsed.inventory.lastRescue?.mode === "direct" ? now : null,
          lastRescueReason: parsed.inventory.lastRescue?.reason,
          lastAppliedRevisionId: parsed.inventory.appliedRevisionId ?? null,
          lastConfigDigest: parsed.inventory.configDigest ?? null,
        })
        .returning();

  if (!persistedRouter) {
    throw new Error("Router registration failed.");
  }

  await insertInventorySnapshot(
    persistedRouter.id,
    parsed.inventory,
    "register",
  );

  const router = parsed.passwallImport
    ? await createImportedBaselineRevision(
        persistedRouter,
        parsed.passwallImport,
        {
          reportedAppliedRevisionId: parsed.inventory.appliedRevisionId ?? null,
        },
      )
    : persistedRouter;

  await db.insert(eventLog).values({
    routerId: router.id,
    type: existingRouter ? "router.reregistered" : "router.registered",
    severity: "info",
    message: existingRouter
      ? `Router ${router.deviceIdentifier} re-registered with ${parsed.inventory.controllerVersion}.`
      : `Router ${router.deviceIdentifier} registered with ${parsed.inventory.controllerVersion}.`,
    metadata: {
      architecture: parsed.inventory.architecture,
      boardName: parsed.inventory.boardName,
      enrollmentMode: "open_global_install",
      pendingReview: !router.approvedAt || router.importState !== "approved",
    },
  });

  const issued = await issueRouterCredential(
    router.id,
    parsed.inventory.devicePublicKey,
  );

  return routerRegisterResponseSchema.parse({
    protocolVersion: parsed.protocolVersion,
    routerId: router.id,
    status: router.status,
    issuedToken: issued.token,
    pollingIntervalSeconds: Number(env.VECTRA_POLLING_INTERVAL_SECONDS),
    pendingApproval: !router.approvedAt || router.importState !== "approved",
    configSyncState: buildConfigSyncState(router),
    rescuePolicy: createDefaultRescuePolicy(),
    updatePolicy: createDefaultUpdatePolicy(),
    operatorMessage: buildRegisterMessage(router),
  });
}

export async function checkInRouter(routerId: string, input: unknown) {
  const parsed = routerCheckInRequestSchema.parse(input);
  if (parsed.routerId !== routerId) {
    throw new Error("Router identity mismatch.");
  }

  const [existingRouter] = await db
    .select()
    .from(routers)
    .where(eq(routers.id, routerId))
    .limit(1);

  if (!existingRouter) {
    throw new Error("Router not found.");
  }

  const now = new Date();
  const nextStatus = deriveRouterStatus(
    existingRouter.status,
    existingRouter.approvedAt,
    parsed.health.currentMode,
  );
  const requestImport = shouldRequestImportOnCheckIn({
    importState: existingRouter.importState,
    hasPasswallImport: Boolean(parsed.passwallImport),
    reportedDigest: parsed.inventory.configDigest,
    authoritativeDigest: existingRouter.lastConfigDigest,
  });

  const [persistedRouter] = await db
    .update(routers)
    .set({
      hostname: parsed.inventory.hostname,
      panelDomain: parsed.inventory.panelDomain ?? existingRouter.panelDomain,
      model: parsed.inventory.model,
      boardName: parsed.inventory.boardName,
      target: parsed.inventory.target,
      architecture: parsed.inventory.architecture,
      openwrtRelease: parsed.inventory.openwrtRelease,
      lastSeenAt: now,
      lastCheckInAt: now,
      status: nextStatus,
          lastDirectModeAt:
            parsed.health.currentMode === "direct"
              ? now
              : existingRouter.lastDirectModeAt,
      lastRescueReason: resolveRescueReason(
        parsed.health.currentMode,
        parsed.inventory.lastRescue?.reason,
        existingRouter.lastRescueReason,
      ),
      lastAppliedRevisionId:
        parsed.inventory.appliedRevisionId ??
        existingRouter.lastAppliedRevisionId,
      lastConfigDigest: resolvePersistedConfigDigest({
        previousDigest: existingRouter.lastConfigDigest,
        reportedDigest: parsed.inventory.configDigest,
        hasPasswallImport: Boolean(parsed.passwallImport),
      }),
    })
    .where(eq(routers.id, existingRouter.id))
    .returning();

  if (!persistedRouter) {
    throw new Error("Router check-in update failed.");
  }

  await insertInventorySnapshot(
    persistedRouter.id,
    parsed.inventory,
    "check_in",
  );

  const [openIncident] = await db
    .select()
    .from(healthIncidents)
    .where(
      and(
        eq(healthIncidents.routerId, persistedRouter.id),
        eq(healthIncidents.state, "open"),
      ),
    )
    .orderBy(desc(healthIncidents.openedAt))
    .limit(1);

  const routerWithImport = parsed.passwallImport
    ? await createImportedBaselineRevision(
        persistedRouter,
        parsed.passwallImport,
        {
          reportedAppliedRevisionId: parsed.inventory.appliedRevisionId ?? null,
        },
      )
    : persistedRouter;

  const recoveryTransitions = buildSyntheticRecoveryTransitions({
    health: parsed.health,
    inventory: parsed.inventory,
    openIncident: openIncident ?? null,
  });

  const router =
    recoveryTransitions.length > 0
      ? await applyIncidentTransitions(routerWithImport, recoveryTransitions)
      : routerWithImport;

  const queuedCandidates = await db
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, router.id),
        eq(jobs.state, "queued"),
        or(isNull(jobs.deliverAfter), lte(jobs.deliverAfter, now)),
      ),
    )
    .orderBy(desc(jobs.createdAt))
    .limit(10);

  const deliverableJobs = selectDeliverableJobsForCheckIn(
    router.importState,
    queuedCandidates,
  );

  const desiredRevision = await resolveDesiredRevision(router, deliverableJobs);

  return routerCheckInResponseSchema.parse({
    protocolVersion: parsed.protocolVersion,
    routerId: router.id,
    status: router.status,
    pollingIntervalSeconds: Number(env.VECTRA_POLLING_INTERVAL_SECONDS),
    configSyncState: buildConfigSyncState(router, { requestImport }),
    rescuePolicy: createDefaultRescuePolicy(),
    updatePolicy: createDefaultUpdatePolicy(),
    desiredRevision,
    jobs: deliverableJobs.map(serializeJob),
    operatorMessage: buildCheckInMessage(router, parsed.health.currentMode),
  });
}

export async function recordJobResult(routerId: string, input: unknown) {
  const parsed = jobResultRequestSchema.parse(input);
  if (parsed.routerId !== routerId) {
    throw new Error("Router identity mismatch.");
  }

  const [[job], [router]] = await Promise.all([
    db
      .select()
      .from(jobs)
      .where(and(eq(jobs.id, parsed.jobId), eq(jobs.routerId, routerId)))
      .limit(1),
    db.select().from(routers).where(eq(routers.id, routerId)).limit(1),
  ]);

  if (!job) {
    throw new Error("Job not found.");
  }

  if (!router) {
    throw new Error("Router not found.");
  }

  const terminalJobStates = new Set(["succeeded", "failed", "cancelled"]);
  if (
    (parsed.status === "accepted" &&
      (job.state === "running" || terminalJobStates.has(job.state))) ||
    (parsed.status !== "accepted" && terminalJobStates.has(job.state))
  ) {
    return jobResultResponseSchema.parse({
      protocolVersion: parsed.protocolVersion,
      acknowledged: true,
    });
  }

  const payload = normalizeJobResultPayload(parsed);

  await db.insert(jobResults).values({
    jobId: job.id,
    routerId,
    status: parsed.status,
    payload,
  });

  await db
    .update(jobs)
    .set({
      state:
        parsed.status === "accepted"
          ? "running"
          : parsed.status === "success"
            ? "succeeded"
            : "failed",
      deliveredAt:
        parsed.status === "accepted"
          ? (job.deliveredAt ?? new Date())
          : (job.deliveredAt ?? new Date()),
      completedAt: parsed.status === "accepted" ? null : new Date(),
      dedupeKey: parsed.status === "accepted" ? job.dedupeKey : null,
    })
    .where(eq(jobs.id, job.id));

  const compatibilityTransitions = [...parsed.incidentTransitions];
  if (parsed.result.enteredDirectMode === true) {
    compatibilityTransitions.push({
      type: "entered_direct_mode",
      state: "open",
      reason:
        typeof parsed.result.reason === "string"
          ? parsed.result.reason
          : "Роутер перешёл в прямой режим после локального аварийного срабатывания.",
      happenedAt: new Date().toISOString(),
      metadata: parsed.result,
    });
  }
  if (parsed.result.recoveredProxy === true) {
    compatibilityTransitions.push({
      type: "recovered",
      state: "resolved",
      reason:
        typeof parsed.result.reason === "string"
          ? parsed.result.reason
          : "Прокси-путь восстановлен.",
      happenedAt: new Date().toISOString(),
      metadata: parsed.result,
    });
  }

  let currentRouter = await applyIncidentTransitions(
    router,
    compatibilityTransitions,
  );

  const appliedRevisionId =
    parsed.appliedRevisionId ?? job.desiredRevisionId ?? null;

  if (appliedRevisionId && parsed.status !== "accepted") {
    const [revision] = await db
      .select()
      .from(passwallDesiredRevisions)
      .where(eq(passwallDesiredRevisions.id, appliedRevisionId))
      .limit(1);

    if (revision) {
      const resultState = parsed.status === "success" ? "applied" : "failed";

      await db.insert(passwallAppliedRevisions).values({
        routerId,
        desiredRevisionId: revision.id,
        jobId: job.id,
        result: resultState,
        uciDigest: parsed.configDigest ?? revision.configDigest ?? null,
        stdout: parsed.stdout ?? null,
        stderr: parsed.stderr ?? null,
        config: revision.config,
        rawSnapshot:
          parsed.result.rawSnapshot &&
          typeof parsed.result.rawSnapshot === "object" &&
          !Array.isArray(parsed.result.rawSnapshot)
            ? sanitizePasswallRawSnapshot(
                parsed.result.rawSnapshot as Record<string, unknown>,
              )
            : null,
      });

      await db
        .update(passwallDesiredRevisions)
        .set({
          status: resultState,
        })
        .where(eq(passwallDesiredRevisions.id, revision.id));

      if (parsed.status === "success") {
        const [updatedRouter] = await db
          .update(routers)
          .set({
            activeRevisionId: revision.id,
            lastAppliedRevisionId: revision.id,
            lastConfigDigest:
              parsed.configDigest ??
              revision.configDigest ??
              currentRouter.lastConfigDigest,
            importState: "approved",
            pendingImportRevisionId: null,
            status:
              currentRouter.status === "direct"
                ? "direct"
                : currentRouter.approvedAt
                  ? "active"
                  : "pending",
          })
          .where(eq(routers.id, routerId))
          .returning();

        currentRouter = updatedRouter ?? currentRouter;
      }
    }
  }

  const reportedHostname =
    parsed.status === "success"
      ? resolveReportedRouterHostname({
          jobType: job.type,
          jobPayload: job.payload,
          resultPayload: payload,
        })
      : null;
  if (reportedHostname) {
    const [updatedRouter] = await db
      .update(routers)
      .set({
        hostname: reportedHostname,
      })
      .where(eq(routers.id, routerId))
      .returning();

    currentRouter = updatedRouter ?? currentRouter;
  }

  await db.insert(eventLog).values({
    routerId,
    type: `job.${parsed.status}`,
    severity: parsed.status === "failure" ? "warning" : "info",
    message: `Router reported ${parsed.status} for job ${job.type}.`,
    metadata: {
      jobId: job.id,
      result: payload,
    },
  });

  return jobResultResponseSchema.parse({
    protocolVersion: parsed.protocolVersion,
    acknowledged: true,
  });
}

export async function getArtifactMetadata(
  channel: string,
  name: string,
  query: URLSearchParams,
) {
  const architecture = query.get("architecture");
  const boardName = query.get("boardName");
  const layoutFamily = query.get("layoutFamily");
  const filters = [
    eq(artifacts.channel, channel === "beta" ? "beta" : "stable"),
    eq(artifacts.name, name),
  ];

  if (architecture) {
    filters.push(
      or(
        eq(artifacts.architecture, architecture),
        isNull(artifacts.architecture),
      )!,
    );
  }
  if (boardName) {
    filters.push(
      or(eq(artifacts.boardName, boardName), isNull(artifacts.boardName))!,
    );
  }
  if (layoutFamily) {
    filters.push(
      or(
        eq(artifacts.layoutFamily, layoutFamily),
        isNull(artifacts.layoutFamily),
      )!,
    );
  }

  const [artifact] = await db
    .select()
    .from(artifacts)
    .where(and(...filters))
    .orderBy(desc(artifacts.publishedAt), desc(artifacts.version))
    .limit(1);

  if (!artifact) {
    return null;
  }

  return artifactMetadataSchema.parse(artifact);
}

export async function getFirmwareManifest(
  boardName: string,
  query: URLSearchParams,
) {
  const target = query.get("target");
  const architecture = query.get("architecture");
  const layoutFamily = query.get("layoutFamily");
  const channel = query.get("channel") === "beta" ? "beta" : "stable";

  if (!target || !architecture || !layoutFamily) {
    throw new Error(
      "Missing target, architecture or layoutFamily query parameters.",
    );
  }

  const [manifest] = await db
    .select()
    .from(firmwareManifests)
    .where(
      and(
        eq(firmwareManifests.boardName, boardName),
        eq(firmwareManifests.target, target),
        eq(firmwareManifests.architecture, architecture),
        eq(firmwareManifests.layoutFamily, layoutFamily),
        eq(firmwareManifests.channel, channel),
      ),
    )
    .limit(1);

  if (!manifest) {
    return null;
  }

  const [artifact] = await db
    .select()
    .from(artifacts)
    .where(eq(artifacts.id, manifest.artifactId))
    .limit(1);

  if (!artifact) {
    return null;
  }

  return firmwareManifestSchema.parse({
    ...manifest,
    artifact,
  });
}

export async function getFullConfigForRevision(revisionId: string) {
  return getFullConfigForRevisionWithDb(db, revisionId);
}

export async function getFullConfigForRevisionWithDb(
  client: DatabaseClient,
  revisionId: string,
) {
  const [revision] = await client
    .select()
    .from(passwallDesiredRevisions)
    .where(eq(passwallDesiredRevisions.id, revisionId))
    .limit(1);

  if (!revision) {
    return null;
  }

  return hydratePasswallConfig(
    revision.config,
    await getSecretCiphertextForRevisionWithDb(client, revision.id),
  );
}

export async function createOperatorDraftRevision(input: {
  routerId: string;
  note?: string;
  config: RevisionRow["config"];
}) {
  return createOperatorDraftRevisionWithDb(db, input);
}

export async function createOperatorDraftRevisionWithDb(
  client: DatabaseClient,
  input: {
    routerId: string;
    note?: string;
    config: RevisionRow["config"];
  },
) {
  const [router] = await client
    .select()
    .from(routers)
    .where(eq(routers.id, input.routerId))
    .limit(1);

  if (!router) {
    throw new Error("Router not found.");
  }

  const [latestRevision] = await client
    .select()
    .from(passwallDesiredRevisions)
    .where(eq(passwallDesiredRevisions.routerId, input.routerId))
    .orderBy(desc(passwallDesiredRevisions.revisionNumber))
    .limit(1);

  const sourceRevisionId =
    router.pendingImportRevisionId ??
    router.activeRevisionId ??
    latestRevision?.id ??
    null;

  const sourceConfig = sourceRevisionId
    ? await getFullConfigForRevisionWithDb(client, sourceRevisionId)
    : null;
  const restoredConfig = restoreMaskedPasswallConfig(
    input.config,
    sourceConfig,
  );
  const maskedConfig = sanitizePasswallConfig(restoredConfig);
  const configDigest = computeConfigDigest(restoredConfig);

  const [revision] = await client
    .insert(passwallDesiredRevisions)
    .values({
      routerId: input.routerId,
      revisionNumber: (latestRevision?.revisionNumber ?? 0) + 1,
      status: "draft",
      origin: "operator_draft",
      configDigest,
      config: maskedConfig,
      createdBy: "operator",
      note: input.note,
    })
    .returning();

  if (!revision) {
    throw new Error("Failed to create draft revision.");
  }

  await upsertRevisionSecretBlobWithDb(
    client,
    input.routerId,
    revision.id,
    "desired_revision",
    createSecretPayload(restoredConfig),
  );

  return revision;
}

export async function queueDesiredRevisionApplyJob(input: {
  routerId: string;
  desiredRevisionId: string;
}) {
  return queueDesiredRevisionApplyJobWithDb(db, input);
}

export async function queueDesiredRevisionApplyJobWithDb(
  client: DatabaseClient,
  input: {
    routerId: string;
    desiredRevisionId: string;
  },
) {
  const dedupeKey = `apply:${input.routerId}:${input.desiredRevisionId}`;

  const [existingJob] = await client
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, input.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, ["queued", "delivered", "running"]),
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
      desiredRevisionId: input.desiredRevisionId,
      payload: {
        desiredRevisionId: input.desiredRevisionId,
      },
    })
    .returning();

  await client
    .update(passwallDesiredRevisions)
    .set({ status: "queued" })
    .where(eq(passwallDesiredRevisions.id, input.desiredRevisionId));

  return job ?? null;
}
