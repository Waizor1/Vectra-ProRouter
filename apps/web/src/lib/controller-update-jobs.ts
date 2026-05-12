import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";

import {
  compareControllerVersions,
  normalizeControllerVersion,
} from "~/lib/controller-version";

export const controllerSelfUpdateTerminalPurpose = "controller-self-update";
export const controllerSelfUpdateCompatTerminalPurpose =
  "controller-self-update-compat";
export const controllerTerminalSupportMinVersion = "0.1.12-r1";

const controllerSelfUpdateTimeoutSeconds = 120;

type ControllerPackageArtifact = {
  name: string;
  artifactUrl: string;
  sha256: string;
  artifactVersion?: string | null;
};

type ControllerUpdateJobLike = {
  type: string;
  payload: Record<string, unknown> | null;
};

function shellSingleQuote(value: string) {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

function findControllerPackageArtifact(
  artifacts: ReadonlyArray<ControllerPackageArtifact>,
  packageName: string,
) {
  return (
    artifacts.find(
      (artifact) =>
        artifact.name === packageName &&
        artifact.artifactUrl.trim().length > 0 &&
        artifact.sha256.trim().length > 0,
    ) ?? null
  );
}

export function isControllerSelfUpdateTerminalPayload(
  payload: Record<string, unknown> | null | undefined,
) {
  return (
    payload?.purpose === controllerSelfUpdateTerminalPurpose ||
    payload?.purpose === controllerSelfUpdateCompatTerminalPurpose
  );
}

export function isControllerUpdateJob(job: ControllerUpdateJobLike) {
  return (
    job.type === "update_controller" ||
    (job.type === "run_terminal_command" &&
      isControllerSelfUpdateTerminalPayload(job.payload))
  );
}

export function shouldUseTerminalControllerSelfUpdate(
  installedControllerVersion: string | null | undefined,
) {
  const normalized = normalizeControllerVersion(installedControllerVersion);
  if (!normalized) {
    return false;
  }

  return (
    compareControllerVersions(normalized, controllerTerminalSupportMinVersion) ??
    -1
  ) >= 0;
}

export function buildTerminalControllerSelfUpdatePayload(args: {
  artifactVersion: string | null | undefined;
  packageArtifacts: ReadonlyArray<ControllerPackageArtifact>;
  purpose?:
    | typeof controllerSelfUpdateTerminalPurpose
    | typeof controllerSelfUpdateCompatTerminalPurpose;
}) {
  const agentArtifact = findControllerPackageArtifact(
    args.packageArtifacts,
    "vectra-controller-agent",
  );
  const luciArtifact = findControllerPackageArtifact(
    args.packageArtifacts,
    "luci-app-vectra-controller",
  );

  if (!agentArtifact || !luciArtifact) {
    return null;
  }

  const artifactVersion =
    normalizeControllerVersion(args.artifactVersion) ??
    normalizeControllerVersion(agentArtifact.artifactVersion) ??
    normalizeControllerVersion(luciArtifact.artifactVersion);
  const installedSummary = `controller self-update to ${
    artifactVersion ?? "target"
  } installed`;

  const command = [
    "set -eu",
    "skip=/tmp/vectra-skip-postinst-restart",
    "mem_available_mb=\"$(awk '/^MemAvailable:/ { print int($2 / 1024); found=1; exit } END { if (!found) print 0 }' /proc/meminfo 2>/dev/null || printf 0)\"",
    "df_free_mb() { df -kP \"$1\" 2>/dev/null | awk 'NR == 2 { print int($4 / 1024); found=1; exit } END { if (!found) print 0 }'; }",
    'overlay_free_mb="$(df_free_mb /overlay)"',
    'tmp_free_mb="$(df_free_mb /tmp)"',
    'if [ "${mem_available_mb:-0}" -lt 48 ] || [ "${overlay_free_mb:-0}" -lt 8 ] || [ "${tmp_free_mb:-0}" -lt 16 ]; then',
    '  echo "controller self-update resource guard: RAM=${mem_available_mb:-0}MB /overlay=${overlay_free_mb:-0}MB /tmp=${tmp_free_mb:-0}MB" >&2',
    "  exit 72",
    "fi",
    'workdir="$(mktemp -d /tmp/vectra-controller-update.XXXXXX)"',
    'cleanup() { rm -rf "$workdir"; rm -f "$skip"; }',
    "trap cleanup EXIT INT TERM",
    `target_version=${shellSingleQuote(artifactVersion ?? "")}`,
    'fail() { echo "controller self-update failed: $*" >&2; exit 1; }',
    'fetch() { if command -v wget >/dev/null 2>&1; then wget -q -O "$1" "$2" || fail "download $2"; elif command -v uclient-fetch >/dev/null 2>&1; then uclient-fetch -q -O "$1" "$2" || fail "download $2"; else fail "missing downloader"; fi; }',
    'check_sha() { actual_sha="$(sha256sum "$1" | awk \'{print $1}\')"; [ "$actual_sha" = "$2" ] || fail "sha256 mismatch for $1"; }',
    'pkg_status() { awk -F\': \' -v pkg="$1" \'/^Package:/ { current = ($2 == pkg); next } current { print }\' /usr/lib/opkg/status 2>/dev/null; }',
    'pkg_ok() { pkg="$1"; status="$(pkg_status "$pkg" || true)"; printf "%s\\n" "$status" | grep -Eq "^Status: install (ok|user) installed$" || fail "$pkg is not installed"; if [ -n "$target_version" ]; then printf "%s\\n" "$status" | grep -Fqx "Version: $target_version" || fail "$pkg is not at $target_version"; fi; }',
    'need_file() { [ -s "$1" ] || fail "missing LuCI file $1"; }',
    'install_pair() { VECTRA_SKIP_POSTINST_RESTART=1 opkg install --force-reinstall "$agent_ipk" "$luci_ipk"; }',
    'cleanup_luci() { rm -f /tmp/luci-indexcache.*; rm -rf /tmp/luci-modulecache/; /etc/init.d/rpcd reload >/dev/null 2>&1 || true; }',
    'schedule_restart() { rm -f "$skip"; (sleep 5; /etc/init.d/vectra-controller enable >/dev/null 2>&1 || true; if /etc/init.d/vectra-controller running >/dev/null 2>&1; then /etc/init.d/vectra-controller restart >/tmp/vectra-controller-self-update.log 2>&1; else /etc/init.d/vectra-controller start >/tmp/vectra-controller-self-update.log 2>&1; fi) & }',
    'agent_ipk="$workdir/vectra-controller-agent.ipk"',
    'luci_ipk="$workdir/luci-app-vectra-controller.ipk"',
    `fetch "$agent_ipk" ${shellSingleQuote(agentArtifact.artifactUrl)}`,
    `check_sha "$agent_ipk" ${shellSingleQuote(agentArtifact.sha256)}`,
    `fetch "$luci_ipk" ${shellSingleQuote(luciArtifact.artifactUrl)}`,
    `check_sha "$luci_ipk" ${shellSingleQuote(luciArtifact.sha256)}`,
    ': > "$skip"',
    'install_pair || fail "opkg install controller/LuCI pair"',
    'pkg_ok vectra-controller-agent',
    'pkg_ok luci-app-vectra-controller',
    'need_file /usr/share/luci/menu.d/luci-app-vectra-controller.json',
    'need_file /usr/share/rpcd/acl.d/luci-app-vectra-controller.json',
    'need_file /usr/libexec/vectra-controller/luci-bridge.sh',
    'need_file /www/luci-static/resources/view/vectra-controller/status.js',
    'cleanup_luci',
    'schedule_restart',
    `printf '%s\\n' ${shellSingleQuote(installedSummary)}`,
  ].join("; ");

  return runTerminalCommandJobPayloadSchema.parse({
    command,
    timeoutSeconds: controllerSelfUpdateTimeoutSeconds,
    purpose: args.purpose ?? controllerSelfUpdateTerminalPurpose,
    artifactVersion: artifactVersion ?? null,
  });
}
