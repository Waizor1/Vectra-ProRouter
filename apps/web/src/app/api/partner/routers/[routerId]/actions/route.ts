import { handlePartnerRouterAction } from "~/server/vectra/partner-routers";
export const dynamic = "force-dynamic";
export async function POST(
  request: Request,
  context: { params: Promise<{ routerId: string }> },
) {
  try {
    return await handlePartnerRouterAction(
      request,
      (await context.params).routerId,
    );
  } catch {
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
