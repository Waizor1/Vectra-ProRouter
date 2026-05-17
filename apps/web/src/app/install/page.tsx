import { env } from "~/env";
import { PublicInstallWorkspace } from "~/components/public-install-workspace";
import {
  DEFAULT_CONTROL_DOMAIN,
  buildFilogicBootstrapCommand,
  buildFilogicBootstrapScriptUrl,
} from "~/app/enrollment/install-presets";

export default function PublicInstallPage() {
  const controlDomain =
    env.VECTRA_DEFAULT_CONTROL_DOMAIN ?? DEFAULT_CONTROL_DOMAIN;

  return (
    <PublicInstallWorkspace
      quickCommand={buildFilogicBootstrapCommand(controlDomain)}
      bootstrapScriptUrl={buildFilogicBootstrapScriptUrl(controlDomain)}
    />
  );
}
