package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
)

const (
	ensureRuntimeActionCompactGeodata = "compact_geodata"
	ensureRuntimeActionDNSMasqFull    = "dnsmasq_full"
	// ensureRuntimeActionXrayBinary puts the fleet's xray build back. It exists
	// because the PassWall LuCI "update" button fetches whatever XTLS released
	// last, and a new enough xray drops options this PassWall still generates:
	// 26.9.9 removed outbound "proxySettings", PassWall 26.4.10 still writes it
	// for dns-out, and xray refused to start (andrey-avito, 2026-09-28). The
	// same router later lost the binary altogether. Opt-in only — it is never
	// part of the default action set.
	ensureRuntimeActionXrayBinary = "xray_binary"

	// defaultFleetXrayVersion is the build the fleet runs; the checksum is the
	// SHA2-256 XTLS publishes for Xray-linux-arm64-v8a.zip, verified against
	// the .dgst on a live router before it was pinned here.
	defaultFleetXrayVersion = "26.7.28"
	defaultFleetXraySHA256  = "f5698bb218ada3b4022db26fafc39601c5f53b46b19eb76c9616325985807501"

	// Single source of truth lives in the passwall package so the import-time
	// fallback and this runtime enforcement can never drift apart again.
	defaultCompactGeoIPURL   = passwall.DefaultCompactGeoIPURL
	defaultCompactGeoSiteURL = passwall.DefaultCompactGeoSiteURL
	defaultPasswallAssetDir  = "/usr/share/v2ray/"
)

func runEnsurePasswallRuntimeJob(
	ctx context.Context,
	backend commandRunner,
	payload map[string]interface{},
	inventoryBefore controlplane.RouterInventory,
) (map[string]interface{}, []passwall.CommandResult, error) {
	actions := ensureRuntimeActions(payload)
	actionResults := make([]map[string]interface{}, 0, len(actions))
	commandResults := make([]passwall.CommandResult, 0, len(actions)+1)
	errors := make([]string, 0)

	for _, action := range actions {
		var (
			result passwall.CommandResult
			err    error
		)
		switch action {
		case ensureRuntimeActionCompactGeodata:
			result, err = backend.Run(
				ctx,
				"sh",
				"-c",
				compactGeodataRepairScript(
					firstNonEmptyRuntimePayloadString(payload, "assetDirectory", defaultPasswallAssetDir),
					firstNonEmptyRuntimePayloadString(payload, "geoipUrl", defaultCompactGeoIPURL),
					firstNonEmptyRuntimePayloadString(payload, "geositeUrl", defaultCompactGeoSiteURL),
				),
			)
		case ensureRuntimeActionDNSMasqFull:
			result, err = backend.Run(ctx, "sh", "-c", dnsmasqFullRepairScript())
		case ensureRuntimeActionXrayBinary:
			version, sha256, pinErr := fleetXrayPin(payload)
			if pinErr != nil {
				err = pinErr
				break
			}
			repairCtx, cancel := context.WithTimeout(ctx, xrayBinaryRepairTimeout)
			result, err = backend.Run(repairCtx, "sh", "-c", xrayBinaryRepairScript(version, sha256, payloadBool(payload, "xrayReplaceInPlace")))
			cancel()
		default:
			err = fmt.Errorf("unsupported ensure_passwall_runtime action %q", action)
		}

		result = passwall.NormalizeCommandResult(result)
		if result.Command != "" {
			commandResults = append(commandResults, result)
		}
		actionResult := map[string]interface{}{
			"action":  action,
			"status":  "success",
			"command": emptyStringToNil(result.Command),
		}
		if err != nil {
			actionResult["status"] = "failure"
			actionResult["error"] = err.Error()
			errors = append(errors, err.Error())
			actionResults = append(actionResults, actionResult)
			payload := ensureRuntimeResultPayload(false, actionResults, commandResults, inventoryBefore, errors)
			return payload, commandResults, err
		}
		actionResults = append(actionResults, actionResult)
	}

	restartResult, err := backend.Run(ctx, "sh", "-c", passwallPostInstallRecoveryCommand)
	restartResult = passwall.NormalizeCommandResult(restartResult)
	if restartResult.Command != "" {
		commandResults = append(commandResults, restartResult)
	}
	if err != nil {
		errors = append(errors, err.Error())
		payload := ensureRuntimeResultPayload(false, actionResults, commandResults, inventoryBefore, errors)
		return payload, commandResults, err
	}

	return ensureRuntimeResultPayload(true, actionResults, commandResults, inventoryBefore, errors), commandResults, nil
}

func ensureRuntimeActions(payload map[string]interface{}) []string {
	actions := payloadStringSlice(payload, "actions")
	if len(actions) == 0 {
		return []string{
			ensureRuntimeActionCompactGeodata,
			ensureRuntimeActionDNSMasqFull,
		}
	}

	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(actions))
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		normalized = append(normalized, action)
	}
	if len(normalized) == 0 {
		return []string{
			ensureRuntimeActionCompactGeodata,
			ensureRuntimeActionDNSMasqFull,
		}
	}
	return normalized
}

func ensureRuntimeResultPayload(
	ok bool,
	actions []map[string]interface{},
	results []passwall.CommandResult,
	inventory controlplane.RouterInventory,
	errors []string,
) map[string]interface{} {
	return map[string]interface{}{
		"ok":              ok,
		"repaired":        len(results) > 0,
		"checkedAt":       time.Now().UTC().Format(time.RFC3339),
		"actions":         actions,
		"commands":        collectCommands(results),
		"services":        inventory.ServiceHealth,
		"resources":       inventory.Resources,
		"rulesAssets":     inventory.RulesAssets,
		"packageVersions": inventory.PackageVersions,
		"binaryVersions":  inventory.BinaryVersions,
		"errors":          errors,
	}
}

func enrichEnsureRuntimeResultPayload(
	payload map[string]interface{},
	inventory controlplane.RouterInventory,
) map[string]interface{} {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["services"] = inventory.ServiceHealth
	payload["resources"] = inventory.Resources
	payload["rulesAssets"] = inventory.RulesAssets
	payload["packageVersions"] = inventory.PackageVersions
	payload["binaryVersions"] = inventory.BinaryVersions
	return payload
}

func firstNonEmptyRuntimePayloadString(payload map[string]interface{}, key string, fallback string) string {
	value := strings.TrimSpace(payloadString(payload, key))
	if value != "" {
		return value
	}
	return fallback
}

func compactGeodataRepairScript(assetDirectory string, geoipURL string, geositeURL string) string {
	return strings.Join([]string{
		"set -eu",
		"asset_dir=" + shellQuote(assetDirectory),
		"geoip_url=" + shellQuote(geoipURL),
		"geosite_url=" + shellQuote(geositeURL),
		"[ -n \"$asset_dir\" ] || asset_dir='/usr/share/v2ray/'",
		"mkdir -p \"$asset_dir\"",
		"uci -q show passwall2.@global_rules[0] >/dev/null 2>&1 || uci -q add passwall2 global_rules >/dev/null",
		"uci -q set passwall2.@global_rules[0].v2ray_location_asset=\"$asset_dir\" || true",
		"uci -q set passwall2.@global_rules[0].geoip_url=\"$geoip_url\" || true",
		"uci -q set passwall2.@global_rules[0].geosite_url=\"$geosite_url\" || true",
		"uci -q set passwall2.@global_rules[0].geoip_update='1' || true",
		"uci -q set passwall2.@global_rules[0].geosite_update='1' || true",
		"uci -q commit passwall2 || true",
		"fetch_asset() {",
		"  stem=\"$1\"",
		"  url=\"$2\"",
		"  dest=\"$asset_dir/$stem.dat\"",
		"  if [ -s \"$dest\" ]; then",
		"    echo \"$stem already present at $dest\"",
		"    return 0",
		"  fi",
		"  tmp=\"/tmp/vectra-$stem.dat.$$\"",
		"  rm -f \"$tmp\"",
		"  if command -v wget >/dev/null 2>&1; then",
		"    wget -T 45 -O \"$tmp\" \"$url\"",
		"  else",
		"    uclient-fetch -T 45 -O \"$tmp\" \"$url\"",
		"  fi",
		"  [ -s \"$tmp\" ] || { echo \"$stem download produced empty file\" >&2; rm -f \"$tmp\"; exit 1; }",
		"  mv \"$tmp\" \"$dest\"",
		"  chmod 0644 \"$dest\" || true",
		"  date -u +%Y%m%d%H%M%S > \"$asset_dir/$stem.version\" 2>/dev/null || true",
		"  echo \"$stem installed at $dest ($(wc -c < \"$dest\") bytes)\"",
		"}",
		"fetch_asset geoip \"$geoip_url\"",
		"fetch_asset geosite \"$geosite_url\"",
	}, "\n")
}

func dnsmasqFullRepairScript() string {
	return strings.Join([]string{
		"set -eu",
		"pkg_installed() { opkg status \"$1\" 2>/dev/null | grep -q '^Status: .* installed'; }",
		"if pkg_installed dnsmasq-full; then",
		"  rm -f /etc/config/dhcp-opkg",
		"  /etc/init.d/dnsmasq restart >/dev/null 2>&1 || true",
		"  echo 'dnsmasq-full already installed'",
		"  exit 0",
		"fi",
		"opkg update >/tmp/vectra-dnsmasq-full-opkg-update.log 2>&1 || echo 'warning: opkg update failed; trying cached package lists' >&2",
		"if ! opkg list dnsmasq-full 2>/dev/null | awk '{ print $1 }' | grep -qx 'dnsmasq-full'; then",
		"  echo 'dnsmasq-full is not available in current opkg feeds/cache; refusing to remove base dnsmasq' >&2",
		"  exit 1",
		"fi",
		"workdir='/root/vectra-runtime-ensure'",
		"mkdir -p \"$workdir\"",
		"pkgdir='/tmp/vectra-dnsmasq-full-package'",
		"rm -rf \"$pkgdir\"",
		"mkdir -p \"$pkgdir\"",
		"(cd \"$pkgdir\" && opkg download dnsmasq-full >/tmp/vectra-dnsmasq-full-download.log 2>&1) || {",
		"  cat /tmp/vectra-dnsmasq-full-download.log >&2 || true",
		"  echo 'dnsmasq-full package could not be downloaded before replacement; refusing to remove base dnsmasq' >&2",
		"  exit 1",
		"}",
		"pkg_file=\"$(find \"$pkgdir\" -type f -name 'dnsmasq-full_*.ipk' | head -n 1)\"",
		"[ -n \"$pkg_file\" ] && [ -s \"$pkg_file\" ] || { echo 'downloaded dnsmasq-full package is missing or empty; refusing to remove base dnsmasq' >&2; exit 1; }",
		// Refuse to remove the running dnsmasq if overlay does not have enough
		// headroom to install dnsmasq-full afterwards — a failed install in
		// that window leaves the router without DNS/DHCP. 30 MB is enough
		// margin for the installed package (~600 KB) plus opkg lists, locks,
		// /var temp space, and the rollback path that re-installs base
		// dnsmasq if the install fails.
		"required_overlay_kb=30720",
		"overlay_avail_kb=\"$(df -k /overlay 2>/dev/null | awk 'NR>1 {print $4; exit}')\"",
		"[ -n \"$overlay_avail_kb\" ] || { echo 'unable to read overlay free space; refusing to remove base dnsmasq' >&2; exit 1; }",
		"if [ \"$overlay_avail_kb\" -lt \"$required_overlay_kb\" ]; then echo \"insufficient overlay free space: ${overlay_avail_kb} KB available, ${required_overlay_kb} KB required; refusing to remove base dnsmasq\" >&2; exit 1; fi",
		"dhcp_backup=''",
		"if [ -f /etc/config/dhcp ]; then",
		"  dhcp_backup=\"$workdir/dhcp.before-dnsmasq-full\"",
		"  cp /etc/config/dhcp \"$dhcp_backup\"",
		"fi",
		"if pkg_installed dnsmasq; then",
		"  opkg remove dnsmasq",
		"fi",
		"rm -f /etc/config/dhcp /etc/config/dhcp-opkg",
		"if ! opkg install \"$pkg_file\"; then",
		"  [ -n \"$dhcp_backup\" ] && [ -f \"$dhcp_backup\" ] && cp \"$dhcp_backup\" /etc/config/dhcp || true",
		"  opkg install dnsmasq >/dev/null 2>&1 || true",
		"  /etc/init.d/dnsmasq restart >/dev/null 2>&1 || true",
		"  echo 'dnsmasq-full local package install failed after base removal; dhcp backup was restored when available' >&2",
		"  exit 1",
		"fi",
		"if [ -n \"$dhcp_backup\" ] && [ -f \"$dhcp_backup\" ]; then",
		"  cp \"$dhcp_backup\" /etc/config/dhcp",
		"fi",
		"rm -f /etc/config/dhcp-opkg",
		"/etc/init.d/dnsmasq restart",
		"echo 'dnsmasq-full installed and dnsmasq restarted'",
	}, "\n")
}

// vectraXrayWrapperPath and vectraXrayWrapperScript mirror the file the
// controller package installs (openwrt/files/usr/sbin/vectra-xray-wrapper), so
// the xray repair can put it back after the LuCI updater has overwritten it.
// A plain string rather than go:embed because the feed build compiles from
// copied Go sources; TestVectraXrayWrapperScriptMatchesPackagedFile keeps the
// two in step.
const vectraXrayWrapperPath = "/usr/sbin/vectra-xray-wrapper"

const vectraXrayWrapperScript = "#!/bin/sh\n# vectra-xray-wrapper: thin wrapper around /usr/bin/xray that applies Go runtime\n# memory caps before exec'ing the real binary. Reduces xray's peak heap by\n# ~30-40% on low-RAM routers, which is the difference between \"occasional OOM\n# under load\" and \"comfortable steady state\" on a 234 MB AX3000T.\n#\n# Intended to be referenced from passwall2 via uci:\n#     passwall2.@global_app[0].xray_file='/usr/sbin/vectra-xray-wrapper'\n#\n# We preserve argv[0] with `exec -a \"$0\"` so that /proc/<pid>/cmdline keeps\n# reading the original symlink path (e.g. /tmp/etc/passwall2/bin/xray) — that\n# way pgrep patterns, monitoring scripts, and PassWall2's own process tracking\n# all keep working unchanged.\n\n# GOMEMLIMIT: soft heap cap. When live heap approaches this, the Go runtime\n# triggers GC aggressively to stay under it. Without this xray can balloon\n# transiently (e.g. on subscription refresh or large geoip reload) and trip\n# OOM even when steady-state RSS is fine.\nexport GOMEMLIMIT=\"${GOMEMLIMIT:-80MiB}\"\n\n# GOGC: smaller value = more frequent GC = lower steady-state heap. Default 100\n# means heap can double between GC cycles, which is too lax on a 234 MB system.\n# 30 gives a good balance: GC runs ~3x more often, heap stays ~30% smaller, the\n# extra CPU is negligible on mt7986 (which idles most of the time anyway).\nexport GOGC=\"${GOGC:-30}\"\n\nexec -a \"$0\" /usr/bin/xray \"$@\"\n"

var (
	fleetXrayVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	fleetXraySHA256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// fleetXrayPin resolves which xray build the repair installs. The panel may pin
// another release, but only together with that release's checksum: a version
// without a hash would install whatever the download happened to contain. The
// URL is always derived from the version, never taken from the payload.
func fleetXrayPin(payload map[string]interface{}) (string, string, error) {
	version := strings.TrimSpace(payloadString(payload, "xrayVersion"))
	sha256 := strings.ToLower(strings.TrimSpace(payloadString(payload, "xraySha256")))
	if version == "" && sha256 == "" {
		return defaultFleetXrayVersion, defaultFleetXraySHA256, nil
	}
	if version == "" {
		version = defaultFleetXrayVersion
	}
	if sha256 == "" {
		return "", "", fmt.Errorf("xray %s requested without its sha256", version)
	}
	if !fleetXrayVersionPattern.MatchString(version) {
		return "", "", fmt.Errorf("invalid xray version %q", version)
	}
	if !fleetXraySHA256Pattern.MatchString(sha256) {
		return "", "", fmt.Errorf("invalid xray sha256 %q", sha256)
	}
	return version, sha256, nil
}

// xrayRepairPaths are the router paths the xray installer touches. Only tests
// point them elsewhere, to run the script against a sandbox.
type xrayRepairPaths struct {
	Bin          string
	Wrapper      string
	PasswallInit string
	Meminfo      string
}

var defaultXrayRepairPaths = xrayRepairPaths{
	Bin:          "/usr/bin/xray",
	Wrapper:      vectraXrayWrapperPath,
	PasswallInit: "/etc/init.d/passwall2",
	Meminfo:      "/proc/meminfo",
}

// xrayBinaryRepairTimeout bounds the whole installer below the controller's
// 900 s dead-man threshold; the check-in loop is blocked while it runs.
const xrayBinaryRepairTimeout = 10 * time.Minute

// xrayBinaryRepairScript installs the official XTLS build of xray over
// /usr/bin/xray, the path the Vectra wrapper execs.
//
// Everything that can refuse does so before PassWall is touched: the build is
// arm64-only, and a router whose xray_file names some other binary is not one
// this repair understands. PassWall is then stopped before the download: while
// the runtime is broken its interception black-holes proxied domains, and the
// router's own fetch of the fix went with them.
//
// The new binary is first unpacked next to the current one, which changes
// nothing if it fails. Only when that runs out of room is the current binary
// removed to make space — and only if it no longer runs, or the operator asked
// for it with xrayReplaceInPlace: a working xray is never traded for a download
// that might not fit. When RAM allows, the old binary is copied to /tmp first
// and put back if the new one cannot be unpacked or fails its self-check.
//
// Every failure that leaves the router with the binary it started with puts
// PassWall back the way it was. Only a failure with no xray left leaves it
// stopped; the job's post-install step starts it again on success.
func xrayBinaryRepairScript(version string, sha256 string, replaceInPlace bool) string {
	return renderXrayBinaryRepairScript(version, sha256, replaceInPlace, defaultXrayRepairPaths)
}

func renderXrayBinaryRepairScript(version string, sha256 string, replaceInPlace bool, paths xrayRepairPaths) string {
	inPlace := "0"
	if replaceInPlace {
		inPlace = "1"
	}
	return strings.Join([]string{
		"set -u",
		"version=" + shellQuote(version),
		"sha=" + shellQuote(sha256),
		"url=" + shellQuote("https://github.com/XTLS/Xray-core/releases/download/v"+version+"/Xray-linux-arm64-v8a.zip"),
		"replace_in_place=" + inPlace,
		"bin=" + shellQuote(paths.Bin),
		"wrapper=" + shellQuote(paths.Wrapper),
		"passwall=" + shellQuote(paths.PasswallInit),
		"meminfo=" + shellQuote(paths.Meminfo),
		`case "$(uname -m)" in aarch64|arm64) ;; *) echo "this repair ships the arm64 xray build; $(uname -m) is not supported" >&2; exit 11 ;; esac`,
		"command -v unzip >/dev/null 2>&1 || { echo 'unzip is not installed; cannot unpack the xray release' >&2; exit 3; }",
		`xray_file=$(uci -q get passwall2.@global_app[0].xray_file 2>/dev/null || true)`,
		`case "$xray_file" in "" | "$bin" | "$wrapper") ;; *) echo "xray_file points at $xray_file; this repair only manages $bin and $wrapper" >&2; exit 10 ;; esac`,
		// The LuCI updater writes to whatever xray_file names, so on a router
		// pointed at the wrapper it replaces the wrapper itself with a raw
		// binary. Put the script back first — before the "already current"
		// shortcut, which would otherwise leave a clobbered wrapper in place.
		`if [ -e "$wrapper" ] && [ "$(head -c 2 "$wrapper")" != '#!' ]; then`,
		`  cat > "$wrapper.tmp" <<'VECTRA_XRAY_WRAPPER'`,
		strings.TrimSuffix(vectraXrayWrapperScript, "\n"),
		"VECTRA_XRAY_WRAPPER",
		`  chmod 0755 "$wrapper.tmp" && mv "$wrapper.tmp" "$wrapper" && echo "restored $wrapper"`,
		"fi",
		`if [ -x "$bin" ] && "$bin" version 2>/dev/null | head -n 1 | grep -q "^Xray $version "; then`,
		`  echo "xray $version already installed"`,
		"  exit 0",
		"fi",
		// PassWall's init script is not procd-based, so `running` proves
		// nothing; its own app.sh looks for the processes it spawned. The
		// bracket keeps the pattern from matching this script's own command
		// line, which contains the literal path.
		"was_running=0",
		"pgrep -f '/tmp/etc/passwall2/bi[n]' >/dev/null 2>&1 && was_running=1",
		`restore_passwall() { if [ "$was_running" = 1 ]; then "$passwall" start >/dev/null 2>&1 || true; fi; }`,
		`work=$(mktemp -d "${TMPDIR:-/tmp}/vectra-xray.XXXXXX") || { echo 'cannot create a work directory' >&2; exit 1; }`,
		`zip="$work/xray.zip"`,
		`trap 'rm -rf "$work"' EXIT`,
		`"$passwall" stop >/dev/null 2>&1 || true`,
		"fetched=0",
		"for attempt in 1 2; do",
		`  if curl -fsSL --connect-timeout 15 --max-time 240 -o "$zip" "$url"; then fetched=1; break; fi`,
		`  [ "$attempt" = 2 ] || sleep 5`,
		"done",
		`[ "$fetched" = 1 ] || { echo "xray $version download failed; PassWall restored" >&2; restore_passwall; exit 4; }`,
		`have=$(sha256sum "$zip" | awk '{print $1}')`,
		`[ "$have" = "$sha" ] || { echo "xray $version checksum mismatch ($have); PassWall restored" >&2; restore_passwall; exit 5; }`,
		`unzip -l "$zip" | awk '$NF == "xray" {found = 1} END {exit !found}' || { echo 'release archive has no xray binary; PassWall restored' >&2; restore_passwall; exit 6; }`,
		"in_place=0",
		`backup=""`,
		`rm -f "$bin.new"`,
		`if ! unzip -p "$zip" xray > "$bin.new" 2>/dev/null; then`,
		`  rm -f "$bin.new"`,
		"  current_runs=0",
		`  [ -x "$bin" ] && "$bin" version >/dev/null 2>&1 && current_runs=1`,
		`  if [ "$current_runs" = 1 ] && [ "$replace_in_place" != 1 ]; then`,
		`    echo "no room to unpack the new xray next to the current one, which still runs, so it is kept (set xrayReplaceInPlace to replace it anyway); PassWall restored" >&2`,
		"    restore_passwall",
		"    exit 7",
		"  fi",
		`  if [ -e "$bin" ]; then`,
		`    old_kb=$(( $(wc -c < "$bin") / 1024 ))`,
		`    mem_kb=$(awk '/^MemAvailable:/ {print $2}' "$meminfo" 2>/dev/null)`,
		`    tmp_kb=$(df -kP "$work" 2>/dev/null | awk 'NR==2 {print $4}')`,
		`    case "$mem_kb$tmp_kb" in '' | *[!0-9]*) ;; *)`,
		`      if [ "$mem_kb" -gt $((old_kb + 32768)) ] && [ "$tmp_kb" -gt $((old_kb + 4096)) ]; then`,
		`        cp "$bin" "$work/xray.previous" && backup="$work/xray.previous"`,
		"      fi ;;",
		"    esac",
		"  fi",
		`  echo "no room to unpack the new xray next to the current one: replacing $bin in place"`,
		"  in_place=1",
		`  rm -f "$bin"`,
		`  if ! unzip -p "$zip" xray > "$bin.new"; then`,
		`    rm -f "$bin.new"`,
		`    if [ -n "$backup" ] && cp "$backup" "$bin" && chmod 0755 "$bin"; then`,
		`      echo 'unpacking xray failed; previous xray restored, PassWall restored' >&2`,
		"      restore_passwall",
		"      exit 8",
		"    fi",
		`    echo 'unpacking xray failed and no previous xray could be kept; PassWall left stopped' >&2`,
		"    exit 8",
		"  fi",
		"fi",
		`chmod 0755 "$bin.new"`,
		`if ! "$bin.new" version 2>/dev/null | head -n 1 | grep -q "^Xray $version "; then`,
		`  rm -f "$bin.new"`,
		`  if [ "$in_place" = 0 ]; then echo 'new xray failed its self-check; current xray kept, PassWall restored' >&2; restore_passwall; exit 9; fi`,
		`  if [ -n "$backup" ] && cp "$backup" "$bin" && chmod 0755 "$bin"; then echo 'new xray failed its self-check; previous xray restored, PassWall restored' >&2; restore_passwall; exit 9; fi`,
		`  echo 'new xray failed its self-check and no previous xray could be kept; PassWall left stopped' >&2`,
		"  exit 9",
		"fi",
		`mv "$bin.new" "$bin"`,
		`"$bin" version | head -n 1`,
	}, "\n")
}
