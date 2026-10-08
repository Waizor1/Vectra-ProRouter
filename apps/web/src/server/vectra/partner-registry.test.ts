import { afterEach, describe, expect, it, vi } from "vitest";
import {
  DEFAULT_PARTNER_ID,
  parsePartners,
  partnersFrom,
  scopeIdempotencyKey,
} from "./partner-registry";

// listPartners()/findPartner() read the validated env on every call; the mock
// is mutated in place per test.
const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);

function setEnv(values: Record<string, unknown>) {
  for (const key of Object.keys(envMock.env)) delete envMock.env[key];
  Object.assign(envMock.env, values);
}

/** A fresh module instance: the registry remembers what it already logged. */
async function freshRegistry() {
  vi.resetModules();
  return import("./partner-registry");
}

const LEGACY_SECRET = "legacy-partner-secret-0123456789abcdef";
const LEGACY_WEBHOOK = {
  url: "https://api-app.example/partner/prorouter/webhook",
  secret: "legacy-webhook-secret-0123456789abcdef",
};
const LEGACY_ENV = {
  VECTRA_PARTNER_SECRET: LEGACY_SECRET,
  VECTRA_CONNECT_WEBHOOK_URL: LEGACY_WEBHOOK.url,
  VECTRA_CONNECT_WEBHOOK_SECRET: LEGACY_WEBHOOK.secret,
};
const LEGACY_PARTNER = {
  id: DEFAULT_PARTNER_ID,
  brand: "vectra",
  label: "Vectra",
  secrets: [LEGACY_SECRET],
  webhook: LEGACY_WEBHOOK,
  claimKey: null,
  botUsername: null,
};

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

function messageOf(run: () => unknown) {
  try {
    run();
  } catch (error) {
    return (error as Error).message;
  }
  return "";
}

describe("partner registry", () => {
  it("keeps today's Vectra Connect as the default partner when nothing is listed", () => {
    const [vectra, ...rest] = partnersFrom(LEGACY_ENV);
    expect(rest).toEqual([]);
    expect(vectra).toEqual(LEGACY_PARTNER);
  });

  it("is just an unconfigured Vectra Connect when nothing is configured at all", () => {
    expect(partnersFrom({})).toEqual([
      {
        id: "vectra",
        brand: "vectra",
        label: "Vectra",
        secrets: [],
        webhook: null,
        claimKey: null,
        botUsername: null,
      },
    ]);
  });

  it("passes the legacy secret on byte for byte, as the partner API always read it", () => {
    const padded = "  padded-partner-secret-0123456789abcdef  ";
    expect(partnersFrom({ VECTRA_PARTNER_SECRET: padded })[0]?.secrets).toEqual([padded]);
    expect(partnersFrom({ VECTRA_PARTNER_SECRET: "" })[0]?.secrets).toEqual([]);
  });

  it("adds listed partners next to the default one", () => {
    const partners = partnersFrom({
      VECTRA_PARTNER_SECRET: LEGACY_SECRET,
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
      VECTRA_PARTNER_SECRET: LEGACY_SECRET,
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

  // `leaks` is the offending value of the row: it must not be echoed.
  it.each([
    { name: "not JSON", raw: '{"secrets":["not-json-SENTINEL', leaks: ["not-json-SENTINEL"] },
    {
      name: "a short secret",
      raw: JSON.stringify([{ ...BLOOP, secrets: ["short-SENTINEL"] }]),
      leaks: ["short-SENTINEL"],
    },
    {
      name: "a bad id",
      raw: JSON.stringify([{ ...BLOOP, id: "Bad Id SENTINEL" }]),
      leaks: ["Bad Id SENTINEL"],
    },
    {
      name: "a webhook url without its secret",
      raw: JSON.stringify([{ ...BLOOP, webhookSecret: undefined }]),
      leaks: [BLOOP.webhookUrl],
    },
    {
      name: "a plain-http webhook",
      raw: JSON.stringify([{ ...BLOOP, webhookUrl: "http://sentinel-host.example/hook-SENTINEL" }]),
      leaks: ["sentinel-host.example", "hook-SENTINEL"],
    },
    {
      name: "a repeated id",
      raw: JSON.stringify([BLOOP, { ...BLOOP, secrets: ["another-partner-secret-0123456789abcdef"] }]),
      leaks: ["another-partner-secret-0123456789abcdef"],
    },
    {
      name: "an unknown key",
      raw: JSON.stringify([{ ...BLOOP, secret: "unknown-key-SENTINEL" }]),
      leaks: ["unknown-key-SENTINEL"],
    },
    {
      name: "the same secret twice in one partner",
      raw: JSON.stringify([{ ...BLOOP, secrets: [BLOOP.secrets[0], BLOOP.secrets[0]] }]),
      leaks: [],
    },
    {
      name: "one secret shared by two partners",
      raw: JSON.stringify([BLOOP, { ...BLOOP, id: "other-one", brand: "other-one" }]),
      leaks: [],
    },
  ])("refuses $name, naming the variable and never a value", ({ raw, leaks }) => {
    const message = messageOf(() => parsePartners(raw));
    expect(message).toMatch(/^VECTRA_PARTNERS/);
    for (const value of [...leaks, BLOOP.secrets[0], BLOOP.webhookSecret]) {
      expect(message).not.toContain(value);
    }
  });

  it("names the partner when a secret is used twice, never the secret", () => {
    const twice = messageOf(() =>
      parsePartners(JSON.stringify([{ ...BLOOP, secrets: [BLOOP.secrets[0], BLOOP.secrets[0]] }])),
    );
    expect(twice).toMatch(/^VECTRA_PARTNERS/);
    expect(twice).toContain("bloopcat");

    const shared = messageOf(() =>
      parsePartners(JSON.stringify([BLOOP, { ...BLOOP, id: "other-one", brand: "other-one" }])),
    );
    expect(shared).toContain("bloopcat");
    expect(shared).toContain("other-one");
  });

  it("refuses a listed partner that reuses the legacy VECTRA_PARTNER_SECRET", () => {
    const message = messageOf(() =>
      partnersFrom({
        VECTRA_PARTNER_SECRET: BLOOP.secrets[0],
        VECTRA_PARTNERS: JSON.stringify([BLOOP]),
      }),
    );
    expect(message).toMatch(/^VECTRA_PARTNERS/);
    expect(message).toContain("VECTRA_PARTNER_SECRET");
    expect(message).toContain("bloopcat");
    expect(message).not.toContain(BLOOP.secrets[0]);
  });
});

describe("scopeIdempotencyKey", () => {
  it("leaves the default partner's keys as they were, and prefixes everyone else's", () => {
    expect(scopeIdempotencyKey("vectra", "k-1")).toBe("k-1");
    expect(scopeIdempotencyKey("vectra", "vc-claim-abc")).toBe("vc-claim-abc");
    expect(scopeIdempotencyKey("vectra", "rb12-x")).toBe("rb12-x");
    expect(scopeIdempotencyKey("bloopcat", "k-1")).toBe("bloopcat:k-1");
  });

  it("never lets a default-partner key land on another partner's scoped key", () => {
    expect(scopeIdempotencyKey("vectra", "bloopcat:k-1")).not.toBe(
      scopeIdempotencyKey("bloopcat", "k-1"),
    );
    expect(scopeIdempotencyKey("vectra", "bloopcat:k-1")).toBe("vectra:bloopcat:k-1");
    expect(scopeIdempotencyKey("vectra", "ab-c:d")).toBe("vectra:ab-c:d");
    expect(scopeIdempotencyKey("vectra", "vectra:x")).toBe("vectra:vectra:x");
    expect(scopeIdempotencyKey("vectra", "vectra:x")).not.toBe(scopeIdempotencyKey("bloopcat", "x"));
  });

  it("keeps a key whose prefix is too short to be any partner's id", () => {
    // Partner ids are 2-32 characters, so "a:b" can collide with nobody and
    // the key Vectra Connect may already have stored for it stays replayable.
    expect(scopeIdempotencyKey("vectra", "a:b")).toBe("a:b");
  });

  it("is injective across partners and keys", () => {
    const partners = ["vectra", "bloopcat", "other-one", "ab"];
    const keys = [
      "",
      "k-1",
      "vc-claim-abc",
      "rb12-x",
      "a:b",
      "ab:c",
      "x:",
      ":y",
      "Upper:case",
      "bloopcat:k-1",
      "vectra:x",
      "vectra:vectra:x",
      "other-one:bloopcat:k-1",
      "ab:ab:ab",
    ];
    const seen = new Map<string, string>();
    for (const partner of partners) {
      for (const key of keys) {
        const scoped = scopeIdempotencyKey(partner, key);
        const owner = `${partner} / ${JSON.stringify(key)}`;
        expect(seen.get(scoped), `${owner} collides with ${seen.get(scoped)}`).toBeUndefined();
        seen.set(scoped, owner);
      }
    }
  });
});

describe("listPartners / findPartner", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    setEnv({});
  });

  it("lists and finds the partners of the live env", async () => {
    setEnv({ ...LEGACY_ENV, VECTRA_PARTNERS: JSON.stringify([BLOOP]) });
    const { listPartners, findPartner } = await freshRegistry();
    expect(listPartners().map((p) => p.id)).toEqual(["vectra", "bloopcat"]);
    expect(findPartner("vectra")).toEqual(LEGACY_PARTNER);
    expect(findPartner("bloopcat")?.secrets).toEqual(BLOOP.secrets);
    expect(findPartner("nobody")).toBeNull();
  });

  it("is the unconfigured default partner alone when nothing is set", async () => {
    setEnv({});
    const { listPartners, findPartner } = await freshRegistry();
    expect(listPartners()).toEqual(partnersFrom({}));
    expect(findPartner("vectra")?.secrets).toEqual([]);
    expect(findPartner("bloopcat")).toBeNull();
  });

  // A bad VECTRA_PARTNERS must never take Vectra Connect down with it.
  it.each([
    { name: "invalid JSON", partners: '{"secrets":["not-json-SENTINEL', leak: "not-json-SENTINEL" },
    {
      name: "a short secret",
      partners: JSON.stringify([{ ...BLOOP, secrets: ["short-SENTINEL"] }]),
      leak: "short-SENTINEL",
    },
    {
      name: "a secret shared with the legacy variable",
      partners: JSON.stringify([{ ...BLOOP, secrets: [LEGACY_SECRET] }]),
      leak: LEGACY_SECRET,
    },
  ])("falls back to Vectra Connect alone on $name, and says so once", async ({ partners, leak }) => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    setEnv({ ...LEGACY_ENV, VECTRA_PARTNERS: partners });
    const { listPartners, findPartner } = await freshRegistry();

    expect(listPartners()).toEqual([LEGACY_PARTNER]);
    expect(findPartner("vectra")).toEqual(LEGACY_PARTNER);
    expect(findPartner("bloopcat")).toBeNull();

    expect(error).toHaveBeenCalledTimes(1);
    const text = error.mock.calls[0]?.map(String).join(" ") ?? "";
    expect(text).toContain("VECTRA_PARTNERS");
    expect(text).not.toContain(leak);
    expect(text).not.toContain(LEGACY_SECRET);
    expect(text).not.toContain(LEGACY_WEBHOOK.secret);
    expect(text).not.toContain(BLOOP.secrets[0]);
  });
});
