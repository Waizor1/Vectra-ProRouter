import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";

export const routerRebootTerminalPurpose = "router-reboot";

const routerRebootDelaySeconds = 5;
const routerRebootTimeoutSeconds = 15;

type RouterRebootJobLike = {
  type: string;
  payload: Record<string, unknown> | null;
};

export function isRouterRebootTerminalPayload(
  payload: Record<string, unknown> | null | undefined,
) {
  return payload?.purpose === routerRebootTerminalPurpose;
}

export function isRouterRebootJob(job: RouterRebootJobLike) {
  return (
    job.type === "run_terminal_command" &&
    isRouterRebootTerminalPayload(job.payload)
  );
}

export function buildTerminalRouterRebootPayload() {
  const command = [
    "set -eu",
    'log_path="/tmp/vectra-router-reboot.log"',
    `(sleep ${routerRebootDelaySeconds}; /sbin/reboot) >"$log_path" 2>&1 &`,
    "printf 'router reboot scheduled\\n'",
  ].join("\n");

  return runTerminalCommandJobPayloadSchema.parse({
    command,
    timeoutSeconds: routerRebootTimeoutSeconds,
    purpose: routerRebootTerminalPurpose,
  });
}
