import { jobResults, jobs, routers } from "@vectra/db";
import { and, desc, eq, inArray } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

import type { FleetNodeHealthSample } from "./fleet-node-health";
import { nodeEndpointKey } from "./fleet-node-health";

type DatabaseClient = typeof appDb;

// Read-only surface: the monitoring loader passes a narrower client than the
// full drizzle instance, and this function only ever selects.
type ReadOnlyDatabase = Pick<DatabaseClient, "select">;

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
  /**
   * When this router last had its proxy stack disturbed — a subscription
   * refresh or a config apply. See `settleMs`.
   */
  lastDisruptionAt?: Date | null;
};

export type RouteHealthSelectionOptions = {
  limit: number;
  staleAfterMs: number;
  /** A router quiet for longer than this is assumed unreachable. */
  reachableWithinMs?: number;
  /**
   * Grace period after a subscription refresh or apply before its nodes may
   * be judged.
   *
   * url_test_node returns 000 while xray is still reloading, so probing right
   * after a refresh manufactures failures. Measured 2026-08-24: a sweep run
   * five minutes after refreshing ten subscriptions reported Special dead on
   * zhenya13911, and a direct retest minutes later returned 204 twice on the
   * same node. Verdicts from that window would have condemned healthy
   * endpoints fleet-wide.
   */
  settleMs?: number;
};

const DEFAULT_REACHABLE_WITHIN_MS = 5 * 60 * 1000;
const DEFAULT_SETTLE_MS = 20 * 60 * 1000;

/**
 * Which router states may be probed.
 *
 * "direct" is here for the one consumer that needs it most. The auto-rescue
 * unpark judges a router parked with PassWall off on exactly one signal — a
 * recent verify_passwall_routes verdict with some slot answering
 * (hasRecentHealthyProxyNode, evidence younger than 6h). Excluding "direct"
 * here meant that evidence was only ever produced for routers that did not
 * need it: the moment a router parked, its verdicts stopped, the last one
 * aged out, and the unpark could never fire again.
 *
 * Measured on DmitryGubenko 2026-08-27: parked in direct on 08-26 08:13Z, last
 * verdict 08-26 07:21Z, stale by 13:21Z, then 33 hours with zero reconnect
 * attempts while all four of its bound nodes answered url_test in under 0.4s.
 *
 * Probing a parked router is sound: url_test_node dials the provider node
 * directly and works with the proxy stack stopped, and buildFleetNodeHealth
 * discards a router whose every probe failed (no control success), so a device
 * parked for a genuinely broken uplink cannot condemn a healthy host.
 */
const PROBEABLE_ROUTER_STATUSES = new Set(["active", "direct"]);

export function selectRoutersForRouteHealthCheck(
  candidates: RouteHealthCandidate[],
  now: Date,
  options: RouteHealthSelectionOptions,
): string[] {
  const reachableWithin =
    options.reachableWithinMs ?? DEFAULT_REACHABLE_WITHIN_MS;
  const settle = options.settleMs ?? DEFAULT_SETTLE_MS;

  const eligible = candidates.filter((candidate) => {
    if (!PROBEABLE_ROUTER_STATUSES.has(candidate.status)) {
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
      candidate.lastDisruptionAt &&
      now.getTime() - candidate.lastDisruptionAt.getTime() < settle
    ) {
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

type NodeLike = {
  id?: string | null;
  address?: string | null;
  port?: number | null;
};

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
    const endpoint = nodeEndpointKey(node.address, node.port);
    if (id && endpoint.length > 0) {
      addressById.set(id, endpoint);
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
  database: ReadOnlyDatabase,
  routerIds: string[],
): Promise<Map<string, { verifiedAt: Date; verification: RouteVerification }>> {
  const latest = new Map<
    string,
    { verifiedAt: Date; verification: RouteVerification }
  >();
  if (routerIds.length === 0) {
    return latest;
  }

  // Two plain selects instead of a join: the job rows carry the type, the
  // result rows carry the payload. Keeps this readable and avoids depending on
  // join support in every database shim the tests use.
  // Telemetry must never break its caller: a client shim that does not return
  // rows for these tables yields "no verdicts", not an exception.
  const verifyJobs = await database
    .select({ id: jobs.id, routerId: jobs.routerId })
    .from(jobs)
    .where(
      and(
        inArray(jobs.routerId, routerIds),
        eq(jobs.type, ROUTE_HEALTH_JOB_TYPE),
      ),
    );
  if (!Array.isArray(verifyJobs) || verifyJobs.length === 0) {
    return latest;
  }
  const routerByJobId = new Map(
    verifyJobs.map((job) => [job.id, job.routerId]),
  );

  const rows = await database
    .select({
      jobId: jobResults.jobId,
      reportedAt: jobResults.reportedAt,
      payload: jobResults.payload,
    })
    .from(jobResults)
    .where(inArray(jobResults.jobId, [...routerByJobId.keys()]))
    .orderBy(desc(jobResults.reportedAt));
  if (!Array.isArray(rows)) {
    return latest;
  }

  for (const row of rows) {
    const routerId = routerByJobId.get(row.jobId);
    if (!routerId || latest.has(routerId)) {
      continue;
    }
    const payload = row.payload as {
      routeVerification?: RouteVerification | null;
    } & RouteVerification;
    // A job emits an "accepted" receipt before the real result; only the one
    // carrying slots is a verdict.
    const verification = payload?.routeVerification ?? payload ?? null;
    if (!verification || !Array.isArray(verification.slots)) {
      continue;
    }
    latest.set(routerId, { verifiedAt: row.reportedAt, verification });
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

  const [queuedRows, verifications, disruptionRows] = await Promise.all([
    database
      .select({ routerId: jobs.routerId, id: jobs.id })
      .from(jobs)
      .where(and(inArray(jobs.routerId, routerIds), eq(jobs.state, "queued"))),
    loadLatestRouteVerifications(database, routerIds),
    // A refresh or an apply restarts the proxy stack; nodes probed while it is
    // still coming up answer 000 and would be condemned on false evidence.
    database
      .select({ routerId: jobs.routerId, createdAt: jobs.createdAt })
      .from(jobs)
      .where(
        and(
          inArray(jobs.routerId, routerIds),
          inArray(jobs.type, [
            SUBSCRIPTION_REFRESH_JOB_TYPE,
            "apply_passwall_config",
          ]),
        ),
      )
      .orderBy(desc(jobs.createdAt)),
  ]);

  const lastDisruptionByRouter = new Map<string, Date>();
  for (const row of Array.isArray(disruptionRows) ? disruptionRows : []) {
    if (!lastDisruptionByRouter.has(row.routerId)) {
      lastDisruptionByRouter.set(row.routerId, row.createdAt);
    }
  }

  const queuedByRouter = new Map<string, number>();
  for (const row of queuedRows) {
    queuedByRouter.set(
      row.routerId,
      (queuedByRouter.get(row.routerId) ?? 0) + 1,
    );
  }

  return routerRows.map((router) => ({
    routerId: router.id,
    status: router.status,
    importState: router.importState,
    lastSeenAt: router.lastSeenAt,
    lastVerifiedAt: verifications.get(router.id)?.verifiedAt ?? null,
    queuedJobCount: queuedByRouter.get(router.id) ?? 0,
    routePolicyExempt: router.routePolicyExempt,
    lastDisruptionAt: lastDisruptionByRouter.get(router.id) ?? null,
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
      // Detection only — the refresh itself stays an operator decision.
      //
      // 2026-08-24: refreshing yuranrod-msk WITH hwid=1 set returned a payload
      // that left him with zero proxy nodes (≈20 hosts before, none after) and
      // all five slots pointing at an id that no longer existed. The hardware
      // id gate is necessary but NOT sufficient, so an unattended refresh can
      // take a customer's node list away. Reporting the state is safe;
      // automatically acting on it is not, until the wipe can be detected and
      // undone.
      const { collectSubscriptionRescueCandidates } =
        await import("./subscription-rescue");
      const stranded = await collectSubscriptionRescueCandidates(db);
      if (stranded.length > 0) {
        console.warn(
          "[route-health] node list exhausted for %d router(s), subscription refresh needed: %o",
          stranded.length,
          stranded.map((entry) => ({
            routerId: entry.routerId,
            slots: entry.strandedSlots,
            canRefresh: entry.hwidPresent,
          })),
        );
      }
    } catch (error) {
      console.error("[route-health]", error);
    } finally {
      globalForVerifier.__vectraRouteHealthVerifierRunning = false;
    }
  };

  // Sweep once on startup rather than waiting out the first interval, so a
  // deploy does not leave the fleet 15 minutes without fresh node telemetry —
  // and so the lane is observable right after it ships instead of looking
  // inert. startRouteHealthVerifier is idempotent, so this runs once per
  // process, not once per health check.
  void run();

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

export const SUBSCRIPTION_REFRESH_JOB_TYPE = "refresh_subscriptions" as const;

export type SubscriptionRescueCandidate = {
  routerId: string;
  strandedSlots: string[];
  /**
   * The provider hands back a stub when the request carries no hardware id,
   * and PassWall then wipes the node list. Refreshing without this is not a
   * repair, it is the outage. Never automate past this gate.
   */
  hwidPresent: boolean;
  lastRefreshAt: Date | null;
  queuedJobCount: number;
};

export type SubscriptionRescueOptions = {
  limit: number;
  cooldownMs: number;
};

/**
 * Picks routers whose node list has run out of live options for a slot.
 *
 * Rebinding cannot fix an exhausted list — every candidate is dead — so the
 * only real repair is asking the provider for current nodes. This is the step
 * that turns the lane from "detects and reports" into "fixes itself".
 */
export function selectRoutersForSubscriptionRescue(
  candidates: SubscriptionRescueCandidate[],
  now: Date,
  options: SubscriptionRescueOptions,
): string[] {
  return candidates
    .filter((candidate) => {
      if (candidate.strandedSlots.length === 0) {
        return false;
      }
      if (!candidate.hwidPresent) {
        return false;
      }
      if (candidate.queuedJobCount > 0) {
        return false;
      }
      // A refresh that did not help must not become a loop: the provider needs
      // time to actually change what it serves.
      if (
        candidate.lastRefreshAt &&
        now.getTime() - candidate.lastRefreshAt.getTime() < options.cooldownMs
      ) {
        return false;
      }
      return true;
    })
    .slice(0, options.limit)
    .map((candidate) => candidate.routerId);
}

export function subscriptionHasHardwareId(
  config:
    | { subscriptions?: { items?: { extras?: Record<string, unknown> }[] } }
    | null
    | undefined,
): boolean {
  const items = config?.subscriptions?.items ?? [];
  return items.some((item) => String(item?.extras?.hwid ?? "") === "1");
}
