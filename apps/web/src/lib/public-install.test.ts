import { describe, expect, it } from "vitest";

import {
  detectHelperDesktopPlatform,
  getHelperDownloadOptions,
  mergeChecklistDelta,
  isProbablyMobileUserAgent,
  selectRecommendedCandidate,
  type HelperScanResponse,
} from "~/lib/public-install";

describe("public install helpers", () => {
  it("detects mobile user agents for fallback mode", () => {
    expect(
      isProbablyMobileUserAgent(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)",
      ),
    ).toBe(true);
    expect(
      isProbablyMobileUserAgent(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0)",
      ),
    ).toBe(false);
  });

  it("sorts helper downloads so the current desktop platform is first", () => {
    const downloads = getHelperDownloadOptions("windows");

    expect(downloads[0]?.label).toContain("Windows");
    expect(downloads.some((option) => option.label.includes("macOS"))).toBe(
      true,
    );
  });

  it("points macOS downloads at the app bundle launcher", () => {
    const downloads = getHelperDownloadOptions("macos");

    expect(downloads[0]?.launcher).toBe("Vectra Install Helper.app");
  });

  it("detects desktop platform families from the browser hints", () => {
    expect(
      detectHelperDesktopPlatform(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0)",
        "MacIntel",
      ),
    ).toBe("macos");
    expect(
      detectHelperDesktopPlatform(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
        "Win32",
      ),
    ).toBe("windows");
  });

  it("prefers a recommended router candidate", () => {
    const scan: HelperScanResponse = {
      recommendedTargetIp: "192.168.99.1",
      candidates: [
        {
          ip: "192.168.1.1",
          source: "known_ip",
          sshReachable: true,
          fingerprintState: "trusted",
          hostKeyFingerprint: "SHA256:old",
          recommended: false,
        },
        {
          ip: "192.168.99.1",
          source: "default_gateway",
          sshReachable: true,
          fingerprintState: "new",
          hostKeyFingerprint: "SHA256:new",
          recommended: true,
        },
      ],
    };

    expect(selectRecommendedCandidate(scan)?.ip).toBe("192.168.99.1");
  });

  it("merges checklist updates by stable item id", () => {
    const merged = mergeChecklistDelta(
      [
        { id: "controller", label: "Controller", status: "pending" },
        { id: "passwall", label: "PassWall2", status: "pending" },
      ],
      [
        {
          id: "controller",
          label: "Controller",
          status: "success",
          details: "service running",
        },
      ],
    );

    expect(merged).toEqual([
      {
        id: "controller",
        label: "Controller",
        status: "success",
        details: "service running",
      },
      { id: "passwall", label: "PassWall2", status: "pending" },
    ]);
  });
});
