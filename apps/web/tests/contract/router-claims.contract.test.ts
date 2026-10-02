import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  partnerRouterClaimRequestSchema,
  partnerRouterUnbindRequestSchema,
  partnerWebhookPayloadSchema,
  routerCheckInRequestSchema,
  routerCheckInResponseSchema,
  routerRegisterRequestSchema,
  routerRegisterResponseSchema,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";

/**
 * ADR-0006 contract, panel side. The router half (vctl) implements the same
 * names: check-in `claim`, answers' `claimKey` / `botUsername` / `owner`, and
 * the operator config's `ui.lock`.
 */
const checkInFixture = JSON.parse(
  readFileSync(
    new URL(
      "../../../../router/vectra-controller-pro/testdata/contract/check-in-request.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as Record<string, unknown>;

const CODE_HASH =
  "287cc1f914dceb7bdec32a2bbb72632815b82bcb4fe25046c8f4fd8fd1247c1b";

function checkIn(claim: unknown) {
  const payload = structuredClone(checkInFixture);
  if (claim === undefined) {
    delete payload.claim;
  } else {
    payload.claim = claim;
  }
  return routerCheckInRequestSchema.safeParse(payload);
}

describe("check-in claim", () => {
  it("keeps the claim a router reports, lower-casing its hash", () => {
    const parsed = checkIn({
      codeHash: CODE_HASH.toUpperCase(),
      expiresAt: "2026-09-28T07:12:00Z",
    });
    expect(parsed.success && parsed.data.claim).toEqual({
      codeHash: CODE_HASH,
      expiresAt: "2026-09-28T07:12:00Z",
    });
  });

  it("accepts null or no claim (a configured router)", () => {
    expect(checkIn(null).success).toBe(true);
    expect(checkIn(undefined).success).toBe(true);
  });

  it("accepts an RFC 3339 expiry with an offset", () => {
    expect(
      checkIn({ codeHash: CODE_HASH, expiresAt: "2026-09-28T10:12:00+03:00" })
        .success,
    ).toBe(true);
  });

  it.each([
    ["a short hash", { codeHash: "abc", expiresAt: "2026-09-28T07:12:00Z" }],
    ["a non-hex hash", { codeHash: "z".repeat(64), expiresAt: "2026-09-28T07:12:00Z" }],
    ["no expiry", { codeHash: CODE_HASH }],
    ["a bare date", { codeHash: CODE_HASH, expiresAt: "2026-09-28" }],
  ])("rejects %s", (_name, claim) => {
    expect(checkIn(claim).success).toBe(false);
  });
});

describe("register / check-in answers", () => {
  const base = {
    protocolVersion: "2026-04-v1",
    routerId: "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31",
    status: "pending",
    pollingIntervalSeconds: 45,
    configSyncState: { importState: "awaiting_import" },
    rescuePolicy: {},
    updatePolicy: {},
    operatorMessage: null,
  };

  it("carries released: true, and only true", () => {
    const answer = {
      ...base,
      desiredRevision: null,
      jobs: [],
      owner: null,
    };
    expect(
      routerCheckInResponseSchema.parse({ ...answer, released: true }).released,
    ).toBe(true);
    expect(routerCheckInResponseSchema.parse(answer)).not.toHaveProperty(
      "released",
    );
    // Never a "released: false": the field is sent or it is absent.
    expect(
      routerCheckInResponseSchema.safeParse({ ...answer, released: false })
        .success,
    ).toBe(false);
    expect(
      routerRegisterResponseSchema.parse({
        ...base,
        issuedToken: "token",
        pendingApproval: true,
        owner: null,
        released: true,
      }).released,
    ).toBe(true);
  });

  it("carries claimKey, botUsername and owner (object or null)", () => {
    const claimFields = {
      claimKey: { kid: 1, publicKey: Buffer.alloc(32, 7).toString("base64") },
      botUsername: "VectraConnectBot",
    };
    const checkInAnswer = routerCheckInResponseSchema.parse({
      ...base,
      desiredRevision: null,
      jobs: [],
      ...claimFields,
      owner: { label: "iv***" },
    });
    expect(checkInAnswer).toMatchObject({
      ...claimFields,
      owner: { label: "iv***" },
    });

    const registerAnswer = routerRegisterResponseSchema.parse({
      ...base,
      issuedToken: "token",
      pendingApproval: true,
      owner: null,
    });
    expect(registerAnswer.owner).toBeNull();
    // Serialized, a null owner stays on the wire: vctl reads "absent" as no news.
    expect(JSON.parse(JSON.stringify(registerAnswer))).toHaveProperty("owner", null);
  });
});

describe("partner claim request", () => {
  // No userAgent: the router's own signed agent, the common case now that
  // the backend need not state a literal.
  const request = {
    code: "7KQ4M9XD",
    owner: { ref: "acct-42", label: "iv***" },
    subscription: {
      url: "https://sub.example.test/x",
    },
  };

  it.each([
    ["omitted", { url: "https://sub.example.test/x" }],
    ["null", { url: "https://sub.example.test/x", userAgent: null }],
  ])(
    "accepts a %s User-Agent as the router's own signed agent",
    (_name, subscription) => {
      const parsed = partnerRouterClaimRequestSchema.safeParse({
        ...request,
        subscription,
      });
      expect(parsed.success).toBe(true);
      if (parsed.success) {
        expect(parsed.data.subscription.userAgent ?? null).toBeNull();
      }
    },
  );

  it("still accepts a literal the backend states for a customer's subscription", () => {
    const parsed = partnerRouterClaimRequestSchema.safeParse({
      ...request,
      subscription: {
        url: "https://sub.example.test/x",
        userAgent: "v2rayNG/1.9.6",
      },
    });
    expect(parsed.success).toBe(true);
    expect(parsed.success && parsed.data.subscription.userAgent).toBe(
      "v2rayNG/1.9.6",
    );
  });

  it("refuses a blank User-Agent as ambiguous", () => {
    const parsed = partnerRouterClaimRequestSchema.safeParse({
      ...request,
      subscription: { url: "https://sub.example.test/x", userAgent: "" },
    });
    expect(parsed.success).toBe(false);
    expect(parsed.error?.issues[0]?.path).toEqual([
      "subscription",
      "userAgent",
    ]);
  });

  it.each([["VectraRouter/0.4.0 (AX3000T)"], ["vectrarouter/0.4.0"]])(
    "refuses a User-Agent claiming to be the router's own signed agent: %s",
    (userAgent) => {
      const parsed = partnerRouterClaimRequestSchema.safeParse({
        ...request,
        subscription: { url: "https://sub.example.test/x", userAgent },
      });
      expect(parsed.success).toBe(false);
      expect(parsed.error?.issues[0]?.path).toEqual([
        "subscription",
        "userAgent",
      ]);
      expect(parsed.error?.issues[0]?.message).toMatch(/VectraRouter/i);
    },
  );

  // As the Vectra backend reads them off the QR (claim-vector.json shapes).
  const device = {
    deviceIdentifier: "vectra-07bf0887f662",
    devicePublicKey: "42vuGfF/uCWnx9pgYHp2WC+BVp6k/seiTPZf2L/naRA=",
    nonce: "4TSeuVnbqURjg1OEDSy4JQ==",
  };

  it("takes exactly one of code and device", () => {
    expect(partnerRouterClaimRequestSchema.safeParse(request).success).toBe(true);
    expect(
      partnerRouterClaimRequestSchema.safeParse({
        ...request,
        code: null,
        device,
      }).success,
    ).toBe(true);
    expect(
      partnerRouterClaimRequestSchema.safeParse({
        ...request,
        device,
      }).success,
    ).toBe(false);
    expect(
      partnerRouterClaimRequestSchema.safeParse({ ...request, code: null }).success,
    ).toBe(false);
  });

  it.each([
    ["no nonce", { deviceIdentifier: device.deviceIdentifier, devicePublicKey: device.devicePublicKey }],
    ["a nonce that is not 16 bytes", { ...device, nonce: "4TSeuVnbqURjg1OEDSy4" }],
    ["a url-safe, unpadded nonce", { ...device, nonce: "4TSeuVnbqURjg1OEDSy4JQ" }],
    ["a key that is not 32 bytes", { ...device, devicePublicKey: "a2V5" }],
  ])("rejects a device claim with %s", (_name, badDevice) => {
    expect(
      partnerRouterClaimRequestSchema.safeParse({
        ...request,
        code: null,
        device: badDevice,
      }).success,
    ).toBe(false);
  });

  it.each([
    ["an unknown key", { ...request, addon: "router" }],
    ["an email as owner ref", { ...request, owner: { ref: "ivan@example.com", label: "x" } }],
    ["a two-line label", { ...request, owner: { ref: "acct-42", label: "a\nb" } }],
    ["a label longer than the router shows", { ...request, owner: { ref: "acct-42", label: "x".repeat(65) } }],
    ["a User-Agent with a line break", { ...request, subscription: { url: "https://x", userAgent: "a\r\nX-Evil: 1" } }],
  ])("rejects %s", (_name, body) => {
    expect(partnerRouterClaimRequestSchema.safeParse(body).success).toBe(false);
  });

  it("binds an unbind to its router and optionally its owner", () => {
    expect(
      partnerRouterUnbindRequestSchema.safeParse({
        routerId: "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31",
        ownerRef: "acct-42",
      }).success,
    ).toBe(true);
    expect(partnerRouterUnbindRequestSchema.safeParse({}).success).toBe(false);
  });

  it("shapes webhooks as {event, routerId, ownerRef, at, detail?}", () => {
    expect(
      partnerWebhookPayloadSchema.safeParse({
        event: "router.ready",
        routerId: "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31",
        ownerRef: "acct-42",
        at: "2026-09-28T10:00:00.000Z",
      }).success,
    ).toBe(true);
    expect(
      partnerWebhookPayloadSchema.safeParse({
        event: "router.unbound",
        routerId: "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31",
        ownerRef: "acct-42",
        at: "2026-09-28T10:00:00.000Z",
      }).success,
    ).toBe(false);
  });
});

describe("operator config ui.lock", () => {
  const config = {
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

  it("accepts ui.lock and nothing else under ui (vctl decodes strictly)", () => {
    expect(
      xrayDesiredConfigSchema.safeParse({ ...config, ui: { lock: true } }).success,
    ).toBe(true);
    expect(xrayDesiredConfigSchema.safeParse(config).success).toBe(true);
    expect(
      xrayDesiredConfigSchema.safeParse({
        ...config,
        ui: { lock: true, theme: "dark" },
      }).success,
    ).toBe(false);
  });
});

describe("register proof", () => {
  const registration = {
    protocolVersion: "2026-04-v1",
    inventory: checkInFixture.inventory,
  };

  it("carries vctl's proof as sent: unix seconds and a standard base64 signature", () => {
    const proof = {
      timestamp: 1790579520,
      signature: Buffer.alloc(64, 7).toString("base64"),
    };
    expect(
      routerRegisterRequestSchema.parse({ ...registration, proof }).proof,
    ).toEqual(proof);
  });

  it("is optional, and a malformed one counts as none instead of failing registration", () => {
    expect(routerRegisterRequestSchema.parse(registration).proof).toBeUndefined();
    expect(
      routerRegisterRequestSchema.parse({
        ...registration,
        proof: { timestamp: "now", signature: 1 },
      }).proof,
    ).toBeNull();
  });
});
