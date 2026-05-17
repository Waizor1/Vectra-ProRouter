import {
  collectOptimizationBaselineJobPayloadSchema,
  routerLogSourceSchema,
} from "@vectra/contracts";
import { jobResults, jobs, routers } from "@vectra/db";
import { TRPCError } from "@trpc/server";
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import type { db as DatabaseClientValue } from "~/server/db";
import { buildRouterOptimizationBaselineHistory } from "~/server/vectra/router-optimization";

const activeOptimizationJobStates: Array<"queued" | "delivered" | "running"> = [
  "queued",
  "delivered",
  "running",
];
type DatabaseClient = typeof DatabaseClientValue;

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

  return router;
}

export const optimizationRouter = createTRPCRouter({
  baselineHistory: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .query(async ({ ctx, input }) => {
      await assertRouterExists(ctx, input.routerId);

      const baselineJobs = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "collect_optimization_baseline"),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(12);

      const jobIds = baselineJobs.map((job) => job.id);
      const relatedResults =
        jobIds.length > 0
          ? await ctx.db
              .select()
              .from(jobResults)
              .where(inArray(jobResults.jobId, jobIds))
              .orderBy(desc(jobResults.reportedAt))
          : [];

      return buildRouterOptimizationBaselineHistory({
        jobs: baselineJobs,
        results: relatedResults,
      });
    }),

  queueBaseline: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        logSource: routerLogSourceSchema.default("all"),
        logLines: z.number().int().min(50).max(400).default(160),
        includeLogs: z.boolean().default(true),
        includeRoutes: z.boolean().default(true),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertRouterExists(ctx, input.routerId);

      const dedupeKey = `collect_optimization_baseline:${input.routerId}`;
      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "collect_optimization_baseline"),
            inArray(jobs.state, activeOptimizationJobStates),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const payload = collectOptimizationBaselineJobPayloadSchema.parse({
        logSource: input.logSource,
        logLines: input.logLines,
        includeLogs: input.includeLogs,
        includeRoutes: input.includeRoutes,
      });

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "collect_optimization_baseline",
          state: "queued",
          dedupeKey,
          payload,
        })
        .onConflictDoNothing({ target: jobs.dedupeKey })
        .returning();

      if (job) {
        return job;
      }

      const [reusedJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "collect_optimization_baseline"),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, activeOptimizationJobStates),
          ),
        )
        .limit(1);

      if (reusedJob) {
        return reusedJob;
      }

      throw new TRPCError({
        code: "CONFLICT",
        message: "Optimization baseline request could not be queued.",
      });
    }),
});
