import { createCipheriv, createHash, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";

import { describe, expect, it, vi } from "vitest";

import {
  MASKED_SECRET_PLACEHOLDER,
  passwallDesiredConfigSchema,
} from "@vectra/contracts";
import { z } from "zod";

import { env, productionSafeStringSchema } from "~/env";

import {
  createSecretPayload,
  encryptJson,
  hydratePasswallConfig,
  restoreMaskedPasswallConfig,
  sanitizePasswallConfig,
  sanitizePasswallRawSnapshot,
  stableStringify,
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
