import { describe, expect, it } from "vitest";

import {
  compareControllerVersions,
  formatControllerVersion,
  normalizeControllerVersion,
  resolveRunningControllerVersion,
  unknownControllerVersionLabel,
} from "./controller-version";

describe("controller version helpers", () => {
  it("normalizes raw unknown sentinels to null", () => {
    expect(normalizeControllerVersion("unknown")).toBeNull();
    expect(normalizeControllerVersion(" неизвестно ")).toBeNull();
    expect(normalizeControllerVersion("   ")).toBeNull();
    expect(normalizeControllerVersion(null)).toBeNull();
  });

  it("formats unknown controller version with operator-friendly copy", () => {
    expect(formatControllerVersion("unknown")).toBe(
      unknownControllerVersionLabel,
    );
  });

  it("does not compare invalid controller versions", () => {
    expect(compareControllerVersions("unknown", "0.1.12-r2")).toBeNull();
  });

  it("compares known controller versions semantically", () => {
    expect(compareControllerVersions("0.1.12-r2", "0.1.12-r2")).toBe(0);
    expect(compareControllerVersions("0.1.11-r1", "0.1.12-r2")).toBeLessThan(0);
  });

  it("resolves running controller version only from the runtime signal", () => {
    expect(
      resolveRunningControllerVersion({
        payload: {
          controllerVersion: "0.1.13-r23",
          controllerRuntimeVersion: "0.1.13-r22",
        },
      }),
    ).toBe("0.1.13-r22");
    expect(
      resolveRunningControllerVersion({
        payload: {
          controllerVersion: "0.1.13-r23",
          packageVersions: { "vectra-controller-agent": "0.1.13-r23" },
        },
      }),
    ).toBeNull();
  });
});
