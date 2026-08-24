import { routers } from "@vectra/db";
import { desc } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

import {
  buildFleetNodeHealth,
  type FleetNodeHealth,
} from "./fleet-node-health";
import {
  collectFleetNodeHealthSample,
  type FleetRoutePolicyOptions,
} from "./fleet-route-policy";
import {
  loadLatestFleetPolicyConfigRows,
  loadLatestSnapshots,
} from "./fleet-monitoring-data";

type DatabaseClient = typeof appDb;

/**
 * The check-in path needs the fleet liveness ledger on every request, but the
 * ledger is a fleet-wide aggregate — recomputing it per check-in would mean a
 * full snapshot + config read roughly once a second across the fleet.
 *
 * Five minutes is deliberately coarse. A provider losing a host is an outage
 * measured in hours, and the cost of reacting a few minutes late is far below
 * the cost of thrashing routers between exits on a transient probe. The ledger
 * only ever gets consulted to *reject* a candidate, so a stale ledger degrades
 * to the behaviour the policy had before it existed.
 */
const TTL_MS = 5 * 60 * 1000;

const EMPTY: FleetNodeHealth = { unhealthyHosts: [], index: new Set() };

let cached: { value: FleetNodeHealth; expiresAt: number } | null = null;
let inFlight: Promise<FleetNodeHealth> | null = null;

export function resetFleetNodeHealthCache() {
  cached = null;
  inFlight = null;
}

async function rebuild(database: DatabaseClient): Promise<FleetNodeHealth> {
  const routerRows = await database
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt));
  const routerIds = routerRows.map((router) => router.id);
  if (routerIds.length === 0) {
    return EMPTY;
  }

  const [snapshots, policyConfigRows] = await Promise.all([
    loadLatestSnapshots(database, routerIds),
    loadLatestFleetPolicyConfigRows(database, routerIds),
  ]);

  const samples = routerIds.flatMap((routerId) => {
    const payload = snapshots.get(routerId)?.payload as
      | {
          telegramReachability?: { status?: string | null } | null;
          youtubeReachability?: { status?: string | null } | null;
          instagramReachability?: { status?: string | null } | null;
        }
      | undefined;
    if (!payload) {
      return [];
    }
    const sample = collectFleetNodeHealthSample(
      routerId,
      policyConfigRows.get(routerId)?.config ?? null,
      {
        telegram: payload.telegramReachability ?? null,
        youtube: payload.youtubeReachability ?? null,
        instagram: payload.instagramReachability ?? null,
      },
    );
    return sample ? [sample] : [];
  });

  return buildFleetNodeHealth(samples);
}

/**
 * Never throws and never blocks a check-in on a bad read: a failure here means
 * "no health opinion", which is exactly the pre-existing behaviour.
 */
export async function getFleetNodeHealth(
  database: DatabaseClient,
  now = Date.now(),
): Promise<FleetNodeHealth> {
  if (cached && cached.expiresAt > now) {
    return cached.value;
  }
  if (inFlight) {
    return inFlight;
  }

  inFlight = rebuild(database)
    .then((value) => {
      cached = { value, expiresAt: Date.now() + TTL_MS };
      return value;
    })
    .catch(() => cached?.value ?? EMPTY)
    .finally(() => {
      inFlight = null;
    });

  return inFlight;
}

export async function fleetRoutePolicyOptions(
  database: DatabaseClient,
): Promise<FleetRoutePolicyOptions> {
  return { nodeHealth: await getFleetNodeHealth(database) };
}
