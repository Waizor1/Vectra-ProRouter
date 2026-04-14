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

      const importedRevision =
        (selectedRouter.pendingImportRevisionId
          ? revisionRows.find(
              (revision) =>
                revision.id === selectedRouter.pendingImportRevisionId,
            )
          : undefined) ??
        revisionRows.find((revision) =>
          ["router_import", "operator_reimport"].includes(revision.origin),
        ) ??
        null;

      const activeRevision =
        (selectedRouter.activeRevisionId
          ? revisionRows.find(
              (revision) => revision.id === selectedRouter.activeRevisionId,
            )
          : undefined) ?? null;

      const latestDraft =
        revisionRows.find((revision) => revision.origin === "operator_draft") ??
        null;

      const workspaceRevision =
        latestDraft ??
        importedRevision ??
        activeRevision ??
        revisionRows[0] ??
        null;

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

      await ctx.db
        .update(passwallDesiredRevisions)
        .set({ status: "queued" })
        .where(eq(passwallDesiredRevisions.id, input.desiredRevisionId));

      return job;
    }),
});
