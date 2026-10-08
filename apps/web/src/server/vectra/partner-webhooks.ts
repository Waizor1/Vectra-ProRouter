import {
  recordLoopStaleFlight,
  recordLoopTick,
  withLoopLock,
} from "./background-lock";
import { sweepPartnerOfflineWithDb } from "./partner-router-events";
import {
  type PartnerWebhookEvent,
  partnerWebhookPayloadSchema,
} from "@vectra/contracts";
import { eventLog, partnerWebhooks, routers } from "@vectra/db";
import { and, asc, eq, isNull, lte, sql } from "drizzle-orm";
import { type AnyPgColumn, alias } from "drizzle-orm/pg-core";

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
 * webhook address and secret (partner-registry.ts). A partner the registry
 * knows without an address gets no rows; one it does not know (yet — a typo
 * in VECTRA_PARTNERS hides every listed partner) gets them, and they wait.
 *
 * Partners never wait for each other: each pass takes every partner's own
 * oldest due rows, and each partner is delivered to by its own loop, at most
 * one at a time per partner in this process. A partner whose endpoint hangs
 * (up to 10 s an attempt) keeps only its own loop busy. A loop that finishes
 * no row for STALE_FLIGHT_MS (an await that never settles) is taken for dead
 * and replaced by the next pass, so it cannot park its partner for good.
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
type DispatchSummary = {
  attempted: number;
  delivered: number;
  rescheduled: number;
  gaveUp: number;
};

export const PARTNER_WEBHOOK_ID_HEADER = "X-Vectra-Webhook-Id";
const DELIVERY_TIMEOUT_MS = 10_000;
// A row being delivered is leased this far into the future so a concurrent
// sweep cannot send it at the same time.
const DELIVERY_LEASE_MS = 60_000;
const DETAIL_MAX_LENGTH = 500;
// A row whose partner has no webhook address is not dropped: the address may
// only be missing because of a typo in VECTRA_PARTNERS (the registry then falls
// back to the default partner alone) that gets fixed on the next deploy. The
// row waits, re-checked hourly, and is given up only after a week, longer than
// any outage worth waiting out (the attempts backoff itself spans ~16 h).
const NO_TARGET_PATIENCE_MS = 7 * 24 * 60 * 60 * 1000;
const NO_TARGET_RECHECK_MS = 60 * 60 * 1000;
const NO_TARGET_ERROR = "no_webhook_target";
export const PARTNER_WEBHOOK_SWEEP_INTERVAL_MS = 30_000;
const SWEEP_INTERVAL_MS = PARTNER_WEBHOOK_SWEEP_INTERVAL_MS;
// Due rows a pass takes of EACH partner.
const DUE_PER_PARTNER = 20;
// A partner's delivery loop that has finished no row for this long is dead: an
// attempt is bounded by DELIVERY_TIMEOUT_MS and its writes take milliseconds,
// so only an await that never settles gets here.
const STALE_FLIGHT_MS = 5 * 60 * 1000;

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
 * A no-op (null) when no partner has a webhook at all, or when the registry
 * knows this partner and it has none. A partner the registry does NOT know is
 * queued all the same: a typo in VECTRA_PARTNERS makes the registry serve
 * Vectra Connect alone, and the partner's events must outlive the typo. Its
 * rows wait for an address (waitForPartnerAddress) and are given up, journaled,
 * after a week.
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
  const partner = findPartner(partnerId);
  if (partner && !partner.webhook) {
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
 * The due rows of every partner, at most `limit` of EACH, oldest first; or,
 * with `partnerId`, the due rows of that partner alone.
 *
 * Ranked per partner in SQL (a row without a partner is Vectra Connect's):
 * across partners, oldest first, one partner's backlog would fill every pass —
 * its failing rows come back due within 30 s, older than anything new — and
 * the others' fresh rows would never be reached. Partners the registry does
 * not know get their share too, so their rows still wait and are given up.
 */
export async function selectDuePartnerWebhooksWithDb(
  client: DatabaseClient,
  now: Date,
  limit = DUE_PER_PARTNER,
  partnerId?: string,
) {
  // Written for the table and for its alias in the ranking below.
  type Columns = Record<"partnerId" | "deliveredAt" | "nextAttemptAt", AnyPgColumn>;
  const partnerOf = (table: Columns) =>
    sql`coalesce(${table.partnerId}, ${DEFAULT_PARTNER_ID})`;
  const due = (table: Columns) =>
    and(isNull(table.deliveredAt), lte(table.nextAttemptAt, now));
  const oldestFirst = [
    asc(partnerWebhooks.nextAttemptAt),
    asc(partnerWebhooks.createdAt),
  ];

  if (partnerId !== undefined) {
    return client
      .select()
      .from(partnerWebhooks)
      .where(and(due(partnerWebhooks), sql`${partnerOf(partnerWebhooks)} = ${partnerId}`))
      .orderBy(...oldestFirst)
      .limit(limit);
  }

  const ranked = alias(partnerWebhooks, "ranked_webhook");
  return client
    .select()
    .from(partnerWebhooks)
    .where(
      and(
        due(partnerWebhooks),
        sql`${partnerWebhooks.id} in (
          select "per_partner"."id" from (
            select ${ranked.id} as "id", row_number() over (
              partition by ${partnerOf(ranked)}
              order by ${ranked.nextAttemptAt}, ${ranked.createdAt}
            ) as "rank"
            from ${partnerWebhooks} ${ranked}
            where ${due(ranked)}
          ) as "per_partner"
          where "per_partner"."rank" <= ${limit})`,
      ),
    )
    .orderBy(...oldestFirst);
}

type DispatchOptions = { now?: Date; fetchImpl?: FetchLike; limit?: number };

// This process's delivery loop per partner: at most one at a time each. A pass
// that finds a partner's loop still running leaves that partner's rows to it
// (`rerun` makes it look again once it is done) and waits for nothing of it —
// unless the loop has finished no row since `progressAt` for STALE_FLIGHT_MS:
// then the pass replaces it. The replaced loop, should its await ever settle,
// stands down before its next row; the lease compare-and-swap keeps it from
// sending a row the new loop has already taken.
type PartnerFlight = { rerun: boolean; startedAt: number; progressAt: number };

const globalForWebhooks = globalThis as typeof globalThis & {
  __vectraPartnerWebhookTimer?: ReturnType<typeof setInterval>;
  __vectraPartnerWebhookFlights?: Map<string, PartnerFlight>;
};

/**
 * Deliver every webhook that is due. Never throws for a delivery failure: a
 * failed attempt is rescheduled with backoff, and after the last attempt the
 * row is given up (next_attempt_at null) and journaled.
 *
 * Each row goes to its own partner's address. Each partner's rows are
 * delivered in order by that partner's own loop, the loops side by side: one
 * partner's slow or dead endpoint, up to 10 s per attempt, holds neither the
 * others' rows in this pass nor any partner's rows in the next. Resolves when
 * the loops THIS pass started are done.
 */
export async function dispatchDuePartnerWebhooksWithDb(
  client: DatabaseClient,
  options: DispatchOptions = {},
) {
  const summary: DispatchSummary = {
    attempted: 0,
    delivered: 0,
    rescheduled: 0,
    gaveUp: 0,
  };
  if (!anyPartnerWebhook()) {
    return summary;
  }

  const limit = options.limit ?? DUE_PER_PARTNER;
  const dueRows = await selectDuePartnerWebhooksWithDb(
    client,
    options.now ?? new Date(),
    limit,
  );

  const groups = new Map<string, WebhookRow[]>();
  for (const due of dueRows) {
    const id = due.partnerId ?? DEFAULT_PARTNER_ID;
    groups.set(id, [...(groups.get(id) ?? []), due]);
  }

  const flights = partnerFlights();
  const started: Array<Promise<void>> = [];
  for (const [partnerId, rows] of groups) {
    const running = flights.get(partnerId);
    if (running && Date.now() - running.progressAt <= STALE_FLIGHT_MS) {
      running.rerun = true;
      continue;
    }
    if (running) {
      // Logged once: the replaced loop is gone from the map. Partner id only.
      console.error(
        "[partner-webhooks] partner %s: delivery made no progress for 5 min; replaced",
        partnerId,
      );
      recordLoopStaleFlight("partnerWebhookDispatcher");
    }
    // Claimed before anything is awaited: a concurrent pass sees it.
    const startedAt = Date.now();
    const flight: PartnerFlight = { rerun: false, startedAt, progressAt: startedAt };
    flights.set(partnerId, flight);
    started.push(
      flyPartner(client, partnerId, rows, flight, { ...options, limit }, summary),
    );
  }

  // allSettled, not all: when one partner's database write fails, the other
  // partners' loops are still left to finish, then the first failure is
  // rethrown.
  const settled = await Promise.allSettled(started);
  for (const outcome of settled) {
    if (outcome.status === "rejected") {
      throw outcome.reason;
    }
  }

  return summary;
}

function partnerFlights() {
  return (globalForWebhooks.__vectraPartnerWebhookFlights ??= new Map<
    string,
    PartnerFlight
  >());
}

/** One partner's delivery loop: its rows, then whatever came due meanwhile. */
async function flyPartner(
  client: DatabaseClient,
  partnerId: string,
  rows: WebhookRow[],
  flight: PartnerFlight,
  options: DispatchOptions & { limit: number },
  summary: DispatchSummary,
) {
  try {
    let batch = rows;
    for (;;) {
      await deliverPartnerRows(client, partnerId, batch, flight, options, summary);
      // Done unless a pass found this loop busy meanwhile. A Connect-only
      // config blip must not touch rows: stop then too, as a pass would. A
      // loop that was replaced while it hung leaves the rest to its successor.
      if (
        !flight.rerun ||
        !anyPartnerWebhook() ||
        partnerFlights().get(partnerId) !== flight
      ) {
        return;
      }
      flight.rerun = false;
      batch = await selectDuePartnerWebhooksWithDb(
        client,
        options.now ?? new Date(),
        options.limit,
        partnerId,
      );
    }
  } finally {
    if (partnerFlights().get(partnerId) === flight) {
      partnerFlights().delete(partnerId);
    }
  }
}

async function deliverPartnerRows(
  client: DatabaseClient,
  partnerId: string,
  rows: WebhookRow[],
  flight: PartnerFlight,
  options: DispatchOptions,
  summary: DispatchSummary,
) {
  const target = partnerWebhookTarget(partnerId);
  for (const due of rows) {
    // Replaced while it hung: the loop that took over has these rows.
    if (partnerFlights().get(partnerId) !== flight) {
      return;
    }
    await deliverPartnerRow(client, partnerId, target, due, options, summary);
    flight.progressAt = Date.now();
  }
}

async function deliverPartnerRow(
  client: DatabaseClient,
  partnerId: string,
  target: ReturnType<typeof partnerWebhookTarget>,
  due: WebhookRow,
  options: DispatchOptions,
  summary: DispatchSummary,
) {
  // The clock of this row, not of the pass: a partner's loop may run for
  // minutes, and a lease or a signature timestamp from its start would be
  // stale by then.
  const now = options.now ?? new Date();
  if (!target) {
    await waitForPartnerAddress(client, due, partnerId, now, summary);
    return;
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
    return;
  }

  summary.attempted += 1;
  const result = await deliverPartnerWebhook(leased, target, {
    fetchImpl: options.fetchImpl,
    nowMs: now.getTime(),
  });

  // What this send learned is written only under the lease it was sent under:
  // the row still at this lease's attempts and not delivered. A flight that
  // was replaced while its send hung can settle after another flight has
  // re-leased the row and written its own outcome; a failure it reports then
  // belongs to a lease that is gone, and must neither reschedule the row nor
  // give it up (nor journal that it did).
  const underThisLease = and(
    eq(partnerWebhooks.id, leased.id),
    eq(partnerWebhooks.attempts, attempts),
    isNull(partnerWebhooks.deliveredAt),
  );

  if (result.ok) {
    // Not tied to this lease's attempts: a 2xx/409 means the partner has the
    // event (the row id is its dedupe key), whichever lease sent it. Leaving
    // it undelivered would send it again, or give up on an event the partner
    // holds. Only a row nobody has marked delivered yet is stamped, and only
    // a row this send stamped is counted.
    const [marked] = await client
      .update(partnerWebhooks)
      .set({
        deliveredAt: new Date(),
        nextAttemptAt: null,
        lastStatus: result.status,
        lastError: null,
      })
      .where(
        and(eq(partnerWebhooks.id, leased.id), isNull(partnerWebhooks.deliveredAt)),
      )
      .returning({ id: partnerWebhooks.id });
    if (marked) {
      summary.delivered += 1;
    }
    return;
  }

  const lastError = result.error.slice(0, DETAIL_MAX_LENGTH);
  if (attempts >= PARTNER_WEBHOOK_MAX_ATTEMPTS) {
    const [givenUp] = await client
      .update(partnerWebhooks)
      .set({ nextAttemptAt: null, lastStatus: result.status, lastError })
      .where(underThisLease)
      .returning({ id: partnerWebhooks.id });
    if (!givenUp) {
      return;
    }
    summary.gaveUp += 1;
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
    return;
  }

  const [rescheduled] = await client
    .update(partnerWebhooks)
    .set({
      nextAttemptAt: new Date(
        now.getTime() + partnerWebhookBackoffSeconds(attempts) * 1000,
      ),
      lastStatus: result.status,
      lastError,
    })
    .where(underThisLease)
    .returning({ id: partnerWebhooks.id });
  if (rescheduled) {
    summary.rescheduled += 1;
  }
}

/**
 * A due row whose partner has no webhook address (it lost it, was never given
 * one, or the registry could not be read). The row waits for the address and
 * is re-checked every NO_TARGET_RECHECK_MS; attempts stay as they were, since
 * nothing was sent. The journal hears about it once per wait, not hourly, and
 * again when the row is finally given up after NO_TARGET_PATIENCE_MS.
 */
async function waitForPartnerAddress(
  client: DatabaseClient,
  due: WebhookRow,
  partnerId: string,
  now: Date,
  summary: { gaveUp: number },
) {
  // Never touch a row another dispatcher has just delivered.
  const pending = and(
    eq(partnerWebhooks.id, due.id),
    isNull(partnerWebhooks.deliveredAt),
  );
  const metadata = { webhookId: due.id, event: due.event, partnerId };

  if (now.getTime() - due.createdAt.getTime() > NO_TARGET_PATIENCE_MS) {
    summary.gaveUp += 1;
    await client
      .update(partnerWebhooks)
      .set({ nextAttemptAt: null, lastError: NO_TARGET_ERROR })
      .where(pending);
    await client.insert(eventLog).values({
      routerId: due.routerId,
      type: "partner.webhook.gave_up",
      severity: "warning",
      message: `Webhook ${due.event} was given up: partner ${partnerId} has had no webhook address for a week.`,
      metadata: { ...metadata, reason: NO_TARGET_ERROR },
    });
    return;
  }

  await client
    .update(partnerWebhooks)
    .set({
      nextAttemptAt: new Date(now.getTime() + NO_TARGET_RECHECK_MS),
      lastError: NO_TARGET_ERROR,
    })
    .where(pending);
  if (due.lastError !== NO_TARGET_ERROR) {
    await client.insert(eventLog).values({
      routerId: due.routerId,
      type: "partner.webhook.no_target",
      severity: "warning",
      message: `Webhook ${due.event} is waiting: partner ${partnerId} has no webhook address.`,
      metadata,
    });
  }
}

/**
 * One pass. Never gated as a whole: a pass only skips the partners whose own
 * loop is still running (they pick their new rows up themselves), so a hung
 * partner never holds another partner's next pass.
 */
async function runDispatch(client: DatabaseClient, fetchImpl?: FetchLike) {
  try {
    await dispatchDuePartnerWebhooksWithDb(client, { fetchImpl });
  } catch (error) {
    console.error("[partner-webhooks]", error);
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

type SweepClient = Pick<typeof db, "select" | "insert" | "update" | "transaction">;

/**
 * One tick of the periodic sweep. The offline sweep runs in one process at a
 * time, under the loop lock. Delivery is started from the tick and NOT
 * awaited in it: a partner whose endpoint hangs would otherwise hold the lock,
 * and with it every other partner's next tick (their retries wait for the
 * sweep). Delivery needs no lock: each row is leased by compare-and-swap, which
 * is also why the web may deliver what it has just queued.
 */
export async function runPartnerWebhookTick(
  options: {
    client?: SweepClient;
    fetchImpl?: FetchLike;
    lock?: typeof withLoopLock;
  } = {},
) {
  if (!anyPartnerWebhook()) {
    // Nothing to deliver to: an idle tick is still a tick, so the worker's
    // stall watchdog does not take "no partner configured" for a hang.
    recordLoopTick("partnerWebhookDispatcher", "completed");
    return;
  }
  const client = options.client ?? db;
  await (options.lock ?? withLoopLock)("partnerWebhookDispatcher", async () => {
    await sweepPartnerOfflineWithDb(client).catch((error) =>
      console.error("[partner-webhooks] offline sweep failed", error),
    );
    void runDispatch(client, options.fetchImpl);
  });
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
    void runPartnerWebhookTick().catch((error) =>
      console.error("[partner-webhooks]", error),
    );
  }, SWEEP_INTERVAL_MS);
  globalForWebhooks.__vectraPartnerWebhookTimer.unref?.();
  return true;
}
