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
