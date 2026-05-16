import { env } from "~/env";
import { buildFilogicBootstrapScript } from "~/app/enrollment/install-presets";

export function GET() {
  return new Response(
    buildFilogicBootstrapScript({
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
