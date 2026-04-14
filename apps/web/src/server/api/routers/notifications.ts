import { TRPCError } from "@trpc/server";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import {
  deleteOperatorPushSubscription,
  getBrowserPushPublicKey,
  isBrowserPushConfigured,
  upsertOperatorPushSubscription,
} from "~/server/vectra/browser-push";

const browserPushSubscriptionSchema = z.object({
  endpoint: z.string().url(),
  expirationTime: z.number().nullable().optional(),
  keys: z.object({
    p256dh: z.string().min(1),
    auth: z.string().min(1),
  }),
});

export const notificationsRouter = createTRPCRouter({
  status: protectedProcedure.query(() => ({
    configured: isBrowserPushConfigured(),
    publicKey: getBrowserPushPublicKey(),
    kinds: [
      "offline",
      "direct_mode",
      "incident",
    ] as const,
  })),

  subscribe: protectedProcedure
    .input(
      z.object({
        subscription: browserPushSubscriptionSchema,
        userAgent: z.string().trim().max(1024).nullish(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      if (!isBrowserPushConfigured()) {
        throw new TRPCError({
          code: "BAD_REQUEST",
          message: "Background browser push is not configured on the server.",
        });
      }

      await upsertOperatorPushSubscription({
        operatorUser: ctx.operatorSession.user,
        subscription: input.subscription,
        userAgent: input.userAgent ?? null,
      });

      return {
        ok: true,
      };
    }),

  unsubscribe: protectedProcedure
    .input(
      z.object({
        endpoint: z.string().url(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await deleteOperatorPushSubscription({
        operatorUser: ctx.operatorSession.user,
        endpoint: input.endpoint,
      });

      return {
        ok: true,
      };
    }),
});
