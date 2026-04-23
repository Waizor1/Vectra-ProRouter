package recovery

import "time"

type Phase string

const (
	PhaseIdle                  Phase = "idle"
	PhaseMonitoring            Phase = "monitoring"
	PhaseControllerRestartWait Phase = "controller_restart_wait"
	PhaseDirectSettle          Phase = "direct_settle"
	PhaseRebootWait            Phase = "reboot_wait"
	PhasePostRebootCheck       Phase = "post_reboot_check"
	PhasePasswallRetryWait     Phase = "passwall_retry_wait"
	PhaseOperatorAttention     Phase = "operator_attention"
)

const (
	StatusReachable = "reachable"
	StatusHealthy   = "healthy"
	StatusPartial   = "partial"
	StatusBlocked   = "blocked"
)

type State struct {
	LastSuccessfulControlPlaneAt string `json:"last_successful_control_plane_at,omitempty"`
	OutageStartedAt              string `json:"outage_started_at,omitempty"`
	Phase                        Phase  `json:"phase,omitempty"`
	LastControllerRestartAt      string `json:"last_controller_restart_at,omitempty"`
	LastAutoRebootAt             string `json:"last_auto_reboot_at,omitempty"`
	LastPasswallRetryAt          string `json:"last_passwall_retry_at,omitempty"`
	AwaitingOperator             bool   `json:"awaiting_operator,omitempty"`
	LastPanelStatus              string `json:"last_panel_status,omitempty"`
	LastRUStatus                 string `json:"last_ru_status,omitempty"`
	LastForeignStatus            string `json:"last_foreign_status,omitempty"`
	LastActionReason             string `json:"last_action_reason,omitempty"`
}

func (s *State) Normalize() {
	if s == nil {
		return
	}
	if s.Phase == "" {
		s.Phase = PhaseIdle
	}
}

func ParseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}

	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}

	return parsed
}

func FormatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format(time.RFC3339)
}

func PasswallOwnedByRecovery(phase Phase) bool {
	switch phase {
	case PhaseDirectSettle,
		PhaseRebootWait,
		PhasePostRebootCheck,
		PhasePasswallRetryWait,
		PhaseOperatorAttention:
		return true
	default:
		return false
	}
}
