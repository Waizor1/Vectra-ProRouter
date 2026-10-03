import { handleRouterUnbindRequest } from "~/server/vectra/partner-api";

export const dynamic = "force-dynamic";

type RouteContext = {
  params: Promise<{
    routerId: string;
  }>;
};

// Server-to-server only (ADR-0006): unlink a router from its Vectra account.
export async function DELETE(request: Request, context: RouteContext) {
  try {
    const { routerId } = await context.params;
    return await handleRouterUnbindRequest(request, routerId);
  } catch (error) {
    console.error("[partner-api] router unbind failed", error);
    return Response.json({ error: "internal" }, { status: 500 });
  }
}
