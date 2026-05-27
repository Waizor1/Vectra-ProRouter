package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
)

// JobSafetyTuning is the operator-overridable knob set that gates the floors
// and the optional cache-reclaim probe. Aliased to the JSON-bound type in
// internal/config so the same struct survives the round-trip through
// /etc/config/vectra-controller and the rendered config.json without an
// extra translation layer.
type JobSafetyTuning = config.JobSafetyConfig

const (
	jobSafetyHeavyMemoryFloorMB       = 40
	jobSafetyStorageMemoryFloorMB     = 64
	jobSafetyOverlayFloorMB           = 8
	jobSafetyStorageOverlayFloorMB    = 16
	jobSafetyTMPFloorMB               = 16
	jobSafetyStorageTMPFloorMB        = 32
	jobSafetyDiagnosticMemoryFloorMB  = 40
	jobSafetyDiagnosticTMPFloorMB     = 8
	jobSafetyControllerOverlayFloorMB = 8
	// jobSafetyAbsoluteMinimumOverlayMB is the hard floor below which we never
	// let an overlay-relaxing knob descend. Operators can lower static floors
	// and panel can ship manifest-aware tightening, but never below this.
	// Set to match the controller-self-update floor — that's the empirically
	// proven minimum where opkg's own staging files don't push overlay full.
	jobSafetyAbsoluteMinimumOverlayMB = 4
	// jobSafetyUBIFSCompressionRatio approximates the typical compression
	// ratio of opkg-installed payloads on UBIFS (binaries + geo data + lua).
	// xray-core measures 32 MB uncompressed → ~10-12 MB on UBIFS; geo .dat
	// files vary 3-5x depending on entropy. 3 is the conservative midpoint
	// for the PassWall2 stack, used to convert installedSizeBytes into a
	// realistic overlay-delta estimate.
	jobSafetyUBIFSCompressionRatio = 3
	// jobSafetyManifestOverlayHeadroomMB is added on top of the
	// manifest-derived estimate to cover opkg's own staging metadata
	// (control files, conffiles backup, scratch buffer) that doesn't show
	// up in installedSizeBytes.
	jobSafetyManifestOverlayHeadroomMB = 2
)

type jobSafetyClass string

const (
	jobSafetyClassNone       jobSafetyClass = "none"
	jobSafetyClassHeavy      jobSafetyClass = "heavy"
	jobSafetyClassStorage    jobSafetyClass = "storage"
	jobSafetyClassDiagnostic jobSafetyClass = "diagnostic"
)

type jobSafetyDecision struct {
	Blocked   bool
	Code      string
	Message   string
	Class     jobSafetyClass
	Resources controlplane.RouterResources
	Reasons   []string
	CheckedAt time.Time
}

func evaluateJobSafety(
	job controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
	resources controlplane.RouterResources,
	now time.Time,
	tuning JobSafetyTuning,
) jobSafetyDecision {
	class := classifyJobSafety(job, desiredRevision)
	return evaluateJobSafetyForClass(job, class, resources, now, tuning)
}

func evaluateJobSafetyWithResourceCollector(
	job controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
	now time.Time,
	collectResources func() controlplane.RouterResources,
	tuning JobSafetyTuning,
) jobSafetyDecision {
	class := classifyJobSafety(job, desiredRevision)
	if class == jobSafetyClassNone {
		return jobSafetyDecision{
			Class:     class,
			CheckedAt: now.UTC(),
		}
	}

	resources := controlplane.RouterResources{}
	if collectResources != nil {
		resources = collectResources()
	}
	// Optional one-shot cache reclaim when the router is just below the memory
	// floor. zram + page cache routinely hold 40-60 MB of cold data on
	// AX3000T-class boxes; freeing it via vm.drop_caches=3 is read-only and
	// lossless (dirty pages stay), and usually lifts MemAvailable enough to
	// pass the guard for an urgent self-update.
	if tuning.PreDropCaches && collectResources != nil {
		memFloor := memoryFloorFor(class, tuning, job)
		if resources.MemoryAvailableMB > 0 && resources.MemoryAvailableMB < memFloor {
			if err := attemptDropCaches(); err != nil {
				// On Linux this is the rare case (kernel rejected the write);
				// on macOS unit-test hosts /proc/sys/vm/drop_caches doesn't
				// exist, so we always end up here. Log and still re-read
				// resources — the collector itself may have a refreshed view.
				log.Printf("job_safety pre-drop_caches write failed: %v", err)
			} else {
				// Give the kernel a beat to actually reclaim before we re-read.
				time.Sleep(300 * time.Millisecond)
			}
			if refreshed := collectResources(); refreshed.MemoryAvailableMB > 0 {
				log.Printf(
					"job_safety pre-drop_caches lifted MemAvailable %d MB -> %d MB (floor %d MB)",
					resources.MemoryAvailableMB, refreshed.MemoryAvailableMB, memFloor,
				)
				resources = refreshed
			}
		}
	}
	return evaluateJobSafetyForClass(job, class, resources, now, tuning)
}

// attemptDropCaches asks the kernel to release clean page cache plus
// dentry/inode slabs by writing "3" to /proc/sys/vm/drop_caches. Dirty pages
// are preserved by the kernel so this is non-destructive. Requires root,
// which the controller agent runs as.
func attemptDropCaches() error {
	return os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0)
}

// memoryFloorFor resolves the effective RAM floor for a given safety class,
// applying operator overrides on top of the compile-time defaults.
func memoryFloorFor(class jobSafetyClass, tuning JobSafetyTuning, job controlplane.Job) int {
	switch class {
	case jobSafetyClassStorage:
		return pickFloor(tuning.StorageMemoryFloorMB, jobSafetyStorageMemoryFloorMB)
	case jobSafetyClassDiagnostic:
		return pickFloor(tuning.DiagnosticMemoryFloorMB, jobSafetyDiagnosticMemoryFloorMB)
	default:
		return pickFloor(tuning.HeavyMemoryFloorMB, jobSafetyHeavyMemoryFloorMB)
	}
}

func overlayFloorFor(class jobSafetyClass, tuning JobSafetyTuning, job controlplane.Job) int {
	base := 0
	switch class {
	case jobSafetyClassStorage:
		base = pickFloor(tuning.StorageOverlayFloorMB, jobSafetyStorageOverlayFloorMB)
	case jobSafetyClassDiagnostic:
		return 0
	default:
		base = pickFloor(tuning.HeavyOverlayFloorMB, jobSafetyOverlayFloorMB)
	}
	if isControllerUpdateSafetyJob(job) {
		// Controller update unpacks a small .ipk; let the operator drop the
		// overlay floor for self-update specifically to recover stuck routers,
		// but don't let it fall under the compile-time minimum.
		if jobSafetyControllerOverlayFloorMB < base {
			base = jobSafetyControllerOverlayFloorMB
		}
	}
	// Manifest-aware tightening: when the panel ships per-package
	// installedSizeBytes for a storage job, compute the realistic
	// UBIFS-compressed overlay delta and use that as the floor when it
	// drops below the static class default. This only RELAXES the floor
	// (never tightens it above the static default) and never descends below
	// jobSafetyAbsoluteMinimumOverlayMB. Keeps stuck low-overlay routers
	// (AX3000T-class with full PassWall2 stack) updatable without weakening
	// the guard for fresh-install or bloated payloads.
	if class == jobSafetyClassStorage {
		if manifestFloor := manifestOverlayFloorMB(job); manifestFloor > 0 && manifestFloor < base {
			base = manifestFloor
		}
	}
	return base
}

// manifestOverlayFloorMB derives a realistic minimum-required overlay floor
// from the per-package installedSizeBytes carried in the job payload. Returns
// 0 when the job has no usable manifest, falling back to the caller's static
// floor. The math: sum installedSizeBytes across packageArtifacts → divide by
// jobSafetyUBIFSCompressionRatio to approximate compressed on-disk usage →
// add jobSafetyManifestOverlayHeadroomMB for opkg's staging overhead → clamp
// to jobSafetyAbsoluteMinimumOverlayMB.
//
// Note this is the worst-case fresh-install delta. For upgrades where the
// same package is being replaced, opkg's package-first strategy removes the
// old version first, so actual peak overlay use is even lower. We do NOT try
// to subtract currently-installed sizes at this layer (would require reading
// /usr/lib/opkg/status from a context that doesn't have backend access) —
// the worst-case estimate is still tight enough to unblock AX3000T routers
// where the static 16 MB floor is the actual problem.
func manifestOverlayFloorMB(job controlplane.Job) int {
	if job.Type != "update_passwall_packages" {
		return 0
	}
	artifacts := parsePackageArtifacts(job.Payload)
	if len(artifacts) == 0 {
		return 0
	}
	var totalInstalledBytes int64
	for _, a := range artifacts {
		if a.InstalledSize > 0 {
			totalInstalledBytes += a.InstalledSize
		}
	}
	if totalInstalledBytes <= 0 {
		return 0
	}
	compressedBytes := totalInstalledBytes / jobSafetyUBIFSCompressionRatio
	requiredMB := int(compressedBytes/(1024*1024)) + jobSafetyManifestOverlayHeadroomMB
	if requiredMB < jobSafetyAbsoluteMinimumOverlayMB {
		return jobSafetyAbsoluteMinimumOverlayMB
	}
	return requiredMB
}

// forceOverlayBypass returns true when the panel marked this job payload with
// {"forceOverlayBypass": true}. Used by operators to override the overlay
// guard for one specific update job when a router is stuck below the floor
// and the operator has externally verified there's enough room (e.g. by
// pre-removing old packages). When true, the overlay reason is dropped from
// the safety decision and a WARN line is emitted for audit. Memory and tmp
// floors are NOT bypassed — those guard against hard failure modes.
func forceOverlayBypass(job controlplane.Job) bool {
	return payloadBool(job.Payload, "forceOverlayBypass")
}

func tmpFloorFor(class jobSafetyClass, tuning JobSafetyTuning) int {
	switch class {
	case jobSafetyClassStorage:
		return pickFloor(tuning.StorageTMPFloorMB, jobSafetyStorageTMPFloorMB)
	case jobSafetyClassDiagnostic:
		return pickFloor(tuning.DiagnosticTMPFloorMB, jobSafetyDiagnosticTMPFloorMB)
	default:
		return pickFloor(tuning.HeavyTMPFloorMB, jobSafetyTMPFloorMB)
	}
}

func pickFloor(override, deflt int) int {
	if override > 0 {
		return override
	}
	return deflt
}

// overlayTuningKnobName returns the UCI option name that operators can set
// to lower the overlay floor for a given safety class. Used inside the
// guard's error message so the recovery path is self-documented.
func overlayTuningKnobName(class jobSafetyClass) string {
	switch class {
	case jobSafetyClassStorage:
		return "job_safety_storage_overlay_floor_mb"
	default:
		return "job_safety_heavy_overlay_floor_mb"
	}
}

func evaluateJobSafetyForClass(
	job controlplane.Job,
	class jobSafetyClass,
	resources controlplane.RouterResources,
	now time.Time,
	tuning JobSafetyTuning,
) jobSafetyDecision {
	decision := jobSafetyDecision{
		Class:     class,
		Resources: resources,
		CheckedAt: now.UTC(),
	}
	if class == jobSafetyClassNone {
		return decision
	}

	memoryFloor := memoryFloorFor(class, tuning, job)
	tmpFloor := tmpFloorFor(class, tuning)
	overlayFloor := overlayFloorFor(class, tuning, job)

	reasons := make([]string, 0, 3)
	if resources.MemoryAvailableMB <= 0 {
		reasons = append(reasons, "available RAM is unknown")
	} else if resources.MemoryAvailableMB < memoryFloor {
		reasons = append(
			reasons,
			fmt.Sprintf("available RAM %d MB is below %d MB floor", resources.MemoryAvailableMB, memoryFloor),
		)
	}

	if overlayFloor > 0 {
		if resources.OverlayFreeMB <= 0 {
			reasons = append(reasons, "/overlay free space is unknown")
		} else if resources.OverlayFreeMB < overlayFloor {
			if forceOverlayBypass(job) {
				// Operator opted into bypass; log to syslog so the audit trail
				// captures who chose to override the guard. We deliberately
				// keep memory/tmp checks intact — those guard against hard
				// failure modes (OOM, opkg staging failure) that bypass can't
				// safely paper over.
				log.Printf(
					"job %s (%s): overlay guard bypassed by operator (forceOverlayBypass=true); /overlay free %d MB vs %d MB floor",
					job.ID, job.Type, resources.OverlayFreeMB, overlayFloor,
				)
			} else {
				// Build an actionable error: report current vs floor, plus
				// the operator knobs that can lift the floor. Keep the
				// "free N MB is below M MB floor" substring intact — multiple
				// downstream log scrapers, self-heal tests, and the panel's
				// rescue UI grep for this exact pattern.
				reasons = append(
					reasons,
					fmt.Sprintf(
						"/overlay free %d MB is below %d MB floor (set %s in /etc/config/vectra-controller or pass forceOverlayBypass=true in job payload)",
						resources.OverlayFreeMB,
						overlayFloor,
						overlayTuningKnobName(class),
					),
				)
			}
		}
	}

	if tmpFloor > 0 {
		if resources.TMPFreeMB <= 0 {
			reasons = append(reasons, "/tmp free space is unknown")
		} else if resources.TMPFreeMB < tmpFloor {
			reasons = append(
				reasons,
				fmt.Sprintf("/tmp free %d MB is below %d MB floor", resources.TMPFreeMB, tmpFloor),
			)
		}
	}

	if len(reasons) == 0 {
		return decision
	}

	decision.Blocked = true
	decision.Code = "router_resource_guard"
	decision.Reasons = reasons
	decision.Message = fmt.Sprintf(
		"resource guard blocked %s job before running router-side commands: %s",
		job.Type,
		strings.Join(reasons, "; "),
	)
	return decision
}

func isControllerUpdateSafetyJob(job controlplane.Job) bool {
	if job.Type == "update_controller" {
		return true
	}
	return job.Type == "run_terminal_command" && isControllerSelfUpdateTerminalPayload(job.Payload)
}

func classifyJobSafety(
	job controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
) jobSafetyClass {
	switch job.Type {
	case "update_controller", "update_passwall_packages", "validate_firmware", "ensure_passwall_runtime":
		return jobSafetyClassStorage
	case "refresh_subscriptions", "refresh_rules":
		return jobSafetyClassHeavy
	case "run_rescue_repair":
		if rescueRepairPayloadHasHeavyActions(job.Payload) {
			return jobSafetyClassHeavy
		}
		return jobSafetyClassNone
	case "collect_router_logs", "collect_optimization_baseline", "verify_passwall_routes":
		return jobSafetyClassDiagnostic
	case "run_terminal_command":
		switch strings.TrimSpace(payloadString(job.Payload, "purpose")) {
		case routerHostnameUpdateTerminalPurpose, "router-reboot":
			return jobSafetyClassNone
		case controllerSelfUpdateTerminalPurpose, controllerSelfUpdateCompatTerminalPurpose:
			return jobSafetyClassStorage
		default:
			return jobSafetyClassDiagnostic
		}
	case "apply_passwall_config":
		if desiredRevision == nil {
			return jobSafetyClassNone
		}
		impact := desiredRevision.Impact
		if impact.PackageInstall || impact.FirmwareValidation {
			return jobSafetyClassStorage
		}
		if impact.RequiresRestart || impact.RefreshRules || impact.RefreshSubscriptions {
			return jobSafetyClassHeavy
		}
	}

	return jobSafetyClassNone
}

func rescueRepairPayloadHasHeavyActions(payload map[string]interface{}) bool {
	for _, action := range payloadStringSlice(payload, "actions") {
		switch strings.TrimSpace(action) {
		case rescueRepairActionRefreshRules, rescueRepairActionRefreshSubscriptions:
			return true
		}
	}
	return false
}

func (decision jobSafetyDecision) ResultPayload(job controlplane.Job) map[string]interface{} {
	return map[string]interface{}{
		"error":       decision.Message,
		"code":        decision.Code,
		"jobType":     job.Type,
		"safetyClass": string(decision.Class),
		"retryable":   true,
		"reasons":     append([]string(nil), decision.Reasons...),
		"resources": map[string]interface{}{
			"memoryAvailableMb": decision.Resources.MemoryAvailableMB,
			"memoryTotalMb":     decision.Resources.MemoryTotalMB,
			"overlayFreeMb":     decision.Resources.OverlayFreeMB,
			"tmpFreeMb":         decision.Resources.TMPFreeMB,
			"swapFreeMb":        decision.Resources.SwapFreeMB,
			"swapTotalMb":       decision.Resources.SwapTotalMB,
		},
		"checkedAt": decision.CheckedAt.Format(time.RFC3339),
	}
}
