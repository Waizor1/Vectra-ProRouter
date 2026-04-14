import { registerRouter } from "~/server/vectra/router-control";

import { parseJsonBody, toRouteErrorResponse } from "../_lib";

export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  try {
    const payload: unknown = await parseJsonBody(request);
    const response = await registerRouter(payload);
    return Response.json(response, { status: 201 });
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
