import {
  getArtifactMetadata,
} from "~/server/vectra/router-control";
import { authenticateRouter } from "~/server/vectra/auth";

import { toRouteErrorResponse } from "../../../_lib";

type RouteContext = {
  params: Promise<{
    channel: string;
    name: string;
  }>;
};

export async function GET(request: Request, context: RouteContext) {
  try {
    const auth = await authenticateRouter(request.headers);
    if (!auth) {
      return Response.json({ error: "Unauthorized router request." }, { status: 401 });
    }
    const { channel, name } = await context.params;
    const { searchParams } = new URL(request.url);
    const artifact = await getArtifactMetadata(channel, name, searchParams);

    if (!artifact) {
      return Response.json({ error: "Artifact not found" }, { status: 404 });
    }

    return Response.json(artifact);
  } catch (error) {
    return toRouteErrorResponse(error);
  }
}
