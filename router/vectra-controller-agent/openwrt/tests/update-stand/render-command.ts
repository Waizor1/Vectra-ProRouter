// Renders the panel's controller self-update command for the docker stand,
// from the same generator the panel uses (apps/web/src/lib/controller-update-jobs.ts).
// Bundled by run.sh with esbuild; input is one JSON argument:
//   { version, agentUrl, agentSha, luciUrl, luciSha, timings }
import { buildTerminalControllerSelfUpdatePayload } from "../../../../../apps/web/src/lib/controller-update-jobs";

const input = JSON.parse(process.argv[2] ?? "{}") as {
  version: string;
  agentUrl: string;
  agentSha: string;
  luciUrl: string;
  luciSha: string;
  timings?: Record<string, number>;
};

const payload = buildTerminalControllerSelfUpdatePayload({
  artifactVersion: input.version,
  packageArtifacts: [
    { name: "vectra-controller-agent", artifactUrl: input.agentUrl, sha256: input.agentSha },
    { name: "luci-app-vectra-controller", artifactUrl: input.luciUrl, sha256: input.luciSha },
  ],
  guardTimings: input.timings,
});
if (!payload) {
  throw new Error("generator returned no payload");
}
process.stdout.write(payload.command);
