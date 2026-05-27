import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";
import {
  jobResults,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import { TRPCError } from "@trpc/server";
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import type { db as DatabaseClientValue } from "~/server/db";
import { buildRouterTerminalHistory } from "~/server/vectra/router-terminal";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

const activeTerminalJobStates: Array<"queued" | "delivered" | "running"> = [
  "queued",
  "delivered",
  "running",
];
type DatabaseClient = typeof DatabaseClientValue;

async function assertTerminalCapableRouter(
  ctx: { db: DatabaseClient },
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
        "Terminal доступен только для поддерживаемых pilot/certified board/layout пар.",
    });
  }

  return { router, snapshot };
}

async function assertRouterExists(
  ctx: { db: DatabaseClient },
  routerId: string,
) {
  const [router] = await ctx.db
    .select({ id: routers.id })
    .from(routers)
    .where(eq(routers.id, routerId))
    .limit(1);

  if (!router) {
    throw new TRPCError({
      code: "NOT_FOUND",
      message: `Router ${routerId} was not found.`,
    });
  }
}

export const terminalRouter = createTRPCRouter({
  history: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .query(async ({ ctx, input }) => {
      await assertRouterExists(ctx, input.routerId);

      const terminalJobs = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "run_terminal_command"),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(12);

      const jobIds = terminalJobs.map((job) => job.id);
      const relatedResults =
        jobIds.length > 0
          ? await ctx.db
              .select()
              .from(jobResults)
              .where(inArray(jobResults.jobId, jobIds))
              .orderBy(desc(jobResults.reportedAt))
          : [];

      return buildRouterTerminalHistory({
        jobs: terminalJobs,
        results: relatedResults,
      });
    }),

  queueCommand: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        command: z.string().trim().min(1).max(8000),
        timeoutSeconds: z.number().int().min(5).max(120).default(30),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertTerminalCapableRouter(ctx, input.routerId);

      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "run_terminal_command"),
            inArray(jobs.state, activeTerminalJobStates),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const payload = runTerminalCommandJobPayloadSchema.parse({
        command: input.command,
        timeoutSeconds: input.timeoutSeconds,
      });

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "run_terminal_command",
          state: "queued",
          dedupeKey: `run_terminal_command:${input.routerId}`,
          payload,
        })
        .returning();

      return job;
    }),
});
