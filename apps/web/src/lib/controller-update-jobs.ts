import { runTerminalCommandJobPayloadSchema } from "@vectra/contracts";

import { controllerUpdateGuardScript } from "~/lib/controller-update-guard-script";
import {
  compareControllerVersions,
  normalizeControllerVersion,
} from "~/lib/controller-version";

export const controllerSelfUpdateTerminalPurpose = "controller-self-update";
export const controllerSelfUpdateCompatTerminalPurpose =
  "controller-self-update-compat";
export const controllerTerminalSupportMinVersion = "0.1.12-r1";

const controllerSelfUpdateTimeoutSeconds = 120;

// Exit codes of the generated command besides 0/1: 72 = resource guard (RAM,
// /overlay, /tmp), 73 = no room for a rollback copy (nothing was changed),
// 74 = a previous update is still being verified (or its rollback is being
// retried) by the guard, 75 = this router rolled back from this very version
// in the last 24 h (an operator-forced update passes).
export const controllerSelfUpdateNoRollbackRoomExitCode = 73;
export const controllerSelfUpdateGuardBusyExitCode = 74;
export const controllerSelfUpdateRecentlyRolledBackExitCode = 75;
export const controllerUpdateGuardPath =
  "/etc/vectra-controller/update-rollback/guard.sh";
export const controllerUpdateRollbackMarkerPath =
  "/etc/vectra-controller/update-rollback.marker";

// Overrides of the guard's judgement windows (seconds). Production leaves
// them unset (guard defaults: stable 120, crash 300, contact 900, probeFrom
// 600, probeEvery 60, hold 180, network 3600, tick 10, restore retry 60
// doubling to 1800, 10 attempts); the docker stand shrinks them.
export type ControllerUpdateGuardTimings = Partial<
  Record<
    | "stable"
    | "crash"
    | "contact"
    | "probeFrom"
    | "probeEvery"
    | "hold"
    | "network"
    | "tick"
    | "restartDelay"
    | "preparedTimeout"
    | "armedTimeout"
    | "restoreRetry"
    | "restoreRetryMax"
    | "restoreMaxAttempts",
    number
  >
>;

const guardTimingEnv: Record<keyof ControllerUpdateGuardTimings, string> = {
  stable: "VECTRA_GUARD_STABLE_SECONDS",
  crash: "VECTRA_GUARD_CRASH_SECONDS",
  contact: "VECTRA_GUARD_CONTACT_SECONDS",
  probeFrom: "VECTRA_GUARD_PROBE_FROM_SECONDS",
  probeEvery: "VECTRA_GUARD_PROBE_EVERY_SECONDS",
  hold: "VECTRA_GUARD_HOLD_SECONDS",
  network: "VECTRA_GUARD_NETWORK_SECONDS",
  tick: "VECTRA_GUARD_TICK_SECONDS",
  restartDelay: "VECTRA_GUARD_RESTART_DELAY_SECONDS",
  preparedTimeout: "VECTRA_GUARD_PREPARED_TIMEOUT_SECONDS",
  armedTimeout: "VECTRA_GUARD_ARMED_TIMEOUT_SECONDS",
  restoreRetry: "VECTRA_GUARD_RESTORE_RETRY_SECONDS",
  restoreRetryMax: "VECTRA_GUARD_RESTORE_RETRY_MAX_SECONDS",
  restoreMaxAttempts: "VECTRA_GUARD_RESTORE_MAX_ATTEMPTS",
};

function guardTimingPrefix(timings: ControllerUpdateGuardTimings | undefined) {
  if (!timings) {
    return "";
  }
  return Object.entries(timings)
    .filter(
      (entry): entry is [keyof ControllerUpdateGuardTimings, number] =>
        typeof entry[1] === "number" &&
        Number.isInteger(entry[1]) &&
        entry[1] >= 0,
    )
    .map(([key, value]) => `${guardTimingEnv[key]}=${value} `)
    .join("");
}

const guardHeredocDelimiter = "VECTRA_UPDATE_GUARD_EOF";
const guardForcedPrepare = 'VECTRA_GUARD_FORCE=1 ';

// Was this self-update command generated with an operator force?
export function isForcedControllerSelfUpdateCommand(
  command: string | null | undefined,
) {
  return (
    typeof command === "string" &&
    command.includes(`\n${guardForcedPrepare}`) &&
    /\nVECTRA_GUARD_FORCE=1 (?:VECTRA_GUARD_[A-Z_]+=\d+ )*sh "\$guard" prepare /.test(command)
  );
}

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
  guardTimings?: ControllerUpdateGuardTimings;
  // Operator forced the update: also past the guard's "rolled back from this
  // version in the last 24 h" refusal.
  force?: boolean;
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
    // After `installing=1` every failure first puts the previous version's
    // files and opkg stanzas back (the old agent is still the running
    // process: the pair is installed with VECTRA_SKIP_POSTINST_RESTART).
    'guard_dir=/etc/vectra-controller/update-rollback',
    'guard="$guard_dir/guard.sh"',
    'installing=0',
    'fail() { if [ "$installing" = 1 ]; then installing=0; sh "$guard" restore-files "$*" >&2 || true; echo "controller self-update failed: $* (previous version restored)" >&2; exit 1; fi; echo "controller self-update failed: $*" >&2; exit 1; }',
    'fetch() { if command -v wget >/dev/null 2>&1; then wget -q -O "$1" "$2" || fail "download $2"; elif command -v uclient-fetch >/dev/null 2>&1; then uclient-fetch -q -O "$1" "$2" || fail "download $2"; else fail "missing downloader"; fi; }',
    'check_sha() { actual_sha="$(sha256sum "$1" | awk \'{print $1}\')"; [ "$actual_sha" = "$2" ] || fail "sha256 mismatch for $1"; }',
    'pkg_status() { awk -F\': \' -v pkg="$1" \'/^Package:/ { current = ($2 == pkg); next } current { print }\' /usr/lib/opkg/status 2>/dev/null; }',
    'pkg_ok() { pkg="$1"; status="$(pkg_status "$pkg" || true)"; printf "%s\\n" "$status" | grep -Eq "^Status: install (ok|user) installed$" || fail "$pkg is not installed"; if [ -n "$target_version" ]; then printf "%s\\n" "$status" | grep -Fqx "Version: $target_version" || fail "$pkg is not at $target_version"; fi; }',
    'need_file() { [ -s "$1" ] || fail "missing LuCI file $1"; }',
    'install_pair() { VECTRA_SKIP_POSTINST_RESTART=1 opkg install --force-reinstall "$agent_ipk" "$luci_ipk"; }',
    'cleanup_luci() { rm -f /tmp/luci-indexcache.*; rm -rf /tmp/luci-modulecache/; /etc/init.d/rpcd reload >/dev/null 2>&1 || true; }',
    // Belt-and-suspenders verification added after the r27 incident
    // (2026-05-28, totchto-filiciy): a binary-less .ipk made it to the feed
    // and opkg cheerfully replaced the controller with an empty package,
    // bricking remote management. These two helpers stop that failure mode
    // at install time. verify_ipk_has_agent inspects the downloaded .ipk
    // BEFORE running opkg, so a corrupted feed is refused without losing
    // the running r{N-1} controller. verify_agent_on_disk re-checks AFTER
    // opkg install but BEFORE scheduling the restart, so even a successful
    // opkg install that somehow left no binary aborts the update before
    // the live controller is killed.
    //
    // Size check uses `wc -c`, NOT `stat`: the AX3000T OpenWrt image ships no
    // `stat` binary (busybox built without it), so `stat -c %s`/`stat -f %z`
    // both exit 127 and the old `|| echo 0` fallback made bin_size=0 on every
    // router — verify_agent_on_disk failed right after a perfectly good
    // install, leaving r28 on disk but never restarted (sergeyavito canary,
    // 2026-05-29). `wc -c` is a core busybox applet and is always present.
    'verify_ipk_has_agent() { ipk="$1"; inspect_dir="$workdir/agent-inspect"; rm -rf "$inspect_dir"; mkdir -p "$inspect_dir"; tar -xzf "$ipk" -C "$inspect_dir" 2>/dev/null || fail "agent .ipk is not a valid tar.gz"; tar -tzf "$inspect_dir/data.tar.gz" 2>/dev/null | grep -qE "^\\./usr/sbin/vectra-controller-agent$" || fail "agent .ipk is missing usr/sbin/vectra-controller-agent (corrupted package — refusing to install)"; rm -rf "$inspect_dir"; }',
    'verify_agent_on_disk() { [ -x /usr/sbin/vectra-controller-agent ] || fail "/usr/sbin/vectra-controller-agent missing or non-executable after opkg install (refusing to schedule restart)"; bin_size="$(wc -c < /usr/sbin/vectra-controller-agent 2>/dev/null | tr -dc 0-9)"; [ "${bin_size:-0}" -ge 1048576 ] || fail "/usr/sbin/vectra-controller-agent is suspiciously small (${bin_size:-0} bytes < 1 MB)"; }',
    'agent_ipk="$workdir/vectra-controller-agent.ipk"',
    'luci_ipk="$workdir/luci-app-vectra-controller.ipk"',
    `fetch "$agent_ipk" ${shellSingleQuote(agentArtifact.artifactUrl)}`,
    `check_sha "$agent_ipk" ${shellSingleQuote(agentArtifact.sha256)}`,
    `fetch "$luci_ipk" ${shellSingleQuote(luciArtifact.artifactUrl)}`,
    `check_sha "$luci_ipk" ${shellSingleQuote(luciArtifact.sha256)}`,
    'verify_ipk_has_agent "$agent_ipk"',
    // The rollback guard (router/vectra-controller-agent/openwrt/update-guard)
    // is written here, outside both packages, so it protects an update from
    // any installed version. `prepare` copies the installed pair aside first:
    // no copy, no update (exit 73).
    'mkdir -p "$guard_dir" || { echo "controller self-update refused (no room for a rollback copy): cannot create $guard_dir" >&2; exit 73; }',
    `cat > "$guard.new" <<'${guardHeredocDelimiter}' || { rm -f "$guard.new"; echo "controller self-update refused (no room for a rollback copy): cannot write $guard" >&2; exit 73; }`,
    controllerUpdateGuardScript,
    guardHeredocDelimiter,
    'chmod 0755 "$guard.new" && mv "$guard.new" "$guard"',
    "guard_rc=0",
    `${args.force ? guardForcedPrepare : ""}${guardTimingPrefix(args.guardTimings)}sh "$guard" prepare "$target_version" || guard_rc=$?`,
    'if [ "$guard_rc" -ne 0 ]; then [ -f "$guard_dir/meta" ] || rm -rf "$guard_dir"; exit "$guard_rc"; fi',
    ': > "$skip"',
    "installing=1",
    'install_pair || fail "opkg install controller/LuCI pair"',
    'verify_agent_on_disk',
    'pkg_ok vectra-controller-agent',
    'pkg_ok luci-app-vectra-controller',
    'need_file /usr/share/luci/menu.d/luci-app-vectra-controller.json',
    'need_file /usr/share/rpcd/acl.d/luci-app-vectra-controller.json',
    'need_file /usr/libexec/vectra-controller/luci-bridge.sh',
    'need_file /www/luci-static/resources/view/vectra-controller/status.js',
    'cleanup_luci',
    "installing=0",
    // The guard restarts the agent itself (after the job result is sent),
    // then judges it: crash loop, missing binary or no panel contact while
    // the panel answers => the previous version comes back on its own.
    'sh "$guard" arm',
    'sh "$guard" launch',
    `printf '%s\\n' ${shellSingleQuote(installedSummary)}`,
  ].join("\n");

  return runTerminalCommandJobPayloadSchema.parse({
    command,
    timeoutSeconds: controllerSelfUpdateTimeoutSeconds,
    purpose: args.purpose ?? controllerSelfUpdateTerminalPurpose,
    artifactVersion: artifactVersion ?? null,
  });
}
