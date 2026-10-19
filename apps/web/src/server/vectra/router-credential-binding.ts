// A3: one router credential running on several devices.
//
// Each router has one row in vectra_router_credential_binding: the device its
// current credential is bound to (the agent's deviceBinding) and where its
// last accepted check-in came from. Every check-in is compared against it and
// what does not fit is recorded in the event log. Detection is on by default
// and never changes an answer; only VECTRA_DUPLICATE_CREDENTIAL_ENFORCE turns
// a device mismatch (and nothing else) into a refusal.
//
// It must never cost a router its check-in or registration: any error in here
// (the table not migrated yet, a database hiccup) is swallowed with a
// throttled warning and the request proceeds as if detection did not exist.
// Contract: ai_docs/develop/features/router-auth-integrity-and-native-runtime.md.

import { eventLog, routerCredentialBindings } from "@vectra/db";
import { eq } from "drizzle-orm";

import { db } from "~/server/db";
import { MemoryWindowRateLimiter } from "~/server/vectra/public-install-rate-limit";
import {
  readReportedDeviceBinding,
  sourceClusterForIp,
  UNKNOWN_SOURCE_CLUSTER,
} from "~/server/vectra/router-auth-integrity";

type BindingRow = typeof routerCredentialBindings.$inferSelect;
type BindingDatabase = Pick<typeof db, "select" | "insert" | "update">;

/** Two accepted check-ins closer than this cannot both be one agent's. */
export const CONCURRENT_SESSION_WINDOW_MS = 20_000;
export const MAX_KNOWN_SOURCE_CLUSTERS = 8;
const EVENT_INTERVAL_MS = 60 * 60 * 1000;
const WARNING_INTERVAL_MS = 10 * 60 * 1000;

export const CREDENTIAL_DEVICE_MISMATCH_CODE = "credential_device_mismatch";
export const CREDENTIAL_DEVICE_MISMATCH_MESSAGE =
  "Router credential is bound to a different device.";

export type CredentialBindingEventKind =
  | "rebound"
  | "device_mismatch"
  | "concurrent_sessions"
  | "new_source_cluster";

type Finding = {
  kind: CredentialBindingEventKind;
  severity: "info" | "warning";
  message: string;
  metadata: Record<string, unknown>;
};

// Every event at most once an hour per router and kind.
let eventThrottle = new MemoryWindowRateLimiter(1, EVENT_INTERVAL_MS);
let warningThrottle = new MemoryWindowRateLimiter(1, WARNING_INTERVAL_MS);

export function resetCredentialBindingStateForTest() {
  eventThrottle = new MemoryWindowRateLimiter(1, EVENT_INTERVAL_MS);
  warningThrottle = new MemoryWindowRateLimiter(1, WARNING_INTERVAL_MS);
}

/** Most recently seen last, at most MAX_KNOWN_SOURCE_CLUSTERS. */
function rememberSourceCluster(known: string[], cluster: string) {
  if (cluster === UNKNOWN_SOURCE_CLUSTER) {
    return known;
  }
  return [...known.filter((entry) => entry !== cluster), cluster].slice(
    -MAX_KNOWN_SOURCE_CLUSTERS,
  );
}

function knownClustersOf(row: BindingRow | null) {
  return Array.isArray(row?.knownSourceClusters)
    ? row.knownSourceClusters.filter(
        (entry): entry is string => typeof entry === "string",
      )
    : [];
}

export type CheckInBindingEvaluation = {
  /** A reported binding that differs from the one bound to this credential. */
  deviceMismatch: boolean;
  findings: Finding[];
  /** The row as it stands once this check-in is accepted. */
  next: Omit<BindingRow, "updatedAt">;
};

/**
 * Pure: what one check-in means against the router's binding row.
 *
 * - First sight of this credential (no row, or a row for another credential:
 *   a legacy router, a rotated credential): bound trust-on-first-use.
 * - device_mismatch: a reported binding that differs from the bound one. An
 *   omitted binding is never a mismatch (a rollback to an older agent).
 * - concurrent_sessions: the previous accepted check-in is under 20 s old and
 *   came from another source cluster or another binding. Only two known
 *   clusters and two reported bindings are compared: an omitted binding or an
 *   unknown address is the absence of evidence, not a second device.
 * - new_source_cluster: a cluster not among the router's known ones; the very
 *   first one is not news.
 */
export function evaluateCheckInBinding(args: {
  row: BindingRow | null;
  routerId: string;
  credentialId: string;
  reportedBinding: string | null;
  sourceCluster: string;
  now: Date;
}): CheckInBindingEvaluation {
  const { row, reportedBinding, sourceCluster, now } = args;
  const findings: Finding[] = [];
  // The binding row of THIS credential; null on first sight.
  const current = row?.credentialId === args.credentialId ? row : null;
  const bound = current?.boundDeviceBinding ?? null;

  const deviceMismatch =
    bound !== null && reportedBinding !== null && reportedBinding !== bound;
  if (deviceMismatch) {
    findings.push({
      kind: "device_mismatch",
      severity: "warning",
      message:
        "Router check-in reported a device other than the one its credential is bound to.",
      metadata: {
        credentialId: args.credentialId,
        boundDeviceBinding: bound,
        reportedDeviceBinding: reportedBinding,
        sourceCluster,
      },
    });
  }

  const previousSeenAt = row?.lastSeenAt ?? null;
  if (row && previousSeenAt) {
    const elapsedMs = now.getTime() - previousSeenAt.getTime();
    const previousCluster = row.lastSourceCluster ?? UNKNOWN_SOURCE_CLUSTER;
    const otherCluster =
      previousCluster !== UNKNOWN_SOURCE_CLUSTER &&
      sourceCluster !== UNKNOWN_SOURCE_CLUSTER &&
      previousCluster !== sourceCluster;
    const otherBinding =
      row.lastDeviceBinding !== null &&
      reportedBinding !== null &&
      row.lastDeviceBinding !== reportedBinding;
    if (
      elapsedMs >= 0 &&
      elapsedMs < CONCURRENT_SESSION_WINDOW_MS &&
      (otherCluster || otherBinding)
    ) {
      findings.push({
        kind: "concurrent_sessions",
        severity: "warning",
        message:
          "Two check-ins of one router credential arrived seconds apart from different networks or devices.",
        metadata: {
          secondsSincePrevious: Math.round(elapsedMs / 100) / 10,
          previousSourceCluster: previousCluster,
          sourceCluster,
          previousDeviceBinding: row.lastDeviceBinding,
          reportedDeviceBinding: reportedBinding,
        },
      });
    }
  }

  const known = knownClustersOf(row);
  if (
    sourceCluster !== UNKNOWN_SOURCE_CLUSTER &&
    known.length > 0 &&
    !known.includes(sourceCluster)
  ) {
    findings.push({
      kind: "new_source_cluster",
      severity: "info",
      message: "Router checked in from a network it was not seen on before.",
      metadata: { sourceCluster, knownSourceClusters: known },
    });
  }

  const boundNow = bound ?? reportedBinding;
  return {
    deviceMismatch,
    findings,
    next: {
      routerId: args.routerId,
      credentialId: args.credentialId,
      boundDeviceBinding: boundNow,
      // Bound now on first sight, or when an unbound credential first reports
      // a binding (trust on first use).
      boundAt:
        current && !(bound === null && boundNow !== null)
          ? current.boundAt
          : now,
      lastDeviceBinding: reportedBinding,
      lastSourceCluster: sourceCluster,
      lastSeenAt: now,
      knownSourceClusters: rememberSourceCluster(known, sourceCluster),
    },
  };
}

async function recordFindings(
  database: BindingDatabase,
  routerId: string,
  findings: Finding[],
) {
  for (const finding of findings) {
    if (!eventThrottle.consume(`${routerId}|${finding.kind}`).allowed) {
      continue;
    }
    await database.insert(eventLog).values({
      routerId,
      type: `router.credential.${finding.kind}`,
      severity: finding.severity,
      message: finding.message,
      metadata: finding.metadata,
    });
  }
}

async function readBindingRow(database: BindingDatabase, routerId: string) {
  const [row] = await database
    .select()
    .from(routerCredentialBindings)
    .where(eq(routerCredentialBindings.routerId, routerId))
    .limit(1);
  return row ?? null;
}

// A Drizzle error's message ends with the bound parameters; only what comes
// before them, and the driver's own message (e.g. relation does not exist).
function describeDetectionError(error: unknown) {
  if (!(error instanceof Error)) {
    return { error: typeof error };
  }
  const cause = (error as { cause?: unknown }).cause;
  return {
    error: error.name,
    message: error.message.replace(/params:[\s\S]*$/, "params: [redacted]"),
    cause: cause instanceof Error ? cause.message : undefined,
  };
}

function warnDetectionFailed(phase: "check_in" | "register", error: unknown) {
  if (warningThrottle.consume(phase).allowed) {
    console.warn(
      `[credential-binding] duplicate-credential detection skipped (${phase}); the request proceeds`,
      describeDetectionError(error),
    );
  }
}

export type CheckInBindingObservation = {
  /** A reported binding that contradicts the credential's. */
  deviceMismatch: boolean;
  /**
   * Records this check-in as the router's last accepted one. Called once
   * the check-in has been accepted (the router row is updated), never for a
   * refused one. Never throws.
   */
  recordAccepted: () => Promise<void>;
};

const NOTHING_TO_RECORD = async () => undefined;

/**
 * Detection for one authenticated check-in: compares it against the binding
 * row, writes what does not fit to the event log and says whether the
 * reported binding contradicts the credential's. Never throws.
 *
 * Only an unreadable row fails open (no mismatch: the spec lets a check-in
 * through when detection cannot run). Once a mismatch is known, a failing
 * event write cannot hide it from enforcement.
 */
export async function observeCheckInCredentialBinding(args: {
  routerId: string;
  credentialId: string;
  deviceBinding: unknown;
  clientIp: string | null | undefined;
  now: Date;
  database?: BindingDatabase;
}): Promise<CheckInBindingObservation> {
  const database = args.database ?? db;
  let row: BindingRow | null;
  let evaluation: CheckInBindingEvaluation;
  try {
    row = await readBindingRow(database, args.routerId);
    evaluation = evaluateCheckInBinding({
      row,
      routerId: args.routerId,
      credentialId: args.credentialId,
      reportedBinding: readReportedDeviceBinding(args.deviceBinding),
      sourceCluster: sourceClusterForIp(args.clientIp),
      now: args.now,
    });
  } catch (error) {
    warnDetectionFailed("check_in", error);
    return { deviceMismatch: false, recordAccepted: NOTHING_TO_RECORD };
  }

  try {
    await recordFindings(database, args.routerId, evaluation.findings);
  } catch (error) {
    warnDetectionFailed("check_in", error);
  }

  const next = evaluation.next;
  return {
    deviceMismatch: evaluation.deviceMismatch,
    recordAccepted: async () => {
      try {
        if (row) {
          await database
            .update(routerCredentialBindings)
            .set(next)
            .where(eq(routerCredentialBindings.routerId, args.routerId));
        } else {
          // A registration may have created the row meanwhile; then this one
          // check-in simply goes unrecorded.
          await database
            .insert(routerCredentialBindings)
            .values(next)
            .onConflictDoNothing();
        }
      } catch (error) {
        warnDetectionFailed("check_in", error);
      }
    },
  };
}

/**
 * A registration issued `credentialId`: bind it to the reported device. A
 * registration that reports no binding (an older agent) keeps the router's
 * previous one, so a credential re-issued that way stays comparable. Never
 * throws.
 */
export async function bindCredentialAtRegistration(args: {
  routerId: string;
  credentialId: string;
  deviceBinding: unknown;
  now: Date;
  database?: BindingDatabase;
}) {
  const database = args.database ?? db;
  try {
    const reported = readReportedDeviceBinding(args.deviceBinding);
    const row = await readBindingRow(database, args.routerId);
    const previous = row?.boundDeviceBinding ?? null;
    const bound = reported ?? previous;
    const values = {
      credentialId: args.credentialId,
      boundDeviceBinding: bound,
      boundAt: args.now,
    };

    const inserted = row
      ? []
      : await database
          .insert(routerCredentialBindings)
          .values({ routerId: args.routerId, ...values })
          .onConflictDoNothing()
          .returning({ routerId: routerCredentialBindings.routerId });
    if (inserted.length === 0) {
      await database
        .update(routerCredentialBindings)
        .set(values)
        .where(eq(routerCredentialBindings.routerId, args.routerId));
    }

    if (previous !== null && reported !== null && previous !== reported) {
      await recordFindings(database, args.routerId, [
        {
          kind: "rebound",
          severity: "info",
          message:
            "Router registration bound its new credential to a different device than before.",
          metadata: {
            credentialId: args.credentialId,
            previousCredentialId: row?.credentialId ?? null,
            previousDeviceBinding: previous,
            deviceBinding: reported,
          },
        },
      ]);
    }
  } catch (error) {
    warnDetectionFailed("register", error);
  }
}
