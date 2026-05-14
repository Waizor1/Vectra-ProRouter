import { routerOnboardingProfileInputSchema } from "~/server/vectra/router-auto-onboarding";
import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import {
  advanceRouterOnboardingWithDb,
  getRouterOnboardingStateWithDb,
  listRouterOnboardingRunsWithDb,
  pauseRouterOnboardingWithDb,
  retryRouterOnboardingWithDb,
  sanitizeOnboardingRunForClient,
  upsertRouterOnboardingProfileWithDb,
} from "~/server/vectra/router-auto-onboarding";
import { z } from "zod";

const routerIdInputSchema = z.object({ routerId: z.string().uuid() });

export const onboardingRouter = createTRPCRouter({
  get: protectedProcedure
    .input(routerIdInputSchema)
    .query(async ({ ctx, input }) => {
      return getRouterOnboardingStateWithDb(ctx.db, input.routerId);
    }),

  listRuns: protectedProcedure
    .input(routerIdInputSchema)
    .query(async ({ ctx, input }) => {
      const runs = await listRouterOnboardingRunsWithDb(ctx.db, input.routerId);
      return runs.map((run) => sanitizeOnboardingRunForClient(run));
    }),

  saveProfile: protectedProcedure
    .input(routerOnboardingProfileInputSchema)
    .mutation(async ({ ctx, input }) => {
      const profile = await upsertRouterOnboardingProfileWithDb(ctx.db, input);
      return getRouterOnboardingStateWithDb(ctx.db, profile.routerId);
    }),

  advance: protectedProcedure
    .input(routerIdInputSchema)
    .mutation(async ({ ctx, input }) => {
      return advanceRouterOnboardingWithDb(ctx.db, input.routerId, {
        featureEnabled: true,
      });
    }),

  pause: protectedProcedure
    .input(routerIdInputSchema)
    .mutation(async ({ ctx, input }) => {
      await pauseRouterOnboardingWithDb(ctx.db, input.routerId);
      return getRouterOnboardingStateWithDb(ctx.db, input.routerId);
    }),

  retry: protectedProcedure
    .input(routerIdInputSchema)
    .mutation(async ({ ctx, input }) => {
      const result = await retryRouterOnboardingWithDb(ctx.db, input.routerId);
      return {
        result,
        state: await getRouterOnboardingStateWithDb(ctx.db, input.routerId),
      };
    }),
});
