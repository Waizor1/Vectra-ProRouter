import { describe, expect, it, vi } from "vitest";

// The encrypt -> decrypt round-trip depends on VECTRA_SECRETS_KEY, which is
// intentionally unset in the shared test env (one of the known env-gated
// failures). Mock ~/env here so this file proves the real cryptographic
// round-trip without relying on, or perturbing, that shared key.
vi.mock("~/env", () => ({
  env: {
    VECTRA_SECRETS_KEY: "test-xray-secrets-key-0123456789",
  },
}));

const { MASKED_SECRET_PLACEHOLDER, xrayDesiredConfigSchema } = await import(
  "@vectra/contracts"
);
const { createXraySecretPayload, hydrateXrayConfig, sanitizeXrayConfig } =
  await import("./secrets");

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
      headers: { Authorization: "Bearer real-bearer-token" },
    },
  ],
  geo: {
    assetDir: "/usr/share/xray",
    geoipUrl: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
  },
});

describe("hydrateXrayConfig encrypt round-trip", () => {
  it("masks secrets at rest yet restores exact cleartext from the encrypted blob", () => {
    const masked = sanitizeXrayConfig(baseXrayConfig);
    const ciphertext = createXraySecretPayload(baseXrayConfig);

    // The masked at-rest config must not leak any real secret material.
    const maskedJson = JSON.stringify(masked);
    expect(maskedJson).not.toContain("11111111-2222-3333-4444-555555555555");
    expect(maskedJson).not.toContain("real-reality-public-key");
    expect(maskedJson).not.toContain("deadbeef");
    expect(maskedJson).not.toContain("REAL_TOKEN");
    expect(maskedJson).not.toContain("real-bearer-token");
    expect(maskedJson).not.toContain("socks-pass");
    expect(maskedJson).toContain(MASKED_SECRET_PLACEHOLDER);

    // Hydrating from the blob yields the exact original cleartext config that
    // the controller renders from.
    expect(hydrateXrayConfig(masked, ciphertext)).toEqual(baseXrayConfig);
  });
});
