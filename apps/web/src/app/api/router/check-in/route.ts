import { checkInRouter } from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";
import { safelyMaybeAdvanceRouterOnboarding } from "~/server/vectra/router-auto-onboarding";
import {
  readRequestIp,
  routerCheckInRateLimiter,
  routerRateLimitedResponse,
} from "~/server/vectra/public-install-rate-limit";
import {
  checkInSignatureMode,
  gateCheckInSignature,
} from "~/server/vectra/router-auth-policy";

import {
  AUTHENTICATED_ROUTER_BODY_MAX_BYTES,
  parseJsonBody,
  parseJsonBodyWithDigest,
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
    // A2: with both signature switches off neither the digest nor the
    // signature headers are even read.
    const signatureMode = checkInSignatureMode();
    let payload: unknown;
    if (signatureMode === "off") {
      payload = await parseJsonBody(request, AUTHENTICATED_ROUTER_BODY_MAX_BYTES);
    } else {
      const body = await parseJsonBodyWithDigest(
        request,
        AUTHENTICATED_ROUTER_BODY_MAX_BYTES,
      );
      payload = body.payload;
      const refusal = await gateCheckInSignature({
        mode: signatureMode,
        routerId: auth.router.id,
        headers: request.headers,
        bodySha256Hex: body.bodySha256Hex,
        devicePublicKey: auth.credential.devicePublicKey,
      });
      if (refusal) {
        return refusal;
      }
    }
    const rowStillFresh =
      performance.now() - authenticatedAt <= AUTH_ROUTER_ROW_MAX_AGE_MS;
    const response = await checkInRouter(auth.router.id, payload, {
      devicePublicKey: auth.credential.devicePublicKey,
      credentialId: auth.credential.id,
      clientIp: readRequestIp(request),
      ...(rowStillFresh ? { router: auth.router } : {}),
    });
    await safelyMaybeAdvanceRouterOnboarding(auth.router.id);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
