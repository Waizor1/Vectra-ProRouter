import { readFileSync } from "node:fs";

import { afterEach, describe, expect, it, vi } from "vitest";

import { routerClaimPubkeySchema } from "./env";

// The router's claim test vector (read-only). Its server key is deliberately
// public: whoever has it can open every router QR sealed to it.
const claimVector = JSON.parse(
  readFileSync(
    new URL(
      "../../../router/vectra-controller-pro/ui/contract/claim-vector.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as { server: { publicKey: string } };

afterEach(() => {
  vi.unstubAllEnvs();
});

describe("VECTRA_ROUTER_CLAIM_PUBKEY", () => {
  it("refuses the public test key in production", () => {
    vi.stubEnv("NODE_ENV", "production");
    const parsed = routerClaimPubkeySchema.safeParse(claimVector.server.publicKey);
    expect(parsed.success).toBe(false);
    expect(parsed.error?.issues[0]?.message).toMatch(/public test key/);
  });

  it("still lets a dev panel or a stand use it", () => {
    vi.stubEnv("NODE_ENV", "development");
    expect(
      routerClaimPubkeySchema.safeParse(claimVector.server.publicKey).success,
    ).toBe(true);
  });

  it("takes a real key in production, and only a standard base64 32-byte one", () => {
    vi.stubEnv("NODE_ENV", "production");
    const realKey = Buffer.alloc(32, 0x5a).toString("base64");
    expect(routerClaimPubkeySchema.safeParse(realKey).success).toBe(true);
    expect(
      routerClaimPubkeySchema.safeParse(realKey.replace(/=$/, "")).success,
    ).toBe(false);
  });
});
