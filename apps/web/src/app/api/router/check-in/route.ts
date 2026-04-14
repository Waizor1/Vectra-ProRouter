import {
  checkInRouter,
} from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";

import { parseJsonBody, toRouteErrorResponse } from "../_lib";

export async function POST(request: Request) {
  try {
    const auth = await authenticateRouter(request.headers);
    if (!auth) {
      return Response.json({ error: "Unauthorized router request." }, { status: 401 });
    }
    const payload: unknown = await parseJsonBody(request);
    const response = await checkInRouter(auth.router.id, payload);
    return Response.json(response);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
