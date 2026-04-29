import {
  healthIncidents,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { createDefaultRescuePolicy } from "@vectra/contracts";
import { TRPCError } from "@trpc/server";
import { desc, eq } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import type { db as webDb } from "~/server/db";
import { hasActiveDirectMode } from "~/server/vectra/router-presence";
import {
  getActiveRescueCaseForRouter,
  listRescueCases,
  loadRescueCaseDetails,
  queueRescueCaseLogCollection,
  queueRescueCaseReconnectProxy,
  queueRescueCaseSafeRepair,
  silenceRescueCase,
} from "~/server/vectra/auto-rescue";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

async function assertCertifiedRouterForRescue(
  ctx: { db: typeof webDb },
  routerId: string,
) {
  const [router] = await ctx.db
    .select()
    .from(routers)
    .where(eq(routers.id, routerId))
    .limit(1);

  if (!router) {
    throw new TRPCError({
      code: "NOT_FOUND",
      message: `Router ${routerId} was not found.`,
    });
  }

  const [snapshot] = await ctx.db
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);

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
        "Операторские rescue-действия разрешены только для поддерживаемых pilot/certified board/layout пар.",
    });
  }
}

export const rescueRouter = createTRPCRouter({
  policy: protectedProcedure.query(() => createDefaultRescuePolicy()),

  cases: protectedProcedure.query(async ({ ctx }) => listRescueCases(ctx.db)),

  activeCaseForRouter: protectedProcedure
    .input(z.object({ routerId: z.string().uuid() }))
    .query(async ({ ctx, input }) =>
      getActiveRescueCaseForRouter(input.routerId, ctx.db),
    ),

  caseById: protectedProcedure
    .input(z.object({ caseId: z.string().uuid() }))
    .query(async ({ ctx, input }) => {
      try {
        return await loadRescueCaseDetails(input.caseId, ctx.db);
      } catch (error) {
        throw new TRPCError({
          code: "NOT_FOUND",
          message:
            error instanceof Error ? error.message : "Rescue case not found.",
        });
      }
    }),

  runCaseSafeRepair: protectedProcedure
    .input(z.object({ caseId: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      try {
        return await queueRescueCaseSafeRepair(
          {
            caseId: input.caseId,
            requestedBy: "operator",
          },
          ctx.db,
        );
      } catch (error) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message:
            error instanceof Error ? error.message : "Failed to queue repair.",
        });
      }
    }),

  reconnectCase: protectedProcedure
    .input(z.object({ caseId: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      try {
        return await queueRescueCaseReconnectProxy(
          input.caseId,
          "operator",
          ctx.db,
        );
      } catch (error) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message:
            error instanceof Error
              ? error.message
              : "Failed to queue reconnect.",
        });
      }
    }),

  collectCaseLogs: protectedProcedure
    .input(z.object({ caseId: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      try {
        return await queueRescueCaseLogCollection(input.caseId, ctx.db);
      } catch (error) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message:
            error instanceof Error
              ? error.message
              : "Failed to queue log collection.",
        });
      }
    }),

  silenceCase: protectedProcedure
    .input(
      z.object({
        caseId: z.string().uuid(),
        durationSeconds: z.number().int().min(300).max(86400).default(3600),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      silenceRescueCase(input.caseId, input.durationSeconds, ctx.db),
    ),

  openIncidents: protectedProcedure.query(async ({ ctx }) => {
    return ctx.db
      .select()
      .from(healthIncidents)
      .where(eq(healthIncidents.state, "open"))
      .orderBy(desc(healthIncidents.openedAt));
  }),

  directRouters: protectedProcedure.query(async ({ ctx }) => {
    const routerRows = await ctx.db
      .select()
      .from(routers)
      .orderBy(desc(routers.lastDirectModeAt));

    return routerRows.filter((router) =>
      hasActiveDirectMode(router.status, router.lastSeenAt),
    );
  }),

  triggerDirectMode: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        reason: z.string().min(1).default("Оператор запросил прямой режим"),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertCertifiedRouterForRescue(ctx, input.routerId);

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "enter_direct_mode",
          state: "queued",
          payload: {
            reason: input.reason,
          },
        })
        .returning();

      return job;
    }),

  triggerReconnect: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        clearRescue: z.boolean().default(true),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertCertifiedRouterForRescue(ctx, input.routerId);

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "reconnect",
          state: "queued",
          payload: {
            resumeProxy: true,
            clearRescue: input.clearRescue,
          },
        })
        .returning();

      return job;
    }),
});
