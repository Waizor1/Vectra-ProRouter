import { jobs, routers } from "@vectra/db";
import { and, desc, eq, inArray } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

import { getFleetPolicyContext } from "./fleet-node-health-cache";
import {
  buildFleetRoutePolicyIdentity,
  findStrandedSlots,
} from "./fleet-route-policy";
import {
  SUBSCRIPTION_REFRESH_JOB_TYPE,
  selectRoutersForSubscriptionRescue,
  subscriptionHasHardwareId,
  type SubscriptionRescueCandidate,
} from "./route-health-verifier";

type DatabaseClient = typeof appDb;

/** At most this many subscription refreshes per tick, fleet-wide. */
const RESCUE_LIMIT = 2;
/** A refresh needs time to change what the provider serves. */
const RESCUE_COOLDOWN_MS = 6 * 60 * 60 * 1000;

export async function collectSubscriptionRescueCandidates(
  database: DatabaseClient,
): Promise<SubscriptionRescueCandidate[]> {
  const context = await getFleetPolicyContext(database);
  if (context.nodeHealth.unhealthyHosts.length === 0) {
    return [];
  }

  const routerRows = await database.select().from(routers);
  const routerIds = routerRows.map((router) => router.id);
  if (routerIds.length === 0) {
    return [];
  }

  const [queuedRows, refreshRows] = await Promise.all([
    database
      .select({ routerId: jobs.routerId })
      .from(jobs)
      .where(and(inArray(jobs.routerId, routerIds), eq(jobs.state, "queued"))),
    database
      .select({ routerId: jobs.routerId, createdAt: jobs.createdAt })
      .from(jobs)
      .where(
        and(
          inArray(jobs.routerId, routerIds),
          eq(jobs.type, SUBSCRIPTION_REFRESH_JOB_TYPE),
        ),
      )
      .orderBy(desc(jobs.createdAt)),
  ]);

  const queuedByRouter = new Map<string, number>();
  for (const row of Array.isArray(queuedRows) ? queuedRows : []) {
    queuedByRouter.set(
      row.routerId,
      (queuedByRouter.get(row.routerId) ?? 0) + 1,
    );
  }
  const lastRefreshByRouter = new Map<string, Date>();
  for (const row of Array.isArray(refreshRows) ? refreshRows : []) {
    if (!lastRefreshByRouter.has(row.routerId)) {
      lastRefreshByRouter.set(row.routerId, row.createdAt);
    }
  }

  return routerRows.flatMap((router) => {
    if (router.status !== "active" || router.importState !== "approved") {
      return [];
    }
    const config = context.configByRouter.get(router.id);
    if (!config) {
      return [];
    }
    const stranded = findStrandedSlots(
      config,
      buildFleetRoutePolicyIdentity(router, {}),
      { nodeHealth: context.nodeHealth },
    );
    if (stranded.length === 0) {
      return [];
    }
    return [
      {
        routerId: router.id,
        strandedSlots: stranded,
        hwidPresent: subscriptionHasHardwareId(config),
        lastRefreshAt: lastRefreshByRouter.get(router.id) ?? null,
        queuedJobCount: queuedByRouter.get(router.id) ?? 0,
      } satisfies SubscriptionRescueCandidate,
    ];
  });
}

/**
 * Asks the provider for current nodes when a router's own list has no live
 * option left for a slot.
 *
 * This is the last link in the self-repair chain. Everything before it can
 * only move a slot between nodes the router already has; when they are all
 * dead — measured, not guessed, by url_test_node through each node — nothing
 * short of a new node list is a fix.
 */
export async function runSubscriptionRescueTick(
  database: DatabaseClient,
  now = new Date(),
) {
  const candidates = await collectSubscriptionRescueCandidates(database);
  const picked = selectRoutersForSubscriptionRescue(candidates, now, {
    limit: RESCUE_LIMIT,
    cooldownMs: RESCUE_COOLDOWN_MS,
  });
  if (picked.length === 0) {
    return { queued: 0, routerIds: [] as string[] };
  }

  for (const routerId of picked) {
    await database.insert(jobs).values({
      routerId,
      type: SUBSCRIPTION_REFRESH_JOB_TYPE,
      state: "queued",
      payload: { reason: "node-list-exhausted" },
    });
  }

  return { queued: picked.length, routerIds: picked };
}
