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
    const response = await checkInRouter(auth.router.id, payload, {
      devicePublicKey: auth.credential.devicePublicKey,
      router: auth.router,
    });
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
