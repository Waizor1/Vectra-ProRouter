import { recordJobResult } from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";
import { safelyMaybeAdvanceRouterOnboarding } from "~/server/vectra/router-auto-onboarding";
import {
  routerJobResultRateLimiter,
  routerRateLimitedResponse,
} from "~/server/vectra/public-install-rate-limit";

import { parseJsonBody, toRouteErrorResponse } from "../_lib";

export async function POST(request: Request) {
  try {
    const auth = await authenticateRouter(request.headers);
    if (!auth) {
      return Response.json(
        { error: "Unauthorized router request." },
        { status: 401 },
      );
    }
    const rateLimit = routerJobResultRateLimiter.consume(auth.router.id);
    if (!rateLimit.allowed) {
      return routerRateLimitedResponse(rateLimit.resetAt);
    }
    const payload: unknown = await parseJsonBody(request);
    const response = await recordJobResult(auth.router.id, payload);
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
