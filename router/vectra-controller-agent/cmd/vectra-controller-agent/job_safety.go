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
			return jobSafetyControllerOverlayFloorMB
		}
	}
	return base
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
			reasons = append(
				reasons,
				fmt.Sprintf("/overlay free %d MB is below %d MB floor", resources.OverlayFreeMB, overlayFloor),
			)
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
