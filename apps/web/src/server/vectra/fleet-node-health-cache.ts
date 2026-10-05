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
  type FleetPolicyConfigSummary,
  loadLatestFleetPolicyConfigSummaries,
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
  configByRouter: Map<string, FleetPolicyConfigSummary>;
  /**
   * Set only when the first build after a start did not finish within
   * FIRST_BUILD_WAIT_MS: the ledger is not known yet, as opposed to built and
   * empty. The check-in sends no route-policy directive then — a directive
   * from an empty ledger cannot tell a dead host from a live one and could
   * pin the router to one.
   */
  unavailable?: true;
};

const EMPTY: FleetPolicyContext = {
  nodeHealth: EMPTY_HEALTH,
  configByRouter: new Map(),
};

const UNAVAILABLE: FleetPolicyContext = { ...EMPTY, unavailable: true };

let cached: { value: FleetPolicyContext; expiresAt: number } | null = null;
let inFlight: Promise<FleetPolicyContext> | null = null;

export function resetFleetNodeHealthCache() {
  cached = null;
  inFlight = null;
}

async function rebuild(
  database: DatabaseClient,
  now: number,
): Promise<FleetPolicyContext> {
  const routerRows = await database
    .select({ id: routers.id })
    .from(routers)
    .orderBy(desc(routers.lastSeenAt));
  const routerIds = routerRows.map((router) => router.id);
  if (routerIds.length === 0) {
    return EMPTY;
  }

  const [snapshots, policyConfigRows, routeVerifications] = await Promise.all([
    loadLatestSnapshots(database, routerIds, { monitoringPayload: true }),
    // Incremental: only revisions new since the last rebuild are read and
    // parsed; the rest come from the per-revision summary cache.
    loadLatestFleetPolicyConfigSummaries(database, routerIds),
    loadLatestRouteVerifications(database, routerIds),
  ]);

  const configByRouter = new Map<string, FleetPolicyConfigSummary>();
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
    const policyRow = policyConfigRows.get(routerId);
    const sample = collectFleetNodeHealthSample(
      routerId,
      policyRow?.config ?? null,
      {
        telegram: payload.telegramReachability ?? null,
        youtube: payload.youtubeReachability ?? null,
        instagram: payload.instagramReachability ?? null,
      },
      // The bindings come from this revision, so a probe older than it was
      // measured through some other node — see collectFleetNodeHealthSample.
      policyRow?.createdAt ?? null,
      // Ages out ghosts: an offline router's last green reading must not keep
      // sparing a host the live fleet has since measured dead.
      new Date(now),
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
 * How long a check-in waits for the very first build after a start. Past it
 * the check-in proceeds without the ledger (UNAVAILABLE: no health opinion
 * and no route-policy directive) and the build finishes in the background
 * for the check-ins after it.
 */
export const FIRST_BUILD_WAIT_MS = 2_000;

function startRebuild(
  database: DatabaseClient,
  now: number,
): Promise<FleetPolicyContext> {
  if (inFlight) {
    return inFlight;
  }
  inFlight = rebuild(database, now)
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

/**
 * Never throws and never blocks a check-in on a bad read: a failure here means
 * "no health opinion", which is exactly the pre-existing behaviour.
 *
 * Stale-while-revalidate: once anything has been built, a check-in gets the
 * last value at once and an expired one is rebuilt in the background
 * (single-flight). Every five minutes one check-in used to wait for the whole
 * fleet read — the p99 tail of the check-in. Only the first build after a
 * start is awaited, and at most FIRST_BUILD_WAIT_MS.
 */
export async function getFleetPolicyContext(
  database: DatabaseClient,
  now = Date.now(),
): Promise<FleetPolicyContext> {
  if (cached) {
    if (cached.expiresAt <= now) {
      void startRebuild(database, now);
    }
    return cached.value;
  }

  const build = startRebuild(database, now);
  let timer: ReturnType<typeof setTimeout> | undefined;
  const fallback = new Promise<FleetPolicyContext>((resolve) => {
    timer = setTimeout(() => resolve(UNAVAILABLE), FIRST_BUILD_WAIT_MS);
    timer.unref?.();
  });
  try {
    return await Promise.race([build, fallback]);
  } finally {
    clearTimeout(timer);
  }
}

export async function fleetRoutePolicyOptions(
  database: DatabaseClient,
): Promise<FleetRoutePolicyOptions> {
  return { nodeHealth: (await getFleetPolicyContext(database)).nodeHealth };
}
