import { readFileSync } from "node:fs";

import { describe, expect, it, vi } from "vitest";

import {
  MASKED_SECRET_PLACEHOLDER,
  passwallDesiredConfigSchema,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";
import { z } from "zod";

import { productionSafeStringSchema } from "~/env";

import {
  createSecretPayload,
  hydratePasswallConfig,
  hydrateXrayConfig,
  restoreMaskedPasswallConfig,
  sanitizePasswallConfig,
  sanitizePasswallRawSnapshot,
  sanitizeXrayConfig,
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
    expect(sanitized.nodes[0]?.extras.api_token).toBe(
      MASKED_SECRET_PLACEHOLDER,
    );
    expect(sanitized.subscriptions.items[0]?.url).toBe(
      MASKED_SECRET_PLACEHOLDER,
    );
  });
});

describe("productionSafeStringSchema", () => {
  it("rejects placeholder secrets in production", () => {
    vi.stubEnv("NODE_ENV", "production");
    try {
      const schema = productionSafeStringSchema(
        z.string().min(1),
        ["change-me"],
        "VECTRA_OPERATOR_PASSWORD",
      );

      expect(schema.safeParse("change-me").success).toBe(false);
      expect(schema.safeParse("real-production-secret").success).toBe(true);
    } finally {
      vi.unstubAllEnvs();
    }
  });
});

describe("web runtime image env validation", () => {
  it("does not persist SKIP_ENV_VALIDATION into the runtime container", () => {
    const dockerfile = readFileSync(
      new URL("../../../../../Dockerfile.web", import.meta.url),
      "utf8",
    );

    expect(dockerfile).toContain("RUN SKIP_ENV_VALIDATION=1");
    expect(dockerfile).not.toMatch(/^ENV\s+SKIP_ENV_VALIDATION=/m);
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
      "https://example.com/subscription",
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
      (sanitized.sections as Array<{ password: string }>)[0]?.password,
    ).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (sanitized.sections as Array<{ options: { url: string[] } }>)[0]?.options
        .url?.[0],
    ).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (sanitized.sections as Array<{ options: { geoip_url: string[] } }>)[0]
        ?.options.geoip_url?.[0],
    ).toBe("https://public.example/geoip.dat");
    expect(
      ((sanitized.uciLines as string[])[0] ?? "").includes(
        MASKED_SECRET_PLACEHOLDER,
      ),
    ).toBe(true);
    expect((sanitized.uciLines as string[])[1]).toContain(
      "https://public.example/geoip.dat",
    );
    expect(
      ((sanitized.uciLines as string[])[2] ?? "").includes(
        MASKED_SECRET_PLACEHOLDER,
      ),
    ).toBe(true);
  });
});

const baseXrayConfig = xrayDesiredConfigSchema.parse({
  schema: 1,
  instance: { name: "test-router", logLevel: "info" },
  process: { xrayBinary: "/usr/bin/xray", workDir: "./work" },
  inbounds: {
    tproxy: { listenIP: "0.0.0.0", port: 12345 },
    socks: {
      listenIP: "127.0.0.1",
      port: 1080,
      username: "socks-user",
      password: "socks-pass",
    },
    realityInbound: {
      listenIP: "0.0.0.0",
      port: 8443,
      protocol: "vless",
      settings: {
        privateKey: "reality-inbound-private-key",
        shortIds: ["abcd"],
      },
    },
  },
  nodes: [
    {
      id: "world-de",
      remark: "WorldProxy DE",
      enabled: true,
      outbound: {
        protocol: "vless",
        server: "de1.example.online",
        port: 443,
        settings: {
          vless: {
            uuid: "11111111-2222-3333-4444-555555555555",
            flow: "xtls-rprx-vision",
            encryption: "none",
          },
        },
        stream: {
          transport: "tcp",
          security: "reality",
          reality: {
            serverName: "www.cloudflare.com",
            publicKey: "real-reality-public-key",
            shortId: "deadbeef",
            fingerprint: "firefox",
          },
        },
      },
    },
    {
      id: "world-trojan",
      remark: "Trojan node",
      enabled: true,
      outbound: {
        protocol: "trojan",
        server: "tj.example.online",
        port: 443,
        settings: { trojan: { password: "trojan-secret-password" } },
      },
    },
  ],
  routing: {
    domainStrategy: "IPIfNonMatch",
    rules: [{ tag: "world-proxy", outboundTag: "node-world-de" }],
  },
  subscriptions: [
    {
      id: "primary",
      remark: "BloopCat",
      url: "https://sub.example.com/api/sub/REAL_TOKEN",
      enabled: true,
      userAgent: "passwall2",
      headers: { Authorization: "Bearer real-bearer-token" },
    },
  ],
  geo: {
    assetDir: "/usr/share/xray",
    geoipUrl: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
    geositeUrl:
      "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
  },
});

describe("sanitizeXrayConfig", () => {
  it("masks node secrets (vless uuid, reality publicKey/shortId, trojan password)", () => {
    const sanitized = sanitizeXrayConfig(baseXrayConfig);

    const firstNode = sanitized.nodes[0] as Record<string, unknown>;
    const firstOutbound = firstNode.outbound as Record<string, unknown>;
    const vlessSettings = (firstOutbound.settings as Record<string, unknown>)
      .vless as Record<string, unknown>;
    const reality = (firstOutbound.stream as Record<string, unknown>)
      .reality as Record<string, unknown>;

    expect(vlessSettings.uuid).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(reality.publicKey).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(reality.shortId).toBe(MASKED_SECRET_PLACEHOLDER);
    // Non-secret node fields stay intact for the operator UI.
    expect(firstOutbound.server).toBe("de1.example.online");
    expect(reality.serverName).toBe("www.cloudflare.com");
    expect(reality.fingerprint).toBe("firefox");

    const secondNode = sanitized.nodes[1] as Record<string, unknown>;
    const trojan = (
      (secondNode.outbound as Record<string, unknown>).settings as Record<
        string,
        unknown
      >
    ).trojan as Record<string, unknown>;
    expect(trojan.password).toBe(MASKED_SECRET_PLACEHOLDER);
  });

  it("masks subscription url + headers and inbound credentials but keeps public geo URLs", () => {
    const sanitized = sanitizeXrayConfig(baseXrayConfig);

    const subscription = sanitized.subscriptions?.[0] as Record<
      string,
      unknown
    >;
    expect(subscription.url).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (subscription.headers as Record<string, unknown>).Authorization,
    ).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(subscription.remark).toBe("BloopCat");

    const socks = sanitized.inbounds?.socks as Record<string, unknown>;
    expect(socks.username).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(socks.password).toBe(MASKED_SECRET_PLACEHOLDER);
    const realityInbound = sanitized.inbounds?.realityInbound as Record<
      string,
      unknown
    >;
    expect(
      (realityInbound.settings as Record<string, unknown>).privateKey,
    ).toBe(MASKED_SECRET_PLACEHOLDER);

    // Public asset URLs under geo are never masked.
    expect(sanitized.geo?.geoipUrl).toBe(
      "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
    );
    expect(sanitized.geo?.geositeUrl).toBe(
      "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
    );
  });

  it("produces a config that still parses through the xray schema", () => {
    expect(() =>
      xrayDesiredConfigSchema.parse(sanitizeXrayConfig(baseXrayConfig)),
    ).not.toThrow();
  });
});

describe("hydrateXrayConfig", () => {
  it("returns the stored (masked) config unchanged when no ciphertext is present", () => {
    const masked = sanitizeXrayConfig(baseXrayConfig);
    expect(hydrateXrayConfig(masked, null)).toEqual(masked);
  });

  // The full encrypt -> hydrate round-trip (which depends on VECTRA_SECRETS_KEY)
  // lives in secrets.xray-roundtrip.test.ts, where ~/env is mocked so it does
  // not depend on the shared, intentionally-unset secrets key.
});
