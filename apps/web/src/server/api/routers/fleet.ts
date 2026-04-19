import {
  eventLog,
  healthIncidents,
  jobs,
  jobResults,
  passwallAppliedRevisions,
  passwallDesiredRevisions,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { TRPCError } from "@trpc/server";
import { desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import { db } from "~/server/db";
import { loadFleetMonitoringSnapshot } from "~/server/vectra/fleet-monitoring-data";
import { buildConfigTrustState } from "~/server/vectra/config-trust";
import {
  getEffectiveRouterStatus,
  isRouterReachable,
} from "~/server/vectra/router-presence";
import { sanitizeRevisionForClient } from "~/server/vectra/router-control";
import { describeRouterSupport } from "~/server/vectra/support";

async function getLatestSnapshots(routerIds: string[]) {
  if (routerIds.length === 0) {
    return new Map<string, typeof routerInventorySnapshots.$inferSelect>();
  }

  const rows = await db
    .select()
    .from(routerInventorySnapshots)
    .where(inArray(routerInventorySnapshots.routerId, routerIds))
    .orderBy(desc(routerInventorySnapshots.createdAt));

  const latest = new Map<
    string,
    typeof routerInventorySnapshots.$inferSelect
  >();
  for (const row of rows) {
    if (!latest.has(row.routerId)) {
      latest.set(row.routerId, row);
    }
  }

  return latest;
}

export const fleetRouter = createTRPCRouter({
  overview: protectedProcedure.query(async ({ ctx }) => {
    const [routerRows, incidentRows, jobRows] = await Promise.all([
      ctx.db.select().from(routers),
      ctx.db.select().from(healthIncidents),
      ctx.db.select().from(jobs),
    ]);

    const byStatus = Object.fromEntries(
      ["pending", "active", "offline", "direct", "rescue", "disabled"].map(
        (status) => [status, 0],
      ),
    ) as Record<string, number>;

    for (const router of routerRows) {
      const effectiveStatus = getEffectiveRouterStatus(
        router.status,
        router.lastSeenAt,
      );
      byStatus[effectiveStatus] = (byStatus[effectiveStatus] ?? 0) + 1;
    }

    return {
      totalRouters: routerRows.length,
      byStatus,
      openIncidents: incidentRows.filter(
        (incident) => incident.state === "open",
      ).length,
      queuedJobs: jobRows.filter((job) =>
        ["queued", "delivered", "running"].includes(job.state),
      ).length,
    };
  }),

  list: protectedProcedure.query(async ({ ctx }) => {
    const routerRows = await ctx.db
      .select()
      .from(routers)
      .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));

    const routerIds = routerRows.map((router) => router.id);
    const [snapshots, incidentRows, revisionRows, queuedJobRows] =
      await Promise.all([
        getLatestSnapshots(routerIds),
        routerIds.length
          ? ctx.db
              .select()
              .from(healthIncidents)
              .where(inArray(healthIncidents.routerId, routerIds))
              .orderBy(desc(healthIncidents.openedAt))
          : Promise.resolve([]),
        routerIds.length
          ? ctx.db
              .select()
              .from(passwallDesiredRevisions)
              .where(inArray(passwallDesiredRevisions.routerId, routerIds))
              .orderBy(desc(passwallDesiredRevisions.createdAt))
          : Promise.resolve([]),
        routerIds.length
          ? ctx.db
              .select()
              .from(jobs)
              .where(inArray(jobs.routerId, routerIds))
              .orderBy(desc(jobs.createdAt))
          : Promise.resolve([]),
      ]);

    const incidentMap = new Map<string, typeof healthIncidents.$inferSelect>();
    const revisionMap = new Map<
      string,
      typeof passwallDesiredRevisions.$inferSelect
    >();
    const revisionsByRouter = new Map<
      string,
      Array<typeof passwallDesiredRevisions.$inferSelect>
    >();
    const jobCountMap = new Map<string, number>();

    for (const incident of incidentRows) {
      if (incident.state === "open" && !incidentMap.has(incident.routerId)) {
        incidentMap.set(incident.routerId, incident);
      }
    }

    for (const revision of revisionRows) {
      if (!revisionMap.has(revision.routerId)) {
        revisionMap.set(revision.routerId, revision);
      }
      revisionsByRouter.set(revision.routerId, [
        ...(revisionsByRouter.get(revision.routerId) ?? []),
        revision,
      ]);
    }

    for (const job of queuedJobRows) {
      if (["queued", "delivered", "running"].includes(job.state)) {
        jobCountMap.set(job.routerId, (jobCountMap.get(job.routerId) ?? 0) + 1);
      }
    }

    return routerRows.map((router) => {
      const snapshot = snapshots.get(router.id);
      const incident = incidentMap.get(router.id);
      const revision = revisionMap.get(router.id);
      const support = describeRouterSupport({
        boardName: snapshot?.payload.boardName ?? router.boardName,
        layoutFamily:
          typeof snapshot?.payload.layoutFamily === "string"
            ? snapshot.payload.layoutFamily
            : null,
        target: snapshot?.payload.target ?? router.target,
        architecture: snapshot?.payload.architecture ?? router.architecture,
        openwrtRelease:
          snapshot?.payload.openwrtRelease ?? router.openwrtRelease,
      });
      const configTrust = buildConfigTrustState({
        routerReachable: isRouterReachable(router.lastSeenAt),
        lastCheckInAt: router.lastCheckInAt ?? router.lastSeenAt,
        authoritativeDigest: router.lastConfigDigest,
        snapshotDigest: snapshot?.payload.configDigest ?? null,
        revisions: revisionsByRouter.get(router.id) ?? [],
        hasAuthoritativeConfig: Boolean(router.activeRevisionId),
      });

      return {
        ...router,
        latestSnapshot: snapshot ?? null,
        openIncident: incident ?? null,
        latestDesiredRevision: sanitizeRevisionForClient(revision),
        queuedJobCount: jobCountMap.get(router.id) ?? 0,
        configTrust,
        support,
      };
    });
  }),

  monitoring: protectedProcedure.query(async ({ ctx }) => {
    return loadFleetMonitoringSnapshot(ctx.db);
  }),

  pendingImportReviews: protectedProcedure.query(async ({ ctx }) => {
    const routerRows = await ctx.db
      .select()
      .from(routers)
      .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));

    const pendingRouters = routerRows.filter((router) =>
      ["import_review", "out_of_sync"].includes(router.importState),
    );

    const revisionIds = pendingRouters
      .map((router) => router.pendingImportRevisionId)
      .filter((revisionId): revisionId is string => Boolean(revisionId));

    const revisionRows = revisionIds.length
      ? await ctx.db
          .select()
          .from(passwallDesiredRevisions)
          .where(inArray(passwallDesiredRevisions.id, revisionIds))
      : [];

    const revisionMap = new Map(
      revisionRows.map((revision) => [revision.id, revision]),
    );

    return pendingRouters.map((router) => ({
      router,
      pendingRevision: router.pendingImportRevisionId
        ? sanitizeRevisionForClient(
            revisionMap.get(router.pendingImportRevisionId) ?? null,
          )
        : null,
    }));
  }),

  byId: protectedProcedure
    .input(z.object({ routerId: z.string().uuid() }))
    .query(async ({ ctx, input }) => {
      const [router] = await ctx.db
        .select()
        .from(routers)
        .where(eq(routers.id, input.routerId))
        .limit(1);

      if (!router) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: `Router ${input.routerId} was not found.`,
        });
      }

      const [
        snapshots,
        revisions,
        recentJobs,
        incidents,
        appliedRevisions,
        resultRows,
      ] = await Promise.all([
        ctx.db
          .select()
          .from(routerInventorySnapshots)
          .where(eq(routerInventorySnapshots.routerId, input.routerId))
          .orderBy(desc(routerInventorySnapshots.createdAt))
          .limit(5),
        ctx.db
          .select()
          .from(passwallDesiredRevisions)
          .where(eq(passwallDesiredRevisions.routerId, input.routerId))
          .orderBy(desc(passwallDesiredRevisions.revisionNumber))
          .limit(6),
        ctx.db
          .select()
          .from(jobs)
          .where(eq(jobs.routerId, input.routerId))
          .orderBy(desc(jobs.createdAt))
          .limit(12),
        ctx.db
          .select()
          .from(healthIncidents)
          .where(eq(healthIncidents.routerId, input.routerId))
          .orderBy(desc(healthIncidents.openedAt))
          .limit(8),
        ctx.db
          .select()
          .from(passwallAppliedRevisions)
          .where(eq(passwallAppliedRevisions.routerId, input.routerId))
          .orderBy(desc(passwallAppliedRevisions.reportedAt))
          .limit(12),
        ctx.db
          .select()
          .from(jobResults)
          .where(eq(jobResults.routerId, input.routerId))
          .orderBy(desc(jobResults.reportedAt))
          .limit(24),
      ]);

      const resultByJobId = new Map<string, typeof jobResults.$inferSelect>();
      for (const result of resultRows) {
        if (!resultByJobId.has(result.jobId)) {
          resultByJobId.set(result.jobId, result);
        }
      }

      const support = describeRouterSupport({
        boardName: snapshots[0]?.payload.boardName ?? router.boardName,
        layoutFamily:
          typeof snapshots[0]?.payload.layoutFamily === "string"
            ? snapshots[0].payload.layoutFamily
            : null,
        target: snapshots[0]?.payload.target ?? router.target,
        architecture: snapshots[0]?.payload.architecture ?? router.architecture,
        openwrtRelease:
          snapshots[0]?.payload.openwrtRelease ?? router.openwrtRelease,
      });
      const configTrust = buildConfigTrustState({
        routerReachable: isRouterReachable(router.lastSeenAt),
        lastCheckInAt: router.lastCheckInAt ?? router.lastSeenAt,
        authoritativeDigest: router.lastConfigDigest,
        snapshotDigest: snapshots[0]?.payload.configDigest ?? null,
        revisions,
        hasAuthoritativeConfig: Boolean(router.activeRevisionId),
      });

      return {
        router,
        latestSnapshot: snapshots[0] ?? null,
        snapshots,
        revisions: revisions.map((revision) =>
          sanitizeRevisionForClient(revision),
        ),
        recentJobs,
        incidents,
        configTrust,
        support,
        applyReceipts: appliedRevisions.map((receipt) => ({
          ...receipt,
          jobResultPayload: receipt.jobId
            ? (resultByJobId.get(receipt.jobId)?.payload ?? null)
            : null,
        })),
      };
    }),

  approveImportedBaseline: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        revisionId: z.string().uuid().optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const [router] = await ctx.db
        .select()
        .from(routers)
        .where(eq(routers.id, input.routerId))
        .limit(1);

      if (!router) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: `Router ${input.routerId} was not found.`,
        });
      }

      const targetRevisionId =
        input.revisionId ?? router.pendingImportRevisionId;
      if (!targetRevisionId) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message: "Router has no pending imported baseline to approve.",
        });
      }

      const [revision] = await ctx.db
        .select()
        .from(passwallDesiredRevisions)
        .where(eq(passwallDesiredRevisions.id, targetRevisionId))
        .limit(1);

      if (revision?.routerId !== router.id) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: "Imported baseline revision was not found for this router.",
        });
      }

      const approvedAt = new Date();

      await ctx.db
        .update(passwallDesiredRevisions)
        .set({
          status: "approved",
          approvedAt,
        })
        .where(eq(passwallDesiredRevisions.id, revision.id));

      const [updatedRouter] = await ctx.db
        .update(routers)
        .set({
          approvedAt: router.approvedAt ?? approvedAt,
          importState: "approved",
          activeRevisionId: revision.id,
          pendingImportRevisionId: null,
          lastConfigDigest: revision.configDigest,
          status: router.status === "direct" ? "direct" : "active",
        })
        .where(eq(routers.id, router.id))
        .returning();

      await ctx.db.insert(eventLog).values({
        routerId: router.id,
        type: "router.import.approved",
        severity: "info",
        message:
          "Operator approved imported PassWall2 baseline and promoted it to authoritative state.",
        metadata: {
          revisionId: revision.id,
        },
      });

      return {
        router: updatedRouter ?? router,
        revision: sanitizeRevisionForClient({
          ...revision,
          status: "approved",
          approvedAt,
        }),
      };
    }),

  requestReimport: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const [router] = await ctx.db
        .select()
        .from(routers)
        .where(eq(routers.id, input.routerId))
        .limit(1);

      if (!router) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: `Router ${input.routerId} was not found.`,
        });
      }

      const [updatedRouter] = await ctx.db
        .update(routers)
        .set({
          importState: "awaiting_import",
        })
        .where(eq(routers.id, router.id))
        .returning();

      await ctx.db.insert(eventLog).values({
        routerId: router.id,
        type: "router.import.requested",
        severity: "info",
        message: "Operator requested a fresh live import from the router.",
        metadata: {},
      });

      return updatedRouter ?? router;
    }),

  deleteRouter: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const [router] = await ctx.db
        .select()
        .from(routers)
        .where(eq(routers.id, input.routerId))
        .limit(1);

      if (!router) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: `Router ${input.routerId} was not found.`,
        });
      }

      await ctx.db.insert(eventLog).values({
        routerId: router.id,
        type: "router.deleted",
        severity: "warning",
        message: "Operator removed router from the Vectra control plane.",
        metadata: {
          routerId: router.id,
          deviceIdentifier: router.deviceIdentifier,
          displayName: router.displayName ?? null,
          hostname: router.hostname ?? null,
          status: router.status,
          importState: router.importState,
        },
      });

      const [deletedRouter] = await ctx.db
        .delete(routers)
        .where(eq(routers.id, router.id))
        .returning();

      return {
        router:
          deletedRouter ??
          ({
            ...router,
          } as typeof routers.$inferSelect),
      };
    }),
});
