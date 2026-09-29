import {
  ensurePasswallRuntimeJobPayloadSchema,
  ensurePasswallRuntimeResultPayloadSchema,
} from "@vectra/contracts";
import { describe, expect, it } from "vitest";

import { compareControllerVersions } from "~/lib/controller-version";
import {
  minimumXrayRuntimeRepairControllerVersion,
  shouldReplaceXrayInPlace,
} from "~/lib/xray-runtime-repair";

describe("xray_binary runtime repair contract", () => {
  it("queues only the xray repair when asked for it", () => {
    const payload = ensurePasswallRuntimeJobPayloadSchema.parse({
      actions: ["xray_binary"],
    });
    expect(payload.actions).toEqual(["xray_binary"]);
    expect(payload.xrayVersion).toBeUndefined();
    expect(payload.xraySha256).toBeUndefined();
  });

  it("never slips the xray repair into the default action set", () => {
    const payload = ensurePasswallRuntimeJobPayloadSchema.parse({});
    expect(payload.actions).toEqual(["compact_geodata", "dnsmasq_full"]);
  });

  it("accepts a pin only in the shape the controller will run", () => {
    const sha = "ab".repeat(32);
    expect(
      ensurePasswallRuntimeJobPayloadSchema.parse({
        actions: ["xray_binary"],
        xrayVersion: "26.8.1",
        xraySha256: sha,
      }).xrayVersion,
    ).toBe("26.8.1");

    for (const bad of [
      { xrayVersion: "26.7.28'; reboot; '" },
      { xraySha256: "abc" },
      { xraySha256: "ZZ".repeat(32) },
    ]) {
      expect(() =>
        ensurePasswallRuntimeJobPayloadSchema.parse({
          actions: ["xray_binary"],
          ...bad,
        }),
      ).toThrow();
    }
  });

  it("carries the in-place replacement opt-in only as a boolean", () => {
    expect(
      ensurePasswallRuntimeJobPayloadSchema.parse({
        actions: ["xray_binary"],
        xrayReplaceInPlace: true,
      }).xrayReplaceInPlace,
    ).toBe(true);
    expect(() =>
      ensurePasswallRuntimeJobPayloadSchema.parse({
        actions: ["xray_binary"],
        xrayReplaceInPlace: "yes",
      }),
    ).toThrow();
  });

  it("accepts the controller's per-action result for xray_binary", () => {
    const result = ensurePasswallRuntimeResultPayloadSchema.parse({
      ok: false,
      checkedAt: "2026-09-29T08:00:00.000Z",
      actions: [
        {
          action: "xray_binary",
          status: "failure",
          error: "xray 26.7.28 checksum mismatch; PassWall left stopped",
        },
      ],
    });
    expect(result.actions[0]?.action).toBe("xray_binary");
  });

  it("gates the repair on the first controller that implements it", () => {
    // r40 is what the fleet runs today and rejects the action as unsupported.
    expect(
      compareControllerVersions("0.1.13-r40", minimumXrayRuntimeRepairControllerVersion),
    ).toBeLessThan(0);
    expect(
      compareControllerVersions("0.1.13-r41", minimumXrayRuntimeRepairControllerVersion),
    ).toBe(0);
  });

  it("replaces in place when asked or when the router reports its runtime unusable", () => {
    const unusable = {
      safetyEvents: [
        { type: "low_memory", severity: "warning" },
        { type: "proxy_runtime_unusable", severity: "critical" },
      ],
    };
    expect(shouldReplaceXrayInPlace(null, true)).toBe(true);
    expect(shouldReplaceXrayInPlace(unusable, false)).toBe(true);
    expect(shouldReplaceXrayInPlace({ safetyEvents: [{ type: "proxy_runtime_missing" }] }, false)).toBe(false);
    expect(shouldReplaceXrayInPlace({}, false)).toBe(false);
    expect(shouldReplaceXrayInPlace(null, false)).toBe(false);
  });
});
