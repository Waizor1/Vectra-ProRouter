import { handleRouterClaimRequest } from "~/server/vectra/partner-api";

// Server-to-server only (ADR-0006): the Vectra backend links a router to a
// customer's account. Authenticated by the partner signature, not by an
// operator session, so /api/partner is deliberately outside the middleware.
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  try {
    return await handleRouterClaimRequest(request);
  } catch (error) {
    console.error("[partner-api] router claim failed", error);
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
