import { env } from "~/env";
import { buildFilogicShuntRebindScript } from "~/app/enrollment/install-presets";

export function GET() {
  return new Response(
    buildFilogicShuntRebindScript({
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
