import "server-only";

import {
  operatorPushAlerts,
  operatorPushSubscriptions,
} from "@vectra/db";
import { and, desc, eq, isNull } from "drizzle-orm";
import type * as WebPushTypes from "web-push";

import { env } from "~/env";
import { db } from "~/server/db";

type PushSeverity = "critical" | "warning" | "info";

type WebPushModule = typeof WebPushTypes;
export type BrowserPushSubscription = WebPushTypes.PushSubscription;

export type BrowserPushPayload = {
  title: string;
  body: string;
  href: string;
  tag: string;
  severity: Extract<PushSeverity, "critical" | "warning">;
  routerId: string;
  routerName: string;
  kind: "offline" | "direct_mode" | "incident";
  createdAt: string;
};

let webPushModulePromise: Promise<WebPushModule> | null = null;

async function loadWebPushModule() {
  webPushModulePromise ??= import(
    /* webpackIgnore: true */
    "web-push"
  ) as Promise<WebPushModule>;

  return webPushModulePromise;
}

function getPushVapidDetails() {
  if (
    !env.VECTRA_WEB_PUSH_PUBLIC_KEY ||
    !env.VECTRA_WEB_PUSH_PRIVATE_KEY ||
    !env.VECTRA_WEB_PUSH_SUBJECT
  ) {
    return null;
  }

  return {
    subject: env.VECTRA_WEB_PUSH_SUBJECT,
    publicKey: env.VECTRA_WEB_PUSH_PUBLIC_KEY,
    privateKey: env.VECTRA_WEB_PUSH_PRIVATE_KEY,
  };
}

export function isBrowserPushConfigured() {
  return getPushVapidDetails() !== null;
}

export function getBrowserPushPublicKey() {
  return getPushVapidDetails()?.publicKey ?? null;
}

function formatPushFailureReason(error: unknown) {
  if (typeof error === "object" && error !== null) {
    const candidate = error as {
      statusCode?: unknown;
      message?: unknown;
    };
    const statusCode =
      typeof candidate.statusCode === "number"
        ? candidate.statusCode
        : null;
    const message =
      typeof candidate.message === "string"
        ? candidate.message
        : "Push delivery failed.";

    if (statusCode) {
      return `${statusCode}: ${message}`;
    }

    return message;
  }

  return "Push delivery failed.";
}

function getPushFailureStatusCode(error: unknown) {
  if (typeof error !== "object" || error === null) {
    return null;
  }

  const candidate = error as { statusCode?: unknown };
  return typeof candidate.statusCode === "number"
    ? candidate.statusCode
    : null;
}

async function markPushDeliveryResult(args: {
  subscriptionId: string;
  disabled: boolean;
  failureReason?: string | null;
}) {
  const now = new Date();

  await db
    .update(operatorPushSubscriptions)
    .set({
      updatedAt: now,
      disabledAt: args.disabled ? now : null,
      lastSuccessfulPushAt:
        args.disabled || args.failureReason ? null : now,
      lastFailureAt: args.failureReason ? now : null,
      lastFailureReason: args.failureReason ?? null,
    })
    .where(eq(operatorPushSubscriptions.id, args.subscriptionId));
}

async function sendPayloadToSubscription(
  subscription: typeof operatorPushSubscriptions.$inferSelect,
  payload: BrowserPushPayload,
) {
  const vapidDetails = getPushVapidDetails();
  if (!vapidDetails || subscription.disabledAt) {
    return;
  }

  try {
    const webpush = await loadWebPushModule();
    await webpush.sendNotification(
      {
        endpoint: subscription.endpoint,
        keys: {
          auth: subscription.auth,
          p256dh: subscription.p256dh,
        },
      },
      JSON.stringify(payload),
      {
        vapidDetails,
        TTL: 60 * 30,
        urgency: payload.severity === "critical" ? "high" : "normal",
      },
    );

    await markPushDeliveryResult({
      subscriptionId: subscription.id,
      disabled: false,
    });
  } catch (error) {
    const statusCode = getPushFailureStatusCode(error);
    await markPushDeliveryResult({
      subscriptionId: subscription.id,
      disabled: statusCode === 404 || statusCode === 410,
      failureReason: formatPushFailureReason(error),
    });
  }
}

export async function listActivePushSubscriptions() {
  return db
    .select()
    .from(operatorPushSubscriptions)
    .where(isNull(operatorPushSubscriptions.disabledAt))
    .orderBy(desc(operatorPushSubscriptions.updatedAt));
}

export async function sendBrowserPushToAll(payload: BrowserPushPayload) {
  const subscriptions = await listActivePushSubscriptions();
  await Promise.all(
    subscriptions.map((subscription) =>
      sendPayloadToSubscription(subscription, payload),
    ),
  );
}

export async function sendRecentPushAlertsToSubscription(
  endpoint: string,
  limit = 3,
) {
  const [subscription] = await db
    .select()
    .from(operatorPushSubscriptions)
    .where(eq(operatorPushSubscriptions.endpoint, endpoint))
    .limit(1);

  if (!subscription || subscription.disabledAt) {
    return;
  }

  const alerts = await db
    .select()
    .from(operatorPushAlerts)
    .where(isNull(operatorPushAlerts.resolvedAt))
    .orderBy(desc(operatorPushAlerts.createdAt))
    .limit(limit);

  for (const alert of alerts.reverse()) {
    await sendPayloadToSubscription(subscription, {
      title: alert.title,
      body: alert.body,
      href: alert.href,
      tag: alert.dedupeKey,
      severity: alert.severity as Extract<PushSeverity, "critical" | "warning">,
      routerId: alert.routerId,
      routerName:
        typeof alert.payload.routerName === "string"
          ? alert.payload.routerName
          : "Роутер",
      kind: alert.kind,
      createdAt: alert.createdAt.toISOString(),
    });
  }
}

export async function upsertOperatorPushSubscription(args: {
  operatorUser: string;
  subscription: BrowserPushSubscription;
  userAgent?: string | null;
}) {
  const now = new Date();
  const [persisted] = await db
    .insert(operatorPushSubscriptions)
    .values({
      operatorUser: args.operatorUser,
      endpoint: args.subscription.endpoint,
      p256dh: args.subscription.keys.p256dh,
      auth: args.subscription.keys.auth,
      userAgent: args.userAgent ?? null,
      disabledAt: null,
      lastFailureAt: null,
      lastFailureReason: null,
      updatedAt: now,
    })
    .onConflictDoUpdate({
      target: operatorPushSubscriptions.endpoint,
      set: {
        operatorUser: args.operatorUser,
        p256dh: args.subscription.keys.p256dh,
        auth: args.subscription.keys.auth,
        userAgent: args.userAgent ?? null,
        disabledAt: null,
        lastFailureAt: null,
        lastFailureReason: null,
        updatedAt: now,
      },
    })
    .returning();

  await sendRecentPushAlertsToSubscription(args.subscription.endpoint);

  return persisted ?? null;
}

export async function deleteOperatorPushSubscription(args: {
  operatorUser: string;
  endpoint: string;
}) {
  const [deleted] = await db
    .delete(operatorPushSubscriptions)
    .where(
      and(
        eq(operatorPushSubscriptions.operatorUser, args.operatorUser),
        eq(operatorPushSubscriptions.endpoint, args.endpoint),
      ),
    )
    .returning();

  return deleted ?? null;
}
