import { describe, expect, it } from "vitest";
import { parseConnectActionParams } from "./partner-action-params";

describe("Connect typed action params", () => {
  it("matches established ID, nullable entry and exact key constraints", () => {
    expect(
      parseConnectActionParams("select_entry", { entryId: null }).success,
    ).toBe(true);
    expect(
      parseConnectActionParams("set_service", {
        service: "youtube",
        entryId: "entry-1",
      }).success,
    ).toBe(true);
    for (const params of [
      { entryId: "../bad" },
      { entryId: "a".repeat(65) },
      {},
      { entryId: null, extra: true },
    ])
      expect(parseConnectActionParams("select_entry", params).success).toBe(
        false,
      );
  });
  it("preserves Unicode, wildcard, suffix and normalized deduplicated rules", () => {
    const parsed = parseConnectActionParams("set_rules", {
      direct: [
        " *.Example.COM ",
        "*.example.com",
        ".суффикс.рф",
        "under_score.test.",
      ],
      vpn: ["vpn.test"],
    });
    expect(parsed.success && parsed.data).toEqual({
      direct: ["*.example.com", ".суффикс.рф", "under_score.test."],
      vpn: ["vpn.test"],
    });
    expect(
      parseConnectActionParams("set_rules", {
        direct: Array(400).fill("duplicate.test"),
        vpn: [],
      }).success,
    ).toBe(true);
  });
  it("rejects invalid or excessive rules", () => {
    for (const domain of [
      "https://example.com",
      "example.com/path",
      "ex ample.com",
      "example..com",
      "example.com:443",
      "a".repeat(254),
    ])
      expect(
        parseConnectActionParams("set_rules", { direct: [domain], vpn: [] })
          .success,
      ).toBe(false);
    expect(
      parseConnectActionParams("set_rules", {
        direct: Array.from({ length: 301 }, (_, i) => `s${i}.test`),
        vpn: [],
      }).success,
    ).toBe(false);
  });
  it("bounds WiFi SSID in bytes, accepts only WPA printable ASCII and rejects extras", () => {
    expect(
      parseConnectActionParams("set_wifi", {
        ssid: "я".repeat(16),
        password: "fake-pass-123",
      }).success,
    ).toBe(true);
    for (const params of [
      { ssid: "я".repeat(17), password: "fake-pass-123" },
      { ssid: "bad\n", password: "fake-pass-123" },
      { ssid: "", password: "fake-pass-123" },
      { ssid: "fake", password: "short" },
      { ssid: "fake", password: "пароль123" },
      { ssid: "fake", password: "a".repeat(64) },
      { ssid: "fake", password: "fake-pass-123", command: "bad" },
    ])
      expect(parseConnectActionParams("set_wifi", params).success).toBe(false);
  });
  it("requires an actual boolean and empty params for parameterless commands", () => {
    expect(
      parseConnectActionParams("set_auto_update", { enabled: true }).success,
    ).toBe(true);
    expect(
      parseConnectActionParams("set_auto_update", { enabled: "true" }).success,
    ).toBe(false);
    for (const action of [
      "reboot",
      "update_now",
      "restart_vpn",
      "refresh_subscription",
    ] as const) {
      expect(parseConnectActionParams(action, {}).success).toBe(true);
      expect(parseConnectActionParams(action, { command: "bad" }).success).toBe(
        false,
      );
    }
  });
});
