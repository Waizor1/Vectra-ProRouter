import {
  collectOptimizationBaselineJobPayloadSchema,
  optimizationBaselineResultPayloadSchema,
  type CollectOptimizationBaselineJobPayload,
  type OptimizationBaselineResultPayload,
} from "@vectra/contracts";

type RouterOptimizationJobLike = {
  id: string;
  state: string;
  payload: Record<string, unknown> | null;
  createdAt: Date;
  completedAt: Date | null;
};

type RouterOptimizationJobResultLike = {
  jobId: string;
  status: string;
  payload: Record<string, unknown> | null;
  reportedAt: Date;
};

export type RouterOptimizationBaselineHistoryItem = {
  jobId: string;
  state: string;
  createdAt: string;
  completedAt: string | null;
  request: CollectOptimizationBaselineJobPayload;
  resultStatus: "accepted" | "success" | "failure" | null;
  collectedAt: string | null;
  ok: boolean | null;
  baselineVersion: string | null;
  resources: Record<string, unknown>;
  serviceHealth: OptimizationBaselineResultPayload["serviceHealth"];
  processCount: number;
  conntrack: OptimizationBaselineResultPayload["conntrack"];
  processes: OptimizationBaselineResultPayload["processes"];
  logs: OptimizationBaselineResultPayload["logs"] | null;
  routeVerification:
    | OptimizationBaselineResultPayload["routeVerification"]
    | null;
  packageVersions: OptimizationBaselineResultPayload["packageVersions"];
  binaryVersions: OptimizationBaselineResultPayload["binaryVersions"];
  warnings: string[];
  errors: string[];
  result: OptimizationBaselineResultPayload | null;
};

export function buildRouterOptimizationBaselineHistory(args: {
  jobs: RouterOptimizationJobLike[];
  results: RouterOptimizationJobResultLike[];
}) {
  const resultsByJob = new Map<string, RouterOptimizationJobResultLike[]>();
  for (const result of args.results) {
    const bucket = resultsByJob.get(result.jobId);
    if (bucket) {
      bucket.push(result);
    } else {
      resultsByJob.set(result.jobId, [result]);
    }
  }

  for (const bucket of resultsByJob.values()) {
    bucket.sort(
      (left, right) => right.reportedAt.getTime() - left.reportedAt.getTime(),
    );
  }

  const history = [...args.jobs]
    .sort((left, right) => right.createdAt.getTime() - left.createdAt.getTime())
    .map((job) => {
      const request = parseOptimizationBaselineRequest(job.payload);
      const relatedResults = resultsByJob.get(job.id) ?? [];
      const preferredResult =
        relatedResults.find((result) => result.status !== "accepted") ??
        relatedResults[0] ??
        null;
      const parsedResult = parseOptimizationBaselineResultPayload(
        preferredResult?.payload ?? null,
      );
      const fallbackFailure = parsedResult
        ? null
        : parseOptimizationBaselineFailurePayload(
            preferredResult?.payload ?? null,
            preferredResult?.status ?? null,
          );

      return {
        jobId: job.id,
        state: job.state,
        createdAt: job.createdAt.toISOString(),
        completedAt: job.completedAt?.toISOString() ?? null,
        request,
        resultStatus:
          preferredResult?.status === "accepted" ||
          preferredResult?.status === "success" ||
          preferredResult?.status === "failure"
            ? preferredResult.status
            : null,
        collectedAt:
          parsedResult?.collectedAt ?? fallbackFailure?.collectedAt ?? null,
        ok:
          typeof parsedResult?.ok === "boolean"
            ? parsedResult.ok
            : fallbackFailure
              ? false
              : null,
        baselineVersion: parsedResult?.baselineVersion ?? null,
        resources: parsedResult?.resources ?? fallbackFailure?.resources ?? {},
        serviceHealth: parsedResult?.serviceHealth ?? {},
        processCount: parsedResult?.processes.length ?? 0,
        conntrack: parsedResult?.conntrack ?? {},
        processes: parsedResult?.processes ?? [],
        logs: parsedResult?.logs ?? null,
        routeVerification: parsedResult?.routeVerification ?? null,
        packageVersions: parsedResult?.packageVersions ?? {},
        binaryVersions: parsedResult?.binaryVersions ?? {},
        warnings: parsedResult?.warnings ?? fallbackFailure?.warnings ?? [],
        errors: parsedResult?.errors ?? fallbackFailure?.errors ?? [],
        result: parsedResult,
      } satisfies RouterOptimizationBaselineHistoryItem;
    });

  return {
    activeRequest:
      history.find((item) =>
        ["queued", "delivered", "running"].includes(item.state),
      ) ?? null,
    latestBaseline:
      history.find(
        (item) =>
          item.resultStatus === "success" || item.resultStatus === "failure",
      ) ?? null,
    history,
  };
}

function parseOptimizationBaselineRequest(
  payload: Record<string, unknown> | null,
): CollectOptimizationBaselineJobPayload {
  const parsed = collectOptimizationBaselineJobPayloadSchema.safeParse(
    payload ?? {},
  );
  return parsed.success
    ? parsed.data
    : collectOptimizationBaselineJobPayloadSchema.parse({});
}

function parseOptimizationBaselineResultPayload(
  payload: Record<string, unknown> | null,
): OptimizationBaselineResultPayload | null {
  if (!payload) {
    return null;
  }

  const parsed = optimizationBaselineResultPayloadSchema.safeParse(payload);
  return parsed.success ? parsed.data : null;
}

function parseOptimizationBaselineFailurePayload(
  payload: Record<string, unknown> | null,
  status: string | null,
) {
  if (status !== "failure" || !payload) {
    return null;
  }

  const resources = asRecord(payload.resources) ?? {};
  const warnings = stringArray(payload.reasons);
  const error = stringValue(payload.error);
  const code = stringValue(payload.code);
  const errors = [error && code ? `${code}: ${error}` : (error ?? code)].filter(
    (value): value is string => typeof value === "string",
  );

  if (
    Object.keys(resources).length === 0 &&
    warnings.length === 0 &&
    errors.length === 0
  ) {
    return null;
  }

  return {
    collectedAt: stringValue(payload.checkedAt),
    resources,
    warnings,
    errors,
  };
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }

  return value as Record<string, unknown>;
}

function stringValue(value: unknown) {
  if (typeof value !== "string") {
    return null;
  }

  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

function stringArray(value: unknown) {
  if (!Array.isArray(value)) {
    return [];
  }

  return value
    .map((item) => stringValue(item))
    .filter((item): item is string => Boolean(item));
}
