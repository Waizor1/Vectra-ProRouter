package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/state"
)

type passwallPackageUpdateResult struct {
	Package              string `json:"package"`
	TargetVersion        string `json:"targetVersion"`
	PackageTargetVersion string `json:"packageTargetVersion,omitempty"`
	RuntimeTargetVersion string `json:"runtimeTargetVersion,omitempty"`
	Status               string `json:"status"`
	PathUsed             string `json:"pathUsed"`
	PackageVersionBefore string `json:"packageVersionBefore,omitempty"`
	PackageVersionAfter  string `json:"packageVersionAfter,omitempty"`
	RuntimeVersionBefore string `json:"runtimeVersionBefore,omitempty"`
	RuntimeVersionAfter  string `json:"runtimeVersionAfter,omitempty"`
	DriftDetected        bool   `json:"driftDetected"`
	Error                string `json:"error,omitempty"`
}

type packageStorageMetadata struct {
	DownloadSizeBytes  int64
	InstalledSizeBytes int64
}

var passwallManagedInstallOrder = []string{
	"xray-core",
	"v2ray-geoip",
	"v2ray-geosite",
	"geoview",
	"sing-box",
	"hysteria",
	"chinadns-ng",
	"tcping",
	"dnsmasq-full",
	"kmod-nft-socket",
	"kmod-nft-tproxy",
	"kmod-nft-nat",
	"luci-app-passwall2",
}

var passwallRuntimeKeyByPackage = map[string]string{
	"xray-core": "xray",
	"sing-box":  "sing-box",
	"hysteria":  "hysteria",
	"geoview":   "geoview",
}

var passwallRuntimeOnlyPackages = map[string]bool{
	"xray-core": true,
	"sing-box":  true,
	"hysteria":  true,
	"geoview":   true,
}

var passwallRuleManagedPackages = map[string]bool{
	"v2ray-geoip":   true,
	"v2ray-geosite": true,
}

var semverFragmentPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)
var versionNumberPattern = regexp.MustCompile(`\d+`)

func runPasswallPackageUpdateJob(
	ctx context.Context,
	client *controlplane.Client,
	cfg *config.Config,
	persisted *state.PersistedState,
	jobID string,
	backend commandRunner,
	job artifactJob,
) error {
	orderedPackages := sortPasswallPackages(job.PackageList)
	currentInventory := inventory.NewCollector().Collect(controlplane.RouterInventory{
		PackageVersions: map[string]string{},
		BinaryVersions:  map[string]string{},
	})
	storageSnapshotBefore := currentInventory.Resources

	results := make([]passwall.CommandResult, 0, len(orderedPackages)+3)
	if passwallPackageUpdateNeedsFeedRefresh(job) {
		updateResult, err := backend.Run(ctx, "opkg", "update")
		results = append(results, updateResult)
		if err != nil {
			return submitFailure(
				ctx,
				client,
				cfg,
				persisted,
				jobID,
				updateResult.Stdout,
				updateResult.Stderr,
				err.Error(),
				map[string]interface{}{
					"packageList":          orderedPackages,
					"strategy":             emptyStringToNil(job.Strategy),
					"targetVersion":        job.TargetVersion,
					"packageTargetVersion": emptyStringToNil(job.PackageTargetVersion),
					"runtimeTargetVersion": emptyStringToNil(job.RuntimeTargetVersion),
					"targetReleaseTag":     job.TargetReleaseTag,
					"originSource":         job.OriginSource,
					"fallbackPolicy":       job.FallbackPolicy,
					"updateScope":          job.UpdateScope,
				},
			)
		}
	}

	packageResults := make([]passwallPackageUpdateResult, 0, len(orderedPackages))
	for _, packageName := range orderedPackages {
		currentResult, commandResults, nextInventory := updateSinglePasswallPackage(
			ctx,
			backend,
			currentInventory,
			packageName,
			job,
		)
		results = append(results, commandResults...)
		currentInventory = nextInventory
		packageResults = append(packageResults, currentResult)
	}

	repairResults, repairErr := runPasswallPostInstallRepair(ctx, backend)
	results = append(results, repairResults...)
	stdout, stderr := collectCommandOutputs(results)
	if repairErr == nil {
		packageResults = reconcileRuleManagedPasswallPackageResults(packageResults)
	}

	serializedPackageResults := serializePasswallPackageResults(packageResults)

	resultPayload := map[string]interface{}{
		"packageList":          orderedPackages,
		"packages":             orderedPackages,
		"strategy":             emptyStringToNil(job.Strategy),
		"targetVersion":        emptyStringToNil(job.TargetVersion),
		"packageTargetVersion": emptyStringToNil(job.PackageTargetVersion),
		"runtimeTargetVersion": emptyStringToNil(job.RuntimeTargetVersion),
		"targetReleaseTag":     emptyStringToNil(job.TargetReleaseTag),
		"originSource":         emptyStringToNil(job.OriginSource),
		"fallbackPolicy":       emptyStringToNil(job.FallbackPolicy),
		"updateScope":          emptyStringToNil(job.UpdateScope),
		"packageResults":       serializedPackageResults,
		"commands":             collectCommands(results),
		"postInstallRepair":    true,
		"ruleRefreshAssets":    append([]string(nil), passwallRuleRefreshAssets...),
		"postInstallCommands":  collectCommands(repairResults),
		"storageSnapshotBefore": map[string]interface{}{
			"overlayFreeMb": storageSnapshotBefore.OverlayFreeMB,
			"tmpFreeMb":     storageSnapshotBefore.TMPFreeMB,
		},
		"driftDetected": anyPasswallResultDrift(serializedPackageResults),
	}

	if repairErr != nil {
		return submitFailure(
			ctx,
			client,
			cfg,
			persisted,
			jobID,
			stdout,
			stderr,
			repairErr.Error(),
			resultPayload,
		)
	}

	if firstFailedPasswallResult(serializedPackageResults) != nil {
		return submitFailure(
			ctx,
			client,
			cfg,
			persisted,
			jobID,
			stdout,
			stderr,
			"one or more PassWall packages did not reach the requested target",
			resultPayload,
		)
	}

	request := controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        cfg.RouterID,
		JobID:           jobID,
		Status:          "success",
		Stdout:          stdout,
		Stderr:          stderr,
		Result:          resultPayload,
	}
	if err := persistPendingJobResult(cfg.StatePath, persisted, request); err != nil {
		return fmt.Errorf("persist passwall package update result: %w", err)
	}
	if err := flushPendingJobResult(ctx, cfg, client, persisted, controlplane.RouterInventory{}); err != nil {
		return fmt.Errorf("submit passwall package update result: %w", err)
	}

	return nil
}

func updateSinglePasswallPackage(
	ctx context.Context,
	backend commandRunner,
	currentInventory controlplane.RouterInventory,
	packageName string,
	job artifactJob,
) (passwallPackageUpdateResult, []passwall.CommandResult, controlplane.RouterInventory) {
	commandResults := make([]passwall.CommandResult, 0, 3)
	artifact := findPasswallPackageArtifact(job, packageName)
	packageTargetVersion := resolvePasswallPackageTargetVersion(
		ctx,
		backend,
		job,
		packageName,
		artifact,
	)
	runtimeTargetVersion := resolvePasswallRuntimeTargetVersion(
		job,
		packageName,
		packageTargetVersion,
	)
	strategy := resolvePasswallPackageStrategy(job, packageName)
	packageVersionBefore := currentInventory.PackageVersions[packageName]
	runtimeVersionBefore := currentInventory.BinaryVersions[passwallRuntimeKeyByPackage[packageName]]

	if status, drift := assessPasswallPackageStatus(
		packageName,
		resolvePasswallStatusTargetVersion(
			packageName,
			packageTargetVersion,
			runtimeTargetVersion,
		),
		packageVersionBefore,
		runtimeVersionBefore,
	); status != "" {
		return passwallPackageUpdateResult{
			Package:              packageName,
			TargetVersion:        packageTargetVersion,
			PackageTargetVersion: packageTargetVersion,
			RuntimeTargetVersion: effectiveRuntimeTargetVersion(
				packageName,
				runtimeTargetVersion,
				packageTargetVersion,
				"not-needed",
				runtimeVersionBefore,
			),
			Status:               status,
			PathUsed:             "not-needed",
			PackageVersionBefore: packageVersionBefore,
			PackageVersionAfter:  packageVersionBefore,
			RuntimeVersionBefore: runtimeVersionBefore,
			RuntimeVersionAfter:  runtimeVersionBefore,
			DriftDetected:        drift,
		}, commandResults, currentInventory
	}

	var lastError error
	if strategy == "xray-built-in-first" && packageName == "xray-core" {
		builtinResults, ok, builtinErr, nextInventory := tryBuiltInPasswallComponentFallback(
			ctx,
			backend,
			currentInventory,
			packageName,
			resolvePasswallStatusTargetVersion(
				packageName,
				packageTargetVersion,
				runtimeTargetVersion,
			),
		)
		commandResults = append(commandResults, builtinResults...)
		if ok {
			return successfulPasswallPackageResult(
				packageName,
				packageTargetVersion,
				runtimeTargetVersion,
				"built-in-updater",
				packageVersionBefore,
				runtimeVersionBefore,
				nextInventory,
			), commandResults, nextInventory
		}
		if builtinErr != nil {
			lastError = builtinErr
		}
		currentInventory = nextInventory
	}

	packagePathAllowed, packagePathErr := assessPasswallPackagePath(
		ctx,
		backend,
		currentInventory,
		packageName,
		artifact,
	)
	if packagePathErr != nil {
		lastError = packagePathErr
	}
	if packagePathAllowed {
		packagePathResults, packagePathResult, installErr := installPasswallPackageViaPackagePath(
			ctx,
			backend,
			packageName,
			artifact,
		)
		commandResults = append(commandResults, packagePathResults...)
		lastError = installErr
		if installErr == nil {
			nextInventory := inventory.NewCollector().Collect(controlplane.RouterInventory{
				PackageVersions: map[string]string{},
				BinaryVersions:  map[string]string{},
			})
			if status, drift := assessPasswallPackageStatus(
				packageName,
				resolvePasswallStatusTargetVersion(
					packageName,
					packageTargetVersion,
					runtimeTargetVersion,
				),
				nextInventory.PackageVersions[packageName],
				nextInventory.BinaryVersions[passwallRuntimeKeyByPackage[packageName]],
			); status != "" {
				_ = drift
				return successfulPasswallPackageResult(
					packageName,
					packageTargetVersion,
					runtimeTargetVersion,
					"package",
					packageVersionBefore,
					runtimeVersionBefore,
					nextInventory,
				), commandResults, nextInventory
			}
			lastError = fmt.Errorf("%s did not reach target through package path", packageName)
			if packagePathResult.Command != "" {
				_ = packagePathResult
			}
			currentInventory = nextInventory
		} else {
			lastError = installErr
		}
	}

	if job.FallbackPolicy == "package-only" || !passwallRuntimeOnlyPackages[packageName] {
		nextInventory := inventory.NewCollector().Collect(controlplane.RouterInventory{
			PackageVersions: map[string]string{},
			BinaryVersions:  map[string]string{},
		})
		return failedPasswallPackageResult(
			packageName,
			packageTargetVersion,
			runtimeTargetVersion,
			packageVersionBefore,
			runtimeVersionBefore,
			nextInventory,
			lastError,
			"package",
		), commandResults, nextInventory
	}

	builtinResults, ok, builtinErr, nextInventory := tryBuiltInPasswallComponentFallback(
		ctx,
		backend,
		currentInventory,
		packageName,
		resolvePasswallStatusTargetVersion(
			packageName,
			packageTargetVersion,
			runtimeTargetVersion,
		),
	)
	commandResults = append(commandResults, builtinResults...)
	if ok {
		return successfulPasswallPackageResult(
			packageName,
			packageTargetVersion,
			runtimeTargetVersion,
			"built-in-updater",
			packageVersionBefore,
			runtimeVersionBefore,
			nextInventory,
		), commandResults, nextInventory
	}
	if builtinErr != nil {
		lastError = builtinErr
	}
	currentInventory = nextInventory

	if packageName == "xray-core" && artifact != nil {
		xrayResults, ok, xrayErr, nextInventory := tryXrayBinaryPayloadFallback(
			ctx,
			backend,
			currentInventory,
			resolvePasswallStatusTargetVersion(
				packageName,
				packageTargetVersion,
				runtimeTargetVersion,
			),
			artifact,
		)
		commandResults = append(commandResults, xrayResults...)
		if ok {
			return successfulPasswallPackageResult(
				packageName,
				packageTargetVersion,
				runtimeTargetVersion,
				"xray-binary-payload",
				packageVersionBefore,
				runtimeVersionBefore,
				nextInventory,
			), commandResults, nextInventory
		}
		if xrayErr != nil {
			lastError = xrayErr
		}
		currentInventory = nextInventory
	}

	nextInventory = inventory.NewCollector().Collect(controlplane.RouterInventory{
		PackageVersions: map[string]string{},
		BinaryVersions:  map[string]string{},
	})
	return failedPasswallPackageResult(
		packageName,
		packageTargetVersion,
		runtimeTargetVersion,
		packageVersionBefore,
		runtimeVersionBefore,
		nextInventory,
		lastError,
		"package",
	), commandResults, nextInventory
}

func installPasswallPackageViaPackagePath(
	ctx context.Context,
	backend commandRunner,
	packageName string,
	artifact *packageArtifact,
) ([]passwall.CommandResult, passwall.CommandResult, error) {
	if artifact == nil {
		result, err := runOpkgInstall(
			ctx,
			backend,
			[]string{"install", packageName},
			false,
		)
		return []passwall.CommandResult{result}, result, err
	}

	staged, err := stageArtifact(
		ctx,
		artifact.ArtifactURL,
		artifact.SHA256,
		artifact.SignatureURL,
		downloadTimeout(0),
	)
	if err != nil {
		return nil, passwall.CommandResult{}, err
	}

	result, installErr := runOpkgInstall(
		ctx,
		backend,
		[]string{"install", staged.Path},
		false,
	)
	return []passwall.CommandResult{result}, result, installErr
}

func tryBuiltInPasswallComponentFallback(
	ctx context.Context,
	backend commandRunner,
	currentInventory controlplane.RouterInventory,
	packageName string,
	targetVersion string,
) ([]passwall.CommandResult, bool, error, controlplane.RouterInventory) {
	componentName, ok := passwallRuntimeKeyByPackage[packageName]
	if !ok {
		return nil, false, fmt.Errorf("runtime component mapping missing for %s", packageName), currentInventory
	}

	command := `component="$1"
lua - "$component" <<'LUA'
local component = arg[1]
local ok, api = pcall(require, 'luci.passwall2.api')
if not ok or type(api) ~= 'table' then
  io.stderr:write('passwall api unavailable\n')
  os.exit(1)
end
local check = api.to_check('', component)
if type(check) ~= 'table' or check.code ~= 0 then
  io.stderr:write((check and check.error) or 'component check failed')
  io.stderr:write('\n')
  os.exit(1)
end
if not check.has_update then
  os.exit(0)
end
local data = check.data or {}
local download_size_kb = tonumber(data.size or 0)
if download_size_kb and download_size_kb > 0 then
  download_size_kb = download_size_kb / 1024
else
  download_size_kb = nil
end
local download = api.to_download(component, data.browser_download_url, download_size_kb)
if type(download) ~= 'table' or download.code ~= 0 then
  io.stderr:write((download and download.error) or 'component download failed')
  io.stderr:write('\n')
  os.exit(1)
end
local file = download.file
if download.zip then
  local extracted = api.to_extract(component, file, data.subfix)
  if type(extracted) ~= 'table' or extracted.code ~= 0 then
    io.stderr:write((extracted and extracted.error) or 'component extract failed')
    io.stderr:write('\n')
    os.exit(1)
  end
  file = extracted.file
end
local moved = api.to_move(component, file)
if type(moved) ~= 'table' or moved.code ~= 0 then
  io.stderr:write((moved and moved.error) or 'component move failed')
  io.stderr:write('\n')
  os.exit(1)
end
os.exit(0)
LUA`

	result, err := backend.Run(ctx, "sh", "-c", command, "passwall-builtin", componentName)
	if err != nil {
		return []passwall.CommandResult{result}, false, err, currentInventory
	}

	nextInventory := inventory.NewCollector().Collect(controlplane.RouterInventory{
		PackageVersions: map[string]string{},
		BinaryVersions:  map[string]string{},
	})
	status, _ := assessPasswallPackageStatus(
		packageName,
		targetVersion,
		nextInventory.PackageVersions[packageName],
		nextInventory.BinaryVersions[componentName],
	)
	if status == "" {
		return []passwall.CommandResult{result}, false, fmt.Errorf("%s built-in updater did not reach target", packageName), nextInventory
	}
	return []passwall.CommandResult{result}, true, nil, nextInventory
}

func tryXrayBinaryPayloadFallback(
	ctx context.Context,
	backend commandRunner,
	currentInventory controlplane.RouterInventory,
	targetVersion string,
	artifact *packageArtifact,
) ([]passwall.CommandResult, bool, error, controlplane.RouterInventory) {
	staged, err := stageArtifact(
		ctx,
		artifact.ArtifactURL,
		artifact.SHA256,
		artifact.SignatureURL,
		downloadTimeout(0),
	)
	if err != nil {
		return nil, false, err, currentInventory
	}

	command := `artifact="$1"
extract_root="$(mktemp -d /tmp/vectra-xray.XXXXXX)" || exit 1
trap 'rm -rf "$extract_root"' EXIT
gzip -dc "$artifact" > "$extract_root/outer.tar" || exit 1
tar -xf "$extract_root/outer.tar" -C "$extract_root" ./data.tar.gz || exit 1
mkdir -p "$extract_root/data"
tar -xzf "$extract_root/data.tar.gz" -C "$extract_root/data" ./usr/bin/xray || exit 1
[ -f "$extract_root/data/usr/bin/xray" ] || exit 1
"$extract_root/data/usr/bin/xray" version >/dev/null 2>&1 || exit 1
new_size="$(wc -c < "$extract_root/data/usr/bin/xray" | tr -d ' ')"
old_size='0'
if [ -f /usr/bin/xray ]; then
  old_size="$(wc -c < /usr/bin/xray | tr -d ' ')"
fi
overlay_bytes="$(df -kP /overlay 2>/dev/null | awk 'NR == 2 { print $4 * 1024; exit }')"
[ -n "$overlay_bytes" ] || overlay_bytes='0'
required_bytes=$((new_size - old_size))
if [ "$required_bytes" -lt 0 ]; then
  required_bytes='0'
fi
if [ "$overlay_bytes" -lt "$required_bytes" ]; then
  echo "overlay not enough space for xray binary payload refresh" >&2
  exit 1
fi
if [ -x /etc/init.d/passwall2 ]; then
  /etc/init.d/passwall2 stop >/dev/null 2>&1 || true
fi
cp "$extract_root/data/usr/bin/xray" /usr/bin/xray || exit 1
chmod 0755 /usr/bin/xray || true`

	result, commandErr := backend.Run(
		ctx,
		"sh",
		"-c",
		command,
		"passwall-xray-payload",
		staged.Path,
	)
	if commandErr != nil {
		return []passwall.CommandResult{result}, false, commandErr, currentInventory
	}

	nextInventory := inventory.NewCollector().Collect(controlplane.RouterInventory{
		PackageVersions: map[string]string{},
		BinaryVersions:  map[string]string{},
	})
	status, _ := assessPasswallPackageStatus(
		"xray-core",
		targetVersion,
		nextInventory.PackageVersions["xray-core"],
		nextInventory.BinaryVersions["xray"],
	)
	if status == "" {
		return []passwall.CommandResult{result}, false, fmt.Errorf("xray binary payload fallback did not reach target"), nextInventory
	}
	return []passwall.CommandResult{result}, true, nil, nextInventory
}

func assessPasswallPackagePath(
	ctx context.Context,
	backend commandRunner,
	currentInventory controlplane.RouterInventory,
	packageName string,
	artifact *packageArtifact,
) (bool, error) {
	if artifact == nil {
		return true, nil
	}

	metadata := packageStorageMetadata{
		DownloadSizeBytes:  artifact.DownloadSize,
		InstalledSizeBytes: artifact.InstalledSize,
	}
	if metadata.DownloadSizeBytes <= 0 || metadata.InstalledSizeBytes <= 0 {
		return true, nil
	}

	tmpFreeBytes := int64(currentInventory.Resources.TMPFreeMB) * 1024 * 1024
	if tmpFreeBytes > 0 && tmpFreeBytes < metadata.DownloadSizeBytes {
		return false, fmt.Errorf("%s package path skipped: not enough tmp staging space", packageName)
	}

	currentInstalledBytes := readInstalledPackageSizeBytes(ctx, backend, packageName)
	overlayFreeBytes := int64(currentInventory.Resources.OverlayFreeMB) * 1024 * 1024
	requiredBytes := metadata.InstalledSizeBytes - currentInstalledBytes
	if requiredBytes < 0 {
		requiredBytes = 0
	}
	if overlayFreeBytes > 0 && overlayFreeBytes < requiredBytes {
		return false, fmt.Errorf("%s package path skipped: not enough overlay space", packageName)
	}

	return true, nil
}

func readInstalledPackageSizeBytes(
	ctx context.Context,
	backend commandRunner,
	packageName string,
) int64 {
	result, err := backend.Run(
		ctx,
		"sh",
		"-c",
		"opkg status "+shellQuote(packageName)+" 2>/dev/null | awk -F': ' '/^Installed-Size: / { print $2; exit }'",
	)
	if err != nil {
		return 0
	}

	value, parseErr := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	if parseErr != nil {
		return 0
	}
	return value
}

func resolvePasswallPackageTargetVersion(
	ctx context.Context,
	backend commandRunner,
	job artifactJob,
	packageName string,
	artifact *packageArtifact,
) string {
	if artifact != nil && strings.TrimSpace(artifact.ArtifactVersion) != "" {
		return artifact.ArtifactVersion
	}
	if job.UpdateScope == "scoped-package" && job.TargetVersion != "" {
		return job.TargetVersion
	}
	result, err := backend.Run(
		ctx,
		"sh",
		"-c",
		"opkg info "+shellQuote(packageName)+" 2>/dev/null | awk -F': ' '/^Version: / { print $2; exit }'",
	)
	if err == nil {
		version := strings.TrimSpace(result.Stdout)
		if version != "" {
			return version
		}
	}
	return job.TargetVersion
}

func assessPasswallPackageStatus(
	packageName string,
	targetVersion string,
	packageVersion string,
	runtimeVersion string,
) (string, bool) {
	if targetVersion == "" {
		if packageVersion != "" {
			return "already-current", false
		}
		return "", false
	}

	if !passwallRuntimeOnlyPackages[packageName] {
		if packageVersion == targetVersion || packageVersionAtLeast(packageVersion, targetVersion) {
			return "already-current", false
		}
		return "", false
	}

	targetRuntime := normalizeRuntimeTargetVersion(targetVersion)
	if versionAtLeast(runtimeVersion, targetRuntime) {
		if packageVersion == targetVersion {
			return "already-current", false
		}
		return "runtime-only-converged", true
	}
	return "", false
}

func failedPasswallPackageResult(
	packageName string,
	packageTargetVersion string,
	runtimeTargetVersion string,
	packageVersionBefore string,
	runtimeVersionBefore string,
	currentInventory controlplane.RouterInventory,
	lastError error,
	pathUsed string,
) passwallPackageUpdateResult {
	status := "failed"
	if passwallStorageBlocked(lastError) {
		status = "storage-blocked"
	}
	return passwallPackageUpdateResult{
		Package:              packageName,
		TargetVersion:        packageTargetVersion,
		PackageTargetVersion: packageTargetVersion,
		RuntimeTargetVersion: effectiveRuntimeTargetVersion(
			packageName,
			runtimeTargetVersion,
			packageTargetVersion,
			pathUsed,
			currentInventory.BinaryVersions[passwallRuntimeKeyByPackage[packageName]],
		),
		Status:               status,
		PathUsed:             pathUsed,
		PackageVersionBefore: packageVersionBefore,
		PackageVersionAfter:  currentInventory.PackageVersions[packageName],
		RuntimeVersionBefore: runtimeVersionBefore,
		RuntimeVersionAfter:  currentInventory.BinaryVersions[passwallRuntimeKeyByPackage[packageName]],
		DriftDetected:        false,
		Error:                errorMessage(lastError),
	}
}

func reconcileRuleManagedPasswallPackageResults(
	results []passwallPackageUpdateResult,
) []passwallPackageUpdateResult {
	reconciled := make([]passwallPackageUpdateResult, 0, len(results))
	for _, result := range results {
		reconciled = append(reconciled, reconcileRuleManagedPasswallPackageResult(result))
	}
	return reconciled
}

func reconcileRuleManagedPasswallPackageResult(
	result passwallPackageUpdateResult,
) passwallPackageUpdateResult {
	if !passwallRuleManagedPackages[result.Package] || !isFailedPasswallStatus(result.Status) {
		return result
	}

	packageVersion := result.PackageVersionAfter
	if strings.TrimSpace(packageVersion) == "" {
		packageVersion = result.PackageVersionBefore
	}

	result.PathUsed = "not-needed"
	result.Error = ""
	if packageVersionAtLeast(packageVersion, result.PackageTargetVersion) {
		result.Status = "already-current"
		result.DriftDetected = false
		return result
	}

	result.Status = "updated"
	result.DriftDetected = result.PackageTargetVersion != "" &&
		!packageVersionAtLeast(packageVersion, result.PackageTargetVersion)
	return result
}

func successfulPasswallPackageResult(
	packageName string,
	packageTargetVersion string,
	runtimeTargetVersion string,
	pathUsed string,
	packageVersionBefore string,
	runtimeVersionBefore string,
	currentInventory controlplane.RouterInventory,
) passwallPackageUpdateResult {
	packageVersionAfter := currentInventory.PackageVersions[packageName]
	runtimeVersionAfter := currentInventory.BinaryVersions[passwallRuntimeKeyByPackage[packageName]]
	effectiveRuntimeTarget := effectiveRuntimeTargetVersion(
		packageName,
		runtimeTargetVersion,
		packageTargetVersion,
		pathUsed,
		runtimeVersionAfter,
	)
	status, drift := classifySuccessfulPasswallStatus(
		packageName,
		pathUsed,
		packageTargetVersion,
		effectiveRuntimeTarget,
		packageVersionBefore,
		packageVersionAfter,
		runtimeVersionBefore,
		runtimeVersionAfter,
	)
	return passwallPackageUpdateResult{
		Package:              packageName,
		TargetVersion:        packageTargetVersion,
		PackageTargetVersion: packageTargetVersion,
		RuntimeTargetVersion: effectiveRuntimeTarget,
		Status:               status,
		PathUsed:             pathUsed,
		PackageVersionBefore: packageVersionBefore,
		PackageVersionAfter:  packageVersionAfter,
		RuntimeVersionBefore: runtimeVersionBefore,
		RuntimeVersionAfter:  runtimeVersionAfter,
		DriftDetected:        drift,
	}
}

func classifySuccessfulPasswallStatus(
	packageName string,
	pathUsed string,
	packageTargetVersion string,
	runtimeTargetVersion string,
	packageVersionBefore string,
	packageVersionAfter string,
	runtimeVersionBefore string,
	runtimeVersionAfter string,
) (string, bool) {
	status, drift := assessPasswallPackageStatus(
		packageName,
		resolvePasswallStatusTargetVersion(
			packageName,
			packageTargetVersion,
			runtimeTargetVersion,
		),
		packageVersionAfter,
		runtimeVersionAfter,
	)
	if status == "" {
		return "failed", false
	}
	if pathUsed == "package" && packageVersionBefore != packageTargetVersion && packageVersionAfter == packageTargetVersion {
		return "package-updated", drift
	}
	if pathUsed == "built-in-updater" || pathUsed == "xray-binary-payload" {
		targetRuntime := normalizeRuntimeTargetVersion(runtimeTargetVersion)
		if targetRuntime == "" {
			targetRuntime = normalizeRuntimeTargetVersion(packageTargetVersion)
		}
		runtimeAdvanced := targetRuntime != "" &&
			!versionAtLeast(runtimeVersionBefore, targetRuntime) &&
			versionAtLeast(runtimeVersionAfter, targetRuntime)
		if runtimeAdvanced {
			return "runtime-updated", packageVersionAfter != packageTargetVersion
		}
	}
	return status, drift
}

func resolvePasswallPackageStrategy(job artifactJob, packageName string) string {
	if strings.TrimSpace(job.Strategy) != "" {
		return job.Strategy
	}
	if job.UpdateScope == "scoped-package" && len(job.PackageList) == 1 && packageName == "xray-core" {
		return "xray-built-in-first"
	}
	return "managed-stack-package-first"
}

func resolvePasswallRuntimeTargetVersion(
	job artifactJob,
	packageName string,
	packageTargetVersion string,
) string {
	if !passwallRuntimeOnlyPackages[packageName] {
		return ""
	}
	if strings.TrimSpace(job.RuntimeTargetVersion) != "" {
		return normalizeRuntimeTargetVersion(job.RuntimeTargetVersion)
	}
	return normalizeRuntimeTargetVersion(packageTargetVersion)
}

func resolvePasswallStatusTargetVersion(
	packageName string,
	packageTargetVersion string,
	runtimeTargetVersion string,
) string {
	if !passwallRuntimeOnlyPackages[packageName] {
		return packageTargetVersion
	}
	if normalized := normalizeRuntimeTargetVersion(runtimeTargetVersion); normalized != "" {
		return normalized
	}
	return packageTargetVersion
}

func effectiveRuntimeTargetVersion(
	packageName string,
	runtimeTargetVersion string,
	packageTargetVersion string,
	pathUsed string,
	runtimeVersionAfter string,
) string {
	if !passwallRuntimeOnlyPackages[packageName] {
		return ""
	}
	if normalized := normalizeRuntimeTargetVersion(runtimeTargetVersion); normalized != "" {
		return normalized
	}
	if (pathUsed == "built-in-updater" || pathUsed == "xray-binary-payload") && runtimeVersionAfter != "" {
		if normalized := normalizeRuntimeTargetVersion(runtimeVersionAfter); normalized != "" {
			return normalized
		}
	}
	return normalizeRuntimeTargetVersion(packageTargetVersion)
}

func passwallStorageBlocked(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not enough tmp staging space") ||
		strings.Contains(lower, "not enough overlay space") ||
		strings.Contains(lower, "overlay not enough space") ||
		strings.Contains(lower, "no space left on device")
}

func sortPasswallPackages(packages []string) []string {
	order := make(map[string]int, len(passwallManagedInstallOrder))
	for index, packageName := range passwallManagedInstallOrder {
		order[packageName] = index
	}

	sorted := make([]string, 0, len(packages))
	seen := map[string]struct{}{}
	for _, packageName := range packages {
		if packageName == "" {
			continue
		}
		if _, ok := seen[packageName]; ok {
			continue
		}
		seen[packageName] = struct{}{}
		sorted = append(sorted, packageName)
	}

	sortFn := func(left, right string) bool {
		leftOrder, leftKnown := order[left]
		rightOrder, rightKnown := order[right]
		if leftKnown && rightKnown {
			return leftOrder < rightOrder
		}
		if leftKnown {
			return true
		}
		if rightKnown {
			return false
		}
		return left < right
	}

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if !sortFn(sorted[i], sorted[j]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

func findPasswallPackageArtifact(job artifactJob, packageName string) *packageArtifact {
	for _, artifact := range job.PackageArtifacts {
		if artifact.Name == packageName {
			copy := artifact
			return &copy
		}
	}
	return nil
}

func passwallPackageUpdateNeedsFeedRefresh(job artifactJob) bool {
	for _, packageName := range job.PackageList {
		if findPasswallPackageArtifact(job, packageName) == nil {
			return true
		}
	}
	return false
}

func anyPasswallResultDrift(results []map[string]interface{}) bool {
	for _, result := range results {
		if drift, ok := result["driftDetected"].(bool); ok && drift {
			return true
		}
	}
	return false
}

func firstFailedPasswallResult(results []map[string]interface{}) map[string]interface{} {
	for _, result := range results {
		if status, ok := result["status"].(string); ok && isFailedPasswallStatus(status) {
			return result
		}
	}
	return nil
}

func isFailedPasswallStatus(status string) bool {
	switch status {
	case "failed", "storage-blocked", "delivery-blocked":
		return true
	default:
		return false
	}
}

func normalizeRuntimeTargetVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = regexp.MustCompile(`-r\d+$`).ReplaceAllString(version, "")
	match := semverFragmentPattern.FindString(version)
	if match == "" {
		return version
	}
	return match
}

func versionAtLeast(actual string, target string) bool {
	target = normalizeRuntimeTargetVersion(target)
	actual = normalizeRuntimeTargetVersion(actual)
	if actual == "" || target == "" {
		return false
	}

	return compareVersionParts(actual, target) >= 0
}

func packageVersionAtLeast(actual string, target string) bool {
	actual = strings.TrimSpace(strings.TrimPrefix(actual, "v"))
	target = strings.TrimSpace(strings.TrimPrefix(target, "v"))
	if actual == "" || target == "" {
		return false
	}

	return opkgVersionCompare(actual, target) >= 0
}

func compareVersionParts(actual string, target string) int {
	actualParts := splitVersionParts(actual)
	targetParts := splitVersionParts(target)
	if len(actualParts) == 0 || len(targetParts) == 0 {
		switch {
		case actual == target:
			return 0
		case actual > target:
			return 1
		default:
			return -1
		}
	}

	maxLen := len(actualParts)
	if len(targetParts) > maxLen {
		maxLen = len(targetParts)
	}

	for index := 0; index < maxLen; index++ {
		var actualPart int64
		if index < len(actualParts) {
			actualPart = actualParts[index]
		}
		var targetPart int64
		if index < len(targetParts) {
			targetPart = targetParts[index]
		}
		if actualPart > targetPart {
			return 1
		}
		if actualPart < targetPart {
			return -1
		}
	}

	return 0
}

func splitVersionParts(version string) []int64 {
	matches := versionNumberPattern.FindAllString(version, -1)
	if len(matches) == 0 {
		return nil
	}

	values := make([]int64, 0, len(matches))
	for index, part := range matches {
		if index == 0 && strings.HasPrefix(part, "20") && len(part) >= 8 && len(part) < 14 {
			part = part + strings.Repeat("0", 14-len(part))
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			continue
		}
		values = append(values, value)
	}
	return values
}

func opkgVersionCompare(actual string, target string) int {
	epochActual, versionActual, revisionActual := splitOpkgVersion(actual)
	epochTarget, versionTarget, revisionTarget := splitOpkgVersion(target)
	switch {
	case epochActual > epochTarget:
		return 1
	case epochActual < epochTarget:
		return -1
	}

	if versionCompare := opkgVersionFragmentCompare(versionActual, versionTarget); versionCompare != 0 {
		return versionCompare
	}

	return opkgVersionFragmentCompare(revisionActual, revisionTarget)
}

func splitOpkgVersion(version string) (uint64, string, string) {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0, "", ""
	}

	epoch := uint64(0)
	if epochText, remainder, ok := strings.Cut(version, ":"); ok {
		if parsed, err := strconv.ParseUint(epochText, 10, 64); err == nil {
			epoch = parsed
			version = remainder
		}
	}

	revision := ""
	if index := strings.LastIndex(version, "-"); index >= 0 {
		revision = version[index+1:]
		version = version[:index]
	}

	return epoch, version, revision
}

func opkgVersionFragmentCompare(actual string, target string) int {
	actualIndex := 0
	targetIndex := 0

	for actualIndex < len(actual) || targetIndex < len(target) {
		firstDiff := 0

		for (actualIndex < len(actual) && !isASCIIDigit(actual[actualIndex])) ||
			(targetIndex < len(target) && !isASCIIDigit(target[targetIndex])) {
			left := opkgOrder(fragmentByte(actual, actualIndex))
			right := opkgOrder(fragmentByte(target, targetIndex))
			if left != right {
				if left > right {
					return 1
				}
				return -1
			}
			if actualIndex < len(actual) {
				actualIndex++
			}
			if targetIndex < len(target) {
				targetIndex++
			}
		}

		for fragmentByte(actual, actualIndex) == '0' {
			actualIndex++
		}
		for fragmentByte(target, targetIndex) == '0' {
			targetIndex++
		}

		for isASCIIDigit(fragmentByte(actual, actualIndex)) &&
			isASCIIDigit(fragmentByte(target, targetIndex)) {
			if firstDiff == 0 {
				firstDiff = int(fragmentByte(actual, actualIndex)) -
					int(fragmentByte(target, targetIndex))
			}
			actualIndex++
			targetIndex++
		}

		if isASCIIDigit(fragmentByte(actual, actualIndex)) {
			return 1
		}
		if isASCIIDigit(fragmentByte(target, targetIndex)) {
			return -1
		}
		if firstDiff != 0 {
			if firstDiff > 0 {
				return 1
			}
			return -1
		}
	}

	return 0
}

func fragmentByte(value string, index int) byte {
	if index < 0 || index >= len(value) {
		return 0
	}
	return value[index]
}

func opkgOrder(value byte) int {
	switch {
	case value == '~':
		return -1
	case value == 0 || isASCIIDigit(value):
		return 0
	case isASCIIAlpha(value):
		return int(value)
	default:
		return int(value) + 256
	}
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func serializePasswallPackageResults(
	results []passwallPackageUpdateResult,
) []map[string]interface{} {
	serialized := make([]map[string]interface{}, 0, len(results))
	for _, result := range results {
		serialized = append(serialized, map[string]interface{}{
			"package":              result.Package,
			"targetVersion":        result.TargetVersion,
			"packageTargetVersion": emptyStringToNil(result.PackageTargetVersion),
			"runtimeTargetVersion": emptyStringToNil(result.RuntimeTargetVersion),
			"status":               result.Status,
			"pathUsed":             result.PathUsed,
			"packageVersionBefore": emptyStringToNil(result.PackageVersionBefore),
			"packageVersionAfter":  emptyStringToNil(result.PackageVersionAfter),
			"runtimeVersionBefore": emptyStringToNil(result.RuntimeVersionBefore),
			"runtimeVersionAfter":  emptyStringToNil(result.RuntimeVersionAfter),
			"driftDetected":        result.DriftDetected,
			"error":                emptyStringToNil(result.Error),
		})
	}
	return serialized
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func emptyStringToNil(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
