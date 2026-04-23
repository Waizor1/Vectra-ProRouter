import {
  type PasswallDesiredConfig,
  MASKED_SECRET_PLACEHOLDER,
  type RouterTelegramReachability,
  subscriptionInspectResultPayloadSchema,
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
import {
  isRouterHostnameUpdateJob,
  isRouterHostnameUpdateTerminalPayload,
} from "~/lib/router-hostname-jobs";
import {
  isRouterRebootJob,
  isRouterRebootTerminalPayload,
} from "~/lib/router-reboot-jobs";
import { db } from "~/server/db";
import { buildEditorSurface } from "~/server/vectra/editor";
import { buildConfigTrustState } from "~/server/vectra/config-trust";
import {
  pickActiveRevision,
  pickCurrentLiveRevision,
  pickImportedRevision,
  pickLatestEditableDraft,
  pickLatestOperatorDraft,
  pickWorkspaceRevision,
} from "~/server/vectra/draft-selection";
import { isRouterReachable } from "~/server/vectra/router-presence";
import {
  buildSubscriptionPreviewDigest,
  buildSubscriptionPreviewLookup,
  buildSubscriptionRuntimeReadModel,
  mergeNodesWithCurrentRuntime,
  mergeSubscriptionsBySemanticIdentity,
  type SubscriptionRuntimeReadModel,
} from "~/server/vectra/subscription-runtime";
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

export type RouterManagementTaskLogItem = {
  jobId: string;
  kind:
    | "controller-update"
    | "controller-self-update"
    | "passwall-update"
    | "subscription-preview"
    | "router-hostname-update"
    | "router-reboot";
  label: string;
  jobType: string;
  updateScope: string | null;
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  createdAt: Date;
  reportedAt: Date | null;
  summary: string;
  error: string | null;
  stdout: string | null;
  stderr: string | null;
  command: string | null;
  artifactVersion: string | null;
  targetVersion: string | null;
  packageTargetVersion: string | null;
  runtimeTargetVersion: string | null;
  deliveryBlocked: boolean;
  deliveryBlockedReason: string | null;
  packageResults: LastPasswallUpdateAttempt["packageResults"];
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

export type RouterSubscriptionRuntime = SubscriptionRuntimeReadModel;

export type UnconfirmedChangeItem = {
  path: string;
  section: string;
  label: string;
  before: string;
  after: string;
};

export type UnconfirmedChangeGroup = {
  status: "none" | "reimport-needed" | "pending-import-review" | "saved-draft-pending-apply";
  exact: boolean;
  title: string;
  summary: string;
  changeCount: number;
  changedSections: string[];
  items: UnconfirmedChangeItem[];
  revisionId: string | null;
};

export type UnconfirmedChangesSummary = {
  router: UnconfirmedChangeGroup;
  panel: UnconfirmedChangeGroup;
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

function buildComparisonSurface(args: {
  currentConfig: PasswallDesiredConfig;
  authoritativeConfig: PasswallDesiredConfig | null;
  draftConfig: PasswallDesiredConfig;
}) {
  return buildEditorSurface({
    routerRuntimeSummary: {
      status: "active",
      importState: "approved",
      lastSeenAt: null,
      passwallEnabled: args.currentConfig.basicSettings.main.mainSwitch,
      selectedNodeId: args.currentConfig.basicSettings.main.selectedNodeId ?? null,
      selectedNodeLabel: args.currentConfig.basicSettings.main.selectedNodeId ?? null,
      pendingChanges: 0,
      supportState: "certified",
      supportTitle: "Сертифицировано",
      supportReason: "Derived comparison summary.",
    },
    currentLiveConfig: args.currentConfig,
    authoritativeConfig: args.authoritativeConfig,
    draftConfig: args.draftConfig,
    currentConfigFreshness: "live",
    configSourceMode: "live-import",
  });
}

function buildChangeItems(args: {
  fieldDiffs: ReturnType<typeof buildComparisonSurface>["fieldDiffs"];
  mode: "current" | "draft";
  limit?: number;
}) {
  const relevantDiffs = args.fieldDiffs.filter((diff) =>
    args.mode === "current" ? !diff.currentMatchesAuthoritative : diff.draftChanged,
  );
  const changedSections = [...new Set(relevantDiffs.map((diff) => diff.section))];
  const items = relevantDiffs.slice(0, args.limit ?? 6).map((diff) => ({
    path: diff.path,
    section: diff.section,
    label: diff.label,
    before: formatValue(diff.authoritativeValue),
    after: formatValue(args.mode === "current" ? diff.currentValue : diff.draftValue),
  }));

  return {
    changeCount: relevantDiffs.length,
    changedSections,
    items,
  };
}

export function buildUnconfirmedChangesSummary(args: {
  importState: string;
  configTrust: ReturnType<typeof buildConfigTrustState>;
  activeRevisionId: string | null;
  importedRevisionId: string | null;
  latestDraftId: string | null;
  authoritativeConfig: PasswallDesiredConfig | null;
  importedConfig: PasswallDesiredConfig | null;
  draftConfig: PasswallDesiredConfig;
}) : UnconfirmedChangesSummary {
  const noneGroup = (title: string, summary: string): UnconfirmedChangeGroup => ({
    status: "none",
    exact: false,
    title,
    summary,
    changeCount: 0,
    changedSections: [],
    items: [],
    revisionId: null,
  });

  let router = noneGroup(
    "Роутер не показал неподтверждённых изменений",
    "Последний подтверждённый deep config панели сейчас не расходится с тем, что уже подтверждено import-ом.",
  );

  if (
    args.importedConfig &&
    args.importedRevisionId &&
    ["import_review", "out_of_sync"].includes(args.importState)
  ) {
    const comparisonBase = args.authoritativeConfig ?? null;
    const comparison = buildComparisonSurface({
      currentConfig: args.importedConfig,
      authoritativeConfig: comparisonBase,
      draftConfig: comparisonBase ?? args.importedConfig,
    });
    const changes = buildChangeItems({
      fieldDiffs: comparison.fieldDiffs,
      mode: "current",
    });

    router = {
      status: "pending-import-review",
      exact: true,
      title: "На роутере есть изменения, которые панель ещё не подтвердила",
      summary:
        changes.changeCount > 0
          ? args.authoritativeConfig
            ? `Эти изменения пришли с роутера новым import-ом. Панель их ещё не приняла как эталон.`
            : `Это первый import с роутера. Панель уже считала live-конфигурацию, но вы ещё не подтвердили её как стартовый эталон.`
          : "Панель получила новый import с роутера, но значимые отличия по полям не удалось выделить автоматически.",
      changeCount: changes.changeCount,
      changedSections: changes.changedSections,
      items: changes.items,
      revisionId: args.importedRevisionId,
    };
  } else if (args.configTrust.requiresReimport) {
    router = {
      status: "reimport-needed",
      exact: false,
      title: "Роутер уже изменился, но точные deep-config правки ещё не считаны",
      summary:
        "Свежий snapshot уже показал новый config digest, но полного live import-а ещё нет. Панель видит, что deep config ушёл вперёд, но точный список полей появится только после re-import.",
      changeCount: 0,
      changedSections: [],
      items: [],
      revisionId: null,
    };
  }

  let panel = noneGroup(
    "В панели нет сохранённых, но неотправленных изменений",
    "Сохранённый черновик не расходится с текущим подтверждённым baseline панели.",
  );

  if (
    args.authoritativeConfig &&
    args.latestDraftId &&
    args.latestDraftId !== args.activeRevisionId
  ) {
    const comparison = buildComparisonSurface({
      currentConfig: args.authoritativeConfig,
      authoritativeConfig: args.authoritativeConfig,
      draftConfig: args.draftConfig,
    });
    const changes = buildChangeItems({
      fieldDiffs: comparison.fieldDiffs,
      mode: "draft",
    });

    if (changes.changeCount > 0) {
      panel = {
        status: "saved-draft-pending-apply",
        exact: true,
        title: "В панели есть сохранённые изменения, которые ещё не подтверждены на роутере",
        summary:
          "Эти правки уже сохранены как ревизия в панели, но router apply ещё не подтвердил их как текущее live-состояние.",
        changeCount: changes.changeCount,
        changedSections: changes.changedSections,
        items: changes.items,
        revisionId: args.latestDraftId,
      };
    }
  }

  return {
    router,
    panel,
  };
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
): Promise<PasswallDesiredConfig | null> {
  if (!revision) {
    return null;
  }

  return hydratePasswallConfig(revision.config, await getSecretCiphertext(revision.id));
}

function parseSubscriptionInspectPayload(payload: Record<string, unknown> | null) {
  const parsed = subscriptionInspectResultPayloadSchema.safeParse(payload ?? {});
  return parsed.success ? parsed.data : null;
}

function selectSubscriptionPreviewSource(args: {
  currentDigest: string;
  jobs: Array<typeof jobs.$inferSelect>;
  results: Array<typeof jobResults.$inferSelect>;
}) {
  const inspectJobs = args.jobs
    .filter((job) => job.type === "inspect_subscriptions")
    .sort((left, right) => right.createdAt.getTime() - left.createdAt.getTime());
  const inspectResults = args.results
    .filter((result) =>
      inspectJobs.some((job) => job.id === result.jobId),
    )
    .sort((left, right) => right.reportedAt.getTime() - left.reportedAt.getTime());
  const successfulResults = inspectResults
    .filter((result) => result.status === "success")
    .map((result) => ({
      row: result,
      payload: parseSubscriptionInspectPayload(result.payload),
    }))
    .filter(
      (
        entry,
      ): entry is {
        row: typeof jobResults.$inferSelect;
        payload: ReturnType<typeof parseSubscriptionInspectPayload> extends infer T
          ? Exclude<T, null>
          : never;
      } => entry.payload !== null,
    );

  const matchingResult =
    successfulResults.find(
      (entry) => entry.payload.subscriptionDigest === args.currentDigest,
    ) ?? null;
  const freshResult =
    matchingResult &&
    Date.now() - new Date(matchingResult.payload.checkedAt).getTime() <=
      24 * 60 * 60 * 1000
      ? matchingResult.payload
      : null;
  const hasPendingJob = inspectJobs.some((job) =>
    ["queued", "delivered", "running"].includes(job.state),
  );
  const hasFailedJob = inspectResults.some((result) => result.status === "failure");
  const hasStaleResult =
    matchingResult !== null ||
    successfulResults.length > 0;

  return {
    freshResult,
    hasPendingJob,
    hasFailedJob,
    hasStaleResult: hasStaleResult && freshResult === null,
  };
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
  const deliveryBlockedReason = readStringField(args.payload, "deliveryBlockedReason");
  if (args.payload?.deliveryBlocked === true) {
    return deliveryBlockedReason
      ? `job поставлен в очередь, но сервер сейчас не сохраняет check-in: ${deliveryBlockedReason}`
      : "job поставлен в очередь, но сервер сейчас не сохраняет check-in";
  }

  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return "обновление ещё выполняется";
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

function summarizeRouterRebootAttempt(args: {
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

  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return "перезагрузка ещё ожидает выполнения";
  }

  const stdoutLine = firstMeaningfulLine(readStringField(args.payload, "stdout"));
  if (stdoutLine) {
    return stdoutLine;
  }

  if (args.resultStatus === "failure") {
    return "перезагрузка завершилась ошибкой без подробностей";
  }

  return "роутер принял задачу на перезагрузку";
}

function summarizeRouterHostnameUpdateAttempt(args: {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  payload: Record<string, unknown> | null;
}) {
  const errorLine = firstMeaningfulLine(readStringField(args.payload, "error"));
  if (errorLine) {
    return errorLine;
  }

  const hostnameAfter =
    readStringField(args.payload, "hostnameAfter") ??
    readStringField(args.payload, "hostname");

  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return hostnameAfter
      ? `ожидаю, когда роутер применит hostname ${hostnameAfter}`
      : "ожидаю, когда роутер применит новый hostname";
  }

  const stdoutLine = firstMeaningfulLine(readStringField(args.payload, "stdout"));
  if (stdoutLine) {
    return stdoutLine;
  }

  if (args.resultStatus === "failure") {
    return "смена hostname завершилась ошибкой без подробностей";
  }

  return hostnameAfter
    ? `hostname ${hostnameAfter} применён`
    : "hostname обновлён";
}

function summarizeSubscriptionInspectAttempt(args: {
  jobState: string;
  resultStatus: ControllerUpdateAttemptStatus;
  payload: Record<string, unknown> | null;
}) {
  const errorLine = firstMeaningfulLine(readStringField(args.payload, "error"));
  if (errorLine) {
    return errorLine;
  }

  if (
    ["queued", "delivered", "running"].includes(args.jobState) ||
    args.resultStatus === "accepted"
  ) {
    return "роутер ещё проверяет текущую подписку PassWall";
  }

  const checkedSubscriptions = args.payload?.checkedSubscriptions;
  if (typeof checkedSubscriptions === "number" && Number.isFinite(checkedSubscriptions)) {
    return `preview собран: ${checkedSubscriptions} подписок`;
  }

  if (args.resultStatus === "failure") {
    return "preview подписок завершился ошибкой без подробностей";
  }

  return "preview подписок завершён";
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

function getPreferredJobResult(args: {
  jobId: string;
  results: Array<typeof jobResults.$inferSelect>;
}) {
  const relatedResults = args.results
    .filter((result) => result.jobId === args.jobId)
    .sort((left, right) => right.reportedAt.getTime() - left.reportedAt.getTime());

  return (
    relatedResults.find((result) => result.status !== "accepted") ??
    relatedResults[0] ??
    null
  );
}

export function buildRouterManagementTaskLog(args: {
  jobs: Array<typeof jobs.$inferSelect>;
  results: Array<typeof jobResults.$inferSelect>;
  installedControllerVersion?: string | null;
}): RouterManagementTaskLogItem[] {
  return [...args.jobs]
    .filter((job) => {
      if (
        isControllerUpdateJob(job) ||
        isRouterRebootJob(job) ||
        isRouterHostnameUpdateJob(job)
      ) {
        return true;
      }

      return (
        job.type === "update_passwall_packages" ||
        job.type === "inspect_subscriptions"
      );
    })
    .sort((left, right) => right.createdAt.getTime() - left.createdAt.getTime())
    .slice(0, 5)
    .map((job) => {
      const preferredResult = getPreferredJobResult({
        jobId: job.id,
        results: args.results,
      });
      const payload = preferredResult?.payload ?? null;
      const rawResultStatus =
        preferredResult?.status === "accepted" ||
        preferredResult?.status === "success" ||
        preferredResult?.status === "failure"
          ? preferredResult.status
          : null;
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

      const updateScope = readStringField(job.payload, "updateScope") ?? readStringField(payload, "updateScope");
      const deliveryBlocked =
        (typeof payload?.deliveryBlocked === "boolean" && payload.deliveryBlocked) ||
        packageResults.some((entry) => entry.status === "delivery-blocked");
      const deliveryBlockedReason =
        readStringField(payload, "deliveryBlockedReason") ??
        packageResults.find((entry) => entry.status === "delivery-blocked")?.error ??
        null;
      const installedControllerVersion = normalizeControllerVersion(
        args.installedControllerVersion,
      );
      const artifactVersion =
        readStringField(job.payload, "artifactVersion") ??
        readStringField(payload, "artifactVersion");
      const convergedAfterFailure =
        rawResultStatus === "failure" &&
        installedControllerVersion !== null &&
        normalizeControllerVersion(artifactVersion) !== null &&
        (compareControllerVersions(installedControllerVersion, artifactVersion) ?? -1) >= 0;
      const resultStatus = convergedAfterFailure ? "success" : rawResultStatus;

      let kind: RouterManagementTaskLogItem["kind"] = "controller-update";
      let label = "Обновление controller";
      let summary = summarizeControllerUpdateAttempt({
        jobState: job.state,
        resultStatus,
        payload,
        installedControllerVersion,
        convergedAfterFailure,
      });

      if (job.type === "run_terminal_command" && isRouterRebootTerminalPayload(job.payload)) {
        kind = "router-reboot";
        label = "Перезагрузка роутера";
        summary = summarizeRouterRebootAttempt({
          jobState: job.state,
          resultStatus,
          payload,
        });
      } else if (
        job.type === "run_terminal_command" &&
        isRouterHostnameUpdateTerminalPayload(job.payload)
      ) {
        kind = "router-hostname-update";
        label = "Смена OpenWrt hostname";
        summary = summarizeRouterHostnameUpdateAttempt({
          jobState: job.state,
          resultStatus,
          payload,
        });
      } else if (job.type === "run_terminal_command") {
        kind = "controller-self-update";
        label = "Self-update controller";
      } else if (job.type === "update_passwall_packages") {
        kind = "passwall-update";
        label = updateScope === "scoped-package" ? "Точечное обновление PassWall" : "Обновление PassWall stack";
        summary = summarizePasswallUpdateAttempt({
          jobState: job.state,
          resultStatus: deliveryBlocked ? null : resultStatus,
          payload,
          packageResults,
          driftDetected:
            (typeof payload?.driftDetected === "boolean" && payload.driftDetected) ||
            packageResults.some((entry) => entry.driftDetected),
        });
      } else if (job.type === "inspect_subscriptions") {
        kind = "subscription-preview";
        label = "Проверка подписки на роутере";
        summary = summarizeSubscriptionInspectAttempt({
          jobState: job.state,
          resultStatus,
          payload,
        });
      }

      return {
        jobId: job.id,
        kind,
        label,
        jobType: job.type,
        updateScope,
        jobState: job.state,
        resultStatus,
        createdAt: job.createdAt,
        reportedAt: preferredResult?.reportedAt ?? null,
        summary,
        error: readStringField(payload, "error"),
        stdout: readStringField(payload, "stdout"),
        stderr: readStringField(payload, "stderr"),
        command: readStringField(job.payload, "command") ?? readStringField(payload, "command"),
        artifactVersion,
        targetVersion:
          readStringField(job.payload, "targetVersion") ??
          readStringField(payload, "targetVersion"),
        packageTargetVersion:
          readStringField(job.payload, "packageTargetVersion") ??
          readStringField(payload, "packageTargetVersion"),
        runtimeTargetVersion:
          readStringField(job.payload, "runtimeTargetVersion") ??
          readStringField(payload, "runtimeTargetVersion"),
        deliveryBlocked,
        deliveryBlockedReason,
        packageResults,
      };
    });
}

type DraftConfig = ReturnType<typeof buildEditorSurface>["draftConfig"];
type DraftNode = DraftConfig["nodes"][number];
type DraftExtras = DraftNode["extras"];
type DraftShuntRule = DraftConfig["basicSettings"]["shuntRules"][number];

const preservedNodeReferenceValues = new Set(["_default", "_direct", "_blackhole"]);

function normalizeNodeReferenceText(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed.toLowerCase() : null;
}

function findNodeById(
  nodes: DraftConfig["nodes"],
  nodeId: string | null | undefined,
) {
  const normalizedId = nodeId?.trim();
  if (!normalizedId) {
    return null;
  }

  return nodes.find((node) => node.id === normalizedId) ?? null;
}

function findMatchingNodeByLabel(
  targetNodes: DraftConfig["nodes"],
  referenceNode: DraftNode | null,
) {
  if (!referenceNode) {
    return null;
  }

  const referenceLabel = normalizeNodeReferenceText(referenceNode.label);
  if (!referenceLabel) {
    return null;
  }

  const referenceGroup = normalizeNodeReferenceText(referenceNode.group);

  return (
    targetNodes.find(
      (node) =>
        normalizeNodeReferenceText(node.label) === referenceLabel &&
        normalizeNodeReferenceText(node.group) === referenceGroup,
    ) ??
    targetNodes.find(
      (node) => normalizeNodeReferenceText(node.label) === referenceLabel,
    ) ??
    null
  );
}

function rebindNodeReferenceId(args: {
  referenceId: string | null | undefined;
  referenceNodes: DraftConfig["nodes"];
  targetNodes: DraftConfig["nodes"];
  preferredNodeId?: string | null;
}) {
  const normalizedId = args.referenceId?.trim();
  if (!normalizedId) {
    return undefined;
  }

  if (preservedNodeReferenceValues.has(normalizedId)) {
    return normalizedId;
  }

  const preferredNode = findNodeById(args.targetNodes, args.preferredNodeId);
  const referenceNode =
    findNodeById(args.referenceNodes, normalizedId) ??
    findNodeById(args.targetNodes, normalizedId);

  if (preferredNode && referenceNode) {
    const preferredLabel = normalizeNodeReferenceText(preferredNode.label);
    const referenceLabel = normalizeNodeReferenceText(referenceNode.label);
    if (preferredLabel && preferredLabel === referenceLabel) {
      return preferredNode.id;
    }
  }

  const matchedNode =
    findMatchingNodeByLabel(args.targetNodes, referenceNode) ??
    findNodeById(args.targetNodes, normalizedId) ??
    preferredNode;

  return matchedNode?.id;
}

function rebindNodeReferenceList(args: {
  referenceIds: string[] | null | undefined;
  referenceNodes: DraftConfig["nodes"];
  targetNodes: DraftConfig["nodes"];
}) {
  if (!args.referenceIds || args.referenceIds.length === 0) {
    return [];
  }

  return [
    ...new Set(
      args.referenceIds
        .map((referenceId) =>
          rebindNodeReferenceId({
            referenceId,
            referenceNodes: args.referenceNodes,
            targetNodes: args.targetNodes,
          }),
        )
        .filter((referenceId): referenceId is string => Boolean(referenceId)),
    ),
  ];
}

function rebindNodeReferenceExtras(args: {
  extras: DraftExtras;
  referenceNodes: DraftConfig["nodes"];
  targetNodes: DraftConfig["nodes"];
}): DraftExtras {
  const nextExtras: DraftExtras = { ...args.extras };
  for (const [key, value] of Object.entries(nextExtras)) {
    if (typeof value !== "string") {
      continue;
    }

    if (
      key === "default_node" ||
      key === "to_node" ||
      key.endsWith("_proxy_tag")
    ) {
      const rebound = rebindNodeReferenceId({
        referenceId: value,
        referenceNodes: args.referenceNodes,
        targetNodes: args.targetNodes,
      });
      if (rebound) {
        nextExtras[key] = rebound;
      }
    }
  }

  return nextExtras;
}

function findMatchingShuntRuleByIdentity(
  rules: DraftShuntRule[],
  rule: DraftShuntRule,
) {
  const normalizedRuleId = normalizeNodeReferenceText(rule.id);
  if (normalizedRuleId) {
    const matchedById = rules.find(
      (entry) => normalizeNodeReferenceText(entry.id) === normalizedRuleId,
    );
    if (matchedById) {
      return matchedById;
    }
  }

  const normalizedLabel = normalizeNodeReferenceText(rule.label);
  if (!normalizedLabel) {
    return null;
  }

  return (
    rules.find(
      (entry) => normalizeNodeReferenceText(entry.label) === normalizedLabel,
    ) ?? null
  );
}

function resolveShuntRuleOutboundNodeId(args: {
  rule: DraftShuntRule;
  liveRules: DraftShuntRule[];
  referenceNodes: DraftConfig["nodes"];
  targetNodes: DraftConfig["nodes"];
}) {
  const rebound = rebindNodeReferenceId({
    referenceId: args.rule.outboundNodeId,
    referenceNodes: args.referenceNodes,
    targetNodes: args.targetNodes,
  });
  if (rebound) {
    return rebound;
  }

  const liveRule = findMatchingShuntRuleByIdentity(args.liveRules, args.rule);
  return liveRule?.outboundNodeId ?? args.rule.outboundNodeId;
}

function rebindDraftNodeReferences(args: {
  draftConfig: DraftConfig;
  referenceNodes: DraftConfig["nodes"];
  currentLiveConfig: DraftConfig | null;
  preferredSelectedNodeId?: string | null;
}): DraftConfig {
  const targetNodes = args.draftConfig.nodes;

  return {
    ...args.draftConfig,
    basicSettings: {
      ...args.draftConfig.basicSettings,
      main: {
        ...args.draftConfig.basicSettings.main,
        selectedNodeId: rebindNodeReferenceId({
          referenceId: args.draftConfig.basicSettings.main.selectedNodeId,
          referenceNodes: args.referenceNodes,
          targetNodes,
          preferredNodeId: args.preferredSelectedNodeId,
        }),
      },
      socks: args.draftConfig.basicSettings.socks.map((entry) => ({
        ...entry,
        nodeId:
          rebindNodeReferenceId({
            referenceId: entry.nodeId,
            referenceNodes: args.referenceNodes,
            targetNodes,
          }) ?? entry.nodeId,
        autoswitchBackupNodeIds: rebindNodeReferenceList({
          referenceIds: entry.autoswitchBackupNodeIds,
          referenceNodes: args.referenceNodes,
          targetNodes,
        }),
      })),
      shuntRules: args.draftConfig.basicSettings.shuntRules.map((rule) => ({
        ...rule,
        outboundNodeId: resolveShuntRuleOutboundNodeId({
          rule,
          liveRules: args.currentLiveConfig?.basicSettings.shuntRules ?? [],
          referenceNodes: args.referenceNodes,
          targetNodes,
        }),
      })),
    },
    nodes: args.draftConfig.nodes.map((node) => ({
      ...node,
      extras: rebindNodeReferenceExtras({
        extras: node.extras,
        referenceNodes: args.referenceNodes,
        targetNodes,
      }),
    })),
    subscriptions: {
      ...args.draftConfig.subscriptions,
      items: args.draftConfig.subscriptions.items.map((item) => ({
        ...item,
        extras: rebindNodeReferenceExtras({
          extras: item.extras,
          referenceNodes: args.referenceNodes,
          targetNodes,
        }),
      })),
    },
    ruleManage: {
      ...args.draftConfig.ruleManage,
      shuntRules: args.draftConfig.ruleManage.shuntRules.map((rule) => ({
        ...rule,
        outboundNodeId: resolveShuntRuleOutboundNodeId({
          rule,
          liveRules: args.currentLiveConfig?.ruleManage.shuntRules ?? [],
          referenceNodes: args.referenceNodes,
          targetNodes,
        }),
      })),
    },
  };
}

export function mergeCurrentLiveRouterDataIntoDraftConfig(args: {
  draftConfig: PasswallDesiredConfig;
  currentLiveConfig: PasswallDesiredConfig | null;
}): PasswallDesiredConfig {
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

  const mergedNodes = mergeNodesWithCurrentRuntime({
    draftNodes: args.draftConfig.nodes,
    liveNodes: args.currentLiveConfig.nodes,
    liveSubscriptions: args.currentLiveConfig.subscriptions.items,
  });
  const mergedSubscriptions = mergeSubscriptionsBySemanticIdentity({
    draftItems: args.draftConfig.subscriptions.items,
    liveItems: args.currentLiveConfig.subscriptions.items,
  });
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

  return rebindDraftNodeReferences({
    draftConfig: {
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
    },
    referenceNodes: args.draftConfig.nodes,
    currentLiveConfig: args.currentLiveConfig,
    preferredSelectedNodeId: shouldPreferCurrentLiveSelectedNode
      ? currentLiveSelectedNodeId
      : null,
  });
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

  const importedRevision = pickImportedRevision({
    pendingImportRevisionId: router.pendingImportRevisionId,
    revisions,
  });
  const activeRevision = pickActiveRevision({
    activeRevisionId: router.activeRevisionId,
    revisions,
  });
  const latestPanelDraftRevision = pickLatestOperatorDraft(revisions);
  const latestEditableDraftRevision = pickLatestEditableDraft(revisions);
  const liveImportRevisions = revisions.filter((revision) =>
    ["router_import", "operator_reimport"].includes(revision.origin),
  );
  const currentLiveRevision = pickCurrentLiveRevision({
    snapshotDigest: latestSnapshot?.payload.configDigest ?? null,
    revisions: liveImportRevisions,
  });
  const workspaceRevision = pickWorkspaceRevision({
    latestEditableDraft: latestEditableDraftRevision,
    currentLiveRevision,
    importedRevision,
    activeRevision,
    revisions,
  });

  const [currentLiveConfig, authoritativeConfig, workspaceConfig, importedConfig, latestPanelDraftConfig] = await Promise.all([
    hydrateRevision(currentLiveRevision),
    hydrateRevision(activeRevision),
    hydrateRevision(workspaceRevision),
    hydrateRevision(importedRevision),
    hydrateRevision(latestPanelDraftRevision),
  ]);

  const routerReachable = isRouterReachable(router.lastSeenAt);
  const configTrust = buildConfigTrustState({
    routerReachable,
    lastCheckInAt: router.lastCheckInAt ?? router.lastSeenAt,
    authoritativeDigest: router.lastConfigDigest,
    snapshotDigest: latestSnapshot?.payload.configDigest ?? null,
    revisions: liveImportRevisions,
    hasAuthoritativeConfig: Boolean(activeRevision),
  });
  const currentConfigFreshness =
    routerReachable
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
    workspaceConfig ??
    currentLiveConfig ??
    authoritativeConfig ??
    importedConfig ??
    null;
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
  const managementTaskLog = buildRouterManagementTaskLog({
    jobs: recentJobs,
    results: resultRows,
    installedControllerVersion,
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
    configSourceMode: configTrust.configSourceMode,
  });
  const unconfirmedChanges = buildUnconfirmedChangesSummary({
    importState: router.importState,
    configTrust,
    activeRevisionId: activeRevision?.id ?? null,
    importedRevisionId: importedRevision?.id ?? null,
    latestDraftId: latestEditableDraftRevision?.id ?? null,
    authoritativeConfig,
    importedConfig,
    draftConfig: effectiveDraft,
  });
  const runtimeConfigForAudit =
    currentLiveConfig ?? authoritativeConfig ?? effectiveDraft;
  const previewSource = selectSubscriptionPreviewSource({
    currentDigest: buildSubscriptionPreviewDigest(
      runtimeConfigForAudit.subscriptions,
    ),
    jobs: recentJobs,
    results: resultRows,
  });
  const previewLookup = buildSubscriptionPreviewLookup({
    subscriptions: runtimeConfigForAudit.subscriptions,
    freshResult: previewSource.freshResult
      ? {
          checkedAt: previewSource.freshResult.checkedAt,
          entries: previewSource.freshResult.entries,
        }
      : null,
    hasPendingJob: previewSource.hasPendingJob,
    hasFailedJob: previewSource.hasFailedJob,
    hasStaleResult: previewSource.hasStaleResult,
  });
  const subscriptionRuntime = buildSubscriptionRuntimeReadModel({
    runtimeConfig: runtimeConfigForAudit,
    draftConfig: effectiveDraft,
    latestPanelDraftConfig,
    previewLookup: previewLookup.stateByKey,
    previewState: previewLookup.previewState,
    selectedNodeId,
  });

  return {
    ...editorSurface,
    routerRuntimeSummary: {
      id: router.id,
      displayName: router.displayName ?? null,
      hostname: router.hostname ?? payload?.hostname ?? null,
      deviceIdentifier: router.deviceIdentifier,
      name:
        router.displayName ??
        router.hostname ??
        payload?.hostname ??
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
    configTrust,
    subscriptionRuntime,
    unconfirmedChanges,
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
    managementTaskLog,
    approvalRequired: ["import_review", "out_of_sync"].includes(router.importState),
    importedRevisionId: importedRevision?.id ?? null,
    activeRevisionId: activeRevision?.id ?? null,
    latestDraftId: latestEditableDraftRevision?.id ?? null,
    workspaceRevisionId: workspaceRevision?.id ?? null,
  };
}
