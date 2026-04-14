import { env } from "~/env";
import { buildAx3000tShuntRebindScript } from "~/app/enrollment/install-presets";

export function GET() {
  return new Response(
    buildAx3000tShuntRebindScript({
      controlDomain: env.VECTRA_DEFAULT_CONTROL_DOMAIN,
    }),
    {
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "public, max-age=300",
      },
    },
  );
}
