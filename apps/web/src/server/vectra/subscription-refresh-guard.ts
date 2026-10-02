/**
 * Undo half of "detected and undone".
 *
 * The self-repair chain ends in a subscription refresh: when every candidate a
 * slot would accept sits on a host the fleet has measured as dead, no rebinding
 * helps and only the provider can fix it. That last link was written but left
 * switched off, with the reason stated in route-health-verifier: an unattended
 * refresh can take a customer's node list away, and "reporting the state is
 * safe; automatically acting on it is not, until the wipe can be detected and
 * undone."
 *
 * Detection already exists, on the router. `VerifySubscriptionRefresh` judges a
 * refresh by what it left behind rather than by its exit code — subscribe.lua
 * exits 0 even when it crashes or imports a refusal placeholder — and reports
 * the verdict to the panel under `subscriptionRefresh`. Nothing consumed it.
 *
 * This module is the undo. It turns that verdict into a restore of the node
 * list the router held before the refresh, so the rescue can run unattended:
 * the worst case stops being "a customer loses every node" and becomes "a
 * refresh achieved nothing and the operator is told".
 */

import { healthIncidents, jobs } from "@vectra/db";
import { and, desc, eq } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

type DatabaseClient = typeof appDb;

export const SUBSCRIPTION_REFRESH_JOB_TYPE = "refresh_subscriptions" as const;

/**
 * Reuses the existing incident kind rather than adding one.
 *
 * "subscription_degraded" is exactly what a wiped or placeholder node list is,
 * it already renders everywhere in the panel, and a new enum value would need
 * a migration to say the same thing.
 */
const REFRESH_DAMAGE_INCIDENT_TYPE = "subscription_degraded" as const;

/**
 * The controller's verdict, as `SubscriptionRefreshOutcome` serialises it.
 *
 * Every field is optional on purpose: this crosses a version boundary, and an
 * older controller that reports nothing must read as "no verdict" rather than
 * as a wipe.
 */
export type SubscriptionRefreshVerdict = {
  ok?: boolean;
  failure?: string;
  measured?: boolean;
  nodesBefore?: number;
  nodesAfter?: number;
  placeholderNodes?: number;
  logErrors?: string[];
};

function readNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function readSubscriptionRefreshVerdict(
  payload: unknown,
): SubscriptionRefreshVerdict | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const raw = (payload as Record<string, unknown>).subscriptionRefresh;
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  return {
    ok: typeof record.ok === "boolean" ? record.ok : undefined,
    failure: typeof record.failure === "string" ? record.failure : undefined,
    measured:
      typeof record.measured === "boolean" ? record.measured : undefined,
    nodesBefore: readNumber(record.nodesBefore) ?? undefined,
    nodesAfter: readNumber(record.nodesAfter) ?? undefined,
    placeholderNodes: readNumber(record.placeholderNodes) ?? undefined,
    logErrors: Array.isArray(record.logErrors)
      ? record.logErrors.filter(
          (entry): entry is string => typeof entry === "string",
        )
      : undefined,
  };
}

/**
 * True only for the failure this module exists to undo: the refresh ran and
 * left the router holding fewer usable nodes than it started with.
 *
 * `measured` is the gate. The controller sets it only when it actually counted
 * the node sections afterwards; a refresh it could not measure is reported
 * unmeasured and left passing, and inventing a restore from a missing
 * measurement would be its own outage.
 *
 * A script error alone is NOT a wipe. subscribe.lua dying before the network
 * request leaves the previous node list untouched — that is the yuranrod-msk
 * shape from 2026-08-24 — so restoring would queue a pointless apply. Only the
 * two verdicts that describe the node list itself count.
 */
export function refreshDestroyedNodeList(
  verdict: SubscriptionRefreshVerdict | null,
): boolean {
  if (!verdict || verdict.ok === true || verdict.measured !== true) {
    return false;
  }
  const before = verdict.nodesBefore ?? -1;
  if (before <= 0) {
    // Nothing to lose, or the pre-count was unavailable (-1). Either way the
    // refresh cannot be shown to have destroyed anything.
    return false;
  }
  const placeholders = verdict.placeholderNodes ?? 0;
  const after = verdict.nodesAfter ?? -1;
  return placeholders > 0 || after === 0;
}

export function describeRefreshDamage(
  verdict: SubscriptionRefreshVerdict,
): string {
  const before = verdict.nodesBefore ?? 0;
  const placeholders = verdict.placeholderNodes ?? 0;
  if (placeholders > 0) {
    return `Обновление подписки вернуло заглушки провайдера: ${placeholders} из ${verdict.nodesAfter ?? 0} узлов. Было ${before}. Список узлов восстановлен из сохранённой ревизии.`;
  }
  return `Обновление подписки стёрло список узлов: было ${before}, осталось ${verdict.nodesAfter ?? 0}. Список узлов восстановлен из сохранённой ревизии.`;
}

/**
 * The revision the rescue recorded before queueing the refresh.
 *
 * Read from the job's own payload rather than looked up now: by the time the
 * result arrives the router has already re-imported its wiped list, so "the
 * latest import" is the damage, not the thing to restore to.
 */
export function readRestoreRevisionId(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const value = (payload as Record<string, unknown>).restoreRevisionId;
  return typeof value === "string" && value.length > 0 ? value : null;
}

async function openRefreshDamageIncident(
  client: DatabaseClient,
  routerId: string,
  reason: string,
) {
  const [existing] = await client
    .select()
    .from(healthIncidents)
    .where(
      and(
        eq(healthIncidents.routerId, routerId),
        eq(healthIncidents.state, "open"),
        eq(healthIncidents.type, REFRESH_DAMAGE_INCIDENT_TYPE),
      ),
    )
    .orderBy(desc(healthIncidents.openedAt))
    .limit(1);

  if (existing) {
    return existing;
  }

  const [incident] = await client
    .insert(healthIncidents)
    .values({
      routerId,
      type: REFRESH_DAMAGE_INCIDENT_TYPE,
      state: "open",
      reason,
    })
    .returning();

  return incident ?? null;
}

export type SubscriptionRefreshGuardOutcome = {
  restored: boolean;
  revisionId: string | null;
  reason: string | null;
};

/**
 * Called for every finished `refresh_subscriptions` job.
 *
 * Returns without acting for the overwhelmingly common cases — a refresh that
 * worked, one the controller could not measure, one from a controller too old
 * to report a verdict — so this is safe to call unconditionally.
 */
export async function handleSubscriptionRefreshResult(
  client: DatabaseClient,
  input: {
    routerId: string;
    jobPayload: unknown;
    resultPayload: unknown;
  },
  queueApply: (args: {
    routerId: string;
    desiredRevisionId: string;
  }) => Promise<unknown>,
): Promise<SubscriptionRefreshGuardOutcome> {
  const verdict = readSubscriptionRefreshVerdict(input.resultPayload);
  if (!refreshDestroyedNodeList(verdict) || !verdict) {
    return { restored: false, revisionId: null, reason: null };
  }

  const reason = describeRefreshDamage(verdict);
  await openRefreshDamageIncident(client, input.routerId, reason);

  const revisionId = readRestoreRevisionId(input.jobPayload);
  if (!revisionId) {
    // Nothing recorded to restore to — an operator-triggered refresh, or one
    // queued before this guard shipped. The incident above is still raised, so
    // the damage is never silent.
    return { restored: false, revisionId: null, reason };
  }

  await queueApply({ routerId: input.routerId, desiredRevisionId: revisionId });

  return { restored: true, revisionId, reason };
}

/**
 * The md5 lock is why an unattended refresh would otherwise achieve nothing.
 *
 * PassWall stores the hash of the last subscription payload and skips the
 * import when the new one matches — "No changes, no update required". The
 * provider's feed is not deterministic between requests, so a router whose
 * slot is stranded CAN be handed different hosts, but only if the lock is
 * cleared first. Measured 2026-09-22 on ar-filicity: a plain refresh returned
 * the identical dead node list, and the same refresh after clearing md5
 * returned nl3/ru15 in place of the dead nl1/nl4 and the stranded slot came
 * back to 204.
 *
 * Guarded on the router rather than here: hwid must be '1' or the provider
 * answers with a placeholder that wipes the list, and a router holding almost
 * no nodes is already broken in a way a re-roll will not fix.
 */
export const SUBSCRIPTION_MD5_RESET_COMMAND = [
  'S=$(uci show passwall2 | grep "=subscribe_list" | cut -d. -f2 | cut -d= -f1 | head -1)',
  'H=$(uci get passwall2.$S.hwid 2>/dev/null)',
  'N=$(uci show passwall2 | grep -c "=nodes")',
  'if [ -n "$S" ] && [ "$H" = "1" ] && [ "$N" -gt 5 ]; then uci -q delete passwall2.$S.md5; uci commit passwall2; echo "md5-cleared nodes=$N"; else echo "skipped hwid=$H nodes=$N"; fi',
].join("; ");

export async function queueSubscriptionMd5Reset(
  client: DatabaseClient,
  routerId: string,
) {
  const [job] = await client
    .insert(jobs)
    .values({
      routerId,
      type: "run_terminal_command",
      state: "queued",
      payload: {
        command: SUBSCRIPTION_MD5_RESET_COMMAND,
        timeoutSeconds: 45,
        reason: "subscription-rescue-force-reroll",
      },
    })
    .returning();

  return job ?? null;
}
