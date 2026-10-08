import {
  createHash,
  createHmac,
  createPrivateKey,
  generateKeyPairSync,
  type KeyObject,
  sign,
} from "node:crypto";
import { readFileSync } from "node:fs";

import { routerCredentials } from "@vectra/db";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  buildRouterClaimResponseFields,
  checkRegisterProof,
  claimCodeFromNonce,
  decodeClaimNonce,
  devicePublicKeysMatch,
  evaluatePreclaimAdoptionWithDb,
  hashClaimCode,
  isReleasedAwaitingOwner,
  liveClaimCodeHashes,
  normalizeClaimCode,
  registerProofMessage,
  resolveCheckInClaimColumns,
  ROUTER_CLAIM_PUBLIC_TEST_KEY,
  sealClaimCodeHash,
} from "./router-claim-state";
import { findPartner } from "./partner-registry";
import { createFakeDb } from "./testing/fake-db";

// The router's own claim test vector (read-only): the panel must derive
// exactly the code and hash vctl derives from the same nonce.
const claimVector = JSON.parse(
  readFileSync(
    new URL(
      "../../../../../router/vectra-controller-pro/ui/contract/claim-vector.json",
      import.meta.url,
    ),
    "utf8",
  ),
) as {
  server: { publicKey: string };
  device: { seed: string; publicKey: string; deviceIdentifier: string };
  nonce: string;
  code: string;
  codeHash: string;
};

/** A real ed25519 device key: its raw public half as vctl sends it. */
function deviceKeyPair() {
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const raw = publicKey.export({ format: "der", type: "spki" }).subarray(12);
  return { privateKey, publicKey: raw.toString("base64") };
}

function proofFor(
  privateKey: KeyObject,
  deviceIdentifier: string,
  timestamp: number,
) {
  return {
    timestamp,
    signature: sign(
      null,
      registerProofMessage(deviceIdentifier, timestamp),
      privateKey,
    ).toString("base64"),
  };
}

const envMock = vi.hoisted(() => ({ env: {} as Record<string, unknown> }));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", () => ({ db: {} }));
// The real registry, with findPartner spy-able: the brand guard below needs a
// partner whose brand the registry itself would never have accepted.
vi.mock("./partner-registry", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./partner-registry")>();
  return { ...actual, findPartner: vi.fn(actual.findPartner) };
});

const SECRETS_KEY = "router-claim-state-test-key-0123456789";
const CLAIM_PUBKEY = Buffer.alloc(32, 7).toString("base64");
// A real ed25519 public key as vctl sends it: standard, padded base64.
const DEVICE_KEY = Buffer.alloc(32, 9).toString("base64");

const BLOOP_CLAIM_PUBKEY = Buffer.alloc(32, 7).toString("base64");

beforeEach(() => {
  envMock.env = {
    VECTRA_SECRETS_KEY: SECRETS_KEY,
    VECTRA_PARTNERS: JSON.stringify([
      {
        id: "bloopcat",
        brand: "bloopcat",
        label: "BloopCat",
        secrets: ["bloopcat-partner-secret-0123456789abcdef"],
        claimKid: 2,
        claimPublicKey: BLOOP_CLAIM_PUBKEY,
        botUsername: "BloopCat_bot",
      },
    ]),
  };
});

describe("claim codes", () => {
  it("normalizes what a person types into the router's canonical code", () => {
    expect(normalizeClaimCode("7KQ4M9XD")).toBe("7KQ4M9XD");
    expect(normalizeClaimCode(" 7kq4-m9xd ")).toBe("7KQ4M9XD");
    expect(normalizeClaimCode("7KQ4 M9XD")).toBe("7KQ4M9XD");
    // Crockford read-alikes: O is 0, I and L are 1 (never valid themselves).
    expect(normalizeClaimCode("7KQ4M9XO")).toBe("7KQ4M9X0");
    expect(normalizeClaimCode("iL345678")).toBe("11345678");
  });

  it("rejects anything that cannot be a code", () => {
    expect(normalizeClaimCode("7KQ4M9X")).toBeNull();
    expect(normalizeClaimCode("7KQ4M9XDD")).toBeNull();
    // U is not in Crockford's alphabet.
    expect(normalizeClaimCode("7KQ4M9XU")).toBeNull();
    expect(normalizeClaimCode(null)).toBeNull();
  });

  it("hashes a code exactly as vctl does", () => {
    expect(hashClaimCode("7KQ4M9XD")).toBe(
      createHash("sha256")
        .update("vectra-claim-codehash/v1:7KQ4M9XD")
        .digest("hex"),
    );
  });

  it("stores a reported digest keyed, never as the reversible sha256", () => {
    const reported = hashClaimCode("7KQ4M9XD");
    const sealed = sealClaimCodeHash(reported);

    expect(sealed).not.toBe(reported);
    expect(sealed).toBe(
      createHmac("sha256", `vectra-claim-code-seal/v1:${SECRETS_KEY}`)
        .update(reported)
        .digest("hex"),
    );
    expect(sealClaimCodeHash(reported.toUpperCase())).toBe(sealed);
  });
});

describe("claim codes from the QR nonce", () => {
  it("derives the router's code and hash from its nonce (claim-vector.json)", () => {
    const nonce = decodeClaimNonce(claimVector.nonce);
    expect(nonce?.length).toBe(16);
    const code = claimCodeFromNonce(nonce!);
    expect(code).toBe(claimVector.code);
    expect(hashClaimCode(code)).toBe(claimVector.codeHash);
  });

  it("refuses a nonce that is not 16 bytes", () => {
    expect(decodeClaimNonce(Buffer.alloc(15).toString("base64"))).toBeNull();
    expect(decodeClaimNonce("not base64!")).toBeNull();
  });
});

describe("resolveCheckInClaimColumns", () => {
  const NOW = new Date("2026-09-28T10:00:00.000Z");
  const A = hashClaimCode("7KQ4M9XD");
  const B = hashClaimCode("HJKMNPQR");
  const A_EXPIRES = new Date("2026-09-28T10:02:00.000Z");
  const B_EXPIRES = new Date("2026-09-28T10:12:00.000Z");
  const none = {
    claimCodeHash: null,
    claimExpiresAt: null,
    previousClaimCodeHash: null,
    previousClaimExpiresAt: null,
  };

  it("stores the first claim a router reports", () => {
    expect(
      resolveCheckInClaimColumns(
        none,
        { codeHash: A, expiresAt: A_EXPIRES.toISOString() },
        NOW,
      ),
    ).toEqual({
      ...none,
      claimCodeHash: sealClaimCodeHash(A),
      claimExpiresAt: A_EXPIRES,
    });
  });

  it("keeps the code a new one replaced, with its own expiry (the grace)", () => {
    const showingA = {
      ...none,
      claimCodeHash: sealClaimCodeHash(A),
      claimExpiresAt: A_EXPIRES,
    };
    expect(
      resolveCheckInClaimColumns(
        showingA,
        { codeHash: B, expiresAt: B_EXPIRES.toISOString() },
        NOW,
      ),
    ).toEqual({
      claimCodeHash: sealClaimCodeHash(B),
      claimExpiresAt: B_EXPIRES,
      previousClaimCodeHash: sealClaimCodeHash(A),
      previousClaimExpiresAt: A_EXPIRES,
    });
  });

  it("keeps the previous code across check-ins of the same new one, until it lapses", () => {
    const showingB = {
      claimCodeHash: sealClaimCodeHash(B),
      claimExpiresAt: B_EXPIRES,
      previousClaimCodeHash: sealClaimCodeHash(A),
      previousClaimExpiresAt: A_EXPIRES,
    };
    const again = { codeHash: B, expiresAt: B_EXPIRES.toISOString() };
    expect(resolveCheckInClaimColumns(showingB, again, NOW)).toEqual(showingB);
    // After A's own expiry it is dropped.
    expect(
      resolveCheckInClaimColumns(
        showingB,
        again,
        new Date("2026-09-28T10:02:01.000Z"),
      ),
    ).toEqual({
      ...showingB,
      previousClaimCodeHash: null,
      previousClaimExpiresAt: null,
    });
  });

  it("does not keep a replaced code that had already lapsed", () => {
    expect(
      resolveCheckInClaimColumns(
        {
          ...none,
          claimCodeHash: sealClaimCodeHash(A),
          claimExpiresAt: new Date("2026-09-28T09:59:00.000Z"),
        },
        { codeHash: B, expiresAt: B_EXPIRES.toISOString() },
        NOW,
      ),
    ).toMatchObject({ previousClaimCodeHash: null, previousClaimExpiresAt: null });
  });

  it("clears both once the router stops reporting a code", () => {
    const showingB = {
      claimCodeHash: sealClaimCodeHash(B),
      claimExpiresAt: B_EXPIRES,
      previousClaimCodeHash: sealClaimCodeHash(A),
      previousClaimExpiresAt: A_EXPIRES,
    };
    expect(resolveCheckInClaimColumns(showingB, null, NOW)).toEqual(none);
    expect(resolveCheckInClaimColumns(showingB, undefined, NOW)).toEqual(none);
  });

  it("lists as live exactly the unexpired codes", () => {
    const showingB = {
      claimCodeHash: sealClaimCodeHash(B),
      claimExpiresAt: B_EXPIRES,
      previousClaimCodeHash: sealClaimCodeHash(A),
      previousClaimExpiresAt: A_EXPIRES,
    };
    expect(liveClaimCodeHashes(showingB, NOW)).toEqual([
      sealClaimCodeHash(B),
      sealClaimCodeHash(A),
    ]);
    expect(
      liveClaimCodeHashes(showingB, new Date("2026-09-28T10:05:00.000Z")),
    ).toEqual([sealClaimCodeHash(B)]);
    expect(
      liveClaimCodeHashes(showingB, new Date("2026-09-28T10:13:00.000Z")),
    ).toEqual([]);
  });
});

describe("checkRegisterProof", () => {
  const NOW_MS = Date.UTC(2026, 8, 28, 10, 0, 0);
  const NOW_S = NOW_MS / 1000;
  const DEVICE = "vectra-07bf0887f662";

  it("verifies a proof by the vector's device key against its published public key", () => {
    // claim-vector.json's device: its ed25519 seed signs, and the panel checks
    // against the public key exactly as vctl encodes it (standard base64).
    const vectorKey = createPrivateKey({
      key: Buffer.concat([
        Buffer.from("302e020100300506032b657004220420", "hex"),
        Buffer.from(claimVector.device.seed, "base64"),
      ]),
      format: "der",
      type: "pkcs8",
    });
    const deviceIdentifier = claimVector.device.deviceIdentifier;
    expect(registerProofMessage(deviceIdentifier, NOW_S).toString()).toBe(
      `vectra-register/v1\n${deviceIdentifier}\n${NOW_S}`,
    );
    expect(
      checkRegisterProof({
        deviceIdentifier,
        publicKey: claimVector.device.publicKey,
        proof: proofFor(vectorKey, deviceIdentifier, NOW_S),
        nowMs: NOW_MS,
      }),
    ).toBe("valid");
  });

  it("refuses no proof, a stale one, another key's, and one over another device", () => {
    const { privateKey, publicKey } = deviceKeyPair();
    const other = deviceKeyPair();
    const check = (proof: ReturnType<typeof proofFor> | null) =>
      checkRegisterProof({ deviceIdentifier: DEVICE, publicKey, proof, nowMs: NOW_MS });

    expect(check(null)).toBe("missing");
    expect(check(proofFor(privateKey, DEVICE, NOW_S - 301))).toBe("stale");
    expect(check(proofFor(privateKey, DEVICE, NOW_S + 301))).toBe("stale");
    expect(check(proofFor(privateKey, DEVICE, NOW_S - 300))).toBe("valid");
    expect(check(proofFor(other.privateKey, DEVICE, NOW_S))).toBe("bad_signature");
    expect(check(proofFor(privateKey, "vectra-other", NOW_S))).toBe("bad_signature");
    expect(check({ timestamp: NOW_S, signature: "AAAA" })).toBe("bad_signature");
    expect(
      checkRegisterProof({
        deviceIdentifier: DEVICE,
        publicKey: "c2hvcnQ=",
        proof: proofFor(privateKey, DEVICE, NOW_S),
        nowMs: NOW_MS,
      }),
    ).toBe("bad_key");
  });
});

describe("devicePublicKeysMatch", () => {
  it("matches the same key however it is base64-encoded", () => {
    const urlSafeUnpadded = DEVICE_KEY.replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
    expect(devicePublicKeysMatch(DEVICE_KEY, DEVICE_KEY)).toBe(true);
    expect(devicePublicKeysMatch(DEVICE_KEY, urlSafeUnpadded)).toBe(true);
  });

  it("does not match another key or nothing", () => {
    expect(
      devicePublicKeysMatch(DEVICE_KEY, Buffer.alloc(32, 8).toString("base64")),
    ).toBe(false);
    expect(devicePublicKeysMatch(DEVICE_KEY, null)).toBe(false);
    expect(devicePublicKeysMatch(null, DEVICE_KEY)).toBe(false);
  });
});

describe("buildRouterClaimResponseFields", () => {
  it("sends an explicit null owner for a router nobody owns", () => {
    // vctl reads an ABSENT owner as "no news": only null un-links it.
    expect(
      buildRouterClaimResponseFields({ ownerRef: null, ownerLabel: null, releasedAt: null, partnerId: null }),
    ).toEqual({ owner: null });
  });

  it("sends the masked label once the router is claimed", () => {
    expect(
      buildRouterClaimResponseFields({
        ownerRef: "acct-42",
        ownerLabel: "iv***@m***",
        releasedAt: null,
        partnerId: null,
      }),
    ).toEqual({
      brand: "vectra",
      owner: { ownerRef: "acct-42", label: "iv***@m***" },
    });
  });

  it("adds the claim key and bot username when configured", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = CLAIM_PUBKEY;
    // A raw string when env validation is skipped, a number otherwise.
    envMock.env.VECTRA_ROUTER_CLAIM_KID = "3";
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "@VectraConnectBot";

    expect(
      buildRouterClaimResponseFields({ ownerRef: null, ownerLabel: null, releasedAt: null, partnerId: null }),
    ).toEqual({
      claimKey: { kid: 3, publicKey: CLAIM_PUBKEY },
      botUsername: "VectraConnectBot",
      owner: null,
    });
  });

  it("omits a claim key vctl could not use", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = CLAIM_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 256;
    expect(
      buildRouterClaimResponseFields({ ownerRef: null, ownerLabel: null, releasedAt: null, partnerId: null }),
    ).not.toHaveProperty("claimKey");

    envMock.env.VECTRA_ROUTER_CLAIM_KID = undefined;
    expect(
      buildRouterClaimResponseFields({ ownerRef: null, ownerLabel: null, releasedAt: null, partnerId: null }),
    ).not.toHaveProperty("claimKey");
  });
});

describe("the released signal", () => {
  const RELEASED_AT = new Date("2026-09-28T11:00:00.000Z");

  it("marks only a router that was unlinked and not claimed since", () => {
    expect(
      isReleasedAwaitingOwner({ releasedAt: RELEASED_AT, ownerRef: null }),
    ).toBe(true);
    // Claimed again: the owner wins even if a stale release were left behind.
    expect(
      isReleasedAwaitingOwner({ releasedAt: RELEASED_AT, ownerRef: "acct-7" }),
    ).toBe(false);
    // An ordinary fleet router has never been released.
    expect(isReleasedAwaitingOwner({ releasedAt: null, ownerRef: null })).toBe(
      false,
    );
  });

  it("answers released: true for a released router", () => {
    expect(
      buildRouterClaimResponseFields({
        ownerRef: null,
        ownerLabel: null,
        releasedAt: RELEASED_AT,
        partnerId: null,
      }),
    ).toEqual({ owner: null, released: true });
  });

  it("omits the field for a fleet router and for a claimed one", () => {
    // `owner: null` is what every fleet router gets; it must never come with
    // a release the router would act on.
    const fleet = buildRouterClaimResponseFields({
      ownerRef: null,
      ownerLabel: null,
      releasedAt: null,
      partnerId: null,
    });
    expect(fleet).not.toHaveProperty("released");
    expect(JSON.parse(JSON.stringify(fleet))).toEqual({ owner: null });

    const claimed = buildRouterClaimResponseFields({
      ownerRef: "acct-7",
      ownerLabel: "an***",
      releasedAt: RELEASED_AT,
      partnerId: null,
    });
    expect(claimed).not.toHaveProperty("released");
  });
});

describe("evaluatePreclaimAdoptionWithDb", () => {
  const NOW_MS = Date.UTC(2026, 8, 28, 10, 0, 0);
  const NOW_S = NOW_MS / 1000;
  const neverSeen = {
    id: "router-1",
    lastSeenAt: null,
    deviceIdentifier: "vectra-cccccccccccc",
  };
  const device = deviceKeyPair();
  const goodProof = () =>
    proofFor(device.privateKey, neverSeen.deviceIdentifier, NOW_S);

  function withCredentials(rows: Array<Record<string, unknown>>) {
    return createFakeDb({ selects: [[routerCredentials, [rows]]] });
  }
  const reserved = [
    { type: "bootstrap", devicePublicKey: device.publicKey, revokedAt: null },
  ];

  it("adopts a never-seen record for a router that proves it holds the key", async () => {
    const fake = withCredentials(reserved);
    expect(
      await evaluatePreclaimAdoptionWithDb(
        fake.db as never,
        neverSeen,
        { devicePublicKey: device.publicKey, proof: goodProof() },
        NOW_MS,
      ),
    ).toEqual({ adopt: true });
  });

  it("refuses the key alone — it is public (in the QR and every inventory)", async () => {
    const fake = withCredentials(reserved);
    expect(
      await evaluatePreclaimAdoptionWithDb(
        fake.db as never,
        neverSeen,
        { devicePublicKey: device.publicKey, proof: null },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "proof_missing" });
  });

  it("refuses a stale proof and one signed by another key", async () => {
    const other = deviceKeyPair();
    expect(
      await evaluatePreclaimAdoptionWithDb(
        withCredentials(reserved).db as never,
        neverSeen,
        {
          devicePublicKey: device.publicKey,
          proof: proofFor(device.privateKey, neverSeen.deviceIdentifier, NOW_S - 301),
        },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "proof_stale" });
    expect(
      await evaluatePreclaimAdoptionWithDb(
        withCredentials(reserved).db as never,
        neverSeen,
        {
          devicePublicKey: device.publicKey,
          proof: proofFor(other.privateKey, neverSeen.deviceIdentifier, NOW_S),
        },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "proof_invalid" });
  });

  it("refuses a router presenting another key, even with a valid proof for it", async () => {
    const other = deviceKeyPair();
    expect(
      await evaluatePreclaimAdoptionWithDb(
        withCredentials(reserved).db as never,
        neverSeen,
        {
          devicePublicKey: other.publicKey,
          proof: proofFor(other.privateKey, neverSeen.deviceIdentifier, NOW_S),
        },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "key_mismatch" });
  });

  it("refuses a record that already issued a router token", async () => {
    const fake = withCredentials([
      { type: "bootstrap", devicePublicKey: device.publicKey, revokedAt: new Date() },
      { type: "agent_token", devicePublicKey: device.publicKey, revokedAt: null },
    ]);
    expect(
      await evaluatePreclaimAdoptionWithDb(
        fake.db as never,
        neverSeen,
        { devicePublicKey: device.publicKey, proof: goodProof() },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "not_preclaimed" });
  });

  it("refuses a router that has been seen, without asking the database", async () => {
    const fake = withCredentials(reserved);
    expect(
      await evaluatePreclaimAdoptionWithDb(
        fake.db as never,
        { ...neverSeen, lastSeenAt: new Date() },
        { devicePublicKey: device.publicKey, proof: goodProof() },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "not_preclaimed" });
    expect(fake.calls).toEqual([]);
  });

  it("refuses an ordinary record with no reserved key", async () => {
    expect(
      await evaluatePreclaimAdoptionWithDb(
        withCredentials([]).db as never,
        neverSeen,
        { devicePublicKey: device.publicKey, proof: goodProof() },
        NOW_MS,
      ),
    ).toEqual({ adopt: false, reason: "not_preclaimed" });
  });
});

describe("the public claim test key", () => {
  it("is the vector's key, and production routers are never given it", () => {
    expect(ROUTER_CLAIM_PUBLIC_TEST_KEY).toBe(claimVector.server.publicKey);

    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = ROUTER_CLAIM_PUBLIC_TEST_KEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    const unowned = { ownerRef: null, ownerLabel: null, releasedAt: null, partnerId: null };

    envMock.env.NODE_ENV = "production";
    expect(buildRouterClaimResponseFields(unowned)).not.toHaveProperty("claimKey");

    // A stand or a dev panel may still use it.
    envMock.env.NODE_ENV = "development";
    expect(buildRouterClaimResponseFields(unowned)).toMatchObject({
      claimKey: { kid: 1, publicKey: ROUTER_CLAIM_PUBLIC_TEST_KEY },
    });
  });
});

describe("the owner's partner speaks to the router", () => {
  const VECTRA_PUBKEY = Buffer.alloc(32, 3).toString("base64");

  it("gives a BloopCat-owned router BloopCat's key, bot and brand", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = VECTRA_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "VectraConnectBot";

    const fields = buildRouterClaimResponseFields({
      ownerRef: "bc_1",
      ownerLabel: "",
      releasedAt: null,
      partnerId: "bloopcat",
    });
    expect(fields).toMatchObject({
      claimKey: { kid: 2, publicKey: BLOOP_CLAIM_PUBKEY },
      botUsername: "BloopCat_bot",
      brand: "bloopcat",
      owner: { ownerRef: "bc_1", label: "BloopCat" },
    });
  });

  it("keeps a masked label a partner sent over the partner's own name", () => {
    expect(
      buildRouterClaimResponseFields({
        ownerRef: "bc_1",
        ownerLabel: "bl***@m***",
        releasedAt: null,
        partnerId: "bloopcat",
      }).owner,
    ).toEqual({ ownerRef: "bc_1", label: "bl***@m***" });
  });

  it("keeps an unclaimed router on Vectra's key and bot, with no brand", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = VECTRA_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "@VectraConnectBot";

    const fields = buildRouterClaimResponseFields({
      ownerRef: null,
      ownerLabel: null,
      releasedAt: null,
      partnerId: null,
    });
    expect(fields).toEqual({
      claimKey: { kid: 1, publicKey: VECTRA_PUBKEY },
      botUsername: "VectraConnectBot",
      owner: null,
    });
    expect(fields).not.toHaveProperty("brand");
  });

  it("does not let a stale partner id on an unowned router change what it hears", () => {
    // A released router has no owner: whatever partner it had no longer speaks to it.
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "VectraConnectBot";
    const fields = buildRouterClaimResponseFields({
      ownerRef: null,
      ownerLabel: null,
      releasedAt: new Date("2026-09-28T11:00:00.000Z"),
      partnerId: "bloopcat",
    });
    expect(fields).not.toHaveProperty("brand");
    expect(fields).toMatchObject({
      botUsername: "VectraConnectBot",
      owner: null,
      released: true,
    });
  });

  it("names a Vectra-owned router 'vectra'", () => {
    const fields = buildRouterClaimResponseFields({
      ownerRef: "vc_1",
      ownerLabel: "Аня",
      releasedAt: null,
      partnerId: null,
    });
    expect(fields).toMatchObject({ brand: "vectra", owner: { label: "Аня" } });
    expect(
      buildRouterClaimResponseFields({
        ownerRef: "vc_1",
        ownerLabel: "Аня",
        releasedAt: null,
        partnerId: "vectra",
      }),
    ).toMatchObject({ brand: "vectra" });
  });

  it("gives a Vectra-owned router the key and bot Vectra always had", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = VECTRA_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    envMock.env.VECTRA_CONNECT_BOT_USERNAME = "VectraConnectBot";
    expect(
      buildRouterClaimResponseFields({
        ownerRef: "vc_1",
        ownerLabel: "",
        releasedAt: null,
        partnerId: null,
      }),
    ).toMatchObject({
      claimKey: { kid: 1, publicKey: VECTRA_PUBKEY },
      botUsername: "VectraConnectBot",
      owner: { label: "Vectra" },
    });
  });

  it("holds a partner's claim key to the production rule on the public test key", () => {
    envMock.env.VECTRA_ROUTER_CLAIM_PUBKEY = VECTRA_PUBKEY;
    envMock.env.VECTRA_ROUTER_CLAIM_KID = 1;
    envMock.env.VECTRA_PARTNERS = JSON.stringify([
      {
        id: "bloopcat",
        brand: "bloopcat",
        label: "BloopCat",
        secrets: ["bloopcat-partner-secret-0123456789abcdef"],
        claimKid: 2,
        claimPublicKey: ROUTER_CLAIM_PUBLIC_TEST_KEY,
        botUsername: "BloopCat_bot",
      },
    ]);
    const owned = {
      ownerRef: "bc_1",
      ownerLabel: "",
      releasedAt: null,
      partnerId: "bloopcat",
    };

    envMock.env.NODE_ENV = "production";
    const production = buildRouterClaimResponseFields(owned);
    expect(JSON.stringify(production)).not.toContain(ROUTER_CLAIM_PUBLIC_TEST_KEY);
    expect(production.claimKey).toEqual({ kid: 1, publicKey: VECTRA_PUBKEY });

    envMock.env.NODE_ENV = "development";
    expect(buildRouterClaimResponseFields(owned).claimKey).toEqual({
      kid: 2,
      publicKey: ROUTER_CLAIM_PUBLIC_TEST_KEY,
    });
  });

  it("serves a partner this panel no longer lists under its own id as the brand", () => {
    // A router keeps its partner_id after the partner leaves VECTRA_PARTNERS.
    envMock.env.VECTRA_PARTNERS = "";
    const fields = buildRouterClaimResponseFields({
      ownerRef: "bc_1",
      ownerLabel: "",
      releasedAt: null,
      partnerId: "bloopcat",
    });
    expect(fields).toMatchObject({
      brand: "bloopcat",
      owner: { ownerRef: "bc_1" },
    });
  });

  it("never emits a brand the contract would refuse (a check-in must not throw)", () => {
    vi.mocked(findPartner).mockReturnValueOnce({
      id: "bloopcat",
      brand: "Bad Brand",
      label: "BloopCat",
      secrets: ["bloopcat-partner-secret-0123456789abcdef"],
      webhook: null,
      claimKey: null,
      botUsername: null,
    });
    const fields = buildRouterClaimResponseFields({
      ownerRef: "bc_1",
      ownerLabel: "",
      releasedAt: null,
      partnerId: "bloopcat",
    });
    expect(fields).not.toHaveProperty("brand");
    expect(fields.owner).toEqual({ ownerRef: "bc_1", label: "BloopCat" });
  });

  it("falls back to the partner id for a brand only when that is valid too", () => {
    const fields = buildRouterClaimResponseFields({
      ownerRef: "bc_1",
      ownerLabel: "",
      releasedAt: null,
      partnerId: "Not A Partner",
    });
    expect(fields).not.toHaveProperty("brand");
  });
});
