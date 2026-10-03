import { handlePartnerRoutersRead } from "~/server/vectra/partner-routers";
export const dynamic = "force-dynamic";
export async function GET(
  request: Request,
  context: { params: Promise<{ routerId: string }> },
) {
  try {
    return await handlePartnerRoutersRead(
      request,
      (await context.params).routerId,
    );
  } catch {
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
