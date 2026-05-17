package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
)

const (
	ensureRuntimeActionCompactGeodata = "compact_geodata"
	ensureRuntimeActionDNSMasqFull    = "dnsmasq_full"

	defaultCompactGeoIPURL   = "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat"
	defaultCompactGeoSiteURL = "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat"
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
