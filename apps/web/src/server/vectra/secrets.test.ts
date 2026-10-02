import { createCipheriv, createHash, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";

import { describe, expect, it, vi } from "vitest";

import {
  MASKED_SECRET_PLACEHOLDER,
  passwallDesiredConfigSchema,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";
import { z } from "zod";

import { env, productionSafeStringSchema } from "~/env";

import {
  createSecretPayload,
  encryptJson,
  hydratePasswallConfig,
  hydrateXrayConfig,
  restoreMaskedPasswallConfig,
  restoreMaskedXrayConfig,
  sanitizePasswallConfig,
  sanitizePasswallRawSnapshot,
  stableStringify,
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

describe("secret blob compression", () => {
  // Ciphertext is incompressible, so TOAST could not shrink these blobs: the
  // table carried 954 MB of TOAST against 1160 kB of heap. Compressing before
  // encrypting is the only place the redundancy is still visible.
  it("writes v2 envelopes that compress the plaintext", () => {
    const envelope = JSON.parse(encryptJson({ config: baseConfig })) as {
      v: number;
      data: string;
    };

    expect(envelope.v).toBe(2);
  });

  it("shrinks repetitive payloads well below their plaintext size", () => {
    // A node list is highly repetitive — the same keys over and over — which is
    // exactly the shape gzip collapses and AES-GCM does not.
    const repetitive = {
      nodes: Array.from({ length: 200 }, (_, index) => ({
        id: `node-${index}`,
        label: `Node number ${index}`,
        protocol: "vless",
        address: "example.nfnpx.online",
        port: 50053,
        extras: { flow: "xtls-rprx-vision", security: "reality" },
      })),
    };
    const plaintextSize = JSON.stringify(repetitive).length;
    const envelopeSize = encryptJson(repetitive).length;

    expect(envelopeSize).toBeLessThan(plaintextSize / 3);
  });

  it("still decrypts legacy v1 blobs written before compression", () => {
    // Hand-built the way encryptJson used to write: no gzip, v: 1.
    const iv = randomBytes(12);
    const key = createHash("sha256").update(env.VECTRA_SECRETS_KEY).digest();
    const cipher = createCipheriv("aes-256-gcm", key, iv);
    const plaintext = Buffer.from(
      stableStringify({ config: baseConfig }),
      "utf8",
    );
    const data = Buffer.concat([cipher.update(plaintext), cipher.final()]);
    const legacy = JSON.stringify({
      v: 1,
      iv: iv.toString("base64url"),
      tag: cipher.getAuthTag().toString("base64url"),
      data: data.toString("base64url"),
    });

    const masked = sanitizePasswallConfig(baseConfig);
    expect(hydratePasswallConfig(masked, legacy)).toEqual(baseConfig);
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

// The xray-direct OPERATOR config. Since the "consume provider JSON" pivot it
// carries no outbounds/nodes/routing at all — the router fetches the provider's
// Xray document itself. The only credential left in it is the subscription URL
// (which embeds the per-router token) plus any auth headers sent with it.
const baseXrayConfig = xrayDesiredConfigSchema.parse({
  schema: 1,
  instance: { name: "test-router", logLevel: "info" },
  process: {
    xrayBinary: "/usr/bin/xray",
    workDir: "/var/run/vectra-controller-pro",
    oomScoreAdj: -500,
    memorySoftMiB: 80,
    restartBackoff: { initialMs: 500, factor: 2, maxMs: 60_000, reset: "60s" },
  },
  inbounds: {
    tproxy: {
      listenIP: "0.0.0.0",
      port: 12345,
      fwmark: 1,
      udpEnabled: true,
      tag: "tproxy-in",
      sniffing: {
        enabled: true,
        destOverride: ["http", "tls", "quic"],
        routeOnly: true,
      },
    },
  },
  subscriptions: [
    {
      id: "primary",
      remark: "BloopCat",
      url: "https://sub.example.com/api/sub/REAL_TOKEN",
      enabled: true,
      userAgent: "Happ/1.0",
      mode: "json",
      entryRemark: "⚡Extreme Польша 🇵🇱",
      headers: { Authorization: "Bearer real-bearer-token" },
    },
  ],
  geo: {
    assetDir: "/usr/share/v2ray",
    geoipUrl: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
    geositeUrl:
      "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
    updateOnStart: false,
  },
});

describe("sanitizeXrayConfig", () => {
  it("masks the subscription url and auth headers but keeps public geo URLs", () => {
    const sanitized = sanitizeXrayConfig(baseXrayConfig);

    const subscription = sanitized.subscriptions?.[0] as Record<
      string,
      unknown
    >;
    expect(subscription.url).toBe(MASKED_SECRET_PLACEHOLDER);
    expect(
      (subscription.headers as Record<string, unknown>).Authorization,
    ).toBe(MASKED_SECRET_PLACEHOLDER);

    // Non-secret subscription fields survive so the operator surface can still
    // show which feed and which provider profile the router is on.
    expect(subscription.remark).toBe("BloopCat");
    expect(subscription.entryRemark).toBe("⚡Extreme Польша 🇵🇱");
    expect(subscription.userAgent).toBe("Happ/1.0");
    expect(subscription.mode).toBe("json");

    // Public asset URLs under geo are never masked.
    expect(sanitized.geo.geoipUrl).toBe(
      "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
    );
    expect(sanitized.geo.geositeUrl).toBe(
      "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
    );
    expect(sanitized.geo.assetDir).toBe("/usr/share/v2ray");
  });

  it("leaves no real credential material anywhere in the masked config", () => {
    const maskedJson = JSON.stringify(sanitizeXrayConfig(baseXrayConfig));
    expect(maskedJson).not.toContain("REAL_TOKEN");
    expect(maskedJson).not.toContain("real-bearer-token");
  });

  it("produces a config that still parses through the xray schema", () => {
    expect(() =>
      xrayDesiredConfigSchema.parse(sanitizeXrayConfig(baseXrayConfig)),
    ).not.toThrow();
  });

  it("does not disturb the tproxy inbound, which carries no credential", () => {
    const sanitized = sanitizeXrayConfig(baseXrayConfig);
    expect(sanitized.inbounds.tproxy).toEqual(baseXrayConfig.inbounds.tproxy);
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

describe("restoreMaskedXrayConfig", () => {
  it("re-injects real secrets when an operator re-submits a previously-masked config", () => {
    // Simulate the latent save-path trap: an edit UI loads the MASKED config
    // and re-submits it (placeholders in place of every secret) with one
    // cosmetic edit. Without restore, placeholders would be persisted as real
    // secrets and the controller would fetch the subscription from the literal
    // string "__VECTRA_MASKED__".
    const masked = sanitizeXrayConfig(baseXrayConfig);
    const maskedSubscriptions = masked.subscriptions!;
    const resubmitted = xrayDesiredConfigSchema.parse({
      ...masked,
      subscriptions: [
        { ...maskedSubscriptions[0]!, entryRemark: "🇷🇺🇪🇺 Авто Самый стабильный" },
      ],
    });

    const restored = restoreMaskedXrayConfig(resubmitted, baseXrayConfig);
    const subscription = restored.subscriptions![0]!;

    // The cosmetic edit — here, switching provider profile — is preserved.
    expect(subscription.entryRemark).toBe("🇷🇺🇪🇺 Авто Самый стабильный");

    // Every masked secret is restored to its real prior value, matched by
    // subscription `id` through the shared restore walker.
    expect(subscription.url).toBe("https://sub.example.com/api/sub/REAL_TOKEN");
    expect(subscription.headers?.Authorization).toBe("Bearer real-bearer-token");

    // No placeholder survives once restored from the prior revision.
    expect(JSON.stringify(restored)).not.toContain(MASKED_SECRET_PLACEHOLDER);
  });

  it("returns the masked config unchanged when there is no source revision", () => {
    const masked = sanitizeXrayConfig(baseXrayConfig);
    expect(restoreMaskedXrayConfig(masked, null)).toEqual(masked);
  });
});
