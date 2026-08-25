import { routers } from "@vectra/db";
import { desc } from "drizzle-orm";

import type { PasswallDesiredConfig } from "@vectra/contracts";

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
import {
  loadLatestRouteVerifications,
  routeVerificationToHealthSample,
} from "./route-health-verifier";

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

const EMPTY_HEALTH: FleetNodeHealth = { unhealthyHosts: [], index: new Set() };

/**
 * Also carries each router's last known config.
 *
 * The check-in directive is computed from the config the router reports — but
 * a router only reports one when its digest has drifted from the panel's, so a
 * router quietly parked on a dead node reports nothing and would receive no
 * directive at all. The self-heal would then only fire for routers that were
 * already changing, which is the opposite of what it is for.
 *
 * Falling back to the stored config makes the directive unconditional. A node
 * id that has since been re-minted simply fails to resolve on the router and
 * the binding is left alone — and that re-mint changes the digest, which asks
 * for a fresh import, so the two converge on their own.
 */
type FleetPolicyContext = {
  nodeHealth: FleetNodeHealth;
  configByRouter: Map<string, PasswallDesiredConfig>;
};

const EMPTY: FleetPolicyContext = {
  nodeHealth: EMPTY_HEALTH,
  configByRouter: new Map(),
};

let cached: { value: FleetPolicyContext; expiresAt: number } | null = null;
let inFlight: Promise<FleetPolicyContext> | null = null;

export function resetFleetNodeHealthCache() {
  cached = null;
  inFlight = null;
}

async function rebuild(database: DatabaseClient): Promise<FleetPolicyContext> {
  const routerRows = await database
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt));
  const routerIds = routerRows.map((router) => router.id);
  if (routerIds.length === 0) {
    return EMPTY;
  }

  const [snapshots, policyConfigRows, routeVerifications] = await Promise.all([
    loadLatestSnapshots(database, routerIds),
    loadLatestFleetPolicyConfigRows(database, routerIds),
    loadLatestRouteVerifications(database, routerIds),
  ]);

  const configByRouter = new Map<string, PasswallDesiredConfig>();
  for (const routerId of routerIds) {
    const config = policyConfigRows.get(routerId)?.config;
    if (config) {
      configByRouter.set(routerId, config);
    }
  }

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

  // Direct per-node verdicts from the router's own url_test_node run. These
  // are the only evidence that reaches Special and Tiktok — the destination
  // probes above never touch those slots — so without them a dead Netherlands
  // or Belarus host is invisible fleet-wide.
  const verifiedSamples = routerIds.flatMap((routerId) => {
    const verification = routeVerifications.get(routerId)?.verification;
    const config = configByRouter.get(routerId);
    if (!verification || !config) {
      return [];
    }
    const sample = routeVerificationToHealthSample(
      routerId,
      config.nodes,
      verification,
    );
    return sample ? [sample] : [];
  });

  return {
    nodeHealth: buildFleetNodeHealth([...samples, ...verifiedSamples]),
    configByRouter,
  };
}

/**
 * Never throws and never blocks a check-in on a bad read: a failure here means
 * "no health opinion", which is exactly the pre-existing behaviour.
 */
export async function getFleetPolicyContext(
  database: DatabaseClient,
  now = Date.now(),
): Promise<FleetPolicyContext> {
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
  return { nodeHealth: (await getFleetPolicyContext(database)).nodeHealth };
}
