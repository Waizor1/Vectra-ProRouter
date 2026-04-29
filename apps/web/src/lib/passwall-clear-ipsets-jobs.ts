import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";

export const passwallClearIpsetsTerminalPurpose = "passwall-clear-ipsets";

const passwallClearIpsetsTimeoutSeconds = 90;

type PasswallClearIpsetsJobLike = {
  type: string;
  payload: Record<string, unknown> | null;
};

export function isPasswallClearIpsetsTerminalPayload(
  payload: Record<string, unknown> | null | undefined,
) {
  return payload?.purpose === passwallClearIpsetsTerminalPurpose;
}

export function isPasswallClearIpsetsJob(job: PasswallClearIpsetsJobLike) {
  return (
    job.type === "run_terminal_command" &&
    isPasswallClearIpsetsTerminalPayload(job.payload)
  );
}

export function buildTerminalPasswallClearIpsetsPayload() {
  const command = [
    "set -eu",
    'export PATH="/sbin:/usr/sbin:/bin:/usr/bin:${PATH:-}"',
    "uci -q set passwall2.@global[0].flush_set='1'",
    "uci -q commit passwall2",
    "/etc/init.d/passwall2 restart",
    "printf 'PassWall2 IPSET/NFTSet clear requested; passwall2 restarted\\n'",
  ].join("\n");

  return runTerminalCommandJobPayloadSchema.parse({
    command,
    timeoutSeconds: passwallClearIpsetsTimeoutSeconds,
    purpose: passwallClearIpsetsTerminalPurpose,
  });
}
