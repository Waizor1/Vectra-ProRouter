import { recordJobResult } from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";
import { safelyMaybeAdvanceRouterOnboarding } from "~/server/vectra/router-auto-onboarding";

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
    const payload: unknown = await parseJsonBody(request);
    const response = await recordJobResult(auth.router.id, payload);
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
