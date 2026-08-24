import { jobResults, jobs, routers } from "@vectra/db";
import { and, desc, eq, inArray } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

import type { FleetNodeHealthSample } from "./fleet-node-health";
import { normalizeNodeHost } from "./fleet-node-health";

type DatabaseClient = typeof appDb;

/**
 * Periodic per-node route health, measured on the router itself.
 *
 * The destination probes that already arrive on every check-in only cover
 * Telegram, YouTube and Instagram — which between them exercise the WorldProxy
 * and YouTube slots and nothing else. Special and Tiktok have no probe at all,
 * so on 2026-08-24 six provider hosts (by1, by2, nl1-nl4) carried live traffic
 * with zero health visibility anywhere in the fleet: if one of them died, the
 * fleet-wide ledger would never hear about it and the policy would keep binding
 * routers to it.
 *
 * `verify_passwall_routes` closes that: the controller runs PassWall's own
 * `url_test_node` against the node bound to EVERY canonical slot and reports a
 * status code per slot. That handler has shipped since 2026-05-14, so this
 * works on every controller in the fleet with no rollout.
 */
export type RouteHealthCandidate = {
  routerId: string;
  status: string;
  importState: string;
  lastSeenAt: Date | null;
  lastVerifiedAt: Date | null;
  queuedJobCount: number;
  routePolicyExempt?: boolean | null;
};

export type RouteHealthSelectionOptions = {
  limit: number;
  staleAfterMs: number;
  /** A router quiet for longer than this is assumed unreachable. */
  reachableWithinMs?: number;
};

const DEFAULT_REACHABLE_WITHIN_MS = 5 * 60 * 1000;

export function selectRoutersForRouteHealthCheck(
  candidates: RouteHealthCandidate[],
  now: Date,
  options: RouteHealthSelectionOptions,
): string[] {
  const reachableWithin =
    options.reachableWithinMs ?? DEFAULT_REACHABLE_WITHIN_MS;

  const eligible = candidates.filter((candidate) => {
    if (candidate.status !== "active") {
      return false;
    }
    if (candidate.importState !== "approved") {
      return false;
    }
    if (candidate.routePolicyExempt) {
      return false;
    }
    // Terminal-style jobs collapse per router: queuing on top of pending work
    // overwrites that row's payload, so an operator command can silently never
    // run. Health telemetry is never worth stepping on operator intent.
    if (candidate.queuedJobCount > 0) {
      return false;
    }
    if (!candidate.lastSeenAt) {
      return false;
    }
    if (now.getTime() - candidate.lastSeenAt.getTime() > reachableWithin) {
      return false;
    }
    if (
      candidate.lastVerifiedAt &&
      now.getTime() - candidate.lastVerifiedAt.getTime() < options.staleAfterMs
    ) {
      return false;
    }
    return true;
  });

  // Never checked first, then longest since the last check.
  eligible.sort((left, right) => {
    const leftAt = left.lastVerifiedAt?.getTime() ?? -Infinity;
    const rightAt = right.lastVerifiedAt?.getTime() ?? -Infinity;
    return leftAt - rightAt;
  });

  return eligible.slice(0, options.limit).map((entry) => entry.routerId);
}

type SlotResult = {
  slotId?: string | null;
  boundNodeId?: string | null;
  smokeOk?: boolean | null;
};

type RouteVerification = {
  exempt?: boolean | null;
  slots?: SlotResult[] | null;
};

type NodeLike = { id?: string | null; address?: string | null };

/**
 * Maps a verification result onto host-level evidence for the fleet ledger.
 *
 * Keyed by host, not node id, because the subscription re-mints node ids every
 * night — a verdict tied to an id would expire before it was useful, while the
 * host is the thing the provider actually operates.
 */
export function routeVerificationToHealthSample(
  routerId: string,
  nodes: NodeLike[],
  verification: RouteVerification | null | undefined,
): FleetNodeHealthSample | null {
  if (!verification || verification.exempt) {
    return null;
  }

  const addressById = new Map<string, string>();
  for (const node of nodes) {
    const id = node.id?.trim();
    const host = normalizeNodeHost(node.address);
    if (id && host.length > 0) {
      addressById.set(id, host);
    }
  }

  const observations: FleetNodeHealthSample["observations"] = [];
  for (const slot of verification.slots ?? []) {
    const boundNodeId = slot.boundNodeId?.trim();
    if (!boundNodeId) {
      continue;
    }
    const host = addressById.get(boundNodeId);
    if (!host) {
      // The node was rotated away by a subscription refresh; the verdict is
      // about a binding that no longer exists and says nothing about today.
      continue;
    }
    observations.push({ host, outcome: slot.smokeOk ? "ok" : "fail" });
  }

  return observations.length > 0 ? { routerId, observations } : null;
}

export const ROUTE_HEALTH_JOB_TYPE = "verify_passwall_routes" as const;
const ROUTE_HEALTH_DEDUPE_PREFIX = "route-health:";

export function routeHealthDedupeKey(routerId: string) {
  return `${ROUTE_HEALTH_DEDUPE_PREFIX}${routerId}`;
}

/**
 * Loads the newest route verification per router, whatever queued it —
 * onboarding, an operator, or this lane. A fresh verdict is a fresh verdict.
 */
export async function loadLatestRouteVerifications(
  database: DatabaseClient,
  routerIds: string[],
): Promise<Map<string, { verifiedAt: Date; verification: RouteVerification }>> {
  const latest = new Map<
    string,
    { verifiedAt: Date; verification: RouteVerification }
  >();
  if (routerIds.length === 0) {
    return latest;
  }

  // Newest-first, then keep the first hit per router. The result set is one
  // row per completed verification and these are queued a couple per tick, so
  // it stays small; no lateral join needed.
  const rows = await database
    .select({
      routerId: jobResults.routerId,
      reportedAt: jobResults.reportedAt,
      payload: jobResults.payload,
    })
    .from(jobResults)
    .innerJoin(jobs, eq(jobs.id, jobResults.jobId))
    .where(
      and(
        inArray(jobResults.routerId, routerIds),
        eq(jobs.type, ROUTE_HEALTH_JOB_TYPE),
      ),
    )
    .orderBy(desc(jobResults.reportedAt));

  for (const row of rows) {
    if (latest.has(row.routerId)) {
      continue;
    }
    const payload = row.payload as {
      routeVerification?: RouteVerification | null;
    } & RouteVerification;
    const verification = payload?.routeVerification ?? payload ?? null;
    if (!verification || !Array.isArray(verification.slots)) {
      continue;
    }
    latest.set(row.routerId, {
      verifiedAt: row.reportedAt,
      verification,
    });
  }

  return latest;
}

export async function loadRouteHealthCandidates(
  database: DatabaseClient,
): Promise<RouteHealthCandidate[]> {
  const routerRows = await database.select().from(routers);
  const routerIds = routerRows.map((router) => router.id);
  if (routerIds.length === 0) {
    return [];
  }

  const [queuedRows, verifications] = await Promise.all([
    database
      .select({ routerId: jobs.routerId, id: jobs.id })
      .from(jobs)
      .where(and(inArray(jobs.routerId, routerIds), eq(jobs.state, "queued"))),
    loadLatestRouteVerifications(database, routerIds),
  ]);

  const queuedByRouter = new Map<string, number>();
  for (const row of queuedRows) {
    queuedByRouter.set(row.routerId, (queuedByRouter.get(row.routerId) ?? 0) + 1);
  }

  return routerRows.map((router) => ({
    routerId: router.id,
    status: router.status,
    importState: router.importState,
    lastSeenAt: router.lastSeenAt,
    lastVerifiedAt: verifications.get(router.id)?.verifiedAt ?? null,
    queuedJobCount: queuedByRouter.get(router.id) ?? 0,
    routePolicyExempt: router.routePolicyExempt,
  }));
}

export async function runRouteHealthVerifierTick(
  database: DatabaseClient,
  now = new Date(),
  options: RouteHealthSelectionOptions = {
    limit: 2,
    staleAfterMs: 6 * 60 * 60 * 1000,
  },
) {
  const candidates = await loadRouteHealthCandidates(database);
  const picked = selectRoutersForRouteHealthCheck(candidates, now, options);
  if (picked.length === 0) {
    return { queued: 0, routerIds: [] as string[] };
  }

  for (const routerId of picked) {
    await database.insert(jobs).values({
      routerId,
      type: ROUTE_HEALTH_JOB_TYPE,
      state: "queued",
      payload: { reason: "route-health-telemetry" },
      dedupeKey: routeHealthDedupeKey(routerId),
    });
  }

  return { queued: picked.length, routerIds: picked };
}

const globalForVerifier = globalThis as unknown as {
  __vectraRouteHealthVerifierTimer?: NodeJS.Timeout;
  __vectraRouteHealthVerifierRunning?: boolean;
};

export function startRouteHealthVerifier() {
  if (globalForVerifier.__vectraRouteHealthVerifierTimer) {
    return;
  }

  const run = async () => {
    if (globalForVerifier.__vectraRouteHealthVerifierRunning) {
      return;
    }
    globalForVerifier.__vectraRouteHealthVerifierRunning = true;
    try {
      const { db } = await import("~/server/db");
      const result = await runRouteHealthVerifierTick(db);
      if (result.queued > 0) {
        console.info(
          "[route-health] queued route verification for %d router(s)",
          result.queued,
        );
      }
    } catch (error) {
      console.error("[route-health]", error);
    } finally {
      globalForVerifier.__vectraRouteHealthVerifierRunning = false;
    }
  };

  globalForVerifier.__vectraRouteHealthVerifierTimer = setInterval(
    () => void run(),
    15 * 60 * 1000,
  );
  globalForVerifier.__vectraRouteHealthVerifierTimer.unref?.();
}

export function stopRouteHealthVerifierForTest() {
  if (globalForVerifier.__vectraRouteHealthVerifierTimer) {
    clearInterval(globalForVerifier.__vectraRouteHealthVerifierTimer);
    globalForVerifier.__vectraRouteHealthVerifierTimer = undefined;
  }
  globalForVerifier.__vectraRouteHealthVerifierRunning = false;
}
