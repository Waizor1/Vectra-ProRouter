import { describe, expect, it } from "vitest";

import {
  buildFleetNodeHealth,
  isUnhealthyNodeHost,
  type FleetNodeHealthSample,
} from "./fleet-node-health";

function sample(
  routerId: string,
  observations: FleetNodeHealthSample["observations"],
): FleetNodeHealthSample {
  return { routerId, observations };
}

describe("buildFleetNodeHealth", () => {
  // The 2026-08-24 outage in miniature: kirill-msk's YouTube slot sat on
  // ru9 and reported blocked, while the same router's Telegram probe went
  // through pl2 and came back fine. The working probe is what makes the
  // failing one meaningful — it proves the uplink and the proxy stack are
  // healthy, so the dead host is the only suspect left.
  it("condemns a host when the same router proves its own uplink works", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "ru9.nfnpx.online", outcome: "fail" },
        { host: "pl2.nfnpx.online", outcome: "ok" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru9.nfnpx.online")).toBe(true);
    expect(isUnhealthyNodeHost(health, "pl2.nfnpx.online")).toBe(false);
  });

  // Without a control sample the router itself is as likely to be the fault
  // as the node. Evicting the node fleet-wide on that evidence would let one
  // router with a broken uplink drag every other router off a good exit.
  it("ignores a failure from a router that reports nothing working", () => {
    const health = buildFleetNodeHealth([
      sample("offline-ish", [{ host: "ru9.nfnpx.online", outcome: "fail" }]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru9.nfnpx.online")).toBe(false);
  });

  // One router's local problem must never condemn a host that demonstrably
  // carries traffic for somebody else. Success anywhere outranks failure
  // everywhere, because moving the fleet is the expensive direction.
  it("keeps a host that any router still reaches", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "ru5.nfnpx.online", outcome: "fail" },
        { host: "pl2.nfnpx.online", outcome: "ok" },
      ]),
      sample("zhenya", [{ host: "ru5.nfnpx.online", outcome: "ok" }]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru5.nfnpx.online")).toBe(false);
  });

  // A partial probe means some destinations answered through this node, so
  // the node is passing traffic. nataliafilisiti reported Telegram "partial"
  // through ru14 while genuinely being on the wrong exit — that is a routing
  // decision to fix elsewhere, not a dead host to evict.
  it("treats a partial probe as evidence the host is alive", () => {
    const health = buildFleetNodeHealth([
      sample("natalia", [
        { host: "ru14.nfnpx.online", outcome: "ok" },
        { host: "ru14.nfnpx.online", outcome: "fail" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru14.nfnpx.online")).toBe(false);
  });

  it("normalises host casing and whitespace before matching", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "  RU9.NFNPX.online ", outcome: "fail" },
        { host: "pl2.nfnpx.online", outcome: "ok" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru9.nfnpx.online")).toBe(true);
  });

  it("has no opinion without samples", () => {
    const health = buildFleetNodeHealth([]);

    expect(isUnhealthyNodeHost(health, "ru9.nfnpx.online")).toBe(false);
    expect(health.unhealthyHosts).toHaveLength(0);
  });

  it("tolerates a null ledger so callers can stay unconditional", () => {
    expect(isUnhealthyNodeHost(null, "ru9.nfnpx.online")).toBe(false);
  });
});

describe("evidence precedence", () => {
  // 2026-09-06: galeevy-dom's YouTube slot sat on ru7:50051, proven dead by
  // tcping from that very router, yet its destination probe still reported
  // youtube.com "reachable" — the probe never crossed the slot it was
  // attributed to. That single inferred success vetoed the condemnation of
  // ru7 fleet-wide and left six routers, kirill-msk among them, pinned to a
  // dead node while the panel called every one of them "compliant".
  //
  // A destination probe only ASSUMES the traffic traversed the slot's node;
  // a route verification tests that node directly. When the two disagree
  // about the same endpoint on the same router, the direct verdict is the
  // one that saw the node.
  it("lets a router's direct verdict override its own inferred probe", () => {
    const health = buildFleetNodeHealth([
      sample("galeevy", [
        { host: "ru7.nfnpx.online:50051", outcome: "ok", source: "inferred" },
        { host: "ru7.nfnpx.online:50051", outcome: "fail", source: "direct" },
        { host: "pl2.nfnpx.online:443", outcome: "ok", source: "direct" },
      ]),
      sample("kirill", [
        { host: "ru7.nfnpx.online:50051", outcome: "fail", source: "direct" },
        { host: "pl1.nfnpx.online:443", outcome: "ok", source: "direct" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru7.nfnpx.online", 50051)).toBe(true);
    expect(isUnhealthyNodeHost(health, "pl2.nfnpx.online", 443)).toBe(false);
  });

  // Precedence is per router and per endpoint, never a fleet-wide veto: a
  // router that genuinely carries traffic through the host still spares it,
  // which is the rule that stops one broken uplink moving everybody.
  it("still spares a host another router reaches directly", () => {
    const health = buildFleetNodeHealth([
      sample("galeevy", [
        { host: "ru7.nfnpx.online:50051", outcome: "ok", source: "inferred" },
        { host: "ru7.nfnpx.online:50051", outcome: "fail", source: "direct" },
        { host: "pl2.nfnpx.online:443", outcome: "ok", source: "direct" },
      ]),
      sample("zhenya", [
        { host: "ru7.nfnpx.online:50051", outcome: "ok", source: "direct" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru7.nfnpx.online", 50051)).toBe(false);
  });

  // Nothing contradicts the probe here, so the old conservatism stands: an
  // inferred success is still a success when no direct verdict disputes it.
  it("keeps trusting an inferred success no direct verdict contradicts", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "ru18.nfnpx.online:50051", outcome: "ok", source: "inferred" },
        { host: "pl2.nfnpx.online:443", outcome: "fail", source: "inferred" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru18.nfnpx.online", 50051)).toBe(false);
  });

  // Direct evidence wins in both directions, so a working node test is not
  // discarded because a destination probe failed for its own reasons — a
  // blocked site, a DNS fault, a geosite rule that never reached the slot.
  it("lets a direct success override this router's inferred failure", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "ru18.nfnpx.online:50051", outcome: "fail", source: "inferred" },
        { host: "ru18.nfnpx.online:50051", outcome: "ok", source: "direct" },
        { host: "pl2.nfnpx.online:443", outcome: "ok", source: "direct" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru18.nfnpx.online", 50051)).toBe(false);
  });

  // Observations arriving without a source are the pre-existing shape and
  // must keep their pre-existing meaning.
  it("treats an unlabelled observation as inferred", () => {
    const health = buildFleetNodeHealth([
      sample("kirill", [
        { host: "ru7.nfnpx.online:50051", outcome: "ok" },
        { host: "ru7.nfnpx.online:50051", outcome: "fail", source: "direct" },
        { host: "pl2.nfnpx.online:443", outcome: "ok", source: "direct" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru7.nfnpx.online", 50051)).toBe(true);
  });
});

describe("endpoint keying", () => {
  // The provider runs several services on one machine, one per port: 50051 is
  // YouTube, 50055 Netherlands. Keyed by host alone, a healthy ru7:50054 hid a
  // dead ru7:50055 and the Netherlands slot stayed broken fleet-wide while the
  // ledger reported nothing (measured 2026-08-24 on zhenya13911).
  it("condemns one port of a host without sparing it for another", () => {
    const health = buildFleetNodeHealth([
      sample("zhenya", [
        { host: "ru7.nfnpx.online:50055", outcome: "fail" },
        { host: "ru7.nfnpx.online:50054", outcome: "ok" },
      ]),
    ]);

    expect(isUnhealthyNodeHost(health, "ru7.nfnpx.online", 50055)).toBe(true);
    expect(isUnhealthyNodeHost(health, "ru7.nfnpx.online", 50054)).toBe(false);
  });
});
