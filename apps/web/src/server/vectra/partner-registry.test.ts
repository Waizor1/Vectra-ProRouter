import { describe, expect, it, vi } from "vitest";
import {
  DEFAULT_PARTNER_ID,
  parsePartners,
  partnersFrom,
  scopeIdempotencyKey,
} from "./partner-registry";

vi.mock("~/env", () => ({ env: {} }));

const BLOOP = {
  id: "bloopcat",
  brand: "bloopcat",
  label: "BloopCat",
  secrets: ["bloopcat-partner-secret-0123456789abcdef"],
  webhookUrl: "https://bloopcat.example/partner/prorouter/webhook",
  webhookSecret: "bloopcat-webhook-secret-0123456789abcdef",
  claimKid: 2,
  // Buffer.alloc(32, 7).toString("base64") — a well-formed X25519 key for tests.
  claimPublicKey: "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=",
  botUsername: "BloopCat_bot",
};

describe("partner registry", () => {
  it("keeps today's Vectra Connect as the default partner when nothing is listed", () => {
    const [vectra, ...rest] = partnersFrom({
      VECTRA_PARTNER_SECRET: "legacy-partner-secret-0123456789abcdef",
      VECTRA_CONNECT_WEBHOOK_URL: "https://api-app.example/partner/prorouter/webhook",
      VECTRA_CONNECT_WEBHOOK_SECRET: "legacy-webhook-secret-0123456789abcdef",
    });
    expect(rest).toEqual([]);
    expect(vectra).toEqual({
      id: DEFAULT_PARTNER_ID,
      brand: "vectra",
      label: "Vectra",
      secrets: ["legacy-partner-secret-0123456789abcdef"],
      webhook: {
        url: "https://api-app.example/partner/prorouter/webhook",
        secret: "legacy-webhook-secret-0123456789abcdef",
      },
      claimKey: null,
      botUsername: null,
    });
  });

  it("adds listed partners next to the default one", () => {
    const partners = partnersFrom({
      VECTRA_PARTNER_SECRET: "legacy-partner-secret-0123456789abcdef",
      VECTRA_PARTNERS: JSON.stringify([BLOOP]),
    });
    expect(partners.map((p) => p.id)).toEqual(["vectra", "bloopcat"]);
    expect(partners[1]).toMatchObject({
      brand: "bloopcat",
      webhook: { url: BLOOP.webhookUrl, secret: BLOOP.webhookSecret },
      claimKey: { kid: 2, publicKey: BLOOP.claimPublicKey },
      botUsername: "BloopCat_bot",
    });
  });

  it("lets a listed 'vectra' entry replace the legacy variables", () => {
    const partners = partnersFrom({
      VECTRA_PARTNER_SECRET: "legacy-partner-secret-0123456789abcdef",
      VECTRA_PARTNERS: JSON.stringify([{ ...BLOOP, id: "vectra", brand: "vectra", label: "Vectra" }]),
    });
    expect(partners).toHaveLength(1);
    expect(partners[0]?.secrets).toEqual(BLOOP.secrets);
  });

  it("accepts two secrets during a rotation and no more", () => {
    const two = parsePartners(JSON.stringify([{ ...BLOOP, secrets: [BLOOP.secrets[0], "bloopcat-partner-secret-old-0123456789ab"] }]));
    expect(two[0]?.secrets).toHaveLength(2);
    expect(() =>
      parsePartners(JSON.stringify([{ ...BLOOP, secrets: ["a".repeat(32), "b".repeat(32), "c".repeat(32)] }])),
    ).toThrow(/VECTRA_PARTNERS/);
  });

  it.each([
    ["not JSON", "{"],
    ["a short secret", JSON.stringify([{ ...BLOOP, secrets: ["short"] }])],
    ["a bad id", JSON.stringify([{ ...BLOOP, id: "Bloop Cat" }])],
    ["a webhook url without its secret", JSON.stringify([{ ...BLOOP, webhookSecret: undefined }])],
    ["a plain-http webhook", JSON.stringify([{ ...BLOOP, webhookUrl: "http://bloopcat.example/hook" }])],
    ["a repeated id", JSON.stringify([BLOOP, BLOOP])],
    ["an unknown key", JSON.stringify([{ ...BLOOP, secret: "x" }])],
  ])("refuses %s, naming the variable and never a value", (_name, raw) => {
    let message = "";
    try {
      parsePartners(raw);
    } catch (error) {
      message = (error as Error).message;
    }
    expect(message).toMatch(/^VECTRA_PARTNERS/);
    expect(message).not.toContain(BLOOP.secrets[0]);
    expect(message).not.toContain(BLOOP.webhookSecret);
  });

  it("scopes a partner's idempotency key, and leaves the default partner's as it was", () => {
    expect(scopeIdempotencyKey("vectra", "k-1")).toBe("k-1");
    expect(scopeIdempotencyKey("bloopcat", "k-1")).toBe("bloopcat:k-1");
  });
});
