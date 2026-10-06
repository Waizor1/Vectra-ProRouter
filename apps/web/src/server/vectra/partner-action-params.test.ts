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
    // ":auto": back to the service's default (capability set_service_auto).
    expect(
      parseConnectActionParams("set_service", {
        service: "ai",
        entryId: ":auto",
      }),
    ).toMatchObject({ success: true, data: { service: "ai", entryId: ":auto" } });
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
      { ssid: "fake", password: "fake-pass-123", band: "60g" },
      { ssid: "fake", password: "fake-pass-123", band: "" },
      { ssid: "fake", password: "fake-pass-123", band: null },
    ])
      expect(parseConnectActionParams("set_wifi", params).success).toBe(false);
  });
  it("takes an optional band for one band's Wi-Fi", () => {
    for (const band of ["2g", "5g", "6g"])
      expect(
        parseConnectActionParams("set_wifi", {
          ssid: "fake",
          password: "fake-pass-123",
          band,
        }),
      ).toMatchObject({ success: true, data: { band } });
    const all = parseConnectActionParams("set_wifi", {
      ssid: "fake",
      password: "fake-pass-123",
    });
    expect(all.success && "band" in all.data).toBe(false);
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

  // set_port_forwards mirrors vctl's portForwardList (connectactions): the
  // contract's keys exactly, the router's syntax for each.
  describe("set_port_forwards", () => {
    const contractExample = {
      rules: [
        {
          id: "3fa1c09e",
          preset: "minecraft-java",
          destIp: "192.168.1.50",
          port: "25565",
          proto: "tcp",
          direct: false,
          enabled: true,
        },
        {
          destIp: "192.168.1.60",
          port: "3478-3479",
          proto: "both",
          direct: true,
          enabled: true,
        },
      ],
    };
    const rule = {
      destIp: "192.168.1.50",
      port: "25565",
      proto: "udp",
      direct: false,
      enabled: false,
    };
    const parse = (params: unknown) =>
      parseConnectActionParams("set_port_forwards", params);

    it("accepts the contract example as it is, and an empty list", () => {
      expect(parse(contractExample)).toEqual({
        success: true,
        data: contractExample,
      });
      expect(parse({ rules: [] })).toEqual({
        success: true,
        data: { rules: [] },
      });
    });
    it("accepts a null preset, single ports and the bounds of the range", () => {
      for (const extra of [
        { preset: null },
        { port: "1" },
        { port: "65535" },
        { port: "1-65535" },
        { port: "80-80" },
        { destIp: "10.0.0.255" },
        { proto: "both" },
      ])
        expect(parse({ rules: [{ ...rule, ...extra }] }).success).toBe(true);
      expect(
        parse({
          rules: Array.from({ length: 32 }, (_, i) => ({
            ...rule,
            port: String(1000 + i),
          })),
        }).success,
      ).toBe(true);
    });
    it("refuses unknown keys, deviceName and a missing required key", () => {
      for (const params of [
        { rules: [{ ...rule, deviceName: "gaming-pc" }] },
        { rules: [{ ...rule, mac: "aa:bb:cc:dd:ee:ff" }] },
        { rules: [{ ...rule, extra: true }] },
        { rules: [], extra: true },
        {},
        ...(["destIp", "port", "proto", "direct", "enabled"] as const).map(
          (key) => {
            const { [key]: _, ...rest } = rule;
            return { rules: [rest] };
          },
        ),
      ])
        expect(parse(params).success).toBe(false);
    });
    it("refuses a bad port, proto, address, id or preset", () => {
      for (const extra of [
        { port: "0" },
        { port: "65536" },
        { port: "200-100" },
        { port: "100-" },
        { port: "80,443" },
        { port: "80:81" },
        { port: " 80" },
        { port: "123456" },
        { port: 25565 },
        { proto: "tcpudp" },
        { proto: "TCP" },
        { proto: "" },
        { destIp: "192.168.1.256" },
        { destIp: "192.168.1" },
        { destIp: "192.168.01.5" },
        { destIp: "::1" },
        { destIp: "fe80::1" },
        { destIp: "gaming-pc" },
        { destIp: "" },
        { id: "3FA1C09E" },
        { id: "3fa1c09" },
        { id: "3fa1c09ea" },
        { id: null },
        { id: "" },
        { preset: "" },
        { preset: "Minecraft" },
        { preset: "a".repeat(25) },
        { preset: "mine craft" },
        { direct: "true" },
        { enabled: 1 },
      ])
        expect(parse({ rules: [{ ...rule, ...extra }] }).success).toBe(false);
    });
    it("refuses more than 32 rules, duplicate ids and a non-array", () => {
      expect(
        parse({
          rules: Array.from({ length: 33 }, (_, i) => ({
            ...rule,
            port: String(1000 + i),
          })),
        }).success,
      ).toBe(false);
      expect(
        parse({
          rules: [
            { ...rule, id: "3fa1c09e" },
            { ...rule, id: "3fa1c09e", port: "25566" },
          ],
        }).success,
      ).toBe(false);
      for (const rules of [rule, "[]", null, { 0: rule }])
        expect(parse({ rules }).success).toBe(false);
    });
  });
});
