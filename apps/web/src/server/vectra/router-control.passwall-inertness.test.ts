import { describe, expect, it } from "vitest";

import {
  desiredRevisionSummarySchema,
  passwallDesiredConfigSchema,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";

// ---------------------------------------------------------------------------
// INERTNESS GUARD for the 30 live passwall routers.
//
// The xray-direct work rewrote `xrayDesiredConfigSchema`, which is the SECOND
// member of the `desiredRevisionSummarySchema.config` union that every
// check-in response is parsed through. This file pins the exact bytes a
// passwall router receives so that change — and any future change to the xray
// branch — is provably inert for them.
//
// GOLDEN_PASSWALL_DESIRED_REVISION below was captured by running this exact
// fixture against the schema on the BASE commit (feat/vctl-provider-json,
// c15dd59) BEFORE the xray rewrite, and re-verified byte-for-byte afterwards:
//   sha256(payload) = 4ed16f795c99ba87aff6b3a527453a9f51026a6ab21aa737285ca357329e45ea
//
// Re-captured on the merge onto main (b3db55a, 2026-10-02): main itself added
// `subscriptions.extras` (a8a50cd, deployed) so the global_subscribe section's
// unmodelled UCI options survive an apply. That `"extras":{}` is the only
// difference and is what live passwall routers already receive from main:
//   sha256(payload) = 6fc3add6210501193dd6286872980d42467a39b5eb999180f02c0c45b1250237
// It is a captured artifact, not a hand-written expectation. If this test
// fails, the passwall wire format moved — treat that as a production incident
// on live customer routers, not a test to update.
// ---------------------------------------------------------------------------

const GOLDEN_PASSWALL_DESIRED_REVISION = `{"id":"11111111-1111-4111-8111-111111111111","revisionNumber":7,"status":"active","origin":"operator_draft","engineMode":"passwall","configDigest":"abc123","config":{"schemaVersion":1,"basicSettings":{"main":{"mainSwitch":true,"selectedNodeId":"node-pl1","localhostProxy":true,"clientProxy":true,"nodeSocksPort":1070,"nodeSocksBindLocal":true,"socksMainSwitch":false,"extras":{}},"dns":{"directQueryStrategy":"UseIP","remoteDnsProtocol":"doh","remoteDns":"1.1.1.1","remoteDnsDoh":"https://1.1.1.1/dns-query","remoteDnsDetour":"remote","remoteFakeDns":false,"remoteDnsQueryStrategy":"UseIPv4","dnsHosts":[],"dnsRedirect":true,"extras":{}},"log":{"enableNodeLog":true,"level":"warning","extras":{}},"maintenance":{"backupPaths":["/etc/config/passwall2","/etc/config/passwall2_server","/usr/share/passwall2/domains_excluded"],"extras":{}},"socks":[],"shuntRules":[]},"nodes":[{"id":"node-pl1","label":"PL1 :443","protocol":"vless","enabled":true,"group":"default","address":"pl1.example.online","port":443,"username":"operator","password":"node-password","tags":[],"extras":{"uuid":"real-uuid","token":"real-token","flow":"xtls-rprx-vision"}}],"subscriptions":{"filterKeywordMode":"0","discardList":[],"keepList":[],"typePreferences":{},"domainStrategy":"auto","items":[{"id":"sub-1","remark":"BloopCat","url":"https://sub.example.com/api/sub/REAL_TOKEN","enabled":true,"addMode":"2","metadata":{},"extras":{"secret":"real-secret"}}],"extras":{}},"appUpdate":{"binaryPaths":{"xray":"/usr/bin/xray","singBox":"/usr/bin/sing-box","hysteria":"/usr/bin/hysteria","geoview":"/usr/bin/geoview"},"updateStrategy":"package-preferred","targetVersions":{},"extras":{}},"ruleManage":{"geoipUrl":"https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat","geositeUrl":"https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat","assetDirectory":"/usr/share/v2ray/","autoUpdate":false,"scheduleMode":"daily","enabledAssets":["geoip","geosite"],"shuntRules":[],"extras":{}}},"impact":{"changedSections":["basicSettings"],"requiresRestart":true,"refreshSubscriptions":false,"refreshRules":false,"packageInstall":false,"firmwareValidation":false}}`;

// A representative live-fleet passwall config: one node with credentials, one
// subscription with a token URL, real fleet geo URLs.
const passwallConfigInput = {
  basicSettings: {
    main: { mainSwitch: true, selectedNodeId: "node-pl1", nodeSocksPort: 1070 },
    dns: { remoteDns: "1.1.1.1", remoteDnsProtocol: "doh" },
    log: {},
    maintenance: {},
  },
  nodes: [
    {
      id: "node-pl1",
      label: "PL1 :443",
      remark: "PL1 :443",
      type: "Xray",
      protocol: "vless",
      address: "pl1.example.online",
      port: 443,
      username: "operator",
      password: "node-password",
      extras: {
        uuid: "real-uuid",
        token: "real-token",
        flow: "xtls-rprx-vision",
      },
    },
  ],
  subscriptions: {
    items: [
      {
        id: "sub-1",
        remark: "BloopCat",
        url: "https://sub.example.com/api/sub/REAL_TOKEN",
        extras: { secret: "real-secret" },
      },
    ],
  },
  appUpdate: {},
  ruleManage: {
    geoipUrl:
      "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
    geositeUrl:
      "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
  },
};

function buildPasswallDesiredRevision() {
  return desiredRevisionSummarySchema.parse({
    id: "11111111-1111-4111-8111-111111111111",
    revisionNumber: 7,
    status: "active",
    origin: "operator_draft",
    configDigest: "abc123",
    config: passwallDesiredConfigSchema.parse(passwallConfigInput),
    impact: {
      changedSections: ["basicSettings"],
      requiresRestart: true,
      refreshSubscriptions: false,
      refreshRules: false,
      packageInstall: false,
      firmwareValidation: false,
    },
  });
}

describe("passwall check-in payload inertness", () => {
  it("serializes byte-for-byte identically to the pre-xray-rewrite golden", () => {
    expect(JSON.stringify(buildPasswallDesiredRevision())).toBe(
      GOLDEN_PASSWALL_DESIRED_REVISION,
    );
  });

  it("defaults engineMode to passwall when the router never reports one", () => {
    // The 30 live routers predate engineMode entirely. The union's discriminator
    // must keep defaulting to passwall or the engine guard in
    // resolveDesiredRevision would start returning null for all of them.
    expect(buildPasswallDesiredRevision().engineMode).toBe("passwall");
  });

  it("routes a passwall config through the passwall union member, never the xray one", () => {
    // The union is [passwall, xray]; passwall is tried first. Prove the xray
    // member cannot also accept a passwall config, so union order can never
    // silently reshape live traffic.
    expect(
      xrayDesiredConfigSchema.safeParse(
        passwallDesiredConfigSchema.parse(passwallConfigInput),
      ).success,
    ).toBe(false);
  });

  it("never lets an xray operator config parse as a passwall config", () => {
    // The inverse direction: basicSettings/subscriptions/appUpdate/ruleManage
    // are all REQUIRED on the passwall schema and absent from the xray operator
    // config, so the first union member always rejects it.
    const xrayConfig = {
      schema: 1,
      instance: { name: "r" },
      process: {
        xrayBinary: "/usr/bin/xray",
        workDir: "/var/run/vectra-controller-pro",
        oomScoreAdj: -500,
        restartBackoff: { initialMs: 500, factor: 2, maxMs: 60_000 },
      },
      inbounds: {
        tproxy: {
          listenIP: "0.0.0.0",
          port: 12345,
          udpEnabled: true,
          sniffing: { enabled: true },
        },
      },
      geo: {
        assetDir: "/usr/share/v2ray",
        geoipUrl: "https://example.test/geoip.dat",
        geositeUrl: "https://example.test/geosite.dat",
        updateOnStart: false,
      },
    };

    expect(passwallDesiredConfigSchema.safeParse(xrayConfig).success).toBe(
      false,
    );
    expect(xrayDesiredConfigSchema.safeParse(xrayConfig).success).toBe(true);
  });
});
