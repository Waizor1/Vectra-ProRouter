import type { PartnerWebhookEvent, RouterInventory } from "@vectra/contracts";
import { type jobs, routers, routerInventorySnapshots } from "@vectra/db";
import { and, desc, eq, isNotNull, isNull, lt, ne, sql } from "drizzle-orm";
import type { db } from "~/server/db";
import { PARTNER_ACTION_DEDUPE_PREFIX } from "./partner-action-key";
import { DEFAULT_PARTNER_ID } from "./partner-registry";
import { routerOwnedByPartner, routerPartnerId } from "./partner-scope";
import { enqueuePartnerWebhookWithDb } from "./partner-webhooks";

type Client = Pick<typeof db, "select" | "insert" | "update" | "transaction">;
type Router = typeof routers.$inferSelect;
// previousVerdict is the router's last MEASURED verdict, which may be older
// than previous: a check-in without one (vctl leaves it out while its own
// measurement is stale) is not a state, and compared with it a
// down → (none) → ok never said vpn_up.
export function reportedPartnerTransitions(
  previous: RouterInventory | null,
  next: RouterInventory,
  previousVerdict: string | undefined = previous?.connect?.verdict,
) {
  const events: Array<{
    event: PartnerWebhookEvent;
    detail?: Record<string, unknown>;
  }> = [];
  const before = previousVerdict;
  const after = next.connect?.verdict;
  const working = (v: string | undefined) => v === "ok" || v === "reserve";
  const failed = (v: string | undefined) =>
    v === "down" || v === "leak" || v === "stopped" || v === "direct";
  // Report only observed transitions; absence never becomes an up/down event.
  if (before && after && before !== after) {
    if (working(before) && failed(after))
      events.push({ event: "router.vpn_down", detail: { verdict: after } });
    if (failed(before) && working(after))
      events.push({ event: "router.vpn_up", detail: { verdict: after } });
  }
  const from =
    previous?.controllerRuntimeVersion ?? previous?.controllerVersion;
  const to = next.controllerRuntimeVersion ?? next.controllerVersion;
  if (from && to && from !== to)
    events.push({ event: "router.updated", detail: { from, to } });
  return events;
}

/**
 * The router's newest stored snapshot as read here, before this check-in's
 * own is written — `null` when it has none, `undefined` when it was not read.
 * The check-in hands it to the snapshot dedupe instead of reading it again.
 */
export type PartnerCheckInReadback = {
  latestSnapshot?: { payload: RouterInventory; createdAt: Date } | null;
};

export async function notifyPartnerCheckInWithDb(
  client: Client,
  previousRouter: Router,
  next: RouterInventory,
  now = new Date(),
): Promise<PartnerCheckInReadback> {
  const readback: PartnerCheckInReadback = {};
  if (!previousRouter.ownerRef || previousRouter.releasedAt) return readback;
  await client.transaction(async (tx) => {
    const [current] = await tx
      .update(routers)
      .set({ updatedAt: now })
      .where(
        and(
          eq(routers.id, previousRouter.id),
          eq(routers.ownerRef, previousRouter.ownerRef!),
          isNull(routers.releasedAt),
        ),
      )
      .returning();
    if (current?.ownerRef !== previousRouter.ownerRef || current.releasedAt)
      return;
    const [prior] = await tx
      .select()
      .from(routerInventorySnapshots)
      .where(eq(routerInventorySnapshots.routerId, previousRouter.id))
      .orderBy(desc(routerInventorySnapshots.createdAt))
      .limit(1);
    readback.latestSnapshot = prior
      ? { payload: prior.payload, createdAt: prior.createdAt }
      : null;
    let previousVerdict = prior?.payload?.connect?.verdict;
    if (prior && !previousVerdict) {
      const [measured] = await tx
        .select({ payload: routerInventorySnapshots.payload })
        .from(routerInventorySnapshots)
        .where(
          and(
            eq(routerInventorySnapshots.routerId, previousRouter.id),
            sql`${routerInventorySnapshots.payload}->'connect'->>'verdict' is not null`,
          ),
        )
        .orderBy(desc(routerInventorySnapshots.createdAt))
        .limit(1);
      previousVerdict = measured?.payload?.connect?.verdict;
    }
    const transitions = reportedPartnerTransitions(
      prior?.payload ?? null,
      next,
      previousVerdict,
    );
    if (
      !previousRouter.lastSeenAt ||
      now.getTime() - previousRouter.lastSeenAt.getTime() > 180_000
    )
      transitions.unshift({ event: "router.online" });
    for (const change of transitions)
      await enqueuePartnerWebhookWithDb(tx, {
        ...change,
        routerId: previousRouter.id,
        ownerRef: previousRouter.ownerRef!,
        at: now,
      });
  });
  return readback;
}

export async function sweepPartnerOfflineWithDb(
  client: Client,
  now = new Date(),
) {
  const cutoff = new Date(now.getTime() - 180_000);
  const stale = await client
    .select()
    .from(routers)
    .where(
      and(
        lt(routers.lastSeenAt, cutoff),
        ne(routers.status, "offline"),
        ne(routers.status, "disabled"),
        isNull(routers.releasedAt),
        // Only owned routers have an owner to tell; the fleet is not read.
        isNotNull(routers.ownerRef),
      ),
    );
  for (const router of stale) {
    if (
      !router.ownerRef ||
      !router.lastSeenAt ||
      router.lastSeenAt >= cutoff ||
      router.status === "offline" ||
      router.status === "disabled"
    )
      continue;
    await client.transaction(async (tx) => {
      const [updated] = await tx
        .update(routers)
        .set({ status: "offline" })
        .where(
          and(
            eq(routers.id, router.id),
            eq(routers.ownerRef, router.ownerRef!),
            eq(routers.lastSeenAt, router.lastSeenAt!),
            ne(routers.status, "offline"),
            ne(routers.status, "disabled"),
            isNull(routers.releasedAt),
          ),
        )
        .returning();
      if (updated)
        await enqueuePartnerWebhookWithDb(tx, {
          event: "router.offline",
          routerId: router.id,
          ownerRef: router.ownerRef!,
          at: now,
        });
    });
  }
}

export async function notifyPartnerActionResultWithDb(
  client: Client,
  args: {
    job: typeof jobs.$inferSelect;
    ownerRef: string | null;
    status: string;
    // The router's reason for a failure; only a plain code is passed on.
    code?: string | null;
  },
) {
  const payload = args.job.payload;
  const code =
    args.status !== "success" && typeof args.code === "string" && /^[a-z][a-z0-9_]{0,47}$/.test(args.code)
      ? args.code
      : undefined;
  if (
    args.status === "accepted" ||
    payload.origin !== "partner_action" ||
    !args.ownerRef ||
    payload.ownerRef !== args.ownerRef ||
    typeof payload.actionId !== "string"
  )
    return;
  // The partner's Idempotency-Key of the action (stored on the job; an older
  // job only has it in its dedupe key): it matches the result to its own
  // action even when the 202 carrying actionId never reached it.
  const idempotencyKey =
    typeof payload.idempotencyKey === "string"
      ? payload.idempotencyKey
      : args.job.dedupeKey?.startsWith(PARTNER_ACTION_DEDUPE_PREFIX)
        ? args.job.dedupeKey.slice(PARTNER_ACTION_DEDUPE_PREFIX.length)
        : undefined;
  // The partner that queued the action; a job from before there were
  // partners is Vectra Connect's. A router that changed hands since is not
  // told about the previous partner's action.
  const partnerId =
    typeof payload.partnerId === "string" ? payload.partnerId : DEFAULT_PARTNER_ID;
  await client.transaction(async (tx) => {
    const [current] = await tx
      .update(routers)
      .set({ updatedAt: new Date() })
      .where(
        and(
          eq(routers.id, args.job.routerId),
          eq(routers.ownerRef, args.ownerRef!),
          routerOwnedByPartner(partnerId),
          isNull(routers.releasedAt),
        ),
      )
      .returning();
    if (
      current?.ownerRef !== args.ownerRef ||
      current.releasedAt ||
      routerPartnerId(current) !== partnerId
    )
      return;
    await enqueuePartnerWebhookWithDb(tx, {
      event: "router.action",
      routerId: args.job.routerId,
      ownerRef: args.ownerRef!,
      detail: {
        actionId: payload.actionId,
        ...(idempotencyKey ? { idempotencyKey } : {}),
        state: args.status === "success" ? "applied" : "failed",
        ...(code ? { detail: code } : {}),
      },
    });
  });
}
