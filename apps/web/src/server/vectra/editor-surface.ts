import { MASKED_SECRET_PLACEHOLDER } from "@vectra/contracts";
import {
  jobResults,
  jobs,
  passwallAppliedRevisions,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { desc, eq } from "drizzle-orm";

import { db } from "~/server/db";
import { buildEditorSurface } from "~/server/vectra/editor";
import { isRouterReachable } from "~/server/vectra/router-presence";
import {
  canRunDestructiveAction,
  canRunUpdateAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";
import { hydratePasswallConfig } from "~/server/vectra/secrets";

type ControllerUpdateAttemptStatus = "accepted" | "success" | "failure" | null;

export type LastControllerUpdateAttempt = {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  artifactVersion: string | null;
  reportedAt: Date | null;
  summary: string;
};

function formatValue(value: unknown): string {
  if (value === undefined) {
    return "Не задано";
  }

  if (value === null) {
    return "null";
  }

  if (value === MASKED_SECRET_PLACEHOLDER) {
    return "сохранённый секрет";
  }

  if (typeof value === "boolean") {
    return value ? "Да" : "Нет";
  }

  if (typeof value === "string") {
    return value.length > 0 ? value : "Пусто";
  }

  if (typeof value === "number") {
    return String(value);
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      return "Пусто";
    }

    return value
      .map((entry) => formatValue(entry))
      .join(", ");
  }

  return JSON.stringify(value);
}

function collectMaskedPaths(value: unknown, prefix = ""): string[] {
  if (value === MASKED_SECRET_PLACEHOLDER) {
    return prefix ? [prefix] : [];
  }

  if (Array.isArray(value)) {
    return value.flatMap((entry, index) =>
      collectMaskedPaths(entry, `${prefix}[${index}]`)
    );
  }

  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>).flatMap(([key, entry]) =>
      collectMaskedPaths(entry, prefix ? `${prefix}.${key}` : key)
    );
  }

  return [];
}

async function getSecretCiphertext(revisionId: string) {
  const [secret] = await db
    .select()
    .from(passwallSecretBlobs)
    .where(eq(passwallSecretBlobs.desiredRevisionId, revisionId))
    .orderBy(desc(passwallSecretBlobs.createdAt))
    .limit(1);

  return secret?.ciphertext ?? null;
}

async function hydrateRevision(
  revision: typeof passwallDesiredRevisions.$inferSelect | null | undefined
) {
  if (!revision) {
    return null;
  }

  return hydratePasswallConfig(revision.config, await getSecretCiphertext(revision.id));
}

export function buildLastControllerUpdateAttempt(args: {
  jobs: Array<typeof jobs.$inferSelect>;
  results: Array<typeof jobResults.$inferSelect>;
}): LastControllerUpdateAttempt | null {
  const latestJob =
    [...args.jobs]
      .filter((job) => job.type === "update_controller")
      .sort((left, right) => right.createdAt.getTime() - left.createdAt.getTime())[0] ??
    null;

  if (!latestJob) {
    return null;
  }

  const relatedResults = args.results
    .filter((result) => result.jobId === latestJob.id)
    .sort((left, right) => right.reportedAt.getTime() - left.reportedAt.getTime());
  const preferredResult =
    relatedResults.find((result) => result.status !== "accepted") ??
    relatedResults[0] ??
    null;
  const resultStatus =
    preferredResult?.status === "accepted" ||
    preferredResult?.status === "success" ||
    preferredResult?.status === "failure"
      ? preferredResult.status
      : null;
  const artifactVersion =
    readStringField(latestJob.payload, "artifactVersion") ??
    readStringField(preferredResult?.payload ?? null, "artifactVersion");

  return {
    jobState: latestJob.state,
    resultStatus,
    artifactVersion,
    reportedAt: preferredResult?.reportedAt ?? null,
    summary: summarizeControllerUpdateAttempt({
      jobState: latestJob.state,
      resultStatus,
      payload: preferredResult?.payload ?? null,
    }),
  };
}

function summarizeControllerUpdateAttempt(args: {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  payload: Record<string, unknown> | null;
}) {
  const errorLine = firstMeaningfulLine(readStringField(args.payload, "error"));
  if (errorLine) {
    return errorLine;
  }

  const stderrLine = firstMeaningfulLine(readStringField(args.payload, "stderr"));
  if (stderrLine) {
    return stderrLine;
  }

  const stdoutLine = firstMeaningfulLine(readStringField(args.payload, "stdout"));
  if (stdoutLine) {
    return stdoutLine;
  }

  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return "обновление ещё выполняется";
  }

  if (args.resultStatus === "failure") {
    return "обновление завершилось ошибкой без подробностей";
  }

  return "обновление завершилось без подробностей";
}

function readStringField(
  payload: Record<string, unknown> | null | undefined,
  key: string,
) {
  const value = payload?.[key];
  return typeof value === "string" && value.trim().length > 0 ? value.trim() : null;
}

function firstMeaningfulLine(value: string | null | undefined) {
  if (!value) {
    return null;
  }

  const line =
    value
      .split(/\r?\n/)
      .map((entry) => entry.trim())
      .find((entry) => entry.length > 0) ?? null;
  return line;
}

export function mergeCurrentLiveRouterDataIntoDraftConfig(args: {
  draftConfig: ReturnType<typeof buildEditorSurface>["draftConfig"];
  currentLiveConfig: ReturnType<typeof buildEditorSurface>["currentLiveConfig"] | null;
}) {
  if (!args.currentLiveConfig) {
    return args.draftConfig;
  }

  const appendMissingById = <T extends { id: string }>(draftItems: T[], liveItems: T[]) => {
    const knownIds = new Set(draftItems.map((item) => item.id));
    return [
      ...draftItems,
      ...liveItems.filter((item) => !knownIds.has(item.id)),
    ];
  };

  const mergedNodes = appendMissingById(
    args.draftConfig.nodes,
    args.currentLiveConfig.nodes,
  );
  const mergedSubscriptions = appendMissingById(
    args.draftConfig.subscriptions.items,
    args.currentLiveConfig.subscriptions.items,
  );
  const mergedSocks = appendMissingById(
    args.draftConfig.basicSettings.socks,
    args.currentLiveConfig.basicSettings.socks,
  );
  const currentLiveSelectedNodeId =
    args.currentLiveConfig.basicSettings.main.selectedNodeId;
  const draftKnownNodeIds = new Set(args.draftConfig.nodes.map((node) => node.id));
  const shouldPreferCurrentLiveSelectedNode =
    typeof currentLiveSelectedNodeId === "string" &&
    currentLiveSelectedNodeId.length > 0 &&
    !draftKnownNodeIds.has(currentLiveSelectedNodeId) &&
    mergedNodes.some((node) => node.id === currentLiveSelectedNodeId);

  return {
    ...args.draftConfig,
    basicSettings: {
      ...args.draftConfig.basicSettings,
      main: {
        ...args.draftConfig.basicSettings.main,
        selectedNodeId: shouldPreferCurrentLiveSelectedNode
          ? currentLiveSelectedNodeId
          : args.draftConfig.basicSettings.main.selectedNodeId,
      },
      socks: mergedSocks,
    },
    nodes: mergedNodes,
    subscriptions: {
      ...args.draftConfig.subscriptions,
      items: mergedSubscriptions,
    },
  };
}

export async function getDraftEditorSurface(routerId: string) {
  const [router] = await db
    .select()
    .from(routers)
    .where(eq(routers.id, routerId))
    .limit(1);

  if (!router) {
    throw new Error("Router not found.");
  }

  const [snapshots, revisions, appliedRows, resultRows, recentJobs] = await Promise.all([
    db
      .select()
      .from(routerInventorySnapshots)
      .where(eq(routerInventorySnapshots.routerId, routerId))
      .orderBy(desc(routerInventorySnapshots.createdAt))
      .limit(5),
    db
      .select()
      .from(passwallDesiredRevisions)
      .where(eq(passwallDesiredRevisions.routerId, routerId))
      .orderBy(desc(passwallDesiredRevisions.revisionNumber))
      .limit(20),
    db
      .select()
      .from(passwallAppliedRevisions)
      .where(eq(passwallAppliedRevisions.routerId, routerId))
      .orderBy(desc(passwallAppliedRevisions.reportedAt))
      .limit(10),
    db
      .select()
      .from(jobResults)
      .where(eq(jobResults.routerId, routerId))
      .orderBy(desc(jobResults.reportedAt))
      .limit(20),
    db
      .select()
      .from(jobs)
      .where(eq(jobs.routerId, routerId))
      .orderBy(desc(jobs.createdAt))
      .limit(20),
  ]);

  const latestSnapshot = snapshots[0] ?? null;
  const payload = latestSnapshot?.payload;
  const layoutFamily =
    typeof payload?.layoutFamily === "string" ? payload.layoutFamily : null;
  const support = describeEffectiveRouterSupport({
    router: {
      boardName: router.boardName,
      target: router.target,
      architecture: router.architecture,
      openwrtRelease: router.openwrtRelease,
    },
    inventory: payload ?? null,
  });

  const importedRevision =
    revisions.find((revision) => revision.id === router.pendingImportRevisionId) ??
    revisions.find((revision) =>
      ["router_import", "operator_reimport"].includes(revision.origin)
    ) ??
    null;
  const activeRevision =
    revisions.find((revision) => revision.id === router.activeRevisionId) ?? null;
  const latestDraft =
    revisions.find((revision) => revision.origin === "operator_draft") ?? null;

  const currentLiveRevision =
    (latestSnapshot?.payload.configDigest
      ? revisions.find(
          (revision) =>
            ["router_import", "operator_reimport"].includes(revision.origin) &&
            revision.configDigest === latestSnapshot.payload.configDigest
        )
      : null) ?? null;

  const [currentLiveConfig, authoritativeConfig, draftConfig] = await Promise.all([
    hydrateRevision(currentLiveRevision),
    hydrateRevision(activeRevision),
    hydrateRevision(latestDraft ?? activeRevision ?? importedRevision),
  ]);

  const routerReachable = isRouterReachable(router.lastSeenAt);
  const currentConfigFreshness =
    currentLiveConfig && routerReachable
      ? "live"
      : currentLiveConfig || authoritativeConfig
        ? "stale"
        : "missing";
  const selectedNodeId =
    payload?.selectedNodeId ??
    currentLiveConfig?.basicSettings.main.selectedNodeId ??
    authoritativeConfig?.basicSettings.main.selectedNodeId ??
    null;
  const selectedNodeLabel =
    (typeof payload?.selectedNodeLabel === "string" &&
    payload.selectedNodeLabel.length > 0
      ? payload.selectedNodeLabel
      : null) ??
    currentLiveConfig?.nodes.find((node) => node.id === selectedNodeId)?.label ??
    authoritativeConfig?.nodes.find((node) => node.id === selectedNodeId)?.label ??
    selectedNodeId;

  const effectiveDraftBase =
    draftConfig ?? authoritativeConfig ?? currentLiveConfig ?? importedRevision?.config ?? null;
  const effectiveDraft = effectiveDraftBase
    ? mergeCurrentLiveRouterDataIntoDraftConfig({
        draftConfig: effectiveDraftBase,
        currentLiveConfig: routerReachable ? currentLiveConfig ?? null : null,
      })
    : null;
  if (!effectiveDraft) {
    throw new Error("Router has no editable PassWall2 configuration yet.");
  }

  const resultByJobId = new Map(resultRows.map((row) => [row.jobId, row]));
  const lastControllerUpdateAttempt = buildLastControllerUpdateAttempt({
    jobs: recentJobs,
    results: resultRows,
  });
  const applyHistory = appliedRows.map((row) => {
    const relatedJob = recentJobs.find((job) => job.id === row.jobId) ?? null;
    const relatedResult = row.jobId ? resultByJobId.get(row.jobId) ?? null : null;
    const payload = relatedResult?.payload ?? {};

    return {
      id: row.id,
      reportedAt: row.reportedAt,
      result: row.result,
      desiredRevisionId: row.desiredRevisionId,
      jobType: relatedJob?.type ?? null,
      configDigest: row.uciDigest ?? null,
      stdout: row.stdout ?? null,
      stderr: row.stderr ?? null,
      uciCommands: Array.isArray(payload.uciCommands)
        ? payload.uciCommands.filter(
            (entry): entry is string => typeof entry === "string"
          )
        : [],
      operationResults: Array.isArray(payload.operationResults)
        ? payload.operationResults
        : [],
      commandResults: Array.isArray(payload.commandResults)
        ? payload.commandResults
        : [],
      postApplyImportDigest:
        typeof payload.postApplyImportDigest === "string"
          ? payload.postApplyImportDigest
          : null,
    };
  });

  const editorSurface = buildEditorSurface({
    routerRuntimeSummary: {
      status: router.status,
      importState: router.importState,
      lastSeenAt: router.lastSeenAt,
      passwallEnabled: latestSnapshot?.passwallEnabled ?? false,
      selectedNodeId,
      selectedNodeLabel,
      pendingChanges: recentJobs.filter((job) =>
        ["queued", "delivered", "running"].includes(job.state)
      ).length,
      supportState: support.state,
      supportTitle: support.title,
      supportReason: support.reason,
      updateActionsAllowed: canRunUpdateAction(support.state),
    },
    currentLiveConfig: currentLiveConfig ?? authoritativeConfig ?? effectiveDraft,
    authoritativeConfig,
    draftConfig: effectiveDraft,
    currentConfigFreshness:
      currentConfigFreshness === "live" ? "live" : "stale",
  });

  return {
    ...editorSurface,
    routerRuntimeSummary: {
      id: router.id,
      name:
        router.displayName ??
        payload?.hostname ??
        router.hostname ??
        router.deviceIdentifier,
      destructiveActionsAllowed: canRunDestructiveAction(support.state),
      boardName: router.boardName,
      layoutFamily,
      controllerVersion: latestSnapshot?.controllerVersion ?? null,
      passwallVersion: latestSnapshot?.passwallAppVersion ?? null,
      lastRescueReason:
        payload?.lastRescue?.reason ?? router.lastRescueReason ?? null,
      currentConfigFreshness,
      ...editorSurface.routerRuntimeSummary,
    },
    fieldDiffs: editorSurface.fieldDiffs.map((diff) => ({
      ...diff,
      currentDisplay: formatValue(diff.currentValue),
      authoritativeDisplay: formatValue(diff.authoritativeValue),
      draftDisplay: formatValue(diff.draftValue),
      sourceBadge: diff.source,
      isMasked:
        diff.currentValue === MASKED_SECRET_PLACEHOLDER ||
        diff.authoritativeValue === MASKED_SECRET_PLACEHOLDER ||
        diff.draftValue === MASKED_SECRET_PLACEHOLDER,
    })),
    maskedFields:
      editorSurface.maskedFields.length > 0
        ? editorSurface.maskedFields
        : collectMaskedPaths(effectiveDraft),
    currentConfigFreshness:
      currentConfigFreshness === "live" ? "live" : "stale",
    applyHistory,
    lastControllerUpdateAttempt,
    approvalRequired: ["import_review", "out_of_sync"].includes(router.importState),
    importedRevisionId: importedRevision?.id ?? null,
    activeRevisionId: activeRevision?.id ?? null,
    latestDraftId: latestDraft?.id ?? null,
  };
}
