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

// The operator config carries no outbounds since the "consume provider JSON"
// pivot. The subscription URL is the credential — it embeds the per-router
// token — along with any auth headers sent with the fetch.
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

describe("hydrateXrayConfig encrypt round-trip", () => {
  it("masks secrets at rest yet restores exact cleartext from the encrypted blob", () => {
    const masked = sanitizeXrayConfig(baseXrayConfig);
    const ciphertext = createXraySecretPayload(baseXrayConfig);

    // The masked at-rest config must not leak any real secret material.
    const maskedJson = JSON.stringify(masked);
    expect(maskedJson).not.toContain("REAL_TOKEN");
    expect(maskedJson).not.toContain("real-bearer-token");
    expect(maskedJson).toContain(MASKED_SECRET_PLACEHOLDER);

    // Public geo asset URLs are NOT credentials and must survive masking.
    expect(maskedJson).toContain("geoip.dat");

    // Hydrating from the blob yields the exact original cleartext config that
    // the controller renders from.
    expect(hydrateXrayConfig(masked, ciphertext)).toEqual(baseXrayConfig);
  });
});
