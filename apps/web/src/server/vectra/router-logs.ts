import {
  collectRouterLogsJobPayloadSchema,
  routerLogResultPayloadSchema,
  type RouterLogResultPayload,
  type RouterLogSnapshot,
  type RouterLogSource,
} from "@vectra/contracts";

type RouterLogJobLike = {
  id: string;
  state: string;
  payload: Record<string, unknown> | null;
  createdAt: Date;
  completedAt: Date | null;
};

type RouterLogJobResultLike = {
  jobId: string;
  status: string;
  payload: Record<string, unknown> | null;
  reportedAt: Date;
};

export type RouterLogHistoryItem = {
  jobId: string;
  state: string;
  createdAt: string;
  completedAt: string | null;
  request: {
    source: RouterLogSource;
    lines: number;
  };
  resultStatus: "accepted" | "success" | "failure" | null;
  collectedAt: string | null;
  snapshots: RouterLogSnapshot[];
  stdout: string | null;
  stderr: string | null;
  error: string | null;
};

export function buildRouterLogHistory(args: {
  jobs: RouterLogJobLike[];
  results: RouterLogJobResultLike[];
}) {
  const resultsByJob = new Map<string, RouterLogJobResultLike[]>();
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
      const parsedRequest = collectRouterLogsJobPayloadSchema.safeParse(
        job.payload ?? {},
      );
      const request = parsedRequest.success
        ? parsedRequest.data
        : collectRouterLogsJobPayloadSchema.parse({});
      const relatedResults = resultsByJob.get(job.id) ?? [];
      const preferredResult =
        relatedResults.find((result) => result.status !== "accepted") ??
        relatedResults[0] ??
        null;
      const parsedResult = parseRouterLogResultPayload(
        preferredResult?.payload ?? null,
      );

      return {
        jobId: job.id,
        state: job.state,
        createdAt: job.createdAt.toISOString(),
        completedAt: job.completedAt?.toISOString() ?? null,
        request: {
          source: request.source,
          lines: request.lines,
        },
        resultStatus:
          preferredResult?.status === "accepted" ||
          preferredResult?.status === "success" ||
          preferredResult?.status === "failure"
            ? preferredResult.status
            : null,
        collectedAt: parsedResult?.collectedAt ?? null,
        snapshots: parsedResult?.snapshots ?? [],
        stdout: parsedResult?.stdout ?? null,
        stderr: parsedResult?.stderr ?? null,
        error: parsedResult?.error ?? null,
      } satisfies RouterLogHistoryItem;
    });

  return {
    activeRequest:
      history.find((item) =>
        ["queued", "delivered", "running"].includes(item.state),
      ) ?? null,
    latestSnapshot:
      history.find(
        (item) =>
          item.resultStatus === "success" || item.resultStatus === "failure",
      ) ?? null,
    history,
  };
}

function parseRouterLogResultPayload(
  payload: Record<string, unknown> | null,
): RouterLogResultPayload | null {
  if (!payload) {
    return null;
  }

  const parsed = routerLogResultPayloadSchema.safeParse(payload);
  return parsed.success ? parsed.data : null;
}
