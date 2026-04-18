import { env } from "~/env";
import { PublicInstallWorkspace } from "~/components/public-install-workspace";
import {
  DEFAULT_CONTROL_DOMAIN,
  buildAx3000tBootstrapCommand,
  buildAx3000tBootstrapScriptUrl,
} from "~/app/enrollment/install-presets";

export default function PublicInstallPage() {
  const controlDomain =
    env.VECTRA_DEFAULT_CONTROL_DOMAIN ?? DEFAULT_CONTROL_DOMAIN;

  return (
    <PublicInstallWorkspace
      quickCommand={buildAx3000tBootstrapCommand(controlDomain)}
      bootstrapScriptUrl={buildAx3000tBootstrapScriptUrl(controlDomain)}
    />
  );
}
