import { describe, expect, it } from "vitest";

import { MASKED_SECRET_PLACEHOLDER, passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  createSecretPayload,
  hydratePasswallConfig,
  restoreMaskedPasswallConfig,
  sanitizePasswallConfig,
  sanitizePasswallRawSnapshot,
} from "./secrets";

const baseConfig = passwallDesiredConfigSchema.parse({
  basicSettings: {
    main: {
      mainSwitch: true,
      selectedNodeId: "node-main",
      localhostProxy: true,
      clientProxy: true,
      nodeSocksPort: 1070,
      nodeSocksBindLocal: true,
      socksMainSwitch: false,
    },
    dns: {
      directQueryStrategy: "UseIP",
      remoteDnsProtocol: "tcp",
      remoteDns: "1.1.1.1",
      remoteDnsDoh: "https://1.1.1.1/dns-query",
      remoteDnsDetour: "remote",
      remoteFakeDns: false,
      remoteDnsQueryStrategy: "UseIPv4",
      dnsHosts: [],
      dnsRedirect: true,
    },
    log: {
      enableNodeLog: true,
      level: "warning",
    },
    maintenance: {
      backupPaths: ["/etc/config/passwall2"],
    },
    socks: [],
    shuntRules: [],
  },
  nodes: [
    {
      id: "node-main",
      label: "Main node",
      protocol: "xray",
      enabled: true,
      group: "default",
      username: "user",
      password: "secret-pass",
      tags: [],
      extras: {
        api_token: "hidden",
      },
    },
  ],
  subscriptions: {
    filterKeywordMode: "0",
    discardList: [],
    keepList: [],
    typePreferences: {},
    domainStrategy: "auto",
    items: [
      {
        id: "sub-1",
        remark: "Primary",
        url: "https://example.com/subscription",
        enabled: true,
        addMode: "2",
        metadata: {},
        extras: {},
      },
    ],
  },
  appUpdate: {
    binaryPaths: {
      xray: "/usr/bin/xray",
      singBox: "/usr/bin/sing-box",
      hysteria: "/usr/bin/hysteria",
      geoview: "/usr/bin/geoview",
    },
    updateStrategy: "package-preferred",
    targetVersions: {},
  },
  ruleManage: {
    geoipUrl: "https://example.com/geoip.dat",
    geositeUrl: "https://example.com/geosite.dat",
    assetDirectory: "/usr/share/v2ray/",
    autoUpdate: false,
    scheduleMode: "daily",
    enabledAssets: ["geoip", "geosite"],
    shuntRules: [],
  },
});

describe("sanitizePasswallConfig", () => {
  it("masks sensitive node and subscription fields", () => {
    const sanitized = sanitizePasswallConfig(baseConfig);

    expect(sanitized.nodes[0]?.username).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(sanitized.nodes[0]?.password).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(sanitized.nodes[0]?.extras.api_token).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(sanitized.subscriptions.items[0]?.url).toBe(MASKED_SECRET_PLACEHOLDER);
  });
});

describe("hydratePasswallConfig", () => {
  it("round-trips encrypted config payloads", () => {
    const masked = sanitizePasswallConfig(baseConfig);
    const ciphertext = createSecretPayload(baseConfig);

    expect(hydratePasswallConfig(masked, ciphertext)).toEqual(baseConfig);
  });
});

describe("restoreMaskedPasswallConfig", () => {
  it("preserves secrets when operator edits a masked config", () => {
    const masked = sanitizePasswallConfig(baseConfig);
    const edited = {
      ...masked,
      nodes: masked.nodes.map((node) => ({
        ...node,
        label: "Updated label",
      })),
    };

    const restored = restoreMaskedPasswallConfig(edited, baseConfig);

    expect(restored.nodes[0]?.label).toBe("Updated label");
    expect(restored.nodes[0]?.password).toBe("secret-pass");
    expect(restored.subscriptions.items[0]?.url).toBe(
      "https://example.com/subscription"
    );
  });
});

describe("sanitizePasswallRawSnapshot", () => {
  it("deeply masks sensitive keys and UCI secret lines in raw imported snapshots", () => {
    const sanitized = sanitizePasswallRawSnapshot({
      uciLines: [
        "passwall2.subscribe_list1.url='https://secret.example/sub'",
        "passwall2.global_rules1.geoip_url='https://public.example/geoip.dat'",
        "passwall2.node_1.uuid='super-secret-uuid'",
      ],
      sections: [
        {
          password: "secret",
          nested: {
            private_key: "hidden",
          },
          options: {
            url: ["https://secret.example/sub"],
            geoip_url: ["https://public.example/geoip.dat"],
            uuid: ["super-secret-uuid"],
          },
        },
      ],
    });

    expect(
      ((sanitized.sections as Array<{ password: string }>)[0]?.password)
    ).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (
        (sanitized.sections as Array<{ options: { url: string[] } }>)[0]?.options
          .url?.[0]
      )
    ).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (
        (sanitized.sections as Array<{ options: { geoip_url: string[] } }>)[0]
          ?.options.geoip_url?.[0]
      )
    ).toBe("https://public.example/geoip.dat");
    expect(
      ((sanitized.uciLines as string[])[0] ?? "").includes(
        MASKED_SECRET_PLACEHOLDER
      )
    ).toBe(true);
    expect((sanitized.uciLines as string[])[1]).toContain(
      "https://public.example/geoip.dat"
    );
    expect(
      ((sanitized.uciLines as string[])[2] ?? "").includes(
        MASKED_SECRET_PLACEHOLDER
      )
    ).toBe(true);
  });
});
