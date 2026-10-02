import type { PartnerWebhookEvent, RouterInventory } from "@vectra/contracts";
import { type jobs, routers, routerInventorySnapshots } from "@vectra/db";
import { and, desc, eq, isNotNull, isNull, lt, ne } from "drizzle-orm";
import type { db } from "~/server/db";
import { enqueuePartnerWebhookWithDb } from "./partner-webhooks";

type Client = Pick<typeof db, "select" | "insert" | "update" | "transaction">;
const PARTNER_ACTION_DEDUPE_PREFIX = "partner-action:";
type Router = typeof routers.$inferSelect;
export function reportedPartnerTransitions(
  previous: RouterInventory | null,
  next: RouterInventory,
) {
  const events: Array<{
    event: PartnerWebhookEvent;
    detail?: Record<string, unknown>;
  }> = [];
  const before = previous?.connect?.verdict;
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

export async function notifyPartnerCheckInWithDb(
  client: Client,
  previousRouter: Router,
  next: RouterInventory,
  now = new Date(),
) {
  if (!previousRouter.ownerRef || previousRouter.releasedAt) return;
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
    const transitions = reportedPartnerTransitions(
      prior?.payload ?? null,
      next,
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
  await client.transaction(async (tx) => {
    const [current] = await tx
      .update(routers)
      .set({ updatedAt: new Date() })
      .where(
        and(
          eq(routers.id, args.job.routerId),
          eq(routers.ownerRef, args.ownerRef!),
          isNull(routers.releasedAt),
        ),
      )
      .returning();
    if (current?.ownerRef !== args.ownerRef || current.releasedAt) return;
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
