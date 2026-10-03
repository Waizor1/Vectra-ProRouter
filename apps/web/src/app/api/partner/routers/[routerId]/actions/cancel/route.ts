import { handlePartnerRouterActionCancel } from "~/server/vectra/partner-routers";
export const dynamic = "force-dynamic";
// Server-to-server only: cancel an owner's action, found by the partner's
// Idempotency-Key, while the router has never been handed it.
export async function POST(
  request: Request,
  context: { params: Promise<{ routerId: string }> },
) {
  try {
    return await handlePartnerRouterActionCancel(
      request,
      (await context.params).routerId,
    );
  } catch {
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
