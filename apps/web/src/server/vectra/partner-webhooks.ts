import { recordLoopTick, withLoopLock } from "./background-lock";
import { sweepPartnerOfflineWithDb } from "./partner-router-events";
import {
  type PartnerWebhookEvent,
  partnerWebhookPayloadSchema,
} from "@vectra/contracts";
import { eventLog, partnerWebhooks, routers } from "@vectra/db";
import { and, asc, eq, isNull, lte } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";
import { DEFAULT_PARTNER_ID, findPartner, listPartners } from "~/server/vectra/partner-registry";
import { buildPartnerRequestHeaders } from "~/server/vectra/partner-request-signature";
import { routerPartnerId } from "~/server/vectra/partner-scope";

/**
 * Webhooks to the partners' backends (ADR-0006): router.claimed, router.ready (the
 * first apply of a claim succeeded) and router.failed (it failed).
 *
 * Durable outbox. The request path only INSERTS a row — it never waits on the
 * network — and delivery happens out of band: right after the request (a
 * zero-delay timer) and then from a periodic sweep, with backoff, so a backend
 * outage or a panel restart delays a webhook instead of losing it. Delivery is
 * at-least-once; X-Vectra-Webhook-Id is stable across retries for dedupe.
 *
 * Every row carries the partner it belongs to and is sent to THAT partner's
 * webhook address and secret (partner-registry.ts); a partner without an
 * address gets no rows.
 *
 * Signed with partner signature v2 (partner-request-signature.ts), the scheme
 * the backend→panel direction uses: version, timestamp, request id, method,
 * path, sorted query, sha256(body) and the idempotency key. The webhook row id
 * is BOTH the request id and the Idempotency-Key, so the backend's durable
 * nonce on the request id is also its dedupe: a retry of a webhook it already
 * processed is answered 409, which means "delivered".
 */

// A transaction handle works too: a webhook is queued in the same transaction
// as the change it reports.
type DatabaseClient = Pick<typeof db, "select" | "insert" | "update">;
type WebhookRow = typeof partnerWebhooks.$inferSelect;

export const PARTNER_WEBHOOK_ID_HEADER = "X-Vectra-Webhook-Id";
const DELIVERY_TIMEOUT_MS = 10_000;
// A row being delivered is leased this far into the future so a concurrent
// sweep cannot send it at the same time.
const DELIVERY_LEASE_MS = 60_000;
const DETAIL_MAX_LENGTH = 500;
export const PARTNER_WEBHOOK_SWEEP_INTERVAL_MS = 30_000;
const SWEEP_INTERVAL_MS = PARTNER_WEBHOOK_SWEEP_INTERVAL_MS;

// Delay before attempt N+1, by attempts already made (~16 h in total).
export const PARTNER_WEBHOOK_BACKOFF_SECONDS = [
  30, 120, 600, 1800, 3600, 7200, 14400, 28800,
] as const;
export const PARTNER_WEBHOOK_MAX_ATTEMPTS =
  PARTNER_WEBHOOK_BACKOFF_SECONDS.length + 1;

export function partnerWebhookBackoffSeconds(attemptsMade: number) {
  const index = Math.min(
    Math.max(attemptsMade, 1),
    PARTNER_WEBHOOK_BACKOFF_SECONDS.length,
  );
  return PARTNER_WEBHOOK_BACKOFF_SECONDS[index - 1] ?? 28800;
}

/** Where a partner's webhooks go; null while that partner has no address (or is unknown). */
export function partnerWebhookTarget(partnerId: string) {
  return findPartner(partnerId)?.webhook ?? null;
}

/** Vectra Connect's target, as before there were several partners. */
export function resolvePartnerWebhookTarget() {
  return partnerWebhookTarget(DEFAULT_PARTNER_ID);
}

function anyPartnerWebhook() {
  return listPartners().some((partner) => partner.webhook !== null);
}

/**
 * Queue a webhook for the partner that owns the router. One insert (plus one
 * read of the router's partner when the caller did not pass it), no network.
 * A no-op (null) while that partner has no webhook target.
 */
export async function enqueuePartnerWebhookWithDb(
  client: DatabaseClient,
  input: {
    event: PartnerWebhookEvent;
    routerId: string;
    ownerRef: string;
    detail?: string | Record<string, unknown> | null;
    at?: Date;
    // The partner the event belongs to; read from the router when absent.
    partnerId?: string;
  },
) {
  // Checked before anything touches the database: with no webhook configured
  // anywhere, queuing is free.
  if (!anyPartnerWebhook()) {
    return null;
  }
  const partnerId =
    input.partnerId ??
    routerPartnerId(
      (
        await client
          .select({ partnerId: routers.partnerId })
          .from(routers)
          .where(eq(routers.id, input.routerId))
          .limit(1)
      )[0] ?? {},
    );
  if (!partnerWebhookTarget(partnerId)) {
    return null;
  }

  const at = input.at ?? new Date();
  const detail = typeof input.detail === "string"
    ? input.detail.trim().slice(0, DETAIL_MAX_LENGTH)
    : input.detail;
  const payload = partnerWebhookPayloadSchema.parse({
    event: input.event,
    routerId: input.routerId,
    ownerRef: input.ownerRef,
    at: at.toISOString(),
    ...(detail ? { detail } : {}),
  });

  const [row] = await client
    .insert(partnerWebhooks)
    .values({
      event: input.event,
      routerId: input.routerId,
      partnerId,
      payload,
      nextAttemptAt: at,
    })
    .returning();

  return row?.id ?? null;
}

type FetchLike = (
  input: string,
  init: RequestInit,
) => Promise<Pick<Response, "ok" | "status" | "body">>;

export async function deliverPartnerWebhook(
  row: Pick<WebhookRow, "id" | "payload">,
  target: { url: string; secret: string },
  options: { fetchImpl?: FetchLike; nowMs?: number } = {},
) {
  const fetchImpl = options.fetchImpl ?? (fetch as FetchLike);
  const rawBody = JSON.stringify(row.payload);

  try {
    const response = await fetchImpl(target.url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "user-agent": "VectraProRouterPanel/1",
        [PARTNER_WEBHOOK_ID_HEADER]: row.id,
        ...buildPartnerRequestHeaders(
          target.secret,
          "POST",
          target.url,
          rawBody,
          row.id,
          options.nowMs ?? Date.now(),
          row.id,
        ),
      },
      body: rawBody,
      // A signed webhook goes to the configured URL or nowhere.
      redirect: "manual",
      signal: AbortSignal.timeout(DELIVERY_TIMEOUT_MS),
    });
    void response.body?.cancel().catch(() => undefined);

    // 409 = the backend already processed this request id (its nonce is
    // spent): an earlier attempt was delivered and only its answer got lost.
    return response.ok || response.status === 409
      ? { ok: true as const, status: response.status }
      : {
          ok: false as const,
          status: response.status,
          error: `HTTP ${response.status}`,
        };
  } catch (error) {
    return {
      ok: false as const,
      status: null,
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

/**
 * Deliver every webhook that is due. Never throws for a delivery failure: a
 * failed attempt is rescheduled with backoff, and after the last attempt the
 * row is given up (next_attempt_at null) and journaled.
 *
 * Each row goes to its own partner's address. The due rows are grouped by
 * partner and the groups run side by side (each in order): one partner's slow
 * or dead endpoint, up to 10 s per attempt, must not hold the others' webhooks.
 */
export async function dispatchDuePartnerWebhooksWithDb(
  client: DatabaseClient,
  options: { now?: Date; fetchImpl?: FetchLike; limit?: number } = {},
) {
  const summary = { attempted: 0, delivered: 0, rescheduled: 0, gaveUp: 0 };
  if (!anyPartnerWebhook()) {
    return summary;
  }

  const now = options.now ?? new Date();
  const dueRows = await client
    .select()
    .from(partnerWebhooks)
    .where(
      and(
        isNull(partnerWebhooks.deliveredAt),
        lte(partnerWebhooks.nextAttemptAt, now),
      ),
    )
    .orderBy(asc(partnerWebhooks.nextAttemptAt))
    .limit(options.limit ?? 20);

  const groups = new Map<string, WebhookRow[]>();
  for (const due of dueRows) {
    const id = due.partnerId ?? DEFAULT_PARTNER_ID;
    groups.set(id, [...(groups.get(id) ?? []), due]);
  }

  await Promise.all(
    [...groups].map(async ([partnerId, rows]) => {
      const target = partnerWebhookTarget(partnerId);
      for (const due of rows) {
        if (!target) {
          // The partner lost its webhook address (or is gone): stop retrying
          // this row, and say why.
          await client
            .update(partnerWebhooks)
            .set({ nextAttemptAt: null, lastError: "no_webhook_target" })
            .where(
              and(
                eq(partnerWebhooks.id, due.id),
                isNull(partnerWebhooks.deliveredAt),
              ),
            );
          continue;
        }

        const attempts = due.attempts + 1;
        // Lease by compare-and-swap on the attempt counter: only one dispatcher
        // wins the row, and the lease expires on its own if this process dies.
        const [leased] = await client
          .update(partnerWebhooks)
          .set({
            attempts,
            nextAttemptAt: new Date(now.getTime() + DELIVERY_LEASE_MS),
          })
          .where(
            and(
              eq(partnerWebhooks.id, due.id),
              eq(partnerWebhooks.attempts, due.attempts),
              isNull(partnerWebhooks.deliveredAt),
            ),
          )
          .returning();
        if (!leased) {
          continue;
        }

        summary.attempted += 1;
        const result = await deliverPartnerWebhook(leased, target, {
          fetchImpl: options.fetchImpl,
          nowMs: now.getTime(),
        });

        if (result.ok) {
          summary.delivered += 1;
          await client
            .update(partnerWebhooks)
            .set({
              deliveredAt: new Date(),
              nextAttemptAt: null,
              lastStatus: result.status,
              lastError: null,
            })
            .where(eq(partnerWebhooks.id, leased.id));
          continue;
        }

        const lastError = result.error.slice(0, DETAIL_MAX_LENGTH);
        if (attempts >= PARTNER_WEBHOOK_MAX_ATTEMPTS) {
          summary.gaveUp += 1;
          await client
            .update(partnerWebhooks)
            .set({ nextAttemptAt: null, lastStatus: result.status, lastError })
            .where(eq(partnerWebhooks.id, leased.id));
          await client.insert(eventLog).values({
            routerId: leased.routerId,
            type: "partner.webhook.gave_up",
            severity: "warning",
            message: `Webhook ${leased.event} was not accepted by partner ${partnerId} after ${attempts} attempts.`,
            metadata: {
              webhookId: leased.id,
              event: leased.event,
              partnerId,
              attempts,
              lastStatus: result.status,
              lastError,
            },
          });
          continue;
        }

        summary.rescheduled += 1;
        await client
          .update(partnerWebhooks)
          .set({
            nextAttemptAt: new Date(
              now.getTime() + partnerWebhookBackoffSeconds(attempts) * 1000,
            ),
            lastStatus: result.status,
            lastError,
          })
          .where(eq(partnerWebhooks.id, leased.id));
      }
    }),
  );

  return summary;
}

const globalForWebhooks = globalThis as typeof globalThis & {
  __vectraPartnerWebhookTimer?: ReturnType<typeof setInterval>;
  __vectraPartnerWebhookRunning?: boolean;
  __vectraPartnerWebhookRerun?: boolean;
};

async function runDispatch(client: DatabaseClient, fetchImpl?: FetchLike) {
  if (globalForWebhooks.__vectraPartnerWebhookRunning) {
    globalForWebhooks.__vectraPartnerWebhookRerun = true;
    return;
  }

  globalForWebhooks.__vectraPartnerWebhookRunning = true;
  try {
    do {
      globalForWebhooks.__vectraPartnerWebhookRerun = false;
      await dispatchDuePartnerWebhooksWithDb(client, { fetchImpl });
    } while (globalForWebhooks.__vectraPartnerWebhookRerun);
  } catch (error) {
    console.error("[partner-webhooks]", error);
  } finally {
    globalForWebhooks.__vectraPartnerWebhookRunning = false;
  }
}

/**
 * Start delivering what was just queued, WITHOUT waiting for it: the caller
 * returns immediately and the attempt runs on a later tick. Skipped under test
 * unless `force` is set, like the other background workers.
 */
export function schedulePartnerWebhookDelivery(
  options: { client?: DatabaseClient; fetchImpl?: FetchLike; force?: boolean } = {},
) {
  if ((!options.force && env.NODE_ENV === "test") || !anyPartnerWebhook()) {
    return;
  }

  const timer = setTimeout(() => {
    void runDispatch(options.client ?? db, options.fetchImpl);
  }, 0);
  timer.unref?.();
}

/** Periodic sweep for retries; started from /api/health like the janitor. */
export function startPartnerWebhookDispatcher() {
  if (env.NODE_ENV === "test") {
    return false;
  }
  if (globalForWebhooks.__vectraPartnerWebhookTimer) {
    return true;
  }

  globalForWebhooks.__vectraPartnerWebhookTimer = setInterval(() => {
    if (anyPartnerWebhook()) {
      // The offline sweep runs in one process at a time. Delivery itself is
      // already safe across processes (each row is leased by compare-and-swap),
      // which is why the web may still deliver what it has just queued.
      void withLoopLock("partnerWebhookDispatcher", () =>
        sweepPartnerOfflineWithDb(db)
          .catch(error => console.error("[partner-webhooks] offline sweep failed", error))
          .then(() => runDispatch(db)),
      ).catch(error => console.error("[partner-webhooks]", error));
    } else {
      // Nothing to deliver to: an idle tick is still a tick, so the worker's
      // stall watchdog does not take "no partner configured" for a hang.
      recordLoopTick("partnerWebhookDispatcher", "completed");
    }
  }, SWEEP_INTERVAL_MS);
  globalForWebhooks.__vectraPartnerWebhookTimer.unref?.();
  return true;
}
