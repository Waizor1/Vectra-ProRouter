import {
  runTerminalCommandJobPayloadSchema,
  routerTerminalResultPayloadSchema,
  type RouterTerminalResultPayload,
} from "@vectra/contracts";

type RouterTerminalJobLike = {
  id: string;
  state: string;
  payload: Record<string, unknown> | null;
  createdAt: Date;
  completedAt: Date | null;
};

type RouterTerminalJobResultLike = {
  jobId: string;
  status: string;
  payload: Record<string, unknown> | null;
  reportedAt: Date;
};

export type RouterTerminalHistoryItem = {
  jobId: string;
  state: string;
  createdAt: string;
  completedAt: string | null;
  request: {
    command: string;
    timeoutSeconds: number;
  };
  resultStatus: "accepted" | "success" | "failure" | null;
  startedAt: string | null;
  finishedAt: string | null;
  durationMs: number | null;
  exitCode: number | null;
  timedOut: boolean;
  stdout: string | null;
  stderr: string | null;
  stdoutTruncated: boolean;
  stderrTruncated: boolean;
  error: string | null;
};

const defaultTerminalRequest = {
  command: "",
  timeoutSeconds: 30,
};

function clampTerminalTimeout(value: number) {
  if (value < 5) {
    return 5;
  }
  if (value > 120) {
    return 120;
  }
  return value;
}

export function buildRouterTerminalHistory(args: {
  jobs: RouterTerminalJobLike[];
  results: RouterTerminalJobResultLike[];
}) {
  const resultsByJob = new Map<string, RouterTerminalJobResultLike[]>();
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
      const parsedRequest = parseTerminalRequest(job.payload);
      const relatedResults = resultsByJob.get(job.id) ?? [];
      const preferredResult =
        relatedResults.find((result) => result.status !== "accepted") ??
        relatedResults[0] ??
        null;
      const parsedResult = parseTerminalResultPayload(
        preferredResult?.payload ?? null,
      );

      return {
        jobId: job.id,
        state: job.state,
        createdAt: job.createdAt.toISOString(),
        completedAt: job.completedAt?.toISOString() ?? null,
        request: parsedRequest,
        resultStatus:
          preferredResult?.status === "accepted" ||
          preferredResult?.status === "success" ||
          preferredResult?.status === "failure"
            ? preferredResult.status
            : null,
        startedAt: parsedResult?.startedAt ?? null,
        finishedAt: parsedResult?.completedAt ?? null,
        durationMs: parsedResult?.durationMs ?? null,
        exitCode: parsedResult?.exitCode ?? null,
        timedOut: parsedResult?.timedOut ?? false,
        stdout: parsedResult?.stdout ?? null,
        stderr: parsedResult?.stderr ?? null,
        stdoutTruncated: parsedResult?.stdoutTruncated ?? false,
        stderrTruncated: parsedResult?.stderrTruncated ?? false,
        error: parsedResult?.error ?? null,
      } satisfies RouterTerminalHistoryItem;
    });

  return {
    activeRequest:
      history.find((item) =>
        ["queued", "delivered", "running"].includes(item.state),
      ) ?? null,
    latestResult:
      history.find(
        (item) =>
          item.resultStatus === "success" || item.resultStatus === "failure",
      ) ?? null,
    history,
  };
}

function parseTerminalRequest(payload: Record<string, unknown> | null) {
  const parsed = runTerminalCommandJobPayloadSchema.safeParse(payload ?? {});
  if (parsed.success) {
    return parsed.data;
  }

  return {
    command:
      typeof payload?.command === "string"
        ? payload.command.trim()
        : defaultTerminalRequest.command,
    timeoutSeconds:
      typeof payload?.timeoutSeconds === "number"
        ? clampTerminalTimeout(payload.timeoutSeconds)
        : defaultTerminalRequest.timeoutSeconds,
  };
}

function parseTerminalResultPayload(
  payload: Record<string, unknown> | null,
): RouterTerminalResultPayload | null {
  if (!payload) {
    return null;
  }

  const parsed = routerTerminalResultPayloadSchema.safeParse(payload);
  return parsed.success ? parsed.data : null;
}
