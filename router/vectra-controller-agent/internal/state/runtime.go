package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type RuntimeStatus struct {
	ControlURL          string `json:"control_url,omitempty"`
	PanelURL            string `json:"panel_url,omitempty"`
	RouterID            string `json:"router_id,omitempty"`
	ControllerVersion   string `json:"controller_version,omitempty"`
	ServiceState        string `json:"service_state,omitempty"`
	RescueMode          string `json:"rescue_mode,omitempty"`
	SelectedNodeID      string `json:"selected_node_id,omitempty"`
	SelectedNodeLabel   string `json:"selected_node_label,omitempty"`
	ImportState         string `json:"import_state,omitempty"`
	ConfigDigest        string `json:"config_digest,omitempty"`
	AppliedRevisionID   string `json:"applied_revision_id,omitempty"`
	LastRegisterAt      string `json:"last_register_at,omitempty"`
	LastCheckInAt       string `json:"last_check_in_at,omitempty"`
	LastOperatorMessage string `json:"last_operator_message,omitempty"`
	LastRescueReason    string `json:"last_rescue_reason,omitempty"`
	LastRescueAt        string `json:"last_rescue_at,omitempty"`
	PasswallEnabled     bool   `json:"passwall_enabled,omitempty"`
	ServerReachable     bool   `json:"server_reachable"`
	PublicReachable     bool   `json:"public_reachable"`
	ProxyFailureCount   int    `json:"proxy_failure_count,omitempty"`
	ProxySuccessCount   int    `json:"proxy_success_count,omitempty"`
	DirectSuccessCount  int    `json:"direct_success_count,omitempty"`
	LastServerError     string `json:"last_server_error,omitempty"`
	LastPublicError     string `json:"last_public_error,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	PendingApproval     bool   `json:"pending_approval,omitempty"`
	JobsAvailable       int    `json:"jobs_available,omitempty"`
}

func SaveRuntimeStatus(path string, status RuntimeStatus) error {
	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create runtime status dir: %w", err)
	}

	bytes, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime status: %w", err)
	}

	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		return fmt.Errorf("write runtime status: %w", err)
	}

	return nil
}
