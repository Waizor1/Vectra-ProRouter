import { checkInRouter } from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";
import { safelyMaybeAdvanceRouterOnboarding } from "~/server/vectra/router-auto-onboarding";
import {
  routerCheckInRateLimiter,
  routerRateLimitedResponse,
} from "~/server/vectra/public-install-rate-limit";

import {
  AUTHENTICATED_ROUTER_BODY_MAX_BYTES,
  parseJsonBody,
  toRouteErrorResponse,
} from "../_lib";

/**
 * How long the router row read by authentication may stand in for the
 * check-in's own read. Reading the body comes in between, and on a slow
 * uplink that can take seconds: past this the check-in reads the row again,
 * so an operator's change made meanwhile (status, owner, engine) is seen as
 * before. A normal check-in is read in milliseconds and reuses the row.
 */
const AUTH_ROUTER_ROW_MAX_AGE_MS = 2_000;

// How long this handler took, for the access log and the client: Caddy logs
// response headers, so check-in latency is visible per request.
function serverTiming(startedAt: number) {
  return `app;dur=${(performance.now() - startedAt).toFixed(1)}`;
}

export async function POST(request: Request) {
  const startedAt = performance.now();
  const response = await handleCheckIn(request);
  response.headers.set("Server-Timing", serverTiming(startedAt));
  return response;
}

async function handleCheckIn(request: Request): Promise<Response> {
  try {
    const auth = await authenticateRouter(request.headers);
    const authenticatedAt = performance.now();
    if (!auth) {
      return Response.json(
        { error: "Unauthorized router request." },
        { status: 401 },
      );
    }
    const rateLimit = routerCheckInRateLimiter.consume(auth.router.id);
    if (!rateLimit.allowed) {
      return routerRateLimitedResponse(rateLimit.resetAt);
    }
    const payload: unknown = await parseJsonBody(
      request,
      AUTHENTICATED_ROUTER_BODY_MAX_BYTES,
    );
    const rowStillFresh =
      performance.now() - authenticatedAt <= AUTH_ROUTER_ROW_MAX_AGE_MS;
    const response = await checkInRouter(auth.router.id, payload, {
      devicePublicKey: auth.credential.devicePublicKey,
      ...(rowStillFresh ? { router: auth.router } : {}),
    });
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
