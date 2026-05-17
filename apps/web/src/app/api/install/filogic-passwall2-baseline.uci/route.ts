import { getCurrentAx3000tInstallBaseline } from "~/server/vectra/global-template";

export const dynamic = "force-dynamic";

export async function GET() {
  const baseline = await getCurrentAx3000tInstallBaseline();

  return new Response(baseline, {
    status: 200,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}
