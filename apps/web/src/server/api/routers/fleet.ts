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
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { resolveInstalledControllerVersion } from "~/lib/controller-version";
import {
  buildTerminalRouterHostnameUpdatePayload,
  normalizeRouterHostname,
  routerHostnameInputPattern,
} from "~/lib/router-hostname-jobs";
import { minimumTerminalControllerVersion, supportsTerminalFeature } from "~/lib/router-terminal-support";
import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import {
  loadFleetMonitoringSnapshot,
  loadLatestFleetPolicyConfigRows,
  loadLatestSnapshots,
} from "~/server/vectra/fleet-monitoring-data";
import { buildConfigTrustState } from "~/server/vectra/config-trust";
import {
  evaluateFleetRoutePolicy,
  normalizeFleetRoutePolicy,
} from "~/server/vectra/fleet-route-policy";
import {
  loadRevisionMetadata,
  type PasswallRevisionMetadataRow,
} from "~/server/vectra/revision-metadata";
import {
  getEffectiveRouterStatus,
  isRouterReachable,
} from "~/server/vectra/router-presence";
import {
  createOperatorDraftRevisionWithDb,
  getFullConfigForRevisionWithDb,
  queueDesiredRevisionApplyJobWithDb,
  sanitizeRevisionForClient,
} from "~/server/vectra/router-control";
import { canRunDestructiveAction, describeRouterSupport } from "~/server/vectra/support";

const activeJobStates: Array<"queued" | "delivered" | "running"> = [
  "queued",
  "delivered",
  "running",
];
const routePolicyNormalizeModeSchema = z
  .enum(["dry_run", "draft", "queue_apply"])
  .default("dry_run");
const routerHostnameInputSchema = z
  .string()
  .trim()
  .min(1, "Введите hostname роутера.")
  .max(63, "Hostname OpenWrt должен помещаться в 63 символа.")
  .regex(
    routerHostnameInputPattern,
    "Hostname OpenWrt может содержать только латиницу, цифры и дефис, без дефиса в начале или в конце.",
  );

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
    const [
      snapshots,
      incidentRows,
      revisionRows,
      queuedJobRows,
      policyConfigRows,
    ] =
      await Promise.all([
        loadLatestSnapshots(ctx.db, routerIds),
        routerIds.length
          ? ctx.db
              .select()
              .from(healthIncidents)
              .where(inArray(healthIncidents.routerId, routerIds))
              .orderBy(desc(healthIncidents.openedAt))
          : Promise.resolve([]),
        loadRevisionMetadata(ctx.db, routerIds),
        routerIds.length
          ? ctx.db
              .select()
              .from(jobs)
              .where(inArray(jobs.routerId, routerIds))
              .orderBy(desc(jobs.createdAt))
          : Promise.resolve([]),
        loadLatestFleetPolicyConfigRows(ctx.db, routerIds),
      ]);

    const incidentMap = new Map<string, typeof healthIncidents.$inferSelect>();
    const revisionMap = new Map<string, PasswallRevisionMetadataRow>();
    const revisionsByRouter = new Map<string, PasswallRevisionMetadataRow[]>();
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
      const routerName =
        router.displayName ??
        snapshot?.payload.hostname ??
        router.hostname ??
        router.deviceIdentifier;

      return {
        ...router,
        latestSnapshot: snapshot ?? null,
        openIncident: incident ?? null,
        latestDesiredRevision: revision ?? null,
        queuedJobCount: jobCountMap.get(router.id) ?? 0,
        configTrust,
        fleetPolicyCompliance: evaluateFleetRoutePolicy(
          policyConfigRows.get(router.id)?.config ?? null,
          {
            id: router.id,
            name: routerName,
            displayName: router.displayName,
            hostname: snapshot?.payload.hostname ?? router.hostname,
            deviceIdentifier: router.deviceIdentifier,
          },
        ),
        support,
      };
    });
  }),

  monitoring: protectedProcedure.query(async ({ ctx }) => {
    return loadFleetMonitoringSnapshot(ctx.db);
  }),

  normalizeRoutePolicy: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1).max(25),
        mode: routePolicyNormalizeModeSchema,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const routerRows = await ctx.db
        .select()
        .from(routers)
        .where(inArray(routers.id, input.routerIds));
      const routerMap = new Map(routerRows.map((router) => [router.id, router]));
      const latestSnapshots = await loadLatestSnapshots(ctx.db, input.routerIds);

      const revisionRows = await ctx.db
        .select()
        .from(passwallDesiredRevisions)
        .where(inArray(passwallDesiredRevisions.routerId, input.routerIds))
        .orderBy(desc(passwallDesiredRevisions.createdAt));
      const latestLiveRevisionByRouter = new Map<
        string,
        typeof passwallDesiredRevisions.$inferSelect
      >();
      for (const revision of revisionRows) {
        if (
          revision.origin !== "router_import" &&
          revision.origin !== "operator_reimport"
        ) {
          continue;
        }
        if (!latestLiveRevisionByRouter.has(revision.routerId)) {
          latestLiveRevisionByRouter.set(revision.routerId, revision);
        }
      }

      const results = [];
      for (const routerId of input.routerIds) {
        const router = routerMap.get(routerId);
        if (!router) {
          results.push({
            routerId,
            status: "router_not_found" as const,
            compliance: null,
            changes: [],
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        const sourceRevision = latestLiveRevisionByRouter.get(router.id) ?? null;
        const snapshot = latestSnapshots.get(router.id) ?? null;
        const routerName =
          router.displayName ??
          snapshot?.payload.hostname ??
          router.hostname ??
          router.deviceIdentifier;
        const identity = {
          id: router.id,
          name: routerName,
          displayName: router.displayName,
          hostname: snapshot?.payload.hostname ?? router.hostname,
          deviceIdentifier: router.deviceIdentifier,
        };
        if (!sourceRevision) {
          results.push({
            routerId: router.id,
            status: "no_live_import" as const,
            compliance: evaluateFleetRoutePolicy(null, identity),
            changes: [],
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        const sourceConfig =
          (await getFullConfigForRevisionWithDb(ctx.db, sourceRevision.id)) ??
          sourceRevision.config;
        const normalization = normalizeFleetRoutePolicy(sourceConfig, identity);
        if (normalization.before.status === "exempt") {
          results.push({
            routerId: router.id,
            status: "exempt" as const,
            compliance: normalization.before,
            changes: normalization.changes,
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        if (!normalization.changed) {
          results.push({
            routerId: router.id,
            status:
              normalization.before.status === "compliant"
                ? ("already_compliant" as const)
                : ("not_normalizable" as const),
            compliance: normalization.before,
            changes: normalization.changes,
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        if (input.mode === "dry_run") {
          results.push({
            routerId: router.id,
            status: "would_change" as const,
            compliance: normalization.before,
            afterCompliance: normalization.after,
            changes: normalization.changes,
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        if (normalization.after.status !== "compliant") {
          results.push({
            routerId: router.id,
            status: "not_normalizable" as const,
            compliance: normalization.before,
            afterCompliance: normalization.after,
            changes: normalization.changes,
            draftRevision: null,
            queuedJob: null,
          });
          continue;
        }

        if (input.mode === "queue_apply") {
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
          if (!canRunDestructiveAction(support.state)) {
            results.push({
              routerId: router.id,
              status: "unsupported" as const,
              compliance: normalization.before,
              afterCompliance: normalization.after,
              changes: normalization.changes,
              draftRevision: null,
              queuedJob: null,
              message:
                "Fleet route policy apply is blocked for unsupported board/layout.",
              support,
            });
            continue;
          }
        }

        const draftRevision = await createOperatorDraftRevisionWithDb(ctx.db, {
          routerId: router.id,
          note: `Normalize fleet route policy ${normalization.policyVersion}`,
          config: normalization.config,
        });
        const queuedJob =
          input.mode === "queue_apply"
            ? await queueDesiredRevisionApplyJobWithDb(ctx.db, {
                routerId: router.id,
                desiredRevisionId: draftRevision.id,
              })
            : null;

        await ctx.db.insert(eventLog).values({
          routerId: router.id,
          type:
            input.mode === "queue_apply"
              ? "router.fleet_policy.normalize_queued"
              : "router.fleet_policy.draft_created",
          severity: "info",
          message:
            input.mode === "queue_apply"
              ? "Operator queued fleet route policy normalization apply job."
              : "Operator created a fleet route policy normalization draft.",
          metadata: {
            policyVersion: normalization.policyVersion,
            sourceRevisionId: sourceRevision.id,
            draftRevisionId: draftRevision.id,
            queuedJobId: queuedJob?.id ?? null,
            changes: normalization.changes,
          },
        });

        results.push({
          routerId: router.id,
          status:
            input.mode === "queue_apply"
              ? ("queued_apply" as const)
              : ("draft_created" as const),
          compliance: normalization.before,
          afterCompliance: normalization.after,
          changes: normalization.changes,
          draftRevision: sanitizeRevisionForClient(draftRevision),
          queuedJob,
        });
      }

      return {
        policyVersion: "2026-05-12-v1" as const,
        mode: input.mode,
        results,
      };
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
      const routerName =
        router.displayName ??
        snapshots[0]?.payload.hostname ??
        router.hostname ??
        router.deviceIdentifier;
      const latestLiveRevision =
        revisions.find(
          (revision) =>
            revision.origin === "router_import" ||
            revision.origin === "operator_reimport",
        ) ?? null;
      const fleetPolicyCompliance = evaluateFleetRoutePolicy(
        latestLiveRevision?.config ?? null,
        {
          id: router.id,
          name: routerName,
          displayName: router.displayName,
          hostname: snapshots[0]?.payload.hostname ?? router.hostname,
          deviceIdentifier: router.deviceIdentifier,
        },
      );

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
        fleetPolicyCompliance,
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

  renameRouter: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        hostname: routerHostnameInputSchema,
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

      const [snapshot] = await ctx.db
        .select()
        .from(routerInventorySnapshots)
        .where(eq(routerInventorySnapshots.routerId, input.routerId))
        .orderBy(desc(routerInventorySnapshots.createdAt))
        .limit(1);
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

      if (!canRunDestructiveAction(support.state)) {
        throw new TRPCError({
          code: "FORBIDDEN",
          message:
            "Смена hostname доступна только для поддерживаемых pilot/certified board/layout пар.",
        });
      }

      const controllerVersion = resolveInstalledControllerVersion({
        controllerVersion: snapshot?.controllerVersion ?? null,
        payload: snapshot?.payload ?? null,
      });
      if (!supportsTerminalFeature(controllerVersion, minimumTerminalControllerVersion)) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message:
            `Смена hostname доступна после обновления controller-agent до ${minimumTerminalControllerVersion} или новее.`,
        });
      }

      const nextHostname = normalizeRouterHostname(input.hostname);
      const currentHostname = normalizeRouterHostname(
        snapshot?.payload.hostname ?? router.hostname ?? "",
      );
      if (nextHostname === currentHostname) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message: `Hostname "${nextHostname}" уже установлен на этом роутере.`,
        });
      }

      const payload = buildTerminalRouterHostnameUpdatePayload(nextHostname);
      const dedupeKey = `router_hostname_update:${router.id}:${nextHostname}`;
      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, router.id),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, activeJobStates),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: router.id,
          type: "run_terminal_command",
          state: "queued",
          dedupeKey,
          payload,
        })
        .returning();

      await ctx.db.insert(eventLog).values({
        routerId: router.id,
        type: "router.hostname.update.requested",
        severity: "info",
        message: `Operator queued OpenWrt hostname update to "${nextHostname}".`,
        metadata: {
          routerId: router.id,
          requestedHostname: nextHostname,
          previousHostname: snapshot?.payload.hostname ?? router.hostname ?? null,
          deviceIdentifier: router.deviceIdentifier,
          jobId: job?.id ?? null,
        },
      });

      return job;
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
