import { healthIncidents, jobs, routerInventorySnapshots, routers } from "@vectra/db";
import { createDefaultRescuePolicy } from "@vectra/contracts";
import { TRPCError } from "@trpc/server";
import { desc, eq } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import { hasActiveDirectMode } from "~/server/vectra/router-presence";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

async function assertCertifiedRouterForRescue(
  ctx: { db: typeof import("~/server/db").db },
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
