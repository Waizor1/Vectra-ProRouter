import { createHash, randomUUID } from "node:crypto";
import {
  connectRouterActionNameSchema,
  partnerOwnerRefSchema,
} from "@vectra/contracts";
import { jobs, routerInventorySnapshots, routers } from "@vectra/db";
import { and, desc, eq, inArray, isNull } from "drizzle-orm";
import { z } from "zod";
import {
  CONNECT_ACTION_NAMES,
  parseConnectActionParams,
} from "./partner-action-params";
import {
  hydrateConnectWifi,
  protectPartnerParams,
} from "./partner-router-secrets";
import { db } from "~/server/db";
import {
  defaultDeps,
  authenticatePartnerRequest,
  executePartnerRequest,
  parseIdempotencyKey,
  partnerHashMatches,
  partnerJson,
  readRawBody,
  type PartnerApiDeps,
} from "./partner-api";
import { keyedDigest } from "./secrets";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "./support";

type Client = Pick<typeof db, "select" | "insert" | "update" | "transaction">;
type Router = typeof routers.$inferSelect;
type Inventory = typeof routerInventorySnapshots.$inferSelect;
export const PARTNER_ACTION_ORIGIN = "partner_action";
const ownerSchema = partnerOwnerRefSchema;
export const partnerActionRequestSchema = z
  .object({
    // Like unbind, bind the signed bytes to the target path.
    routerId: z.string().uuid(),
    ownerRef: ownerSchema,
    action: connectRouterActionNameSchema,
    params: z.record(z.string(), z.unknown()),
  })
  .strict();

export function projectPartnerRouter(
  router: Router,
  inventory: Inventory | null,
  now = new Date(),
) {
  const payload = inventory?.payload;
  const reported =
    inventory && (!router.claimedAt || inventory.createdAt >= router.claimedAt)
      ? payload?.connect
      : undefined;
  const measured =
    reported?.ownerRef !== undefined && reported.ownerRef !== router.ownerRef
      ? undefined
      : reported;
  const capable =
    router.status !== "disabled" &&
    router.engineMode === "xray-direct" &&
    !!router.approvedAt &&
    !!inventory &&
    canRunDestructiveAction(
      describeEffectiveRouterSupport({ router, inventory: payload! }).state,
    );
  return {
    routerId: router.id,
    model: router.model,
    name: router.displayName,
    deviceIdentifier: router.deviceIdentifier,
    online:
      !!router.lastSeenAt &&
      now.getTime() - router.lastSeenAt.getTime() <= 180_000,
    lastSeenAt: router.lastSeenAt?.toISOString() ?? null,
    // The row is rewritten on a material change, a reboot (uptime going down)
    // or the heartbeat, not on every check-in: uptime runs on from its time.
    // Counted on only while the router was seen: an offline router's uptime
    // stops at its last check-in.
    uptimeSec:
      typeof measured?.uptimeSec === "number" && inventory
        ? measured.uptimeSec +
          Math.max(
            0,
            Math.floor(
              (Math.min(now.getTime(), router.lastSeenAt?.getTime() ?? inventory.createdAt.getTime()) -
                inventory.createdAt.getTime()) /
                1000,
            ),
          )
        : null,
    verdict: measured?.verdict ?? null,
    exitCountry: measured?.exitCountry ?? null,
    lanClients: measured?.lanClients ?? null,
    location: measured?.location ?? null,
    entries: measured?.entries ?? [],
    sites: measured?.sites ?? null,
    services: measured?.services ?? null,
    wifi: measured ? (hydrateConnectWifi(router, inventory) ?? null) : null,
    routerPasswordSet: measured?.routerPasswordSet ?? null,
    supportAccess: measured?.supportAccess ?? null,
    version: {
      current:
        payload?.controllerRuntimeVersion ?? payload?.controllerVersion ?? null,
      available: measured?.availableVersion ?? null,
      autoUpdate: measured?.autoUpdate ?? null,
    },
    memory: payload?.resources
      ? `${payload.resources.memoryAvailableMb}/${payload.resources.memoryTotalMb} MB`
      : null,
    // Only what the router itself reported for its CURRENT owner. A router
    // that reports nothing (an older controller, a snapshot from before the
    // claim) supports nothing through Connect — never a guessed default.
    capabilities:
      capable &&
      measured?.capabilities &&
      measured.ownerRef === router.ownerRef
        ? CONNECT_ACTION_NAMES.filter((action) =>
            measured.capabilities!.includes(action),
          )
        : [],
  };
}

async function latestInventory(client: Client, routerId: string) {
  const [inventory] = await client
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);
  return inventory ?? null;
}

export async function readPartnerRoutersWithDb(
  client: Client,
  ownerRef: string,
  routerId?: string,
  now = new Date(),
) {
  const owned = await client
    .select()
    .from(routers)
    .where(
      and(
        eq(routers.ownerRef, ownerRef),
        isNull(routers.releasedAt),
        ...(routerId ? [eq(routers.id, routerId)] : []),
      ),
    );
  // Explicit check keeps the ownership boundary even for injected providers.
  const visible = owned.filter(
    (r) =>
      r.ownerRef === ownerRef &&
      !r.releasedAt &&
      (!routerId || r.id === routerId),
  );
  const snapshots = await Promise.all(
    visible.map((r) =>
      client.transaction(async (tx) => {
        // Keep ownership stable while decrypting a confidential snapshot. A shared
        // row lock lets parallel reads proceed and blocks an unbind/reclaim race.
        const [current] = await tx
          .select()
          .from(routers)
          .where(
            and(
              eq(routers.id, r.id),
              eq(routers.ownerRef, ownerRef),
              isNull(routers.releasedAt),
            ),
          )
          .for("share")
          .limit(1);
        if (current?.ownerRef !== ownerRef || current.releasedAt) return null;
        return projectPartnerRouter(
          current,
          await latestInventory(tx, current.id),
          now,
        );
      }),
    ),
  );
  return snapshots.filter(
    (snapshot): snapshot is NonNullable<typeof snapshot> => snapshot !== null,
  );
}

export async function queuePartnerActionWithDb(
  client: Client,
  input: z.infer<typeof partnerActionRequestSchema>,
  key: string,
  now = new Date(),
) {
  return client.transaction(async (tx) => {
    // Take a row lock and recheck ownership in the same transaction as the job.
    // Release cannot race a queue operation and leave a foreign owner's job.
    const [router] = await tx
      .update(routers)
      .set({ updatedAt: now })
      .where(
        and(
          eq(routers.id, input.routerId),
          eq(routers.ownerRef, input.ownerRef),
          isNull(routers.releasedAt),
        ),
      )
      .returning();
    if (router?.ownerRef !== input.ownerRef || router.releasedAt)
      return { ok: false, status: 404, body: { error: "not_found" } };
    if (!router.lastAppliedRevisionId)
      return { ok: false, status: 409, body: { error: "not_ready" } };
    const inventory = await latestInventory(tx, router.id);
    const snapshot = projectPartnerRouter(router, inventory, now);
    if (!snapshot.capabilities.includes(input.action))
      return { ok: false, status: 409, body: { error: "not_supported" } };
    const parsedParams = parseConnectActionParams(input.action, input.params);
    if (!parsedParams.success)
      return { ok: false, status: 400, body: { error: "invalid_params" } };
    const params = parsedParams.data;
    if (
      (input.action === "select_entry" || input.action === "set_service") &&
      "entryId" in params &&
      params.entryId !== null &&
      !snapshot.entries.some((entry) => entry.id === params.entryId)
    )
      return { ok: false, status: 400, body: { error: "invalid_params" } };
    if (
      input.action === "set_service" &&
      "service" in params &&
      !snapshot.services?.some((service) => service.id === params.service)
    )
      return { ok: false, status: 400, body: { error: "invalid_params" } };
    const dedupeKey = `partner-action:${key}`;
    // Keyed: the input of set_wifi carries the Wi-Fi password, and this hash
    // is stored in the job row for as long as the job is kept.
    const hashes = {
      requestHash: keyedDigest("partner-action-v1", JSON.stringify(input)),
      legacyHash: createHash("sha256")
        .update(JSON.stringify(input))
        .digest("hex"),
    };
    const requestHash = hashes.requestHash;
    const [existing] = await tx
      .select()
      .from(jobs)
      .where(eq(jobs.dedupeKey, dedupeKey))
      .limit(1);
    if (existing)
      return partnerHashMatches(existing.payload.requestHash, hashes)
        ? {
            ok: true,
            status: 202,
            body: { actionId: existing.id, state: "queued" },
          }
        : {
            ok: false,
            status: 422,
            body: { error: "idempotency_key_mismatch" },
          };
    const pending = await tx
      .select()
      .from(jobs)
      .where(
        and(
          eq(jobs.routerId, router.id),
          inArray(jobs.state, ["queued", "delivered", "running"]),
        ),
      );
    if (pending.length >= 10)
      return { ok: false, status: 429, body: { error: "too_many_actions" } };
    const actionId = randomUUID();
    const [inserted] = await tx
      .insert(jobs)
      .values({
        id: actionId,
        routerId: router.id,
        state: "queued",
        type:
          input.action === "restart_vpn"
            ? "reload_xray_outbound"
            : input.action === "refresh_subscription"
              ? "refresh_xray_subscriptions"
              : "connect_router_action",
        dedupeKey,
        payload: {
          origin: PARTNER_ACTION_ORIGIN,
          actionId,
          action: input.action,
          ownerRef: input.ownerRef,
          // The partner's own key for this action: the router.action result
          // carries it back, so the partner matches the result even when the
          // 202 with this actionId never reached it. Never sent to the router.
          idempotencyKey: key,
          requestHash,
          ...protectPartnerParams(input.action, params, {
            routerId: router.id,
            ownerRef: input.ownerRef,
            actionId,
          }),
        },
      })
      .onConflictDoNothing({ target: jobs.dedupeKey })
      .returning();
    if (!inserted) {
      const [winner] = await tx
        .select()
        .from(jobs)
        .where(eq(jobs.dedupeKey, dedupeKey))
        .limit(1);
      if (!partnerHashMatches(winner?.payload.requestHash, hashes))
        return {
          ok: false,
          status: 422,
          body: { error: "idempotency_key_mismatch" },
        };
      return {
        ok: true,
        status: 202,
        body: { actionId: winner!.id, state: "queued" },
      };
    }
    return { ok: true, status: 202, body: { actionId, state: "queued" } };
  });
}

export const partnerActionCancelRequestSchema = z
  .object({
    // The signature covers the body: bind both path ids into it.
    routerId: z.string().uuid(),
    ownerRef: ownerSchema,
    actionId: z.string().uuid(),
  })
  .strict();

/**
 * Cancel an owner's action ONLY while it still waits for the router: a
 * queued job never reached the router, so cancelling it means it will never
 * run and the partner may honestly say "did not run, try again". Once
 * delivered (or running, or finished) it is not cancelled: the answer is a
 * 409 with the job's state, and the partner keeps waiting for its result.
 */
export async function cancelPartnerActionWithDb(
  client: Client,
  input: z.infer<typeof partnerActionCancelRequestSchema>,
  now = new Date(),
) {
  return client.transaction(async (tx) => {
    // Same ownership lock as queueing: a release cannot race the cancel.
    const [router] = await tx
      .update(routers)
      .set({ updatedAt: now })
      .where(
        and(
          eq(routers.id, input.routerId),
          eq(routers.ownerRef, input.ownerRef),
          isNull(routers.releasedAt),
        ),
      )
      .returning();
    if (router?.ownerRef !== input.ownerRef || router.releasedAt)
      return { ok: false, status: 404, body: { error: "not_found" } };
    const [job] = await tx
      .select()
      .from(jobs)
      .where(and(eq(jobs.id, input.actionId), eq(jobs.routerId, router.id)))
      .limit(1);
    // Only this owner's own partner action: an operator job, or another
    // owner's, is not the partner's to cancel and is not even confirmed.
    if (
      !job ||
      job.routerId !== router.id ||
      job.payload.origin !== PARTNER_ACTION_ORIGIN ||
      job.payload.ownerRef !== input.ownerRef
    )
      return { ok: false, status: 404, body: { error: "action_not_found" } };
    const cancelled = {
      ok: true,
      status: 200,
      body: { actionId: job.id, state: "cancelled" },
    };
    if (job.state === "cancelled") return cancelled;
    if (job.state === "queued") {
      const [updated] = await tx
        .update(jobs)
        .set({ state: "cancelled", completedAt: now })
        .where(and(eq(jobs.id, job.id), eq(jobs.state, "queued")))
        .returning();
      if (updated) return cancelled;
      // A check-in delivered it in between: report what it is now.
      const [current] = await tx
        .select()
        .from(jobs)
        .where(eq(jobs.id, job.id))
        .limit(1);
      return {
        ok: false,
        status: 409,
        body: { error: "not_cancellable", state: current?.state ?? "delivered" },
      };
    }
    return {
      ok: false,
      status: 409,
      body: { error: "not_cancellable", state: job.state },
    };
  });
}

export type PartnerRoutersDeps = {
  api: PartnerApiDeps;
  read: (
    owner: string,
    id?: string,
  ) => Promise<ReturnType<typeof projectPartnerRouter>[]>;
  action: (
    input: z.infer<typeof partnerActionRequestSchema>,
    key: string,
  ) => ReturnType<typeof queuePartnerActionWithDb>;
  cancel: (
    input: z.infer<typeof partnerActionCancelRequestSchema>,
  ) => ReturnType<typeof cancelPartnerActionWithDb>;
};
function defaults(): PartnerRoutersDeps {
  return {
    api: defaultDeps(),
    read: (owner, id) => readPartnerRoutersWithDb(db, owner, id),
    action: (input, key) => queuePartnerActionWithDb(db, input, key),
    cancel: (input) => cancelPartnerActionWithDb(db, input),
  };
}

export async function handlePartnerRoutersRead(
  request: Request,
  routerId?: string,
  deps = defaults(),
) {
  const rawBody = await readRawBody(request);
  if (!rawBody || rawBody.length) return partnerJson({ error: "invalid" }, 400);
  const auth = await authenticatePartnerRequest(
    request,
    rawBody,
    deps.api,
    "GET",
    routerId ? `/api/partner/routers/${routerId}` : "/api/partner/routers",
  );
  if (auth) return auth;
  const owner = ownerSchema.safeParse(
    new URL(request.url).searchParams.get("ownerRef"),
  );
  if (
    !owner.success ||
    (routerId && !z.string().uuid().safeParse(routerId).success)
  )
    return partnerJson({ error: "invalid" }, 400);
  const snapshots = await deps.read(owner.data, routerId);
  return routerId
    ? snapshots[0]
      ? partnerJson(snapshots[0], 200)
      : partnerJson({ error: "not_found" }, 404)
    : partnerJson({ routers: snapshots }, 200);
}

export async function handlePartnerRouterAction(
  request: Request,
  routerId: string,
  deps = defaults(),
) {
  const key = parseIdempotencyKey(request.headers.get("Idempotency-Key"));
  if (!key)
    return partnerJson(
      { error: "invalid", detail: "Idempotency-Key required" },
      400,
    );
  return executePartnerRequest({
    request,
    deps: deps.api,
    method: "POST",
    path: `/api/partner/routers/${routerId}/actions`,
    run: async (body) => {
      const parsed = partnerActionRequestSchema.safeParse(body);
      if (!parsed.success || parsed.data.routerId !== routerId)
        return partnerJson({ error: "invalid" }, 400);
      return deps.action(parsed.data, key);
    },
  });
}

/**
 * POST /api/partner/routers/:routerId/actions/:actionId/cancel
 *
 * 200 {actionId, state: "cancelled"} — it will never run (also on a repeat);
 * 409 {error: "not_cancellable", state} — already delivered/running/finished;
 * 404 {error: "action_not_found"} — no such action of this owner on the router.
 */
export async function handlePartnerRouterActionCancel(
  request: Request,
  routerId: string,
  actionId: string,
  deps = defaults(),
) {
  const key = parseIdempotencyKey(request.headers.get("Idempotency-Key"));
  if (!key)
    return partnerJson(
      { error: "invalid", detail: "Idempotency-Key required" },
      400,
    );
  return executePartnerRequest({
    request,
    deps: deps.api,
    method: "POST",
    path: `/api/partner/routers/${routerId}/actions/${actionId}/cancel`,
    run: async (body) => {
      const parsed = partnerActionCancelRequestSchema.safeParse(body);
      if (
        !parsed.success ||
        parsed.data.routerId !== routerId ||
        parsed.data.actionId !== actionId
      )
        return partnerJson({ error: "invalid" }, 400);
      return deps.cancel(parsed.data);
    },
  });
}
