import { createHash, randomBytes } from "node:crypto";

import type {
  PartnerRouterClaimRequest,
  PartnerRouterClaimResponse,
} from "@vectra/contracts";
import {
  eventLog,
  jobs,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  rescueCases,
  routerCredentials,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { and, desc, eq, inArray, isNull, or } from "drizzle-orm";

import type { db } from "~/server/db";
import { DEFAULT_PARTNER_ID } from "~/server/vectra/partner-registry";
import {
  routerOwnedByPartner,
  routerPartnerId,
} from "~/server/vectra/partner-scope";
import {
  enqueuePartnerWebhookWithDb,
  schedulePartnerWebhookDelivery,
} from "~/server/vectra/partner-webhooks";
import {
  claimCodeFromNonce,
  decodeClaimNonce,
  devicePublicKeysMatch,
  hashClaimCode,
  liveClaimCodeHashes,
  normalizeClaimCode,
  sealClaimCodeHash,
} from "~/server/vectra/router-claim-state";
import {
  configureXrayRevisionWithDb,
  PARTNER_CLAIM_APPLY_ORIGIN,
  queueXrayApplyJobWithDb,
} from "~/server/vectra/router-control";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";
import {
  assertUsableSubscriptionUrl,
  assertUsableSubscriptionUserAgent,
} from "~/server/vectra/xray-operator-config";

/**
 * ADR-0006 — the Vectra backend links a router to a customer's account.
 *
 * A claim binds the owner, APPROVES the router (an xray-direct router has no
 * PassWall import to approve, so this is the only way it becomes approved —
 * PassWall approval is untouched), authors its operator config from the
 * owner's subscription and queues the apply, in ONE transaction: a crash can
 * never leave a claimed router without its config, which a retry (answered
 * "already claimed") would then never repair.
 */

type ClaimsDatabase = Pick<
  typeof db,
  "select" | "insert" | "update" | "delete" | "transaction"
>;
type RouterRow = typeof routers.$inferSelect;

export type PartnerErrorCode =
  | "invalid"
  | "unknown_code"
  | "expired"
  | "claimed_by_other"
  | "not_claimed";

export type PartnerFailure = {
  ok: false;
  status: 400 | 404 | 409 | 410;
  body: { error: PartnerErrorCode; detail?: string };
};

export type RouterClaimOutcome =
  | { ok: true; status: 200; body: PartnerRouterClaimResponse }
  | PartnerFailure;

export type RouterUnbindOutcome =
  | { ok: true; status: 200; body: { routerId: string; state: "unclaimed" } }
  | PartnerFailure;

function fail(
  status: PartnerFailure["status"],
  error: PartnerErrorCode,
  detail?: string,
): PartnerFailure {
  return { ok: false, status, body: detail ? { error, detail } : { error } };
}

async function claimed(
  client: ClaimsDatabase,
  router: RouterRow,
  alreadyClaimed: boolean,
): Promise<RouterClaimOutcome> {
  const storedKey = await loadStoredDevicePublicKey(client, router.id);
  const key =
    storedKey && /^[A-Za-z0-9+/_-]{43}=?$/.test(storedKey)
      ? Buffer.from(storedKey, "base64")
      : null;
  return {
    ok: true,
    status: 200,
    body: {
      routerId: router.id,
      state: "claimed",
      alreadyClaimed,
      deviceIdentifier: router.deviceIdentifier,
      devicePublicKey: key?.length === 32 ? key.toString("base64") : null,
      model: router.model ?? null,
    },
  };
}

export type ClaimTarget =
  | { kind: "reject"; failure: PartnerFailure }
  | { kind: "already_claimed"; router: RouterRow }
  | { kind: "claim"; router: RouterRow }
  | { kind: "create" };

/**
 * Which router a code names, and what a claim on it means. Pure: `rows` are the
 * routers with `sealedCodeHash` in either code slot — the code they show now,
 * or the one they just replaced (valid until its own expiry).
 *
 * The same owner again is "already claimed" even after the code expired — a
 * retry must stay idempotent. Otherwise a code no router shows any more is 410
 * before anything about ownership is revealed, and a live code on someone
 * else's router is 409.
 */
export function selectClaimTargetByCode(
  rows: RouterRow[],
  sealedCodeHash: string,
  ownerRef: string,
  now: Date,
  partnerId: string = DEFAULT_PARTNER_ID,
): ClaimTarget {
  if (rows.length === 0) {
    return { kind: "reject", failure: fail(404, "unknown_code") };
  }

  const owned = rows.find(
    (row) => row.ownerRef === ownerRef && routerPartnerId(row) === partnerId,
  );
  if (owned) {
    return { kind: "already_claimed", router: owned };
  }

  const live = rows.filter((row) =>
    liveClaimCodeHashes(row, now).includes(sealedCodeHash),
  );
  if (live.length === 0) {
    return { kind: "reject", failure: fail(410, "expired") };
  }
  // Two routers showing the same live 40-bit code at once: refuse to guess.
  const [router] = live;
  if (!router || live.length > 1) {
    return { kind: "reject", failure: fail(404, "unknown_code") };
  }

  return router.ownerRef
    ? { kind: "reject", failure: fail(409, "claimed_by_other") }
    : { kind: "claim", router };
}

/**
 * What a claim by device means for the router with that identifier. Pure.
 *
 * The key is checked before anything about the router is revealed. Then:
 * - the same owner again is "already claimed" (idempotent retries);
 * - a router the panel has SEEN must be asking to be claimed — showing a live
 *   code (410 `expired` when it shows none: it is linked, operator-run, or its
 *   codes lapsed) — and the QR's nonce must derive one of the codes it shows
 *   (404 `unknown_code`: a stale or foreign QR). Without this a device claim
 *   could take over any unowned router, e.g. one an operator runs;
 * - a record the panel has never seen (created by an earlier claim) is judged
 *   by ownership alone: the router proves its key when it first registers.
 */
export function selectClaimTargetByDevice(
  router: RouterRow | null,
  storedPublicKey: string | null,
  presented: { devicePublicKey: string; sealedNonceCodeHash: string },
  ownerRef: string,
  now: Date,
  partnerId: string = DEFAULT_PARTNER_ID,
): ClaimTarget {
  if (!router) {
    return { kind: "create" };
  }
  if (!storedPublicKey) {
    return {
      kind: "reject",
      failure: fail(400, "invalid", "device_key_unknown"),
    };
  }
  if (!devicePublicKeysMatch(storedPublicKey, presented.devicePublicKey)) {
    return {
      kind: "reject",
      failure: fail(400, "invalid", "device_key_mismatch"),
    };
  }
  if (router.ownerRef === ownerRef && routerPartnerId(router) === partnerId) {
    return { kind: "already_claimed", router };
  }

  if (router.lastSeenAt) {
    const liveCodes = liveClaimCodeHashes(router, now);
    if (liveCodes.length === 0) {
      return { kind: "reject", failure: fail(410, "expired") };
    }
    if (!liveCodes.includes(presented.sealedNonceCodeHash)) {
      return { kind: "reject", failure: fail(404, "unknown_code") };
    }
  }

  return router.ownerRef
    ? { kind: "reject", failure: fail(409, "claimed_by_other") }
    : { kind: "claim", router };
}

/** The device key the panel knows for a registered router. */
async function loadStoredDevicePublicKey(
  client: ClaimsDatabase,
  routerId: string,
) {
  const credentials = await client
    .select()
    .from(routerCredentials)
    .where(eq(routerCredentials.routerId, routerId))
    .orderBy(desc(routerCredentials.issuedAt));
  const credential = credentials.find((row) => !row.revokedAt) ?? null;
  if (credential) {
    return credential.devicePublicKey;
  }
  // A revoked identity must not be resurrected from an older inventory.
  if (credentials.length > 0) return null;

  // Registration writes the router and its inventory before its credential.
  const [snapshot] = await client
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);
  return snapshot?.payload.devicePublicKey ?? null;
}

async function resolveClaimTarget(
  client: ClaimsDatabase,
  request: PartnerRouterClaimRequest,
  now: Date,
  partnerId: string,
): Promise<ClaimTarget> {
  if (request.device) {
    const nonce = decodeClaimNonce(request.device.nonce);
    if (!nonce) {
      return {
        kind: "reject",
        failure: fail(400, "invalid", "device.nonce must be 16 bytes"),
      };
    }
    const [router] = await client
      .select()
      .from(routers)
      .where(eq(routers.deviceIdentifier, request.device.deviceIdentifier))
      .limit(1);
    const storedPublicKey = router
      ? await loadStoredDevicePublicKey(client, router.id)
      : null;
    return selectClaimTargetByDevice(
      router ?? null,
      storedPublicKey,
      {
        devicePublicKey: request.device.devicePublicKey,
        sealedNonceCodeHash: sealClaimCodeHash(
          hashClaimCode(claimCodeFromNonce(nonce)),
        ),
      },
      request.owner.ref,
      now,
      partnerId,
    );
  }

  const code = normalizeClaimCode(request.code);
  if (!code) {
    return {
      kind: "reject",
      failure: fail(
        400,
        "invalid",
        "code must be 8 characters of Crockford base32",
      ),
    };
  }

  const sealedCodeHash = sealClaimCodeHash(hashClaimCode(code));
  const rows = await client
    .select()
    .from(routers)
    .where(
      or(
        eq(routers.claimCodeHash, sealedCodeHash),
        eq(routers.previousClaimCodeHash, sealedCodeHash),
      ),
    )
    .orderBy(desc(routers.claimExpiresAt))
    .limit(2);
  return selectClaimTargetByCode(
    rows,
    sealedCodeHash,
    request.owner.ref,
    now,
    partnerId,
  );
}

/**
 * Why an existing router cannot be claimed, or null. A router that never
 * reported inventory (created ahead of its first contact) has nothing to judge.
 */
async function findClaimBlocker(client: ClaimsDatabase, router: RouterRow) {
  const [snapshot] = await client
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, router.id))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);
  if (!snapshot) {
    return null;
  }

  // Never flip a live PassWall router onto the xray engine: only a router
  // that runs the new controller (it reports engineMode) can be claimed.
  if (
    router.engineMode !== "xray-direct" &&
    snapshot.payload.engineMode !== "xray-direct"
  ) {
    return "router_not_xray_direct";
  }

  // The same gate draft.queueApplyXray applies before any config is queued.
  const support = describeEffectiveRouterSupport({
    router: {
      boardName: router.boardName,
      target: router.target,
      architecture: router.architecture,
      openwrtRelease: router.openwrtRelease,
    },
    inventory: snapshot.payload,
  });
  return canRunDestructiveAction(support.state) ? null : "unsupported_hardware";
}

type ClaimWork = {
  request: PartnerRouterClaimRequest;
  subscriptionUrl: string;
  // The backend's stated literal, or null for the router's own signed agent.
  // Never an invented default either way.
  userAgent: string | null;
  via: "code" | "device";
  now: Date;
  // The partner that makes this claim; the router belongs to it from now on.
  partnerId: string;
};

type ClaimTransaction = Parameters<
  Parameters<ClaimsDatabase["transaction"]>[0]
>[0];

// Author the owner's config, queue its apply and record the claim — inside
// the caller's transaction.
async function configureClaimedRouter(
  tx: ClaimTransaction,
  router: RouterRow,
  work: ClaimWork,
  preRegistered: boolean,
) {
  const revision = await configureXrayRevisionWithDb(tx, {
    router,
    note: "Partner claim: linked to a Vectra account.",
    subscriptionUrl: work.subscriptionUrl,
    userAgent: work.userAgent,
    entryRemark: null,
    replaceSubscription: true,
  });
  const job = await queueXrayApplyJobWithDb(tx, {
    routerId: router.id,
    desiredRevision: revision,
    origin: PARTNER_CLAIM_APPLY_ORIGIN,
  });

  await tx.insert(eventLog).values({
    routerId: router.id,
    type: "router.claimed",
    severity: "info",
    message: `Router linked to a Vectra account by ${work.via}; approved and configured for xray-direct.`,
    metadata: {
      ownerRef: work.request.owner.ref,
      via: work.via,
      preRegistered,
      revisionId: revision.id,
      jobId: job?.id ?? null,
    },
  });
  await enqueuePartnerWebhookWithDb(tx, {
    event: "router.claimed",
    routerId: router.id,
    ownerRef: work.request.owner.ref,
    at: work.now,
    partnerId: work.partnerId,
  });
}

async function claimExistingRouter(
  client: ClaimsDatabase,
  router: RouterRow,
  work: ClaimWork,
) {
  return client.transaction(async (tx) => {
    const [claimedRouter] = await tx
      .update(routers)
      .set({
        ownerRef: work.request.owner.ref,
        ownerLabel: work.request.owner.label,
        partnerId: work.partnerId,
        claimedAt: work.now,
        // A new owner ends a previous release.
        releasedAt: null,
        engineMode: "xray-direct",
        // Approval without a PassWall import — the gap this closes.
        approvedAt: router.approvedAt ?? work.now,
        importState: "approved",
        pendingImportRevisionId: null,
        status:
          router.status === "pending" && router.lastSeenAt
            ? "active"
            : router.status,
      })
      // Only while nobody owns it: two concurrent claims cannot both win.
      .where(and(eq(routers.id, router.id), isNull(routers.ownerRef)))
      .returning();
    if (!claimedRouter) {
      return null;
    }

    await configureClaimedRouter(tx, claimedRouter, work, false);
    return claimedRouter;
  });
}

/**
 * Claim by device for a router that has never called in: create its record
 * ahead of time, already owned and approved, holding the device key the
 * backend verified in a reserved credential. Its first registration adopts the
 * record (router-control registerRouter) when it presents that key.
 */
async function claimUnregisteredRouter(
  client: ClaimsDatabase,
  device: NonNullable<PartnerRouterClaimRequest["device"]>,
  work: ClaimWork,
) {
  return client.transaction(async (tx) => {
    const [created] = await tx
      .insert(routers)
      .values({
        deviceIdentifier: device.deviceIdentifier,
        status: "pending",
        importState: "approved",
        engineMode: "xray-direct",
        approvedAt: work.now,
        ownerRef: work.request.owner.ref,
        ownerLabel: work.request.owner.label,
        partnerId: work.partnerId,
        claimedAt: work.now,
      })
      // The router registered in between: claim it as an existing one.
      .onConflictDoNothing({ target: routers.deviceIdentifier })
      .returning();
    if (!created) {
      return null;
    }

    await tx.insert(routerCredentials).values({
      routerId: created.id,
      type: "bootstrap",
      // Unusable on purpose: no one holds a token for this hash. The row only
      // carries the device key the first registration must present.
      tokenHash: createHash("sha256").update(randomBytes(32)).digest("hex"),
      tokenPreview: "partner-claim",
      devicePublicKey: device.devicePublicKey,
    });

    await configureClaimedRouter(tx, created, work, true);
    return created;
  });
}

export async function claimRouterWithDb(
  client: ClaimsDatabase,
  request: PartnerRouterClaimRequest,
  options: { now?: Date; partnerId?: string } = {},
): Promise<RouterClaimOutcome> {
  const now = options.now ?? new Date();
  const partnerId = options.partnerId ?? DEFAULT_PARTNER_ID;

  // The same boundary checks draft.configureXray relies on, before any write.
  // Absent/null is the router's own signed agent — passed through as null,
  // never defaulted. A stated literal is still held to the router's own
  // Happ-format (and self-signed-agent) guard; there is no fallback.
  let subscriptionUrl: string;
  let userAgent: string | null;
  try {
    subscriptionUrl = assertUsableSubscriptionUrl(request.subscription.url);
    const statedUserAgent = request.subscription.userAgent ?? null;
    userAgent =
      statedUserAgent === null
        ? null
        : assertUsableSubscriptionUserAgent(statedUserAgent);
  } catch (error) {
    return fail(
      400,
      "invalid",
      error instanceof Error ? error.message : "invalid subscription",
    );
  }

  const work: ClaimWork = {
    request,
    subscriptionUrl,
    userAgent,
    via: request.device ? "device" : "code",
    now,
    partnerId,
  };

  // A lost race (a concurrent claim or first registration got there first)
  // is resolved by looking again: the second look sees the winner.
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const target = await resolveClaimTarget(client, request, now, partnerId);

    if (target.kind === "reject") {
      return target.failure;
    }
    if (target.kind === "already_claimed") {
      return claimed(client, target.router, true);
    }

    if (target.kind === "create") {
      if (!request.device) {
        return fail(400, "invalid", "device is required");
      }
      const created = await claimUnregisteredRouter(
        client,
        request.device,
        work,
      );
      if (created) {
        schedulePartnerWebhookDelivery();
        return claimed(client, created, false);
      }
      continue;
    }

    const blocker = await findClaimBlocker(client, target.router);
    if (blocker) {
      return fail(400, "invalid", blocker);
    }

    const claimedRouter = await claimExistingRouter(
      client,
      target.router,
      work,
    );
    if (claimedRouter) {
      schedulePartnerWebhookDelivery();
      return claimed(client, claimedRouter, false);
    }
  }

  return fail(409, "claimed_by_other");
}

const CLAIM_JOB_TYPES: Array<(typeof jobs.$inferSelect)["type"]> = [
  "apply_xray_config",
  "refresh_xray_subscriptions",
  "reload_xray_outbound",
  "connect_router_action",
];

/**
 * Unbind: the router goes back to "not linked" — no owner, not approved, no
 * authoritative config — and nothing of the owner's config can still reach it:
 * its in-flight xray applies are cancelled (so a late result cannot re-approve
 * it), unapplied xray drafts are discarded, and the encrypted copies of the
 * owner's subscription are purged from the panel.
 *
 * It is also marked RELEASED (released_at) until the next claim: the router is
 * told `released: true` so it can drop the previous owner's config, and while
 * it waits for a new owner it raises no fleet or rescue alerts. Its open
 * rescue cases are closed here, or they would still escalate to Telegram.
 */
export async function unbindRouterClaimWithDb(
  client: ClaimsDatabase,
  input: { routerId: string; ownerRef: string; partnerId?: string },
  options: { now?: Date } = {},
): Promise<RouterUnbindOutcome> {
  // The schema requires it; refused here too, so no caller can unbind a
  // router without naming its owner.
  if (!input.ownerRef) {
    return fail(400, "invalid", "ownerRef is required");
  }
  const now = options.now ?? new Date();
  const [router] = await client
    .select()
    .from(routers)
    .where(eq(routers.id, input.routerId))
    .limit(1);

  if (!router?.ownerRef) {
    return fail(404, "not_claimed");
  }
  const partnerId = input.partnerId ?? DEFAULT_PARTNER_ID;
  if (
    input.ownerRef !== router.ownerRef ||
    routerPartnerId(router) !== partnerId
  ) {
    return fail(409, "claimed_by_other");
  }
  const previousOwnerRef = router.ownerRef;

  const unbound = await client.transaction(async (tx) => {
    const [updated] = await tx
      .update(routers)
      .set({
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
        status: router.status === "disabled" ? "disabled" : "pending",
        releasedAt: now,
      })
      .where(
        and(
          eq(routers.id, router.id),
          eq(routers.ownerRef, previousOwnerRef),
          routerOwnedByPartner(partnerId),
        ),
      )
      .returning();
    if (!updated) {
      return null;
    }

    await tx.update(routerInventorySnapshots).set({connectSecretCiphertext: null}).where(eq(routerInventorySnapshots.routerId, router.id));

    const closedCases = await tx
      .update(rescueCases)
      .set({ state: "resolved", resolvedAt: now })
      .where(
        and(
          eq(rescueCases.routerId, router.id),
          inArray(rescueCases.state, [
            "open",
            "repairing",
            "escalated",
            "silenced",
          ]),
        ),
      )
      .returning();

    const cancelledJobs = await tx
      .update(jobs)
      .set({ state: "cancelled", completedAt: now, dedupeKey: null })
      .where(
        and(
          eq(jobs.routerId, router.id),
          inArray(jobs.type, CLAIM_JOB_TYPES),
          inArray(jobs.state, ["queued", "delivered", "running"]),
        ),
      )
      .returning();

    const xrayRevisions = await tx
      .select()
      .from(passwallDesiredRevisions)
      .where(
        and(
          eq(passwallDesiredRevisions.routerId, router.id),
          eq(passwallDesiredRevisions.engineMode, "xray-direct"),
        ),
      );
    const revisionIds = xrayRevisions.map((revision) => revision.id);
    if (revisionIds.length > 0) {
      await tx
        .update(passwallDesiredRevisions)
        .set({ status: "discarded" })
        .where(
          and(
            inArray(passwallDesiredRevisions.id, revisionIds),
            inArray(passwallDesiredRevisions.status, ["draft", "queued"]),
          ),
        );
      await tx
        .delete(passwallSecretBlobs)
        .where(
          and(
            eq(passwallSecretBlobs.routerId, router.id),
            inArray(passwallSecretBlobs.desiredRevisionId, revisionIds),
          ),
        );
    }

    await tx.insert(eventLog).values({
      routerId: router.id,
      type: "router.claim.unbound",
      severity: "warning",
      message:
        "Router unlinked from its Vectra account; approval and config withdrawn.",
      metadata: {
        previousOwnerRef,
        cancelledJobIds: cancelledJobs.map((job) => job.id),
        purgedRevisionIds: revisionIds,
        closedRescueCaseIds: closedCases.map((rescueCase) => rescueCase.id),
      },
    });
    return updated;
  });

  if (!unbound) {
    // Someone changed the owner in between; report what is true now.
    const [current] = await client
      .select()
      .from(routers)
      .where(eq(routers.id, input.routerId))
      .limit(1);
    return current?.ownerRef
      ? fail(409, "claimed_by_other")
      : fail(404, "not_claimed");
  }

  return {
    ok: true,
    status: 200,
    body: { routerId: unbound.id, state: "unclaimed" },
  };
}
