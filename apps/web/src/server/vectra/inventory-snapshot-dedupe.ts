// Inventory-snapshot write suppression.
//
// Every router check-in wrote a full JSONB snapshot: ~77 rows per router per
// hour, ~50k rows/day for a 30-router fleet, 459 MB standing inside a 72h
// retention window. Every payload was distinct, so no naive dedupe could help
// — free memory, tmp space and probe timestamps move on literally every
// check-in. Yet no reader wants that resolution: every query in the codebase
// takes `orderBy(desc(createdAt))` and reads the newest row or two.
//
// So the write is gated on a fingerprint of the fields that carry meaning,
// plus a heartbeat so a stable router still leaves a periodic trace.
//
// The fingerprint is a WHITELIST, deliberately. A blacklist of volatile fields
// would silently regress to a write per check-in the first time the agent
// grows a new telemetry counter — the exact failure this module exists to
// prevent, and an invisible one. A whitelist fails the other way: a genuinely
// new material field is missed until the heartbeat fires, bounded by one
// interval and self-healing. Add new material fields here when the agent
// contract grows.

import { createHash } from "node:crypto";

import type { RouterInventory } from "@vectra/contracts";

import { stableStringify } from "./secrets";

// Free-memory, overlay and tmp gauges are excluded from the fingerprint. They
// are sampled telemetry: they move a little on every check-in and never settle.
//
// Quantising them does not work. Any bucket or threshold ladder has edges, and
// a router sitting near an edge — a 234 MB AX3000T hovering at the low-memory
// line is the normal case, not the exotic one — flips its bucket on every
// sample and writes a row every time. That is the very churn this module
// exists to stop, reintroduced in a form that looks deliberate.
//
// Their time series is carried by the heartbeat instead, bounded to one
// interval of staleness. Alerting never read this table anyway: the safety
// guard and health incidents act on the live check-in payload.
//
// Total memory and total swap ARE fingerprinted: they are static hardware
// facts, so a change means the device was re-provisioned or zram was switched
// on — squarely material.

// Reachability probes and safety events are NOT fingerprinted, for the same
// reason as the gauges above: they are intermittent, not just noisy.
//
// Measured against 238 real production snapshots, they were the entire
// remaining churn. The agent does not run every probe on every check-in, so a
// probe group present in one payload is simply absent in the next
// (`[true,"reachable",2,2]` → `null`) — an appearance/disappearance cycle that
// looks like change on every other sample while nothing about the router
// changed. Safety events behave the same way: `low_memory` fires as RAM dips
// and clears as it recovers, which on a 234 MB AX3000T is the normal resting
// state, not an event.
//
// Nothing depends on this table for alerting. Health incidents, the safety
// guard and auto-rescue all act on the live check-in payload in real time; a
// sustained outage lasts far longer than one heartbeat and is captured anyway.
// What is given up is sub-hourly probe history, which no reader asked for.
//
// The safety-event message deserves its own warning: it restates the live gauge
// in prose — "available RAM is low: 50 MB available (21% of 234 MB)" — so
// fingerprinting it re-imports the exact jitter the resource rules exclude,
// through the back door. That one shipped: the first production deploy wrote
// 142 rows in 5 minutes, essentially the pre-fix rate.

export function materialInventoryFingerprint(inventory: RouterInventory) {
  const material = {
    // Identity and platform.
    deviceIdentifier: inventory.deviceIdentifier,
    hostname: inventory.hostname ?? null,
    panelDomain: inventory.panelDomain ?? null,
    model: inventory.model,
    boardName: inventory.boardName,
    layoutFamily: inventory.layoutFamily ?? null,
    target: inventory.target,
    architecture: inventory.architecture,
    openwrtRelease: inventory.openwrtRelease,

    // Versions — the whole point of the fleet-rollout tooling.
    controllerVersion: inventory.controllerVersion,
    controllerRuntimeVersion: inventory.controllerRuntimeVersion ?? null,
    packageVersions: inventory.packageVersions,
    binaryVersions: inventory.binaryVersions,
    rulesAssets: inventory.rulesAssets,

    // Proxy configuration state.
    passwallEnabled: inventory.passwallEnabled,
    selectedNodeId: inventory.selectedNodeId ?? null,
    selectedNodeLabel: inventory.selectedNodeLabel ?? null,
    nodeCount: inventory.nodeCount,
    subscriptionCount: inventory.subscriptionCount,
    configDigest: inventory.configDigest ?? null,
    appliedRevisionId: inventory.appliedRevisionId ?? null,

    // Health.
    serviceHealth: inventory.serviceHealth,
    lastRescue: inventory.lastRescue
      ? {
          mode: inventory.lastRescue.mode,
          reason: inventory.lastRescue.reason,
        }
      : null,

    // Hardware facts only — see the note above on why the free-space gauges
    // are absent.
    resources: {
      memoryTotalMb: inventory.resources?.memoryTotalMb ?? null,
      swapTotalMb: inventory.resources?.swapTotalMb ?? null,
    },
  };

  return createHash("sha256").update(stableStringify(material)).digest("hex");
}

export function shouldWriteInventorySnapshot(args: {
  inventory: RouterInventory;
  latest: { payload: unknown; createdAt: Date } | null;
  now: Date;
  heartbeatMinutes: number;
}) {
  const { inventory, latest, now, heartbeatMinutes } = args;

  if (!latest) {
    return true;
  }

  const ageMinutes = (now.getTime() - latest.createdAt.getTime()) / 60_000;
  if (!Number.isFinite(ageMinutes) || ageMinutes >= heartbeatMinutes) {
    return true;
  }

  let previous: string;
  try {
    previous = materialInventoryFingerprint(latest.payload as RouterInventory);
  } catch {
    // An unreadable stored payload is not evidence that nothing changed.
    return true;
  }

  return previous !== materialInventoryFingerprint(inventory);
}
