import { handlePartnerRoutersRead } from "~/server/vectra/partner-routers";
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  try {
    return await handlePartnerRoutersRead(request);
  } catch {
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
