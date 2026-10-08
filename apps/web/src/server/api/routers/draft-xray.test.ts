import {
  eventLog,
  jobs,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routers,
} from "@vectra/db";
import { MASKED_SECRET_PLACEHOLDER } from "@vectra/contracts";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";
import { createXraySecretPayload } from "~/server/vectra/secrets";
import { createFakeDb } from "~/server/vectra/testing/fake-db";
import { buildXrayOperatorConfig } from "~/server/vectra/xray-operator-config";

import { draftRouter } from "./draft";

vi.mock("~/env", () => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "draft-xray-test-secrets-key-0123456789",
  },
}));
const { notifyVendorAccessWithDb } = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
}));
vi.mock("~/server/vectra/vendor-access", () => ({ notifyVendorAccessWithDb }));

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const REVISION_ID = "0c9a8b7d-6e5f-4a3b-8c2d-1e0f9a8b7c6d";
const SUBSCRIPTION_URL = "https://sub.example.test/api/sub/REAL_TOKEN";

function routerRow(overrides: Record<string, unknown> = {}) {
  return {
    id: ROUTER_ID,
    deviceIdentifier: "vectra-07bf0887f662",
    displayName: null,
    hostname: "1111111111",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    engineMode: "xray-direct",
    pendingImportRevisionId: null,
    activeRevisionId: REVISION_ID,
    ownerRef: null,
    releasedAt: null,
    ...overrides,
  };
}

const RELEASED_AT = new Date("2026-09-28T11:00:00.000Z");

function caller(db: unknown) {
  return createCallerFactory(draftRouter as never)({
    db: db as never,
    operatorSession: { user: "operator" } as never,
    headers: new Headers(),
  }) as unknown as Record<string, (input: unknown) => Promise<unknown>>;
}

// The latest xray revision as stored: masked config + encrypted cleartext.
function storedXrayRevision() {
  const cleartext = buildXrayOperatorConfig({
    instanceName: "1111111111",
    subscriptionUrl: SUBSCRIPTION_URL,
  });
  return {
    revision: {
      id: REVISION_ID,
      routerId: ROUTER_ID,
      revisionNumber: 4,
      status: "applied",
      origin: "operator_draft",
      engineMode: "xray-direct",
      configDigest: "digest-4",
      config: {
        ...cleartext,
        subscriptions: [
          { ...cleartext.subscriptions![0]!, url: MASKED_SECRET_PLACEHOLDER },
        ],
      },
    },
    blob: { ciphertext: createXraySecretPayload(cleartext) },
  };
}

describe("draft.setXrayUiLock", () => {
  it("authors a new xray revision with the lock on, secrets kept", async () => {
    const { revision, blob } = storedXrayRevision();
    const fake = createFakeDb({
      selects: [
        [routers, [[routerRow()], [routerRow()]]],
        [passwallDesiredRevisions, Array.from({ length: 5 }, () => [revision])],
        [passwallSecretBlobs, [[blob], [blob]]],
      ],
    });

    const result = (await caller(fake.db).setXrayUiLock!({
      routerId: ROUTER_ID,
      lock: true,
    })) as { config: Record<string, unknown>; engineMode: string };

    expect(result.engineMode).toBe("xray-direct");
    expect(result.config.ui).toEqual({ lock: true });
    const [inserted] = fake.inserts(passwallDesiredRevisions);
    expect(inserted).toMatchObject({
      engineMode: "xray-direct",
      revisionNumber: 5,
      note: "Lock the router UI to the simple view.",
    });
    // The owner's subscription survives the edit, encrypted as before.
    const [newBlob] = fake.inserts(passwallSecretBlobs);
    expect(newBlob!.ciphertext).toEqual(expect.any(String));
    expect(
      (inserted!.config as { subscriptions: Array<{ url: string }> })
        .subscriptions[0]!.url,
    ).toBe(MASKED_SECRET_PLACEHOLDER);
  });

  it("refuses a router that is not on the xray engine", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ engineMode: "passwall" })]]]],
    });

    await expect(
      caller(fake.db).setXrayUiLock!({ routerId: ROUTER_ID, lock: true }),
    ).rejects.toMatchObject({ code: "PRECONDITION_FAILED" });
    expect(fake.inserts(passwallDesiredRevisions)).toEqual([]);
  });

  it("refuses a router with no xray config yet", async () => {
    const fake = createFakeDb({ selects: [[routers, [[routerRow()]]]] });

    const error: unknown = await caller(fake.db)
      .setXrayUiLock!({ routerId: ROUTER_ID, lock: true })
      .catch((caught: unknown) => caught);
    expect(error).toMatchObject({ code: "PRECONDITION_FAILED" });
    expect((error as Error).message).toMatch(/no xray config yet/);
  });

  it("answers NOT_FOUND for an unknown router", async () => {
    const fake = createFakeDb();

    await expect(
      caller(fake.db).setXrayUiLock!({ routerId: ROUTER_ID, lock: false }),
    ).rejects.toMatchObject({ code: "NOT_FOUND" });
  });
});

describe("draft.configureXray on the shared helper", () => {
  it("creates the first config from a subscription URL", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[routerRow({ activeRevisionId: null })], [routerRow({ activeRevisionId: null })]]]],
    });

    const result = (await caller(fake.db).configureXray!({
      routerId: ROUTER_ID,
      subscriptionUrl: SUBSCRIPTION_URL,
    })) as { config: { subscriptions: Array<Record<string, unknown>> } };

    expect(result.config.subscriptions[0]).toMatchObject({
      id: "primary",
      url: MASKED_SECRET_PLACEHOLDER,
      userAgent: "v2rayNG/1.9.5",
      entryIndex: 0,
    });
  });

  it("keeps refusing a cleartext URL as BAD_REQUEST", async () => {
    const fake = createFakeDb({ selects: [[routers, [[routerRow()]]]] });

    await expect(
      caller(fake.db).configureXray!({
        routerId: ROUTER_ID,
        subscriptionUrl: "http://sub.example.test/x",
      }),
    ).rejects.toMatchObject({ code: "BAD_REQUEST" });
  });

  it("will not re-persist a purged (unbound) subscription as a real URL", async () => {
    const { revision } = storedXrayRevision();
    const fake = createFakeDb({
      selects: [
        [routers, [[routerRow()]]],
        // The blob is gone: hydration yields the at-rest placeholder.
        [passwallDesiredRevisions, [[revision], [revision]]],
      ],
    });

    const error: unknown = await caller(fake.db)
      .configureXray!({ routerId: ROUTER_ID, entryRemark: "PL" })
      .catch((caught: unknown) => caught);
    expect(error).toMatchObject({ code: "BAD_REQUEST" });
    expect((error as Error).message).toMatch(/no longer available/);
    expect(fake.inserts(passwallDesiredRevisions)).toEqual([]);
  });
});

describe("draft.queueApplyXray on the shared helper", () => {
  it("queues apply_xray_config for an xray revision", async () => {
    const { revision } = storedXrayRevision();
    const draftRevision = { ...revision, status: "draft" };
    const fake = createFakeDb({
      selects: [
        [routers, [[routerRow()]]],
        [passwallDesiredRevisions, [[draftRevision]]],
      ],
    });

    const job = (await caller(fake.db).queueApplyXray!({
      routerId: ROUTER_ID,
      desiredRevisionId: REVISION_ID,
    })) as Record<string, unknown>;

    expect(job).toMatchObject({
      type: "apply_xray_config",
      dedupeKey: `apply:${ROUTER_ID}:${REVISION_ID}`,
      payload: { desiredRevisionId: REVISION_ID },
    });
    expect(fake.inserts(jobs)).toHaveLength(1);
    expect(fake.updates(passwallDesiredRevisions)).toEqual([{ status: "queued" }]);
    // An ordinary router: no release to end, nothing else written.
    expect(fake.updates(routers)).toEqual([]);
    expect(fake.inserts(eventLog)).toEqual([]);
  });

  it("ends a release when the operator takes the router over by queueing its apply", async () => {
    const { revision } = storedXrayRevision();
    const fake = createFakeDb({
      selects: [
        // Approved again by the operator after the unbind withdrew approval.
        [routers, [[routerRow({ releasedAt: RELEASED_AT })]]],
        [passwallDesiredRevisions, [[{ ...revision, status: "draft" }]]],
      ],
    });

    const job = (await caller(fake.db).queueApplyXray!({
      routerId: ROUTER_ID,
      desiredRevisionId: REVISION_ID,
    })) as Record<string, unknown>;

    // No longer told `released: true` (the answer omits it once this is null).
    expect(fake.updates(routers)).toEqual([{ releasedAt: null }]);
    expect(job).toMatchObject({ type: "apply_xray_config" });
    expect(fake.inserts(eventLog)).toEqual([
      expect.objectContaining({
        routerId: ROUTER_ID,
        type: "router.release.ended",
        metadata: {
          releasedAt: RELEASED_AT.toISOString(),
          desiredRevisionId: REVISION_ID,
          operatorUser: "operator",
        },
      }),
    ]);
  });

  it("does not end a release when the apply is refused", async () => {
    // Unbind withdrew the approval; the operator has not approved it again.
    const fake = createFakeDb({
      selects: [
        [
          routers,
          [
            [
              routerRow({
                releasedAt: RELEASED_AT,
                importState: "awaiting_import",
                activeRevisionId: null,
              }),
            ],
          ],
        ],
      ],
    });

    await expect(
      caller(fake.db).queueApplyXray!({
        routerId: ROUTER_ID,
        desiredRevisionId: REVISION_ID,
      }),
    ).rejects.toMatchObject({ code: "PRECONDITION_FAILED" });
    expect(fake.updates(routers)).toEqual([]);
    expect(fake.inserts(jobs)).toEqual([]);
  });
});

describe("draft.queueApplyXray and the partner's vendor-access notice", () => {
  beforeEach(() => {
    notifyVendorAccessWithDb.mockReset();
    notifyVendorAccessWithDb.mockResolvedValue(null);
  });

  const partnerRouter = (overrides: Record<string, unknown> = {}) =>
    routerRow({ ownerRef: "bc_1", partnerId: "bloopcat", ...overrides });
  const draft = () => ({ ...storedXrayRevision().revision, status: "draft" });

  it("tells the router's partner once the apply is queued", async () => {
    const fake = createFakeDb({
      selects: [
        [routers, [[partnerRouter()]]],
        [passwallDesiredRevisions, [[draft()]]],
      ],
    });

    await caller(fake.db).queueApplyXray!({
      routerId: ROUTER_ID,
      desiredRevisionId: REVISION_ID,
    });

    expect(fake.inserts(jobs)).toHaveLength(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledTimes(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledWith(
      fake.db,
      expect.objectContaining({ id: ROUTER_ID, ownerRef: "bc_1", partnerId: "bloopcat" }),
      { kind: "config_apply", by: "operator" },
    );
  });

  it("says nothing when the same apply is already queued", async () => {
    const fake = createFakeDb({
      selects: [
        [routers, [[partnerRouter()]]],
        [passwallDesiredRevisions, [[draft()]]],
        [jobs, [[{ id: "job-0", type: "apply_xray_config", state: "queued" }]]],
      ],
    });

    await caller(fake.db).queueApplyXray!({
      routerId: ROUTER_ID,
      desiredRevisionId: REVISION_ID,
    });

    expect(fake.inserts(jobs)).toHaveLength(0);
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });

  it("tells the partner of a takeover only after the transaction committed", async () => {
    const fake = createFakeDb({
      selects: [
        [routers, [[partnerRouter({ releasedAt: RELEASED_AT })]]],
        [passwallDesiredRevisions, [[draft()]]],
      ],
    });
    let committed = false;
    const run = fake.db.transaction;
    fake.db.transaction = async (work) => {
      const result = await run(work);
      committed = true;
      return result;
    };
    const committedWhenTold: boolean[] = [];
    notifyVendorAccessWithDb.mockImplementation(async () => {
      committedWhenTold.push(committed);
      return null;
    });

    await caller(fake.db).queueApplyXray!({
      routerId: ROUTER_ID,
      desiredRevisionId: REVISION_ID,
    });

    expect(fake.updates(routers)).toEqual([{ releasedAt: null }]);
    expect(committedWhenTold).toEqual([true]);
  });

  it("says nothing when the apply is refused", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[partnerRouter({ importState: "awaiting_import" })]]]],
    });

    await expect(
      caller(fake.db).queueApplyXray!({
        routerId: ROUTER_ID,
        desiredRevisionId: REVISION_ID,
      }),
    ).rejects.toMatchObject({ code: "PRECONDITION_FAILED" });
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });
});

describe("draft.configureXray on a released router", () => {
  it("drafts a config without ending the release (drafting is not a takeover)", async () => {
    const released = routerRow({ releasedAt: RELEASED_AT, activeRevisionId: null });
    const fake = createFakeDb({
      selects: [[routers, [[released], [released]]]],
    });

    await caller(fake.db).configureXray!({
      routerId: ROUTER_ID,
      subscriptionUrl: SUBSCRIPTION_URL,
    });

    expect(fake.inserts(passwallDesiredRevisions)).toHaveLength(1);
    expect(fake.updates(routers)).toEqual([]);
  });
});
