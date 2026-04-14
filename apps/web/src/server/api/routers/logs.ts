import {
  collectRouterLogsJobPayloadSchema,
  routerLogSourceSchema,
} from "@vectra/contracts";
import { jobResults, jobs, routers } from "@vectra/db";
import { TRPCError } from "@trpc/server";
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import type { db as DatabaseClientValue } from "~/server/db";
import { buildRouterLogHistory } from "~/server/vectra/router-logs";

const activeLogJobStates: Array<"queued" | "delivered" | "running"> = [
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
}

export const logsRouter = createTRPCRouter({
  history: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .query(async ({ ctx, input }) => {
      await assertRouterExists(ctx, input.routerId);

      const logJobs = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "collect_router_logs"),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(12);

      const jobIds = logJobs.map((job) => job.id);
      const relatedResults =
        jobIds.length > 0
          ? await ctx.db
              .select()
              .from(jobResults)
              .where(inArray(jobResults.jobId, jobIds))
              .orderBy(desc(jobResults.reportedAt))
          : [];

      return buildRouterLogHistory({
        jobs: logJobs,
        results: relatedResults,
      });
    }),

  queueSnapshot: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        source: routerLogSourceSchema.default("all"),
        lines: z.number().int().min(50).max(400).default(200),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertRouterExists(ctx, input.routerId);

      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.type, "collect_router_logs"),
            inArray(jobs.state, activeLogJobStates),
          ),
        )
        .orderBy(desc(jobs.createdAt))
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const payload = collectRouterLogsJobPayloadSchema.parse({
        source: input.source,
        lines: input.lines,
      });

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "collect_router_logs",
          state: "queued",
          dedupeKey: `collect_router_logs:${input.routerId}`,
          payload,
        })
        .returning();

      return job;
    }),
});
