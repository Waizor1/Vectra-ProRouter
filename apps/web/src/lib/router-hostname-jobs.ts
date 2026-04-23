import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";

export const routerHostnameUpdateTerminalPurpose = "router-hostname-update";
export const routerHostnameInputPattern =
  /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/;

const routerHostnameUpdateTimeoutSeconds = 30;

type RouterHostnameUpdateJobLike = {
  type: string;
  payload: Record<string, unknown> | null;
};

function shellSingleQuote(value: string) {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

export function normalizeRouterHostname(value: string) {
  return value.trim().toLowerCase();
}

export function isRouterHostnameUpdateTerminalPayload(
  payload: Record<string, unknown> | null | undefined,
) {
  return payload?.purpose === routerHostnameUpdateTerminalPurpose;
}

export function isRouterHostnameUpdateJob(job: RouterHostnameUpdateJobLike) {
  return (
    job.type === "run_terminal_command" &&
    isRouterHostnameUpdateTerminalPayload(job.payload)
  );
}

export function buildTerminalRouterHostnameUpdatePayload(hostname: string) {
  const normalizedHostname = normalizeRouterHostname(hostname);
  const command = [
    "set -eu",
    `new_hostname=${shellSingleQuote(normalizedHostname)}`,
    'uci set system.@system[0].hostname="$new_hostname"',
    "uci commit system",
    "reload_config",
    "/etc/init.d/system reload",
    'printf "hostname updated to %s\\n" "$new_hostname"',
    "ubus call system board",
  ].join("\n");

  return runTerminalCommandJobPayloadSchema.parse({
    command,
    timeoutSeconds: routerHostnameUpdateTimeoutSeconds,
    purpose: routerHostnameUpdateTerminalPurpose,
    hostname: normalizedHostname,
  });
}
