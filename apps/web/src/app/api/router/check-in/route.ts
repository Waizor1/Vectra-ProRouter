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

export async function POST(request: Request) {
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
    const response = await checkInRouter(auth.router.id, payload, {devicePublicKey: auth.credential.devicePublicKey});
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
