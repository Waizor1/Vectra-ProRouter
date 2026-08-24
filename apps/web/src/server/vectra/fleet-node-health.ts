/**
 * Fleet-wide liveness ledger for provider node hosts.
 *
 * The route policy scores nodes by label, host and port — it has no idea
 * whether the node it picks actually carries traffic. On 2026-08-24 the
 * provider lost ru9-ru12 outright and the policy kept every affected router
 * pinned to a dead node: eleven routers with YouTube down, all of them
 * reported "compliant", none able to self-heal because the check-in directive
 * names the node explicitly and the controller obeys it over its own scorer.
 *
 * The panel is the only place that can tell a dead node from a bad uplink,
 * because it is the only place that sees every router at once. This module
 * turns the reachability probes already arriving on every check-in into that
 * judgement.
 *
 * The inference is deliberately conservative in one direction: it takes a lot
 * to condemn a host and almost nothing to spare one. Moving the fleet is the
 * expensive, user-visible direction, so success anywhere outranks failure
 * everywhere.
 */

export type FleetNodeHealthObservation = {
  /** Node host the probed traffic was routed through. */
  host: string;
  outcome: "ok" | "fail";
};

export type FleetNodeHealthSample = {
  routerId: string;
  observations: FleetNodeHealthObservation[];
};

export type FleetNodeHealth = {
  unhealthyHosts: string[];
  /** Lookup set, kept alongside the array so callers can serialise the ledger. */
  index: ReadonlySet<string>;
};

export function normalizeNodeHost(value: string | null | undefined) {
  return (value ?? "").trim().toLowerCase();
}

export function isUnhealthyNodeHost(
  health: FleetNodeHealth | null | undefined,
  host: string | null | undefined,
) {
  if (!health) {
    return false;
  }
  const normalized = normalizeNodeHost(host);
  return normalized.length > 0 && health.index.has(normalized);
}

/**
 * Condemns a host when, and only when:
 *
 *  1. no router anywhere reached it — a single success is enough to keep it,
 *     because a node that carries traffic for somebody is not the fault; and
 *  2. at least one router that failed on it simultaneously succeeded through
 *     some other host.
 *
 * Rule 2 is the control sample, and it is what separates "this node is dead"
 * from "this router's line is dead". kirill-msk on 2026-08-24 is the shape it
 * is built for: YouTube through ru9 returned nothing while Telegram through
 * pl2 returned 200 on the same router in the same minute. Without a working
 * probe to compare against, a failure says nothing about the node, and acting
 * on it would let one router with a broken uplink drag the whole fleet off a
 * healthy exit.
 */
export function buildFleetNodeHealth(
  samples: FleetNodeHealthSample[],
): FleetNodeHealth {
  const reached = new Set<string>();
  const failedWithControl = new Set<string>();

  for (const sample of samples) {
    const hostsSeen = new Map<string, "ok" | "fail">();
    for (const observation of sample.observations) {
      const host = normalizeNodeHost(observation.host);
      if (host.length === 0) {
        continue;
      }
      // A host this router reached even once counts as reached, whatever else
      // it did: a partial probe still proves packets crossed the node.
      if (observation.outcome === "ok") {
        reached.add(host);
        hostsSeen.set(host, "ok");
        continue;
      }
      if (!hostsSeen.has(host)) {
        hostsSeen.set(host, "fail");
      }
    }

    const hasControlSuccess = [...hostsSeen.values()].includes("ok");
    if (!hasControlSuccess) {
      continue;
    }
    for (const [host, outcome] of hostsSeen) {
      if (outcome === "fail") {
        failedWithControl.add(host);
      }
    }
  }

  const unhealthyHosts = [...failedWithControl]
    .filter((host) => !reached.has(host))
    .sort();

  return { unhealthyHosts, index: new Set(unhealthyHosts) };
}
