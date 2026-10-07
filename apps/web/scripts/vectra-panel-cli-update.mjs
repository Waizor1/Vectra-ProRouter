// `update controller <selector> [--channel stable|beta] [--force]` of
// vectra-panel-cli.mjs, as the tRPC input it sends. --force passes the panel's
// rollout guard (shaky control plane) and the router guard's refusal of a
// version it rolled back from in the last 24 h.
import { parseArgs } from "node:util";

/**
 * @param {string} routerId
 * @param {string[]} args the arguments after the selector
 * @returns {{ routerId: string; channel: string; force: boolean }}
 */
export function controllerUpdateMutationInput(routerId, args) {
  const parsed = parseArgs({
    args,
    allowPositionals: true,
    options: {
      channel: { type: "string" },
      force: { type: "boolean" },
    },
  });
  const channel = parsed.values.channel ?? "stable";
  if (channel !== "stable" && channel !== "beta") {
    throw new Error(`--channel must be stable or beta, not ${channel}`);
  }
  return { routerId, channel, force: parsed.values.force === true };
}
