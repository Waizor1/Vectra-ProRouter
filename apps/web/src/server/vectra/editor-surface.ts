import {
  MASKED_SECRET_PLACEHOLDER,
  type RouterTelegramReachability,
} from "@vectra/contracts";
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

import {
  compareControllerVersions,
  normalizeControllerVersion,
  resolveInstalledControllerVersion,
} from "~/lib/controller-version";
import { isControllerUpdateJob } from "~/lib/controller-update-jobs";
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
type PasswallUpdateAttemptStatus = ControllerUpdateAttemptStatus;

export type LastControllerUpdateAttempt = {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  artifactVersion: string | null;
  reportedAt: Date | null;
  summary: string;
};

export type LastPasswallUpdateAttempt = {
  jobState: string;
  resultStatus: PasswallUpdateAttemptStatus;
  strategy: string | null;
  targetVersion: string | null;
  packageTargetVersion: string | null;
  runtimeTargetVersion: string | null;
  updateScope: string | null;
  originSource: string | null;
  driftDetected: boolean;
  deliveryBlocked: boolean;
  deliveryBlockedReason: string | null;
  fallbackSummary: string | null;
  reportedAt: Date | null;
  summary: string;
  packageResults: Array<{
    package: string;
    targetVersion: string | null;
    packageTargetVersion: string | null;
    runtimeTargetVersion: string | null;
    status: string | null;
    pathUsed: string | null;
    packageVersionBefore: string | null;
    packageVersionAfter: string | null;
    runtimeVersionBefore: string | null;
    runtimeVersionAfter: string | null;
    driftDetected: boolean;
    error: string | null;
  }>;
};

export type RouterWorkspaceInventory = {
  controllerVersion?: string | null;
  passwallVersion?: string | null;
  packageVersions: Record<string, string | null | undefined>;
  binaryVersions: Record<string, string | null | undefined>;
  resources?: {
    overlayFreeMb?: number | null;
    tmpFreeMb?: number | null;
  } | null;
  rulesAssets?: {
    assetDirectory?: string | null;
    geoipVersion?: string | null;
    geositeVersion?: string | null;
    geoipUpdatedAt?: string | null;
    geositeUpdatedAt?: string | null;
  } | null;
  serviceHealth?: {
    controller?: string | null;
    passwall?: string | null;
    dnsmasq?: string | null;
  } | null;
  telegramReachability?: RouterTelegramReachability | null;
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
  installedControllerVersion?: string | null;
}): LastControllerUpdateAttempt | null {
  const latestJob =
    [...args.jobs]
      .filter((job) => isControllerUpdateJob(job))
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
  const installedControllerVersion = normalizeControllerVersion(
    args.installedControllerVersion,
  );
  const convergedAfterFailure =
    resultStatus === "failure" &&
    installedControllerVersion !== null &&
    normalizeControllerVersion(artifactVersion) !== null &&
    (compareControllerVersions(installedControllerVersion, artifactVersion) ?? -1) >=
      0;
  const effectiveResultStatus = convergedAfterFailure ? "success" : resultStatus;

  return {
    jobState: latestJob.state,
    resultStatus: effectiveResultStatus,
    artifactVersion,
    reportedAt: preferredResult?.reportedAt ?? null,
    summary: summarizeControllerUpdateAttempt({
      jobState: latestJob.state,
      resultStatus: effectiveResultStatus,
      payload: preferredResult?.payload ?? null,
      installedControllerVersion,
      convergedAfterFailure,
    }),
  };
}

export function buildLastPasswallUpdateAttempt(args: {
  jobs: Array<typeof jobs.$inferSelect>;
  results: Array<typeof jobResults.$inferSelect>;
}): LastPasswallUpdateAttempt | null {
  const latestJob =
    [...args.jobs]
      .filter((job) => {
        if (job.type !== "update_passwall_packages") {
          return false;
        }
        const updateScope = readStringField(job.payload, "updateScope");
        return updateScope === null || updateScope === "managed-stack";
      })
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
  const payload = preferredResult?.payload ?? null;
  const packageResults = Array.isArray(payload?.packageResults)
    ? payload.packageResults
        .map((entry) => parsePasswallPackageResult(entry))
        .filter(
          (
            entry,
          ): entry is NonNullable<ReturnType<typeof parsePasswallPackageResult>> =>
            entry !== null,
        )
    : [];
  const driftDetected =
    (typeof payload?.driftDetected === "boolean" && payload.driftDetected) ||
    packageResults.some((entry) => entry.driftDetected);
  const deliveryBlocked =
    (typeof payload?.deliveryBlocked === "boolean" && payload.deliveryBlocked) ||
    packageResults.some((entry) => entry.status === "delivery-blocked");
  const deliveryBlockedReason =
    readStringField(payload, "deliveryBlockedReason") ??
    packageResults.find((entry) => entry.status === "delivery-blocked")?.error ??
    null;

  return {
    jobState: latestJob.state,
    resultStatus,
    strategy:
      readStringField(latestJob.payload, "strategy") ??
      readStringField(payload, "strategy"),
    targetVersion:
      readStringField(latestJob.payload, "targetVersion") ??
      readStringField(payload, "targetVersion"),
    packageTargetVersion:
      readStringField(latestJob.payload, "packageTargetVersion") ??
      readStringField(payload, "packageTargetVersion"),
    runtimeTargetVersion:
      readStringField(latestJob.payload, "runtimeTargetVersion") ??
      readStringField(payload, "runtimeTargetVersion"),
    updateScope:
      readStringField(latestJob.payload, "updateScope") ??
      readStringField(payload, "updateScope"),
    originSource:
      readStringField(latestJob.payload, "originSource") ??
      readStringField(payload, "originSource"),
    driftDetected,
    deliveryBlocked,
    deliveryBlockedReason,
    fallbackSummary: summarizePasswallFallback(packageResults),
    reportedAt: preferredResult?.reportedAt ?? null,
    summary: summarizePasswallUpdateAttempt({
      jobState: latestJob.state,
      resultStatus,
      payload,
      packageResults,
      driftDetected,
    }),
    packageResults,
  };
}

function summarizeControllerUpdateAttempt(args: {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  payload: Record<string, unknown> | null;
  installedControllerVersion: string | null;
  convergedAfterFailure: boolean;
}) {
  if (args.convergedAfterFailure && args.installedControllerVersion) {
    return `controller уже на ${args.installedControllerVersion}; старый failure-result после self-update больше не актуален`;
  }

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

function summarizePasswallUpdateAttempt(args: {
  jobState: string;
  resultStatus: PasswallUpdateAttemptStatus;
  payload: Record<string, unknown> | null;
  packageResults: LastPasswallUpdateAttempt["packageResults"];
  driftDetected: boolean;
}) {
  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return "обновление ещё выполняется";
  }

  const deliveryBlockedReason = readStringField(args.payload, "deliveryBlockedReason");
  if (args.payload?.deliveryBlocked === true) {
    return deliveryBlockedReason
      ? `job поставлен в очередь, но сервер сейчас не сохраняет check-in: ${deliveryBlockedReason}`
      : "job поставлен в очередь, но сервер сейчас не сохраняет check-in";
  }

  const blockedPackage = args.packageResults.find((entry) =>
    ["storage-blocked", "delivery-blocked"].includes(entry.status ?? ""),
  );
  if (blockedPackage) {
    if (blockedPackage.status === "storage-blocked") {
      return `${blockedPackage.package}: package path пропущен из-за места${
        blockedPackage.error ? ` (${blockedPackage.error})` : ""
      }`;
    }

    return `${blockedPackage.package}: доставка job на роутер сейчас блокируется${
      blockedPackage.error ? ` (${blockedPackage.error})` : ""
    }`;
  }

  const failedPackage = args.packageResults.find((entry) => entry.status === "failed");
  if (failedPackage) {
    return `${failedPackage.package}: ${
      failedPackage.error ?? "обновление пакета завершилось ошибкой"
    }`;
  }

  const errorLine = firstMeaningfulLine(readStringField(args.payload, "error"));
  if (errorLine) {
    return errorLine;
  }

  const fallbackSummary = summarizePasswallFallback(args.packageResults);
  if (fallbackSummary) {
    return fallbackSummary;
  }

  if (
    args.packageResults.length > 0 &&
    args.packageResults.every((entry) => entry.status === "already-current")
  ) {
    return "пакеты уже были на целевых версиях";
  }

  if (
    args.packageResults.length > 0 &&
    args.packageResults.every((entry) =>
      [
        "updated",
        "package-updated",
        "already-current",
        "runtime-updated",
        "runtime-only-converged",
      ].includes(entry.status ?? ""),
    )
  ) {
    return "обновление PassWall-стека завершено";
  }

  if (args.driftDetected) {
    return "обновление завершилось с runtime/package drift";
  }

  if (args.resultStatus === "failure") {
    return "обновление PassWall-стека завершилось ошибкой без подробностей";
  }

  return "обновление PassWall-стека завершилось без подробностей";
}

function summarizePasswallFallback(
  packageResults: LastPasswallUpdateAttempt["packageResults"],
) {
  const fallbackPackages = packageResults.filter(
    (entry) =>
      entry.status === "runtime-updated" ||
      entry.status === "runtime-only-converged" ||
      (entry.pathUsed !== null &&
        !["package", "not-needed"].includes(entry.pathUsed)),
  );

  if (fallbackPackages.length === 0) {
    return null;
  }

  return fallbackPackages
    .map((entry) => {
      const packageRecord =
        entry.packageVersionAfter ?? entry.packageVersionBefore ?? "unknown";
      const runtimeVersion =
        entry.runtimeVersionAfter ??
        entry.runtimeTargetVersion ??
        entry.targetVersion ??
        "target";
      if (entry.pathUsed === "built-in-updater") {
        if (entry.status === "runtime-only-converged") {
          return `${entry.package}: built-in updater уже держал runtime ${runtimeVersion}; запись пакета осталась ${packageRecord}`;
        }
        if (entry.status === "runtime-updated") {
          return `${entry.package}: built-in updater довёл runtime до ${runtimeVersion}; запись пакета осталась ${packageRecord}`;
        }
      }
      if (entry.pathUsed === "xray-binary-payload") {
        return `${entry.package}: fallback payload довёл runtime до ${runtimeVersion}; запись пакета осталась ${packageRecord}`;
      }

      const pathUsed =
        entry.pathUsed === "built-in-updater"
          ? "built-in updater"
          : entry.pathUsed === "xray-binary-payload"
            ? "xray binary payload"
            : entry.pathUsed ?? "fallback";
      return `${entry.package} via ${pathUsed}`;
    })
    .join(", ");
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

function parsePasswallPackageResult(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }

  const entry = value as Record<string, unknown>;
  const packageName = readStringField(entry, "package");
  if (!packageName) {
    return null;
  }

  return {
    package: packageName,
    targetVersion: readStringField(entry, "targetVersion"),
    packageTargetVersion: readStringField(entry, "packageTargetVersion"),
    runtimeTargetVersion: readStringField(entry, "runtimeTargetVersion"),
    status: readStringField(entry, "status"),
    pathUsed: readStringField(entry, "pathUsed"),
    packageVersionBefore: readStringField(entry, "packageVersionBefore"),
    packageVersionAfter: readStringField(entry, "packageVersionAfter"),
    runtimeVersionBefore: readStringField(entry, "runtimeVersionBefore"),
    runtimeVersionAfter: readStringField(entry, "runtimeVersionAfter"),
    driftDetected: entry.driftDetected === true,
    error: readStringField(entry, "error"),
  };
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
  const installedControllerVersion = resolveInstalledControllerVersion({
    controllerVersion: latestSnapshot?.controllerVersion ?? null,
    payload: payload ?? null,
  });
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
  const inventory: RouterWorkspaceInventory = {
    controllerVersion: installedControllerVersion,
    passwallVersion:
      latestSnapshot?.passwallAppVersion ??
      payload?.packageVersions["luci-app-passwall2"] ??
      null,
    packageVersions: payload?.packageVersions ?? {},
    binaryVersions: payload?.binaryVersions ?? {},
    resources: payload?.resources ?? null,
    rulesAssets: payload?.rulesAssets ?? null,
    serviceHealth: payload?.serviceHealth ?? null,
    telegramReachability: payload?.telegramReachability ?? null,
  };

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
    installedControllerVersion,
  });
  const lastPasswallUpdateAttempt = buildLastPasswallUpdateAttempt({
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
    inventory,
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
    lastPasswallUpdateAttempt,
    approvalRequired: ["import_review", "out_of_sync"].includes(router.importState),
    importedRevisionId: importedRevision?.id ?? null,
    activeRevisionId: activeRevision?.id ?? null,
    latestDraftId: latestDraft?.id ?? null,
  };
}
