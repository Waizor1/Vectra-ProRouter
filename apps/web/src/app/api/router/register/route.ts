import { eventLog } from "@vectra/db";

import { db } from "~/server/db";
import { registerRouter } from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";
import {
  publicInstallRegisterRateLimiter,
  readRequestIp,
} from "~/server/vectra/public-install-rate-limit";

import { parseJsonBody, toRouteErrorResponse } from "../_lib";

export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  try {
    const clientIp = readRequestIp(request);
    const rateLimit = publicInstallRegisterRateLimiter.consume(clientIp);

    if (!rateLimit.allowed) {
      await db.insert(eventLog).values({
        type: "router.enrollment.rate_limited",
        severity: "warning",
        message:
          "Public router enrollment request was rate-limited before registration.",
        metadata: {
          clientIp,
          resetAt: new Date(rateLimit.resetAt).toISOString(),
        },
      });

      return Response.json(
        {
          error:
            "Too many router enrollment attempts from this address. Retry later.",
        },
        {
          status: 429,
          headers: {
            "retry-after": String(
              Math.max(1, Math.ceil((rateLimit.resetAt - Date.now()) / 1000)),
            ),
          },
        },
      );
    }

    const payload: unknown = await parseJsonBody(request);
    const authenticatedRouter = await authenticateRouter(request.headers);
    const response = await registerRouter(payload, {
      authenticatedRouterId: authenticatedRouter?.router.id ?? null,
    });
    return Response.json(response, { status: 201 });
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
