import { describe, expect, it } from "vitest";

import { buildConfigTrustState } from "./config-trust";

describe("buildConfigTrustState", () => {
  it("marks stale-authoritative state when snapshot digest has no matching live import", () => {
    const trust = buildConfigTrustState({
      routerReachable: true,
      lastCheckInAt: new Date("2026-04-19T10:00:00.000Z"),
      authoritativeDigest: "digest-authoritative",
      snapshotDigest: "digest-live",
      revisions: [
        {
          origin: "router_import",
          configDigest: "digest-old",
          createdAt: new Date("2026-04-18T10:00:00.000Z"),
        },
      ],
      hasAuthoritativeConfig: true,
    });

    expect(trust).toMatchObject({
      liveConfigAvailable: false,
      requiresReimport: true,
      digestMismatch: true,
      configSourceMode: "stale-authoritative",
    });
  });

  it("marks live-import only when snapshot digest matches a live import revision", () => {
    const trust = buildConfigTrustState({
      routerReachable: true,
      lastCheckInAt: new Date("2026-04-19T10:00:00.000Z"),
      authoritativeDigest: "digest-live",
      snapshotDigest: "digest-live",
      revisions: [
        {
          origin: "operator_reimport",
          configDigest: "digest-live",
          createdAt: new Date("2026-04-19T09:59:00.000Z"),
        },
      ],
      hasAuthoritativeConfig: true,
    });

    expect(trust).toMatchObject({
      liveConfigAvailable: true,
      requiresReimport: false,
      digestMismatch: false,
      configSourceMode: "live-import",
    });
  });

  it("falls back to authoritative when no fresh snapshot digest exists", () => {
    const trust = buildConfigTrustState({
      routerReachable: false,
      lastCheckInAt: null,
      authoritativeDigest: "digest-authoritative",
      snapshotDigest: null,
      revisions: [],
      hasAuthoritativeConfig: true,
    });

    expect(trust).toMatchObject({
      liveConfigAvailable: false,
      requiresReimport: false,
      digestMismatch: false,
      configSourceMode: "authoritative",
    });
  });

  it("falls back to inventory-only when router has no authoritative baseline", () => {
    const trust = buildConfigTrustState({
      routerReachable: true,
      lastCheckInAt: new Date("2026-04-19T10:00:00.000Z"),
      authoritativeDigest: null,
      snapshotDigest: "digest-live",
      revisions: [],
      hasAuthoritativeConfig: false,
    });

    expect(trust).toMatchObject({
      liveConfigAvailable: false,
      requiresReimport: true,
      digestMismatch: false,
      configSourceMode: "inventory-only",
    });
  });
});
