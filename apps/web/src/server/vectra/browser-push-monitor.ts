import { operatorPushAlerts } from "@vectra/db";
import { eq, isNull } from "drizzle-orm";

import { env } from "~/env";
import { db } from "~/server/db";

import {
  sendBrowserPushToAll,
  type BrowserPushPayload,
  isBrowserPushConfigured,
} from "./browser-push";
import { type FleetMonitoringSnapshot } from "./fleet-monitoring";
import { loadFleetMonitoringSnapshot } from "./fleet-monitoring-data";

type PushCandidate = {
  dedupeKey: string;
  routerId: string;
  routerName: string;
  kind: "offline" | "direct_mode" | "incident";
  severity: "critical" | "warning";
  title: string;
  body: string;
  href: string;
  createdAt: string;
};

function toPushCandidateKey(
  alert: FleetMonitoringSnapshot["alerts"][number],
  generatedAt: string,
) {
  const openedAt = alert.openedAt ?? generatedAt;
  return `${alert.id}:${openedAt}`;
}

export function buildFleetPushCandidates(snapshot: FleetMonitoringSnapshot) {
  return snapshot.alerts.flatMap((alert) => {
    if (
      (alert.kind !== "offline" &&
        alert.kind !== "direct_mode" &&
        alert.kind !== "incident") ||
      alert.severity === "info"
    ) {
      return [];
    }

    return [
      {
        dedupeKey: toPushCandidateKey(alert, snapshot.generatedAt),
        routerId: alert.routerId,
        routerName: alert.routerName,
        kind: alert.kind,
        severity: alert.severity,
        title: alert.title,
        body: alert.description,
        href: alert.href,
        createdAt: alert.openedAt ?? snapshot.generatedAt,
      } satisfies PushCandidate,
    ];
  });
}

function toBrowserPushPayload(candidate: PushCandidate): BrowserPushPayload {
  return {
    title: candidate.title,
    body: `${candidate.routerName} · ${candidate.body}`,
    href: candidate.href,
    tag: candidate.dedupeKey,
    severity: candidate.severity,
    routerId: candidate.routerId,
    routerName: candidate.routerName,
    kind: candidate.kind,
    createdAt: candidate.createdAt,
  };
}

export async function reconcileFleetPushAlerts(now = new Date()) {
  if (!isBrowserPushConfigured()) {
    return {
      emitted: 0,
      resolved: 0,
      tracked: 0,
    };
  }

  const snapshot = await loadFleetMonitoringSnapshot(db, now);
  const candidates = buildFleetPushCandidates(snapshot);
  const unresolvedAlerts = await db
    .select()
    .from(operatorPushAlerts)
    .where(isNull(operatorPushAlerts.resolvedAt));

  const activeKeys = new Set(candidates.map((candidate) => candidate.dedupeKey));
  const unresolvedKeys = new Set(
    unresolvedAlerts.map((alert) => alert.dedupeKey),
  );

  let emitted = 0;
  for (const candidate of candidates) {
    if (unresolvedKeys.has(candidate.dedupeKey)) {
      continue;
    }

    const [inserted] = await db
      .insert(operatorPushAlerts)
      .values({
        routerId: candidate.routerId,
        kind: candidate.kind,
        severity: candidate.severity,
        dedupeKey: candidate.dedupeKey,
        title: candidate.title,
        body: candidate.body,
        href: candidate.href,
        payload: {
          routerName: candidate.routerName,
        },
        createdAt: new Date(candidate.createdAt),
      })
      .onConflictDoNothing({
        target: operatorPushAlerts.dedupeKey,
      })
      .returning();

    if (!inserted) {
      continue;
    }

    emitted += 1;
    await sendBrowserPushToAll(toBrowserPushPayload(candidate));
  }

  let resolved = 0;
  for (const unresolved of unresolvedAlerts) {
    if (activeKeys.has(unresolved.dedupeKey)) {
      continue;
    }

    await db
      .update(operatorPushAlerts)
      .set({
        resolvedAt: now,
      })
      .where(eq(operatorPushAlerts.id, unresolved.id));

    resolved += 1;
  }

  return {
    emitted,
    resolved,
    tracked: candidates.length,
  };
}

const globalForPushMonitor = globalThis as typeof globalThis & {
  __vectraBrowserPushMonitorTimer?: ReturnType<typeof setInterval>;
  __vectraBrowserPushMonitorRunning?: boolean;
};

export function startBrowserPushMonitor() {
  if (env.NODE_ENV === "test" || !isBrowserPushConfigured()) {
    return;
  }

  if (globalForPushMonitor.__vectraBrowserPushMonitorTimer) {
    return;
  }

  const run = async () => {
    if (globalForPushMonitor.__vectraBrowserPushMonitorRunning) {
      return;
    }

    globalForPushMonitor.__vectraBrowserPushMonitorRunning = true;
    try {
      await reconcileFleetPushAlerts(new Date());
    } catch (error) {
      console.error("[browser-push-monitor]", error);
    } finally {
      globalForPushMonitor.__vectraBrowserPushMonitorRunning = false;
    }
  };

  void run();

  globalForPushMonitor.__vectraBrowserPushMonitorTimer = setInterval(
    () => void run(),
    env.VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS * 1000,
  );
  globalForPushMonitor.__vectraBrowserPushMonitorTimer.unref?.();
}
