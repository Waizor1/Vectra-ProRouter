import {
  getFirmwareManifest,
} from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";

import { toRouteErrorResponse } from "../../_lib";

type RouteContext = {
  params: Promise<{
    board: string;
  }>;
};

export async function GET(request: Request, context: RouteContext) {
  try {
    const auth = await authenticateRouter(request.headers);
    if (!auth) {
      return Response.json({ error: "Unauthorized router request." }, { status: 401 });
    }
    const { board } = await context.params;
    const { searchParams } = new URL(request.url);
    const manifest = await getFirmwareManifest(board, searchParams);

    if (!manifest) {
      return Response.json({ error: "Firmware manifest not found" }, { status: 404 });
    }

    return Response.json(manifest);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
