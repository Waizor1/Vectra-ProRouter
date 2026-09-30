// Package jobsafety gates resource-hungry jobs behind RAM/overlay/tmp floors,
// porting the resource-guard discipline from vectra-controller-agent. On
// low-memory Filogic routers, running a heavy job (config apply, subscription
// refresh, package install) at the wrong moment is what historically caused
// xray OOM. A blocked job is reported back as retryable, not failed-for-good.
package jobsafety

import (
	"fmt"
	"strings"

	"vectra-controller-pro/internal/controlplane"
)

// Class buckets jobs by their resource cost profile.
type Class string

const (
	ClassNone       Class = "none"       // mode toggles, reconnect — no gating
	ClassHeavy      Class = "heavy"      // config apply, subscription refresh, geo
	ClassStorage    Class = "storage"    // package install / self-update (disk-heavy)
	ClassDiagnostic Class = "diagnostic" // log/baseline collection
)

// Config holds the per-class floors (in MB). Defaults mirror the agent.
type Config struct {
	HeavyMemoryFloorMB      int  `json:"heavyMemoryFloorMb"`
	StorageMemoryFloorMB    int  `json:"storageMemoryFloorMb"`
	DiagnosticMemoryFloorMB int  `json:"diagnosticMemoryFloorMb"`
	HeavyOverlayFloorMB     int  `json:"heavyOverlayFloorMb"`
	StorageOverlayFloorMB   int  `json:"storageOverlayFloorMb"`
	HeavyTMPFloorMB         int  `json:"heavyTmpFloorMb"`
	StorageTMPFloorMB       int  `json:"storageTmpFloorMb"`
	DiagnosticTMPFloorMB    int  `json:"diagnosticTmpFloorMb"`
	PreDropCaches           bool `json:"preDropCaches"`
}

// DefaultConfig returns the agent-equivalent floors.
func DefaultConfig() Config {
	return Config{
		HeavyMemoryFloorMB:      40,
		StorageMemoryFloorMB:    64,
		DiagnosticMemoryFloorMB: 40,
		HeavyOverlayFloorMB:     8,
		StorageOverlayFloorMB:   16,
		HeavyTMPFloorMB:         16,
		StorageTMPFloorMB:       32,
		DiagnosticTMPFloorMB:    8,
		PreDropCaches:           false,
	}
}

// WithDefaults fills any zero floor with its default (so a partial UCI config
// still gets safe floors).
func (c Config) WithDefaults() Config {
	d := DefaultConfig()
	if c.HeavyMemoryFloorMB == 0 {
		c.HeavyMemoryFloorMB = d.HeavyMemoryFloorMB
	}
	if c.StorageMemoryFloorMB == 0 {
		c.StorageMemoryFloorMB = d.StorageMemoryFloorMB
	}
	if c.DiagnosticMemoryFloorMB == 0 {
		c.DiagnosticMemoryFloorMB = d.DiagnosticMemoryFloorMB
	}
	if c.HeavyOverlayFloorMB == 0 {
		c.HeavyOverlayFloorMB = d.HeavyOverlayFloorMB
	}
	if c.StorageOverlayFloorMB == 0 {
		c.StorageOverlayFloorMB = d.StorageOverlayFloorMB
	}
	if c.HeavyTMPFloorMB == 0 {
		c.HeavyTMPFloorMB = d.HeavyTMPFloorMB
	}
	if c.StorageTMPFloorMB == 0 {
		c.StorageTMPFloorMB = d.StorageTMPFloorMB
	}
	if c.DiagnosticTMPFloorMB == 0 {
		c.DiagnosticTMPFloorMB = d.DiagnosticTMPFloorMB
	}
	return c
}

// Classify maps a job type to its resource class.
func Classify(jobType string) Class {
	switch jobType {
	case "apply_xray_config", "refresh_xray_subscriptions", "update_xray_assets":
		return ClassHeavy
	case "update_controller", "validate_firmware", "update_xray_binary":
		return ClassStorage
	case "collect_router_logs", "collect_optimization_baseline":
		return ClassDiagnostic
	default:
		// reload_xray_outbound, run_terminal_command, enter_direct_mode,
		// reconnect, and anything unknown are treated as no-gate (cheap).
		return ClassNone
	}
}

// Decision is the outcome of a safety evaluation.
type Decision struct {
	Blocked   bool                         `json:"blocked"`
	Code      string                       `json:"code,omitempty"`
	Message   string                       `json:"message,omitempty"`
	Class     Class                        `json:"class"`
	Reasons   []string                     `json:"reasons,omitempty"`
	Resources controlplane.RouterResources `json:"resources"`
}

// Evaluate decides whether a job of jobType may run given current resources.
// It is pure (no side effects) so it is fully testable; the daemon owns the
// optional drop_caches retry.
func Evaluate(jobType string, res controlplane.RouterResources, cfg Config) Decision {
	cfg = cfg.WithDefaults()
	class := Classify(jobType)
	d := Decision{Class: class, Resources: res}
	if class == ClassNone {
		return d
	}

	var memFloor, overlayFloor, tmpFloor int
	switch class {
	case ClassHeavy:
		memFloor, overlayFloor, tmpFloor = cfg.HeavyMemoryFloorMB, cfg.HeavyOverlayFloorMB, cfg.HeavyTMPFloorMB
	case ClassStorage:
		memFloor, overlayFloor, tmpFloor = cfg.StorageMemoryFloorMB, cfg.StorageOverlayFloorMB, cfg.StorageTMPFloorMB
	case ClassDiagnostic:
		memFloor, overlayFloor, tmpFloor = cfg.DiagnosticMemoryFloorMB, 0, cfg.DiagnosticTMPFloorMB
	}

	// A reading of 0 means "unknown" (e.g. probed on macOS during dev) — we do
	// not block on an unknown reading, only on a known-too-low one.
	if memFloor > 0 && res.MemoryAvailableMB > 0 && res.MemoryAvailableMB < memFloor {
		d.Reasons = append(d.Reasons, fmt.Sprintf("memory %dMB < floor %dMB", res.MemoryAvailableMB, memFloor))
	}
	if overlayFloor > 0 && res.OverlayFreeMB > 0 && res.OverlayFreeMB < overlayFloor {
		d.Reasons = append(d.Reasons, fmt.Sprintf("overlay %dMB < floor %dMB", res.OverlayFreeMB, overlayFloor))
	}
	if tmpFloor > 0 && res.TMPFreeMB > 0 && res.TMPFreeMB < tmpFloor {
		d.Reasons = append(d.Reasons, fmt.Sprintf("tmp %dMB < floor %dMB", res.TMPFreeMB, tmpFloor))
	}

	if len(d.Reasons) > 0 {
		d.Blocked = true
		d.Code = "router_resource_guard"
		d.Message = fmt.Sprintf("job %q blocked by resource guard (%s): %s",
			jobType, class, strings.Join(d.Reasons, "; "))
	}
	return d
}

// ResultPayload renders the decision into the job-result Result map the panel
// understands (matching the agent's resource-guard payload shape).
func (d Decision) ResultPayload() map[string]interface{} {
	return map[string]interface{}{
		"error":       d.Message,
		"code":        d.Code,
		"safetyClass": string(d.Class),
		"retryable":   true,
		"reasons":     d.Reasons,
		"resources": map[string]interface{}{
			"memoryAvailableMb": d.Resources.MemoryAvailableMB,
			"overlayFreeMb":     d.Resources.OverlayFreeMB,
			"tmpFreeMb":         d.Resources.TMPFreeMB,
		},
	}
}
