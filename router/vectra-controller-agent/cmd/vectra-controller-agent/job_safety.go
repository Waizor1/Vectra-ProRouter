package main

import (
	"fmt"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
)

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
) jobSafetyDecision {
	class := classifyJobSafety(job, desiredRevision)
	return evaluateJobSafetyForClass(job, class, resources, now)
}

func evaluateJobSafetyWithResourceCollector(
	job controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
	now time.Time,
	collectResources func() controlplane.RouterResources,
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
	return evaluateJobSafetyForClass(job, class, resources, now)
}

func evaluateJobSafetyForClass(
	job controlplane.Job,
	class jobSafetyClass,
	resources controlplane.RouterResources,
	now time.Time,
) jobSafetyDecision {
	decision := jobSafetyDecision{
		Class:     class,
		Resources: resources,
		CheckedAt: now.UTC(),
	}
	if class == jobSafetyClassNone {
		return decision
	}

	memoryFloor := jobSafetyHeavyMemoryFloorMB
	tmpFloor := jobSafetyTMPFloorMB
	overlayFloor := jobSafetyOverlayFloorMB
	switch class {
	case jobSafetyClassStorage:
		memoryFloor = jobSafetyStorageMemoryFloorMB
		tmpFloor = jobSafetyStorageTMPFloorMB
		overlayFloor = jobSafetyStorageOverlayFloorMB
	case jobSafetyClassDiagnostic:
		memoryFloor = jobSafetyDiagnosticMemoryFloorMB
		tmpFloor = jobSafetyDiagnosticTMPFloorMB
		overlayFloor = 0
	}
	if isControllerUpdateSafetyJob(job) {
		overlayFloor = jobSafetyControllerOverlayFloorMB
	}

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
	return job.Type == "run_terminal_command" &&
		strings.TrimSpace(payloadString(job.Payload, "purpose")) == controllerSelfUpdateTerminalPurpose
}

func classifyJobSafety(
	job controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
) jobSafetyClass {
	switch job.Type {
	case "update_controller", "update_passwall_packages", "validate_firmware":
		return jobSafetyClassStorage
	case "refresh_subscriptions", "refresh_rules":
		return jobSafetyClassHeavy
	case "run_rescue_repair":
		if rescueRepairPayloadHasHeavyActions(job.Payload) {
			return jobSafetyClassHeavy
		}
		return jobSafetyClassNone
	case "collect_router_logs":
		return jobSafetyClassDiagnostic
	case "run_terminal_command":
		switch strings.TrimSpace(payloadString(job.Payload, "purpose")) {
		case routerHostnameUpdateTerminalPurpose, "router-reboot":
			return jobSafetyClassNone
		case controllerSelfUpdateTerminalPurpose:
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
