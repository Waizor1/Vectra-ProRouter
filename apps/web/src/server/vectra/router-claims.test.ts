import { beforeEach, describe, expect, it, vi } from "vitest";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "router-claims-test-secrets-key-0123456789",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET:
      "router-claims-test-webhook-secret-0123456789",
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);
// The claim service must only use the client it is handed.
vi.mock("~/server/db", () => ({ db: {} }));
// A claim is the partner's own act, not a visit by Vectra's engineer.
const vendorAccess = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
}));
vi.mock("~/server/vectra/vendor-access", () => vendorAccess);

const { MASKED_SECRET_PLACEHOLDER, partnerRouterClaimRequestSchema } =
  await import("@vectra/contracts");
const {
  eventLog,
  jobs,
  partnerWebhooks,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  rescueCases,
  routerCredentials,
  routerInventorySnapshots,
  routers,
} = await import("@vectra/db");
const {
  claimRouterWithDb,
  selectClaimTargetByCode,
  selectClaimTargetByDevice,
  unbindRouterClaimWithDb,
} = await import("./router-claims");
const { hydrateXrayConfig } = await import("./secrets");
const { createFakeDb } = await import("./testing/fake-db");
const { claimCodeFromNonce, hashClaimCode, sealClaimCodeHash } =
  await import("./router-claim-state");

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const NOW = new Date("2026-09-28T10:00:00.000Z");
const IN_FIVE_MINUTES = new Date("2026-09-28T10:05:00.000Z");
const A_MINUTE_AGO = new Date("2026-09-28T09:59:00.000Z");
const IN_ONE_MINUTE = new Date("2026-09-28T10:01:00.000Z");
// The router's ed25519 public key as vctl sends it: base64 of 32 bytes.
const DEVICE_KEY = Buffer.alloc(32, 9).toString("base64");
// The QR's 16-byte nonce, and the sealed code it stands for.
const NONCE_BYTES = Buffer.alloc(16, 3);
const NONCE = NONCE_BYTES.toString("base64");
const NONCE_CODE = sealClaimCodeHash(
  hashClaimCode(claimCodeFromNonce(NONCE_BYTES)),
);
// The code claimRequest() sends by default, sealed as the panel stores it.
const CODE_SEALED = sealClaimCodeHash(hashClaimCode("7KQ4M9XD"));

type RouterFixture = Record<string, unknown> & { id: string };

function routerRow(overrides: Record<string, unknown> = {}): RouterFixture {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "vectra-07bf0887f662",
    displayName: null,
    hostname: "1111111111",
    panelDomain: "https://router.vectra-pro.net",
    model: "Xiaomi Mi Router AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "pending",
    importState: "awaiting_import",
    controllerChannel: "stable",
    engineMode: "xray-direct",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: null,
    lastSeenAt: A_MINUTE_AGO,
    lastCheckInAt: A_MINUTE_AGO,
    lastDirectModeAt: null,
    lastRescueReason: null,
    ownerRef: null,
    ownerLabel: null,
    partnerId: null,
    claimCodeHash: CODE_SEALED,
    claimExpiresAt: IN_FIVE_MINUTES,
    previousClaimCodeHash: null,
    previousClaimExpiresAt: null,
    claimedAt: null,
    releasedAt: null,
    createdAt: A_MINUTE_AGO,
    updatedAt: A_MINUTE_AGO,
    ...overrides,
  };
}

function snapshotRow(payload: Record<string, unknown> = {}) {
  return {
    id: "snapshot-1",
    routerId: ROUTER_ID,
    createdAt: A_MINUTE_AGO,
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      engineMode: "xray-direct",
      devicePublicKey: DEVICE_KEY,
      ...payload,
    },
  };
}

// No userAgent: the default fixture is the now-common case, the router's
// own signed agent. Tests that care about a backend-stated literal override
// `subscription` explicitly.
function claimRequest(overrides: Record<string, unknown> = {}) {
  return partnerRouterClaimRequestSchema.parse({
    code: "7KQ4M9XD",
    owner: { ref: "acct-42", label: "iv***@m***" },
    subscription: {
      url: "https://sub.example.test/api/sub/REAL_TOKEN",
    },
    ...overrides,
  });
}

// A successful claim of a registered router by code.
function claimableScript(router = routerRow()) {
  return {
    selects: [
      [routers, [[router], [{ ...router, ownerRef: "acct-42" }]]],
      [routerInventorySnapshots, [[snapshotRow()], [snapshotRow()]]],
      [passwallDesiredRevisions, [[], []]],
      [jobs, [[]]],
    ] as Array<[unknown, Array<Array<Record<string, unknown>>>]>,
    updateReturns: [
      [
        routers,
        [
          [
            {
              ...router,
              ownerRef: "acct-42",
              ownerLabel: "iv***@m***",
              approvedAt: NOW,
              importState: "approved",
            },
          ],
        ],
      ],
    ] as Array<[unknown, Array<Array<Record<string, unknown>>>]>,
  };
}

beforeEach(() => {
  envMock.env.VECTRA_CONNECT_WEBHOOK_URL = "https://backend.example.test/hooks";
});

describe("claim response trusted device identity", () => {
  it("returns the persisted key on an idempotent code claim", async () => {
    const fake = createFakeDb({
      selects: [
        [routers, [[routerRow({ ownerRef: "acct-42" })]]],
        [
          routerCredentials,
          [[{ devicePublicKey: DEVICE_KEY, revokedAt: null }]],
        ],
      ],
    });
    const result = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });
    expect(result).toMatchObject({
      ok: true,
      body: {
        alreadyClaimed: true,
        devicePublicKey: DEVICE_KEY,
        deviceIdentifier: "vectra-07bf0887f662",
      },
    });
    expect(fake.calls.filter((c) => c.kind !== "select")).toEqual([]);
  });
  it("does not resurrect revoked keys from old inventory", async () => {
    const fake = createFakeDb({selects: [[routers, [[routerRow({ownerRef: "acct-42"})]]], [routerCredentials, [[{devicePublicKey: DEVICE_KEY, revokedAt: NOW}]]], [routerInventorySnapshots, [[snapshotRow()]]]]});
    expect(await claimRouterWithDb(fake.db as never, claimRequest(), {now: NOW})).toMatchObject({ok: true, body: {devicePublicKey: null}});
  });
  it("never invents a missing registered key", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ ownerRef: "acct-42" })]]]],
    });
    expect(
      await claimRouterWithDb(fake.db as never, claimRequest(), { now: NOW }),
    ).toMatchObject({ ok: true, body: { devicePublicKey: null } });
  });
});

describe("selectClaimTargetByCode", () => {
  const unowned = routerRow() as never;

  it("is an unknown code when no router reported it", () => {
    expect(selectClaimTargetByCode([], CODE_SEALED, "acct-42", NOW)).toEqual({
      kind: "reject",
      failure: { ok: false, status: 404, body: { error: "unknown_code" } },
    });
  });

  it("claims an unowned router whose code is live", () => {
    expect(
      selectClaimTargetByCode([unowned], CODE_SEALED, "acct-42", NOW),
    ).toEqual({
      kind: "claim",
      router: unowned,
    });
  });

  it("answers 410 once the code expired", () => {
    const expired = routerRow({ claimExpiresAt: A_MINUTE_AGO }) as never;
    expect(
      selectClaimTargetByCode([expired], CODE_SEALED, "acct-42", NOW),
    ).toMatchObject({
      kind: "reject",
      failure: { status: 410, body: { error: "expired" } },
    });
  });

  it("answers 409 when another account owns the router", () => {
    const owned = routerRow({ ownerRef: "acct-7" }) as never;
    expect(
      selectClaimTargetByCode([owned], CODE_SEALED, "acct-42", NOW),
    ).toMatchObject({
      kind: "reject",
      failure: { status: 409, body: { error: "claimed_by_other" } },
    });
  });

  it("is idempotent for the same owner, even after the code expired", () => {
    const mine = routerRow({
      ownerRef: "acct-42",
      claimExpiresAt: A_MINUTE_AGO,
    }) as never;
    expect(
      selectClaimTargetByCode([mine], CODE_SEALED, "acct-42", NOW),
    ).toEqual({
      kind: "already_claimed",
      router: mine,
    });
  });

  it("refuses to guess between two routers showing the same live code", () => {
    const other = routerRow({ id: "7a1b2c3d-0000-4000-8000-000000000001" });
    expect(
      selectClaimTargetByCode(
        [unowned, other as never],
        CODE_SEALED,
        "acct-42",
        NOW,
      ),
    ).toMatchObject({ kind: "reject", failure: { status: 404 } });
  });

  it("honours the code the router just replaced, until that code's own expiry", () => {
    // The router moved on to a new code; the old one keeps its 2-minute grace.
    const rotated = routerRow({
      claimCodeHash: "sealed-new",
      claimExpiresAt: IN_FIVE_MINUTES,
      previousClaimCodeHash: CODE_SEALED,
      previousClaimExpiresAt: IN_ONE_MINUTE,
    }) as never;
    expect(
      selectClaimTargetByCode([rotated], CODE_SEALED, "acct-42", NOW),
    ).toEqual({
      kind: "claim",
      router: rotated,
    });

    const lapsed = routerRow({
      claimCodeHash: "sealed-new",
      claimExpiresAt: IN_FIVE_MINUTES,
      previousClaimCodeHash: CODE_SEALED,
      previousClaimExpiresAt: A_MINUTE_AGO,
    }) as never;
    expect(
      selectClaimTargetByCode([lapsed], CODE_SEALED, "acct-42", NOW),
    ).toMatchObject({ kind: "reject", failure: { status: 410 } });
  });

  it("does not call another partner's router with the same ownerRef 'already claimed'", () => {
    const vectras = routerRow({ ownerRef: "acct-42", partnerId: null }) as never;
    expect(
      selectClaimTargetByCode([vectras], CODE_SEALED, "acct-42", NOW, "bloopcat"),
    ).toMatchObject({
      kind: "reject",
      failure: { status: 409, body: { error: "claimed_by_other" } },
    });
  });

  it("is idempotent for the same partner and owner", () => {
    const mine = routerRow({ ownerRef: "acct-42", partnerId: "bloopcat" }) as never;
    expect(
      selectClaimTargetByCode([mine], CODE_SEALED, "acct-42", NOW, "bloopcat"),
    ).toEqual({ kind: "already_claimed", router: mine });
  });
});

describe("selectClaimTargetByDevice", () => {
  const presented = (sealedNonceCodeHash = NONCE_CODE) => ({
    devicePublicKey: DEVICE_KEY,
    sealedNonceCodeHash,
  });
  // A registered router asking to be claimed: it shows the QR's code now.
  const asking = (overrides: Record<string, unknown> = {}) =>
    routerRow({
      claimCodeHash: NONCE_CODE,
      claimExpiresAt: IN_FIVE_MINUTES,
      ...overrides,
    }) as never;
  const select = (
    router: Parameters<typeof selectClaimTargetByDevice>[0],
    ownerRef = "acct-42",
    sealed = NONCE_CODE,
    storedKey: string | null = DEVICE_KEY,
  ) =>
    selectClaimTargetByDevice(
      router,
      storedKey,
      presented(sealed),
      ownerRef,
      NOW,
    );

  it("creates the record ahead of a router that never called in", () => {
    expect(select(null, "acct-42", NONCE_CODE, null)).toEqual({
      kind: "create",
    });
  });

  it("requires the stored device key to match before anything else", () => {
    const owned = asking({ ownerRef: "acct-7" });
    expect(select(owned, "acct-42", NONCE_CODE, "b3RoZXIta2V5")).toMatchObject({
      kind: "reject",
      failure: {
        status: 400,
        body: { error: "invalid", detail: "device_key_mismatch" },
      },
    });
    expect(select(owned, "acct-42", NONCE_CODE, null)).toMatchObject({
      kind: "reject",
      failure: { body: { detail: "device_key_unknown" } },
    });
  });

  it("claims a registered router showing the QR's code", () => {
    const router = asking();
    expect(select(router)).toEqual({ kind: "claim", router });
  });

  it("accepts the code the router just replaced, until that code's own expiry", () => {
    const rotated = asking({
      claimCodeHash: "sealed-newer",
      previousClaimCodeHash: NONCE_CODE,
      previousClaimExpiresAt: IN_ONE_MINUTE,
    });
    expect(select(rotated)).toEqual({ kind: "claim", router: rotated });

    const lapsed = asking({
      claimCodeHash: "sealed-newer",
      previousClaimCodeHash: NONCE_CODE,
      previousClaimExpiresAt: A_MINUTE_AGO,
    });
    expect(select(lapsed)).toMatchObject({
      kind: "reject",
      failure: { status: 404, body: { error: "unknown_code" } },
    });
  });

  it("answers 410 expired for a registered router that is not asking to be claimed", () => {
    // Linked, run by the operator, or its codes lapsed: no live code at all.
    expect(
      select(asking({ claimCodeHash: null, claimExpiresAt: null })),
    ).toMatchObject({
      kind: "reject",
      failure: { status: 410, body: { error: "expired" } },
    });
    expect(select(asking({ claimExpiresAt: A_MINUTE_AGO }))).toMatchObject({
      failure: { status: 410 },
    });
  });

  it("answers 404 unknown_code when the QR's nonce is not a code the router shows", () => {
    expect(select(asking({ claimCodeHash: "sealed-other" }))).toMatchObject({
      kind: "reject",
      failure: { status: 404, body: { error: "unknown_code" } },
    });
  });

  it("maps ownership once the router is asking", () => {
    // The same owner again is idempotent even when no code is shown any more.
    const mine = asking({ ownerRef: "acct-42", claimCodeHash: null });
    expect(select(mine)).toEqual({ kind: "already_claimed", router: mine });
    expect(select(asking({ ownerRef: "acct-7" }))).toMatchObject({
      kind: "reject",
      failure: { status: 409 },
    });
  });

  it("does not call another partner's router with the same ownerRef 'already claimed'", () => {
    expect(
      selectClaimTargetByDevice(
        asking({ ownerRef: "acct-42", partnerId: null }),
        DEVICE_KEY,
        presented(NONCE_CODE),
        "acct-42",
        NOW,
        "bloopcat",
      ),
    ).toMatchObject({
      kind: "reject",
      failure: { status: 409, body: { error: "claimed_by_other" } },
    });
  });

  it("judges a record the panel has never seen by ownership alone", () => {
    // Made by an earlier device claim; the router proves its key on first
    // registration, so there is no code to check here.
    const unseen = routerRow({
      lastSeenAt: null,
      claimCodeHash: null,
      claimExpiresAt: null,
    }) as never;
    expect(select(unseen)).toEqual({ kind: "claim", router: unseen });
    const unseenTheirs = routerRow({
      lastSeenAt: null,
      claimCodeHash: null,
      ownerRef: "acct-7",
    }) as never;
    expect(select(unseenTheirs)).toMatchObject({ failure: { status: 409 } });
  });
});

describe("claimRouterWithDb", () => {
  it("binds the owner and approves the router without a PassWall import", async () => {
    const fake = createFakeDb(claimableScript());

    const outcome = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });

    expect(outcome).toEqual({
      ok: true,
      status: 200,
      body: {
        routerId: ROUTER_ID,
        state: "claimed",
        alreadyClaimed: false,
        deviceIdentifier: "vectra-07bf0887f662",
        devicePublicKey: DEVICE_KEY,
        model: "Xiaomi Mi Router AX3000T",
      },
    });
    expect(fake.updates(routers)[0]).toEqual({
      ownerRef: "acct-42",
      ownerLabel: "iv***@m***",
      partnerId: "vectra",
      claimedAt: NOW,
      releasedAt: null,
      engineMode: "xray-direct",
      approvedAt: NOW,
      importState: "approved",
      pendingImportRevisionId: null,
      status: "active",
    });
  });

  it("binds the router to the partner that claims it", async () => {
    const fake = createFakeDb(claimableScript());

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest(),
      { now: NOW, partnerId: "bloopcat" },
    );

    expect(outcome).toMatchObject({ ok: true, body: { alreadyClaimed: false } });
    expect(fake.updates(routers)[0]).toMatchObject({
      ownerRef: "acct-42",
      partnerId: "bloopcat",
    });
  });

  it("builds the xray config from the subscription and queues its apply", async () => {
    const fake = createFakeDb(claimableScript());

    await claimRouterWithDb(fake.db as never, claimRequest(), { now: NOW });

    const [revision] = fake.inserts(passwallDesiredRevisions);
    expect(revision).toMatchObject({
      routerId: ROUTER_ID,
      engineMode: "xray-direct",
      status: "draft",
      origin: "operator_draft",
    });
    // Masked at rest; the cleartext lives only in the encrypted blob.
    const masked = revision!.config as {
      subscriptions: Array<Record<string, unknown>>;
    };
    expect(masked.subscriptions[0]!.url).toBe(MASKED_SECRET_PLACEHOLDER);
    const [blob] = fake.inserts(passwallSecretBlobs);
    const hydrated = hydrateXrayConfig(
      revision!.config as never,
      blob!.ciphertext as string,
    );
    // No userAgent stated: the router fetches with its own signed agent, so
    // the key is absent — never defaulted to the fleet agent.
    expect(hydrated.subscriptions).toEqual([
      {
        id: "primary",
        url: "https://sub.example.test/api/sub/REAL_TOKEN",
        enabled: true,
        mode: "json",
        entryIndex: 0,
      },
    ]);
    expect(hydrated.subscriptions![0]).not.toHaveProperty("userAgent");
    expect(hydrated.inbounds.tproxy.port).toBe(12345);

    const [job] = fake.inserts(jobs);
    expect(job).toMatchObject({
      routerId: ROUTER_ID,
      type: "apply_xray_config",
      state: "queued",
    });
    expect(job!.payload).toMatchObject({ origin: "partner_claim" });
    expect(job!.dedupeKey).toBe(
      `apply:${ROUTER_ID}:${String(job!.desiredRevisionId)}`,
    );
    // The revision leaves "draft" once its apply is queued.
    expect(fake.updates(passwallDesiredRevisions)).toEqual([
      { status: "queued" },
    ]);
  });

  it("queues the apply of the partner's own claim without announcing a Vectra visit", async () => {
    vendorAccess.notifyVendorAccessWithDb.mockClear();
    const fake = createFakeDb(claimableScript());

    await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
      partnerId: "bloopcat",
    });

    expect(fake.inserts(jobs)).toEqual([
      expect.objectContaining({ type: "apply_xray_config", routerId: ROUTER_ID }),
    ]);
    expect(vendorAccess.notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });

  it("carries a backend-stated literal User-Agent straight through", async () => {
    const fake = createFakeDb(claimableScript());

    await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        subscription: {
          url: "https://sub.example.test/api/sub/REAL_TOKEN",
          userAgent: "v2rayNG/1.9.6",
        },
      }),
      { now: NOW },
    );

    const [revision] = fake.inserts(passwallDesiredRevisions);
    const [blob] = fake.inserts(passwallSecretBlobs);
    const hydrated = hydrateXrayConfig(
      revision!.config as never,
      blob!.ciphertext as string,
    );
    expect(hydrated.subscriptions![0]).toMatchObject({
      userAgent: "v2rayNG/1.9.6",
    });
  });

  it("omitting the User-Agent is accepted and never invents one for a customer's subscription", async () => {
    // The request schema accepts an absent userAgent...
    const parsed = partnerRouterClaimRequestSchema.safeParse({
      code: "7KQ4M9XD",
      owner: { ref: "acct-42", label: "iv***" },
      subscription: { url: "https://sub.example.test/api/sub/REAL_TOKEN" },
    });
    expect(parsed.success).toBe(true);
    expect(
      parsed.success ? parsed.data.subscription.userAgent : "unreachable",
    ).toBeUndefined();

    // ...and the service claims successfully with no fallback literal either.
    const fake = createFakeDb(claimableScript());
    const outcome = await claimRouterWithDb(
      fake.db as never,
      {
        ...claimRequest(),
        subscription: { url: "https://sub.example.test/api/sub/REAL_TOKEN" },
      } as never,
      { now: NOW },
    );
    expect(outcome).toEqual({
      ok: true,
      status: 200,
      body: {
        routerId: ROUTER_ID,
        state: "claimed",
        alreadyClaimed: false,
        deviceIdentifier: "vectra-07bf0887f662",
        devicePublicKey: DEVICE_KEY,
        model: "Xiaomi Mi Router AX3000T",
      },
    });
  });

  it("refuses a blank User-Agent as ambiguous", async () => {
    const parsed = partnerRouterClaimRequestSchema.safeParse({
      code: "7KQ4M9XD",
      owner: { ref: "acct-42", label: "iv***" },
      subscription: {
        url: "https://sub.example.test/api/sub/REAL_TOKEN",
        userAgent: "",
      },
    });
    expect(parsed.success).toBe(false);
    expect(parsed.error?.issues[0]?.path).toEqual([
      "subscription",
      "userAgent",
    ]);

    // The service has no fallback if a caller skips the schema either.
    const fake = createFakeDb(claimableScript());
    const outcome = await claimRouterWithDb(
      fake.db as never,
      {
        ...claimRequest(),
        subscription: {
          url: "https://sub.example.test/api/sub/REAL_TOKEN",
          userAgent: "",
        },
      } as never,
      { now: NOW },
    );
    expect(outcome).toMatchObject({
      ok: false,
      status: 400,
      body: { error: "invalid" },
    });
    expect(fake.calls).toEqual([]);
  });

  it("refuses a User-Agent claiming to be the router's own signed agent", async () => {
    // The schema boundary refuses it outright (see the contract test suite)...
    const parsed = partnerRouterClaimRequestSchema.safeParse({
      code: "7KQ4M9XD",
      owner: { ref: "acct-42", label: "iv***" },
      subscription: {
        url: "https://sub.example.test/api/sub/REAL_TOKEN",
        userAgent: "VectraRouter/0.4.0 (AX3000T)",
      },
    });
    expect(parsed.success).toBe(false);
    expect(parsed.error?.issues[0]?.path).toEqual([
      "subscription",
      "userAgent",
    ]);

    // ...and the service has no fallback if a caller skips the schema either,
    // case-insensitively.
    const fake = createFakeDb(claimableScript());
    const outcome = await claimRouterWithDb(
      fake.db as never,
      {
        ...claimRequest(),
        subscription: {
          url: "https://sub.example.test/api/sub/REAL_TOKEN",
          userAgent: "vectrarouter/0.4.0",
        },
      } as never,
      { now: NOW },
    );

    expect(outcome).toMatchObject({
      ok: false,
      status: 400,
      body: { error: "invalid" },
    });
    expect((outcome as { body: { detail?: string } }).body.detail).toMatch(
      /router's own signed agent/,
    );
    expect(fake.calls).toEqual([]);
  });

  it("clears a previous release when the router is claimed again", async () => {
    const released = routerRow({ releasedAt: A_MINUTE_AGO });
    const fake = createFakeDb(claimableScript(released));

    const outcome = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });

    expect(outcome).toMatchObject({
      ok: true,
      body: { alreadyClaimed: false },
    });
    expect(fake.updates(routers)[0]).toMatchObject({
      ownerRef: "acct-42",
      releasedAt: null,
    });
  });

  it("journals the claim and queues router.claimed for the backend", async () => {
    const fake = createFakeDb(claimableScript());

    await claimRouterWithDb(fake.db as never, claimRequest(), { now: NOW });

    expect(fake.inserts(eventLog)[0]).toMatchObject({
      routerId: ROUTER_ID,
      type: "router.claimed",
      metadata: { ownerRef: "acct-42", via: "code", preRegistered: false },
    });
    expect(fake.inserts(partnerWebhooks)).toEqual([
      expect.objectContaining({
        event: "router.claimed",
        routerId: ROUTER_ID,
        payload: {
          event: "router.claimed",
          routerId: ROUTER_ID,
          ownerRef: "acct-42",
          at: NOW.toISOString(),
        },
      }),
    ]);
  });

  it("queues no webhook while the backend webhook is not configured", async () => {
    envMock.env.VECTRA_CONNECT_WEBHOOK_URL = undefined;
    const fake = createFakeDb(claimableScript());

    await claimRouterWithDb(fake.db as never, claimRequest(), { now: NOW });

    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });

  it("refuses a cleartext subscription before touching the database", async () => {
    const fake = createFakeDb(claimableScript());

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        subscription: {
          url: "http://sub.example.test/x",
        },
      }),
      { now: NOW },
    );

    expect(outcome).toMatchObject({
      ok: false,
      status: 400,
      body: { error: "invalid" },
    });
    expect(fake.calls).toEqual([]);
  });

  it("refuses a User-Agent that would get the customer's device deleted", async () => {
    const fake = createFakeDb(claimableScript());

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        subscription: {
          url: "https://sub.example.test/x",
          userAgent: "Happ/1.0",
        },
      }),
      { now: NOW },
    );

    expect(outcome).toMatchObject({ ok: false, status: 400 });
    expect(fake.calls).toEqual([]);
  });

  it("never flips a live PassWall router onto the xray engine", async () => {
    const passwallRouter = routerRow({
      engineMode: "passwall",
      approvedAt: A_MINUTE_AGO,
      importState: "approved",
    });
    const fake = createFakeDb({
      selects: [
        [routers, [[passwallRouter]]],
        [routerInventorySnapshots, [[snapshotRow({ engineMode: undefined })]]],
      ],
    });

    const outcome = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });

    expect(outcome).toEqual({
      ok: false,
      status: 400,
      body: { error: "invalid", detail: "router_not_xray_direct" },
    });
    expect(fake.updates(routers)).toEqual([]);
  });

  it("keeps the support gate queueApplyXray applies", async () => {
    const fake = createFakeDb({
      selects: [
        [
          routers,
          [
            [
              routerRow({
                target: "ramips/mt7621",
                architecture: "mipsel_24kc",
              }),
            ],
          ],
        ],
        [
          routerInventorySnapshots,
          [
            [
              snapshotRow({
                target: "ramips/mt7621",
                architecture: "mipsel_24kc",
              }),
            ],
          ],
        ],
      ],
    });

    const outcome = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });

    expect(outcome).toMatchObject({
      status: 400,
      body: { detail: "unsupported_hardware" },
    });
    expect(fake.inserts(jobs)).toEqual([]);
  });

  it("re-reads after losing a race and reports the winner", async () => {
    const router = routerRow();
    const fake = createFakeDb({
      selects: [
        [routers, [[router], [{ ...router, ownerRef: "acct-7" }]]],
        [routerInventorySnapshots, [[snapshotRow()], [snapshotRow()]]],
      ],
      // The conditional update matched nothing: someone claimed it first.
      updateReturns: [[routers, [[]]]],
    });

    const outcome = await claimRouterWithDb(fake.db as never, claimRequest(), {
      now: NOW,
    });

    expect(outcome).toMatchObject({
      status: 409,
      body: { error: "claimed_by_other" },
    });
    expect(fake.inserts(jobs)).toEqual([]);
  });

  it("creates a pre-claimed record for a router that never registered", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[], [{ ...routerRow(), lastSeenAt: null }]]]],
    });

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        code: null,
        device: {
          deviceIdentifier: "vectra-cccccccccccc",
          devicePublicKey: DEVICE_KEY,
          nonce: NONCE,
        },
      }),
      { now: NOW },
    );

    expect(outcome).toMatchObject({
      ok: true,
      body: { alreadyClaimed: false },
    });
    expect(fake.inserts(routers)).toEqual([
      {
        deviceIdentifier: "vectra-cccccccccccc",
        status: "pending",
        importState: "approved",
        engineMode: "xray-direct",
        approvedAt: NOW,
        ownerRef: "acct-42",
        ownerLabel: "iv***@m***",
        partnerId: "vectra",
        claimedAt: NOW,
      },
    ]);
    // The key the backend verified, held for the router's first registration
    // in a credential nobody can authenticate with.
    const [credential] = fake.inserts(routerCredentials);
    expect(credential).toMatchObject({
      type: "bootstrap",
      devicePublicKey: DEVICE_KEY,
    });
    expect(credential!.tokenHash).toMatch(/^[0-9a-f]{64}$/);
    expect(fake.inserts(jobs)[0]).toMatchObject({ type: "apply_xray_config" });
    expect(fake.inserts(eventLog)[0]).toMatchObject({
      metadata: { via: "device", preRegistered: true },
    });
  });

  it("creates the pre-claimed record under the partner that claimed it", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[], [{ ...routerRow(), lastSeenAt: null }]]]],
    });

    await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        code: null,
        device: {
          deviceIdentifier: "vectra-cccccccccccc",
          devicePublicKey: DEVICE_KEY,
          nonce: NONCE,
        },
      }),
      { now: NOW, partnerId: "bloopcat" },
    );

    expect(fake.inserts(routers)).toMatchObject([{ partnerId: "bloopcat" }]);
  });

  it("claims a registered router by device when it shows the QR's code", async () => {
    const router = routerRow({
      claimCodeHash: NONCE_CODE,
      claimExpiresAt: IN_FIVE_MINUTES,
    });
    const fake = createFakeDb({
      selects: [
        [routers, [[router], [router]]],
        [
          routerCredentials,
          [
            [
              {
                type: "agent_token",
                devicePublicKey: DEVICE_KEY,
                revokedAt: null,
              },
            ],
          ],
        ],
        [routerInventorySnapshots, [[snapshotRow()], [snapshotRow()]]],
      ],
      updateReturns: [[routers, [[{ ...router, ownerRef: "acct-42" }]]]],
    });

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        code: null,
        device: {
          deviceIdentifier: router.deviceIdentifier,
          devicePublicKey: DEVICE_KEY,
          nonce: NONCE,
        },
      }),
      { now: NOW },
    );

    expect(outcome).toMatchObject({
      ok: true,
      body: { alreadyClaimed: false },
    });
  });

  it("will not replace an operator's config on an unowned router that is not asking", async () => {
    // Run by the operator: approved, configured, no owner, and no claim code
    // shown. A valid device QR (e.g. an old photo) must not take it over.
    const operatorRun = routerRow({
      claimCodeHash: null,
      claimExpiresAt: null,
      importState: "approved",
      approvedAt: A_MINUTE_AGO,
      status: "active",
      activeRevisionId: "rev-operator",
    });
    const fake = createFakeDb({
      selects: [
        [routers, [[operatorRun]]],
        [
          routerCredentials,
          [
            [
              {
                type: "agent_token",
                devicePublicKey: DEVICE_KEY,
                revokedAt: null,
              },
            ],
          ],
        ],
      ],
    });

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        code: null,
        device: {
          deviceIdentifier: operatorRun.deviceIdentifier,
          devicePublicKey: DEVICE_KEY,
          nonce: NONCE,
        },
      }),
      { now: NOW },
    );

    expect(outcome).toEqual({
      ok: false,
      status: 410,
      body: { error: "expired" },
    });
    expect(fake.updates(routers)).toEqual([]);
    expect(fake.inserts(passwallDesiredRevisions)).toEqual([]);
    expect(fake.inserts(jobs)).toEqual([]);
  });

  it("refuses a device claim whose QR nonce is not the router's current code", async () => {
    const router = routerRow({
      claimCodeHash: "sealed-other-code",
      claimExpiresAt: IN_FIVE_MINUTES,
    });
    const fake = createFakeDb({
      selects: [
        [routers, [[router]]],
        [
          routerCredentials,
          [
            [
              {
                type: "agent_token",
                devicePublicKey: DEVICE_KEY,
                revokedAt: null,
              },
            ],
          ],
        ],
      ],
    });

    const outcome = await claimRouterWithDb(
      fake.db as never,
      claimRequest({
        code: null,
        device: {
          deviceIdentifier: router.deviceIdentifier,
          devicePublicKey: DEVICE_KEY,
          nonce: NONCE,
        },
      }),
      { now: NOW },
    );

    expect(outcome).toEqual({
      ok: false,
      status: 404,
      body: { error: "unknown_code" },
    });
    expect(fake.updates(routers)).toEqual([]);
  });
});

describe("unbindRouterClaimWithDb", () => {
  it("answers 404 for a router nobody owns", async () => {
    const fake = createFakeDb({ selects: [[routers, [[routerRow()]]]] });

    expect(
      await unbindRouterClaimWithDb(fake.db as never, {
        routerId: ROUTER_ID,
        ownerRef: "acct-42",
      }),
    ).toEqual({ ok: false, status: 404, body: { error: "not_claimed" } });
  });

  it("refuses an unbind that names no owner", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ ownerRef: "acct-42" })]]]],
    });

    expect(
      await unbindRouterClaimWithDb(fake.db as never, {
        routerId: ROUTER_ID,
        ownerRef: "",
      }),
    ).toMatchObject({ status: 400, body: { error: "invalid" } });
    expect(fake.updates(routers)).toEqual([]);
  });

  it("refuses to unbind for an account that no longer owns the router", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ ownerRef: "acct-42" })]]]],
    });

    expect(
      await unbindRouterClaimWithDb(fake.db as never, {
        routerId: ROUTER_ID,
        ownerRef: "acct-7",
      }),
    ).toMatchObject({ status: 409, body: { error: "claimed_by_other" } });
    expect(fake.updates(routers)).toEqual([]);
  });

  it("refuses to unbind another partner's router even with its ownerRef", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ ownerRef: "acct-42", partnerId: null })]]]],
    });

    expect(
      await unbindRouterClaimWithDb(fake.db as never, {
        routerId: ROUTER_ID,
        ownerRef: "acct-42",
        partnerId: "bloopcat",
      }),
    ).toMatchObject({ status: 409, body: { error: "claimed_by_other" } });
    expect(fake.updates(routers)).toEqual([]);
  });

  it("unbinds a partner's own router and leaves it with no partner", async () => {
    const owned = routerRow({
      ownerRef: "acct-42",
      partnerId: "bloopcat",
      status: "active",
    });
    const fake = createFakeDb({
      selects: [[routers, [[owned]]]],
      updateReturns: [
        [routers, [[{ ...owned, ownerRef: null, partnerId: null, releasedAt: NOW }]]],
      ],
    });

    expect(
      await unbindRouterClaimWithDb(
        fake.db as never,
        { routerId: ROUTER_ID, ownerRef: "acct-42", partnerId: "bloopcat" },
        { now: NOW },
      ),
    ).toMatchObject({ ok: true, status: 200, body: { state: "unclaimed" } });
    expect(fake.updates(routers)[0]).toMatchObject({
      ownerRef: null,
      partnerId: null,
    });
  });

  it("returns the router to 'not linked' and withdraws the owner's config", async () => {
    const owned = routerRow({
      ownerRef: "acct-42",
      ownerLabel: "iv***",
      claimedAt: A_MINUTE_AGO,
      approvedAt: A_MINUTE_AGO,
      importState: "approved",
      status: "active",
      activeRevisionId: "rev-1",
    });
    const fake = createFakeDb({
      selects: [
        [routers, [[owned]]],
        [
          passwallDesiredRevisions,
          [
            [
              { id: "rev-1", status: "applied" },
              { id: "rev-2", status: "queued" },
            ],
          ],
        ],
      ],
      updateReturns: [
        [routers, [[{ ...owned, ownerRef: null, releasedAt: NOW }]]],
        [jobs, [[{ id: "job-delivered" }]]],
        [rescueCases, [[{ id: "case-open" }]]],
      ],
    });

    const outcome = await unbindRouterClaimWithDb(
      fake.db as never,
      { routerId: ROUTER_ID, ownerRef: "acct-42" },
      { now: NOW },
    );

    expect(outcome).toEqual({
      ok: true,
      status: 200,
      body: { routerId: ROUTER_ID, state: "unclaimed" },
    });
    expect(fake.updates(routers)[0]).toEqual({
      ownerRef: null,
      ownerLabel: null,
      claimedAt: null,
      claimCodeHash: null,
      claimExpiresAt: null,
      previousClaimCodeHash: null,
      previousClaimExpiresAt: null,
      // An unlinked router belongs to no partner.
      partnerId: null,
      approvedAt: null,
      importState: "awaiting_import",
      activeRevisionId: null,
      pendingImportRevisionId: null,
      status: "pending",
      // Released until the next claim: the router is told to drop the
      // previous owner's config and raises no alerts while it waits.
      releasedAt: NOW,
    });
    // Its open rescue cases are closed, or they would still page Telegram.
    expect(fake.updates(rescueCases)).toEqual([
      { state: "resolved", resolvedAt: NOW },
    ]);
    // In-flight applies are cancelled so a late result cannot re-approve it.
    expect(fake.updates(jobs)).toEqual([
      { state: "cancelled", completedAt: NOW, dedupeKey: null },
    ]);
    expect(fake.updates(passwallDesiredRevisions)).toEqual([
      { status: "discarded" },
    ]);
    // The encrypted copies of the owner's subscription are purged.
    expect(fake.deletes(passwallSecretBlobs)).toBe(1);
    expect(fake.inserts(eventLog)[0]).toMatchObject({
      type: "router.claim.unbound",
      metadata: {
        previousOwnerRef: "acct-42",
        cancelledJobIds: ["job-delivered"],
        purgedRevisionIds: ["rev-1", "rev-2"],
        closedRescueCaseIds: ["case-open"],
      },
    });
  });
});

// The journal and the revision note name the partner the router is linked to
// (its label, or its id when the registry does not know it), not "Vectra" for
// everyone.
describe("the claim and unbind journal names the partner", () => {
  const BLOOP_PARTNERS = JSON.stringify([
    {
      id: "bloopcat",
      brand: "bloopcat",
      label: "BloopCat",
      secrets: ["router-claims-bloopcat-partner-secret-0123"],
    },
  ]);

  function withPartners<T>(run: () => Promise<T>) {
    envMock.env.VECTRA_PARTNERS = BLOOP_PARTNERS;
    return run().finally(() => {
      delete envMock.env.VECTRA_PARTNERS;
    });
  }

  it.each([
    [undefined, "Vectra", "vectra"],
    ["bloopcat", "BloopCat", "bloopcat"],
    ["ghost", "ghost", "ghost"],
  ])("a claim by %s says %s and records %s", (partnerId, name, recorded) =>
    withPartners(async () => {
      const fake = createFakeDb(claimableScript());

      await claimRouterWithDb(fake.db as never, claimRequest(), {
        now: NOW,
        ...(partnerId ? { partnerId } : {}),
      });

      const [entry] = fake.inserts(eventLog);
      expect(entry).toMatchObject({
        type: "router.claimed",
        message: `Router linked to a ${name} account by code; approved and configured for xray-direct.`,
        metadata: { ownerRef: "acct-42", partnerId: recorded },
      });
      expect(fake.inserts(passwallDesiredRevisions)[0]).toMatchObject({
        note: `Partner claim: linked to a ${name} account.`,
      });
    }),
  );

  it.each([
    [undefined, null, "Vectra", "vectra"],
    ["bloopcat", "bloopcat", "BloopCat", "bloopcat"],
  ])("an unbind by %s (of a router owned by %s) says %s and records %s", (partnerId, owner, name, recorded) =>
    withPartners(async () => {
      const owned = routerRow({ ownerRef: "acct-42", partnerId: owner, status: "active" });
      const fake = createFakeDb({
        selects: [[routers, [[owned]]]],
        updateReturns: [
          [routers, [[{ ...owned, ownerRef: null, partnerId: null, releasedAt: NOW }]]],
        ],
      });

      expect(
        await unbindRouterClaimWithDb(
          fake.db as never,
          { routerId: ROUTER_ID, ownerRef: "acct-42", ...(partnerId ? { partnerId } : {}) },
          { now: NOW },
        ),
      ).toMatchObject({ ok: true });

      expect(fake.inserts(eventLog)[0]).toMatchObject({
        type: "router.claim.unbound",
        message: `Router unlinked from its ${name} account; approval and config withdrawn.`,
        metadata: { previousOwnerRef: "acct-42", partnerId: recorded },
      });
    }),
  );
});
