import { handlePartnerRouterActionCancel } from "~/server/vectra/partner-routers";
export const dynamic = "force-dynamic";
// Server-to-server only: cancel an owner's action while it is still queued.
export async function POST(
  request: Request,
  context: { params: Promise<{ routerId: string; actionId: string }> },
) {
  try {
    const { routerId, actionId } = await context.params;
    return await handlePartnerRouterActionCancel(request, routerId, actionId);
  } catch {
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
