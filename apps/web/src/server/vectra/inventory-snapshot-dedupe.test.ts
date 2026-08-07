import { describe, expect, it } from "vitest";

import { routerInventorySchema } from "@vectra/contracts";

import {
  materialInventoryFingerprint,
  shouldWriteInventorySnapshot,
} from "./inventory-snapshot-dedupe";

const baseInventory = routerInventorySchema.parse({
  protocolVersion: "2026-04-v1",
  deviceIdentifier: "vectra-test",
  devicePublicKey: "pub",
  controllerVersion: "0.1.13-r36",
  model: "AX3000T",
  boardName: "xiaomi,mi-router-ax3000t",
  target: "mediatek/filogic",
  architecture: "aarch64_cortex-a53",
  openwrtRelease: "24.10.6",
  passwallEnabled: true,
  selectedNodeId: "node-pl1",
  nodeCount: 26,
  subscriptionCount: 1,
  configDigest: "digest-a",
  packageVersions: { "luci-app-passwall2": "26.7.16-r1" },
  binaryVersions: { xray: "26.7.28" },
  rulesAssets: {},
  resources: {
    memoryTotalMb: 234,
    memoryAvailableMb: 128,
    swapTotalMb: 0,
    swapFreeMb: 0,
    overlayFreeMb: 32,
    tmpFreeMb: 64,
  },
  serviceHealth: {
    controller: "running",
    passwall: "running",
    passwallServer: "unknown",
    dnsmasq: "running",
  },
  panelReachability: {
    reachable: true,
    checkedAt: "2026-08-07T10:00:00.000Z",
    status: "healthy",
    reachableCount: 1,
    totalCount: 1,
    checks: [],
  },
});

const now = new Date("2026-08-07T12:00:00.000Z");

function latest(inventory: unknown, ageMinutes: number) {
  return {
    payload: inventory,
    createdAt: new Date(now.getTime() - ageMinutes * 60_000),
  };
}

describe("materialInventoryFingerprint", () => {
  it("ignores free-memory jitter, which changes on every single check-in", () => {
    const jittered = routerInventorySchema.parse({
      ...baseInventory,
      resources: {
        ...baseInventory.resources,
        memoryAvailableMb: baseInventory.resources.memoryAvailableMb - 3,
        tmpFreeMb: baseInventory.resources.tmpFreeMb - 1,
      },
    });

    expect(materialInventoryFingerprint(jittered)).toBe(
      materialInventoryFingerprint(baseInventory),
    );
  });

  it("ignores gauge drift right next to a round number", () => {
    // The fixture sits at 128 MB free. Any bucketing scheme would put an edge
    // here and write a row on every sample; a router hovering at the low-memory
    // line is the normal case on a 234 MB AX3000T.
    for (const memoryAvailableMb of [129, 128, 127, 120, 64, 63, 16, 4]) {
      const drifted = routerInventorySchema.parse({
        ...baseInventory,
        resources: { ...baseInventory.resources, memoryAvailableMb },
      });

      expect(materialInventoryFingerprint(drifted)).toBe(
        materialInventoryFingerprint(baseInventory),
      );
    }
  });

  it("ignores the live gauge smuggled inside a safety-event message", () => {
    // Taken from production: the low_memory event restates the exact free-RAM
    // figure in its prose, so fingerprinting the message re-imports the very
    // jitter the resources rules exclude. This defeated the first deploy —
    // 142 rows in 5 minutes, essentially the old rate.
    const withEvent = (message: string, observedAt: string) =>
      routerInventorySchema.parse({
        ...baseInventory,
        safetyEvents: [
          {
            type: "low_memory",
            source: "resources",
            severity: "warning",
            message,
            observedAt,
          },
        ],
      });

    expect(
      materialInventoryFingerprint(
        withEvent(
          "available RAM is low: 50 MB available (21% of 234 MB)",
          "2026-08-07T12:48:21.000Z",
        ),
      ),
    ).toBe(
      materialInventoryFingerprint(
        withEvent(
          "available RAM is low: 37 MB available (15% of 234 MB)",
          "2026-08-07T12:47:36.000Z",
        ),
      ),
    );
  });

  it("ignores safety events, which flap as memory dips and recovers", () => {
    const none = routerInventorySchema.parse({
      ...baseInventory,
      safetyEvents: [],
    });
    const warn = routerInventorySchema.parse({
      ...baseInventory,
      safetyEvents: [
        {
          type: "low_memory",
          source: "resources",
          severity: "warning",
          message: "available RAM is low: 50 MB available",
          observedAt: "2026-08-07T12:00:00.000Z",
        },
      ],
    });
    const critical = routerInventorySchema.parse({
      ...baseInventory,
      safetyEvents: [
        {
          type: "low_memory",
          source: "resources",
          severity: "critical",
          message: "available RAM is low: 50 MB available",
          observedAt: "2026-08-07T12:00:00.000Z",
        },
      ],
    });
    const otherKind = routerInventorySchema.parse({
      ...baseInventory,
      safetyEvents: [
        {
          type: "low_overlay",
          source: "resources",
          severity: "warning",
          message: "overlay is low",
          observedAt: "2026-08-07T12:00:00.000Z",
        },
      ],
    });

    // low_memory firing and clearing is the resting state of a 234 MB router,
    // not news. Real pressure is surfaced by health incidents and the safety
    // guard from the live check-in.
    const fp = materialInventoryFingerprint;
    expect(fp(warn)).toBe(fp(none));
    expect(fp(critical)).toBe(fp(warn));
    expect(fp(otherKind)).toBe(fp(warn));
  });

  it("reacts when zram changes the swap the device actually has", () => {
    const withZram = routerInventorySchema.parse({
      ...baseInventory,
      resources: { ...baseInventory.resources, swapTotalMb: 117 },
    });

    expect(materialInventoryFingerprint(withZram)).not.toBe(
      materialInventoryFingerprint(baseInventory),
    );
  });

  it("ignores probe timestamps while the verdict is unchanged", () => {
    const reprobed = routerInventorySchema.parse({
      ...baseInventory,
      panelReachability: {
        ...baseInventory.panelReachability,
        checkedAt: "2026-08-07T11:59:00.000Z",
      },
    });

    expect(materialInventoryFingerprint(reprobed)).toBe(
      materialInventoryFingerprint(baseInventory),
    );
  });

  it("ignores reachability, which the agent probes intermittently", () => {
    // Measured on 238 real snapshots: probe groups appear and vanish between
    // consecutive check-ins because the agent does not run every probe every
    // time, so fingerprinting them writes a row on every other sample.
    // The heartbeat carries probe history instead; incidents and auto-rescue
    // act on the live payload, not on this table.
    const blocked = routerInventorySchema.parse({
      ...baseInventory,
      panelReachability: {
        ...baseInventory.panelReachability,
        reachable: false,
        status: "blocked",
        reachableCount: 0,
      },
    });
    const absent = routerInventorySchema.parse({
      ...baseInventory,
      panelReachability: undefined,
    });

    expect(materialInventoryFingerprint(blocked)).toBe(
      materialInventoryFingerprint(baseInventory),
    );
    expect(materialInventoryFingerprint(absent)).toBe(
      materialInventoryFingerprint(baseInventory),
    );
  });

  it.each([
    ["configDigest", { configDigest: "digest-b" }],
    ["selectedNodeId", { selectedNodeId: "node-pl2" }],
    ["passwallEnabled", { passwallEnabled: false }],
    ["nodeCount", { nodeCount: 27 }],
    ["controllerVersion", { controllerVersion: "0.1.14-r1" }],
    ["packageVersions", { packageVersions: { "luci-app-passwall2": "26.8" } }],
    [
      "serviceHealth",
      {
        serviceHealth: {
          controller: "running",
          passwall: "stopped",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
      },
    ],
  ])("reacts to a change in %s", (_label, patch) => {
    const changed = routerInventorySchema.parse({ ...baseInventory, ...patch });

    expect(materialInventoryFingerprint(changed)).not.toBe(
      materialInventoryFingerprint(baseInventory),
    );
  });
});

describe("shouldWriteInventorySnapshot", () => {
  it("writes when the router has no snapshot yet", () => {
    expect(
      shouldWriteInventorySnapshot({
        inventory: baseInventory,
        latest: null,
        now,
        heartbeatMinutes: 60,
      }),
    ).toBe(true);
  });

  it("skips a check-in that carries no material change", () => {
    expect(
      shouldWriteInventorySnapshot({
        inventory: baseInventory,
        latest: latest(baseInventory, 5),
        now,
        heartbeatMinutes: 60,
      }),
    ).toBe(false);
  });

  it("writes immediately on a material change, heartbeat notwithstanding", () => {
    const changed = routerInventorySchema.parse({
      ...baseInventory,
      configDigest: "digest-b",
    });

    expect(
      shouldWriteInventorySnapshot({
        inventory: changed,
        latest: latest(baseInventory, 1),
        now,
        heartbeatMinutes: 60,
      }),
    ).toBe(true);
  });

  it("writes an unchanged payload once the heartbeat window lapses", () => {
    // Without this the telemetry series would stop dead on a stable router and
    // the panel would show a last-seen snapshot from days ago.
    expect(
      shouldWriteInventorySnapshot({
        inventory: baseInventory,
        latest: latest(baseInventory, 61),
        now,
        heartbeatMinutes: 60,
      }),
    ).toBe(true);
  });

  it("writes when the stored payload predates the current schema", () => {
    // A payload the fingerprint cannot read is not evidence of no-change.
    expect(
      shouldWriteInventorySnapshot({
        inventory: baseInventory,
        latest: latest({ garbage: true }, 5),
        now,
        heartbeatMinutes: 60,
      }),
    ).toBe(true);
  });
});
