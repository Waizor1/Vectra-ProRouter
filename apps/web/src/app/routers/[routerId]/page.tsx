import { notFound, redirect } from "next/navigation";

import { TRPCError } from "@trpc/server";

import { RouterDetailV2 } from "~/features/router-detail";
import {
  hasActiveDirectMode,
  isRouterReachable,
} from "~/server/vectra/router-presence";
import { api } from "~/trpc/server";

export default async function RouterDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ routerId: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const { routerId } = await params;
  const resolvedSearchParams = await searchParams;

  if (!isUuid(routerId)) {
    const resolvedRouterId = await resolveRouterIdFromSelector(routerId);

    if (!resolvedRouterId) {
      notFound();
    }

    redirect(
      `/routers/${resolvedRouterId}${buildSearchSuffix(resolvedSearchParams)}`,
    );
  }

  const surface = await api.draft
    .editorSurface({ routerId })
    .catch((error: unknown) => {
      if (error instanceof TRPCError && error.code === "NOT_FOUND") {
        notFound();
      }

      throw error;
    });

  const routerReachable = isRouterReachable(
    surface.routerRuntimeSummary.lastSeenAt,
  );
  const directModeActive = hasActiveDirectMode(
    (surface.routerRuntimeSummary.status ?? "offline") as
      | "pending"
      | "active"
      | "offline"
      | "direct"
      | "rescue"
      | "disabled",
    surface.routerRuntimeSummary.lastSeenAt,
  );
  const needsRecoveryAction =
    surface.routerRuntimeSummary.status === "direct" ||
    surface.routerRuntimeSummary.passwallEnabled === false ||
    Boolean(surface.routerRuntimeSummary.lastRescueReason);

  return (
    <section>
      <RouterDetailV2
        routerId={surface.routerRuntimeSummary.id}
        initialSurface={surface}
        routerReachable={routerReachable}
        directModeActive={directModeActive}
        needsRecoveryAction={needsRecoveryAction}
      />
    </section>
  );
}

function isUuid(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
    value,
  );
}

async function resolveRouterIdFromSelector(selector: string) {
  const needle = decodeURIComponent(selector).trim().toLowerCase();
  if (!needle) {
    return null;
  }

  const fleet = await api.fleet.list();
  const matches = fleet.filter((router) => {
    const candidates = [
      router.id,
      router.displayName,
      router.hostname,
      router.panelDomain,
      router.deviceIdentifier,
    ]
      .filter(
        (value): value is string =>
          typeof value === "string" && value.trim().length > 0,
      )
      .map((value) => value.toLowerCase());

    return candidates.some(
      (value) => value === needle || value.includes(needle),
    );
  });

  return matches.length === 1 ? (matches[0]?.id ?? null) : null;
}

function buildSearchSuffix(
  searchParams: Record<string, string | string[] | undefined>,
) {
  const params = new URLSearchParams();

  for (const [key, rawValue] of Object.entries(searchParams)) {
    if (Array.isArray(rawValue)) {
      for (const value of rawValue) {
        params.append(key, value);
      }
      continue;
    }

    if (typeof rawValue === "string") {
      params.set(key, rawValue);
    }
  }

  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}
