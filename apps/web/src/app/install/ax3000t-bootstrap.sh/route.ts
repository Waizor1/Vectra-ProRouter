import { env } from "~/env";
import { buildAx3000tBootstrapScript } from "~/app/enrollment/install-presets";

export function GET() {
  return new Response(
    buildAx3000tBootstrapScript({
      controlDomain: env.VECTRA_DEFAULT_CONTROL_DOMAIN,
      routerApiBase: env.VECTRA_ROUTER_API_BASE_URL,
      artifactBase: env.VECTRA_ARTIFACT_BASE_URL,
    }),
    {
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "public, max-age=300",
      },
    },
  );
}
