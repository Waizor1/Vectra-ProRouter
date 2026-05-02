import {
  jobs,
  passwallDesiredRevisions,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import {
  passwallDesiredConfigSchema,
  summarizePasswallRevisionDiff,
} from "@vectra/contracts";
import { TRPCError } from "@trpc/server";
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import { buildDraftPreview } from "~/server/vectra/editor";
import { getDraftEditorSurface } from "~/server/vectra/editor-surface";
import {
  isEditableDraftRevision,
  isOperatorDraftRevision,
  isSupersededEditableDraft,
  pickActiveRevision,
  pickCurrentLiveRevision,
  pickImportedRevision,
  pickLatestEditableDraft,
  pickWorkspaceRevision,
} from "~/server/vectra/draft-selection";
import {
  createOperatorDraftRevision,
  sanitizeRevisionForClient,
} from "~/server/vectra/router-control";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

export const draftRouter = createTRPCRouter({
  list: protectedProcedure.query(async ({ ctx }) => {
    const revisions = await ctx.db
      .select()
      .from(passwallDesiredRevisions)
      .orderBy(
        desc(passwallDesiredRevisions.createdAt),
        desc(passwallDesiredRevisions.revisionNumber),
      )
      .limit(50);

    const previousByRouter = new Map<string, (typeof revisions)[number]>();

    return revisions.map((revision) => {
      const previous = previousByRouter.get(revision.routerId);
      const impact = summarizePasswallRevisionDiff(
        previous?.config ?? null,
        revision.config,
      );

      previousByRouter.set(revision.routerId, revision);

      return {
        ...sanitizeRevisionForClient(revision),
        impact,
      };
    });
  }),

  workspace: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid().optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const routerRows = await ctx.db
        .select()
        .from(routers)
        .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));

      if (routerRows.length === 0) {
        return {
          routers: [],
          selectedRouter: null,
          importedRevision: null,
          activeRevision: null,
          latestDraft: null,
          workspaceRevision: null,
        };
      }

      const selectedRouter =
        routerRows.find((router) => router.id === input.routerId) ??
        routerRows[0]!;

      const revisionRows = await ctx.db
        .select()
        .from(passwallDesiredRevisions)
        .where(eq(passwallDesiredRevisions.routerId, selectedRouter.id))
        .orderBy(desc(passwallDesiredRevisions.revisionNumber))
        .limit(12);

      const importedRevision = pickImportedRevision({
        pendingImportRevisionId: selectedRouter.pendingImportRevisionId,
        revisions: revisionRows,
      });
      const activeRevision = pickActiveRevision({
        activeRevisionId: selectedRouter.activeRevisionId,
        revisions: revisionRows,
      });
      const [latestSnapshot] = await ctx.db
        .select()
        .from(routerInventorySnapshots)
        .where(eq(routerInventorySnapshots.routerId, selectedRouter.id))
        .orderBy(desc(routerInventorySnapshots.createdAt))
        .limit(1);
      const currentLiveRevision = pickCurrentLiveRevision({
        snapshotDigest: latestSnapshot?.payload.configDigest ?? null,
        revisions: revisionRows.filter((revision) =>
          ["router_import", "operator_reimport"].includes(revision.origin),
        ),
      });
      const latestDraft = pickLatestEditableDraft({
        revisions: revisionRows,
        activeRevision,
        currentLiveRevision,
      });
      const workspaceRevision = pickWorkspaceRevision({
        latestEditableDraft: latestDraft,
        currentLiveRevision,
        importedRevision,
        activeRevision,
        revisions: revisionRows,
      });

      return {
        routers: routerRows.map((router) => ({
          id: router.id,
          deviceIdentifier: router.deviceIdentifier,
          displayName: router.displayName,
          hostname: router.hostname,
          status: router.status,
          importState: router.importState,
          pendingImportRevisionId: router.pendingImportRevisionId,
          activeRevisionId: router.activeRevisionId,
        })),
        selectedRouter,
        importedRevision: sanitizeRevisionForClient(importedRevision),
        activeRevision: sanitizeRevisionForClient(activeRevision),
        latestDraft: sanitizeRevisionForClient(latestDraft),
        workspaceRevision: sanitizeRevisionForClient(workspaceRevision),
      };
    }),

  editorSurface: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .query(async ({ input }) => {
      try {
        return await getDraftEditorSurface(input.routerId);
      } catch (error) {
        if (error instanceof Error && error.message === "Router not found.") {
          throw new TRPCError({
            code: "NOT_FOUND",
            message: `Router ${input.routerId} was not found.`,
          });
        }

        if (
          error instanceof Error &&
          error.message ===
            "Router has no editable PassWall2 configuration yet."
        ) {
          throw new TRPCError({
            code: "NOT_FOUND",
            message:
              "Router has no imported or authoritative PassWall2 configuration yet.",
          });
        }

        throw error;
      }
    }),

  preview: protectedProcedure
    .input(
      z.object({
        previous: passwallDesiredConfigSchema.nullable().default(null),
        next: passwallDesiredConfigSchema,
      }),
    )
    .query(({ input }) => ({
      ...summarizePasswallRevisionDiff(input.previous, input.next),
      ...buildDraftPreview(input.previous, input.next),
    })),

  save: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        note: z.string().trim().max(500).optional(),
        config: passwallDesiredConfigSchema,
      }),
    )
    .mutation(async ({ input }) => {
      const revision = await createOperatorDraftRevision({
        routerId: input.routerId,
        note: input.note,
        config: input.config,
      });
      return sanitizeRevisionForClient(revision);
    }),

  queueApply: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        desiredRevisionId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const [router] = await ctx.db
        .select()
        .from(routers)
        .where(eq(routers.id, input.routerId))
        .limit(1);

      const [snapshot] = await ctx.db
        .select()
        .from(routerInventorySnapshots)
        .where(eq(routerInventorySnapshots.routerId, input.routerId))
        .orderBy(desc(routerInventorySnapshots.createdAt))
        .limit(1);

      if (!router) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: `Router ${input.routerId} was not found.`,
        });
      }

      const support = describeEffectiveRouterSupport({
        router: {
          boardName: router.boardName,
          target: router.target,
          architecture: router.architecture,
          openwrtRelease: router.openwrtRelease,
        },
        inventory: snapshot?.payload ?? null,
      });

      if (!canRunDestructiveAction(support.state)) {
        throw new TRPCError({
          code: "FORBIDDEN",
          message:
            "Конфигурация может применяться только на поддерживаемых pilot/certified роутерах.",
        });
      }

      if (router.importState !== "approved") {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Сначала нужно завершить import review или re-import, затем сервер снова станет authoritative.",
        });
      }

      const revisionRows = await ctx.db
        .select()
        .from(passwallDesiredRevisions)
        .where(eq(passwallDesiredRevisions.routerId, input.routerId))
        .orderBy(desc(passwallDesiredRevisions.revisionNumber))
        .limit(100);
      const desiredRevision =
        revisionRows.find(
          (revision) => revision.id === input.desiredRevisionId,
        ) ?? null;

      if (!desiredRevision) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message:
            "Выбранная ревизия не найдена у этого роутера. Обновите рабочую поверхность перед apply.",
        });
      }

      const activeRevision = pickActiveRevision({
        activeRevisionId: router.activeRevisionId,
        revisions: revisionRows,
      });
      const currentLiveRevision = pickCurrentLiveRevision({
        snapshotDigest: snapshot?.payload.configDigest ?? null,
        revisions: revisionRows.filter((revision) =>
          ["router_import", "operator_reimport"].includes(revision.origin),
        ),
      });
      const latestDraft = pickLatestEditableDraft({
        revisions: revisionRows,
        activeRevision,
        currentLiveRevision,
      });

      if (
        isSupersededEditableDraft({
          draftRevision: desiredRevision,
          activeRevision,
          currentLiveRevision,
        })
      ) {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Этот черновик уже перекрыт более свежим live-импортом с роутера. Откройте актуальную рабочую поверхность и сохраните новый черновик перед apply.",
        });
      }

      const applyableRevisionIds = new Set(
        [latestDraft?.id, activeRevision?.id].filter(Boolean),
      );
      if (!applyableRevisionIds.has(desiredRevision.id)) {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Apply можно поставить только для текущей authoritative-ревизии или самого свежего не перекрытого черновика. Обновите рабочую поверхность и сохраните новый черновик.",
        });
      }

      const dedupeKey = `apply:${input.routerId}:${input.desiredRevisionId}`;

      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, ["queued", "delivered", "running"]),
          ),
        )
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "apply_passwall_config",
          state: "queued",
          dedupeKey,
          desiredRevisionId: input.desiredRevisionId,
          payload: {
            desiredRevisionId: input.desiredRevisionId,
          },
        })
        .returning();

      if (isEditableDraftRevision(desiredRevision)) {
        await ctx.db
          .update(passwallDesiredRevisions)
          .set({ status: "queued" })
          .where(eq(passwallDesiredRevisions.id, input.desiredRevisionId));
      }

      return job;
    }),

  discard: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        revisionId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const [revision] = await ctx.db
        .select()
        .from(passwallDesiredRevisions)
        .where(
          and(
            eq(passwallDesiredRevisions.routerId, input.routerId),
            eq(passwallDesiredRevisions.id, input.revisionId),
          ),
        )
        .limit(1);

      if (!revision) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message: "Черновик не найден у этого роутера.",
        });
      }

      if (!isOperatorDraftRevision(revision)) {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Можно отбросить только operator draft. Импортированные и approved baseline-ревизии остаются в истории.",
        });
      }

      if (!isEditableDraftRevision(revision)) {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Этот черновик уже не находится в editable-состоянии и не будет выбран для apply.",
        });
      }

      const activeApplyJobs = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "apply_passwall_config"),
            eq(jobs.desiredRevisionId, input.revisionId),
            inArray(jobs.state, ["queued", "delivered", "running"]),
          ),
        )
        .limit(20);
      const alreadyDeliveredJob = activeApplyJobs.find(
        (job) => job.state !== "queued",
      );

      if (alreadyDeliveredJob) {
        throw new TRPCError({
          code: "PRECONDITION_FAILED",
          message:
            "Apply-задача по этому черновику уже доставлена на роутер или выполняется. Дождитесь результата и затем сделайте re-import.",
        });
      }

      if (activeApplyJobs.length > 0) {
        await ctx.db
          .update(jobs)
          .set({
            state: "cancelled",
            completedAt: new Date(),
            dedupeKey: null,
          })
          .where(
            and(
              eq(jobs.routerId, input.routerId),
              eq(jobs.type, "apply_passwall_config"),
              eq(jobs.desiredRevisionId, input.revisionId),
              eq(jobs.state, "queued"),
            ),
          );
      }

      const [updatedRevision] = await ctx.db
        .update(passwallDesiredRevisions)
        .set({ status: "discarded" })
        .where(
          and(
            eq(passwallDesiredRevisions.routerId, input.routerId),
            eq(passwallDesiredRevisions.id, input.revisionId),
          ),
        )
        .returning();

      return sanitizeRevisionForClient(
        updatedRevision ?? { ...revision, status: "discarded" },
      );
    }),
});
