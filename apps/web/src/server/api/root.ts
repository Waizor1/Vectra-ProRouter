import { draftRouter } from "~/server/api/routers/draft";
import { fleetRouter } from "~/server/api/routers/fleet";
import { logsRouter } from "~/server/api/routers/logs";
import { notificationsRouter } from "~/server/api/routers/notifications";
import { onboardingRouter } from "~/server/api/routers/onboarding";
import { optimizationRouter } from "~/server/api/routers/optimization";
import { rescueRouter } from "~/server/api/routers/rescue";
import { terminalRouter } from "~/server/api/routers/terminal";
import { updateRouter } from "~/server/api/routers/update";
import { createCallerFactory, createTRPCRouter } from "~/server/api/trpc";

/**
 * This is the primary router for your server.
 *
 * All routers added in /api/routers should be manually added here.
 */
export const appRouter = createTRPCRouter({
  draft: draftRouter,
  fleet: fleetRouter,
  logs: logsRouter,
  notifications: notificationsRouter,
  onboarding: onboardingRouter,
  optimization: optimizationRouter,
  rescue: rescueRouter,
  terminal: terminalRouter,
  update: updateRouter,
});

// export type definition of API
export type AppRouter = typeof appRouter;

/**
 * Create a server-side caller for the tRPC API.
 * @example
 * const trpc = createCaller(createContext);
 * const res = await trpc.post.all();
 *       ^? Post[]
 */
export const createCaller = createCallerFactory(appRouter);
