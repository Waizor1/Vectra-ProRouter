package rescue

import "time"

type Mode string

const (
	ModeProxy  Mode = "proxy"
	ModeDirect Mode = "direct"
)

var defaultHealthURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://cp.cloudflare.com/",
}

type Policy struct {
	HealthURLs               []string      `json:"health_urls,omitempty"`
	TriggerFailureCount      int           `json:"trigger_failure_count"`
	RecoverySuccessCount     int           `json:"recovery_success_count"`
	Cooldown                 time.Duration `json:"cooldown"`
	RequireDirectPathSuccess bool          `json:"require_direct_path_success"`
	DirectModeReason         string        `json:"direct_mode_reason"`
}

func (p *Policy) Normalize() {
	if len(p.HealthURLs) == 0 {
		p.HealthURLs = append([]string(nil), defaultHealthURLs...)
	}
	if p.TriggerFailureCount <= 0 {
		p.TriggerFailureCount = 3
	}
	if p.RecoverySuccessCount <= 0 {
		p.RecoverySuccessCount = 2
	}
	if p.Cooldown <= 0 {
		p.Cooldown = 5 * time.Minute
	}
	if p.DirectModeReason == "" {
		p.DirectModeReason = "Subscription expired or upstream proxy unavailable"
	}
}

type State struct {
	Mode               Mode      `json:"mode"`
	ProxyFailureCount  int       `json:"proxy_failure_count"`
	DirectSuccessCount int       `json:"direct_success_count"`
	ProxySuccessCount  int       `json:"proxy_success_count"`
	LastTransitionAt   time.Time `json:"last_transition_at"`
}

type EvaluationInput struct {
	Now                    time.Time
	Policy                 Policy
	State                  State
	ProxyFailureIncrement  int
	DirectSuccessIncrement int
	ProxySuccessIncrement  int
}

type Decision struct {
	ShouldTransition bool
	NextMode         Mode
	Reason           string
	NextState        State
}

func CooldownActive(now time.Time, policy Policy, state State) bool {
	policy.Normalize()

	if now.IsZero() {
		now = time.Now()
	}

	return !state.LastTransitionAt.IsZero() &&
		now.Sub(state.LastTransitionAt) < policy.Cooldown
}

func ShouldAttemptDirectFallback(now time.Time, policy Policy, state State) bool {
	policy.Normalize()

	if state.Mode != "" && state.Mode != ModeProxy {
		return false
	}

	if CooldownActive(now, policy, state) {
		return false
	}

	return state.ProxyFailureCount+1 >= policy.TriggerFailureCount
}

func Evaluate(input EvaluationInput) Decision {
	policy := input.Policy
	policy.Normalize()

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	next := input.State
	if next.Mode == "" {
		next.Mode = ModeProxy
	}

	if input.ProxyFailureIncrement > 0 {
		next.ProxyFailureCount += input.ProxyFailureIncrement
	} else if input.ProxySuccessIncrement > 0 {
		next.ProxyFailureCount = 0
	}
	if input.DirectSuccessIncrement > 0 {
		next.DirectSuccessCount += input.DirectSuccessIncrement
	}
	if input.ProxySuccessIncrement > 0 {
		next.ProxySuccessCount += input.ProxySuccessIncrement
	}

	cooldownActive := CooldownActive(now, policy, next)

	if next.Mode == ModeProxy &&
		!cooldownActive &&
		next.ProxyFailureCount >= policy.TriggerFailureCount &&
		(!policy.RequireDirectPathSuccess || next.DirectSuccessCount > 0) {
		next.Mode = ModeDirect
		next.LastTransitionAt = now
		next.ProxyFailureCount = 0
		next.ProxySuccessCount = 0
		return Decision{
			ShouldTransition: true,
			NextMode:         ModeDirect,
			Reason:           policy.DirectModeReason,
			NextState:        next,
		}
	}

	if next.Mode == ModeDirect &&
		!cooldownActive &&
		next.ProxySuccessCount >= policy.RecoverySuccessCount {
		next.Mode = ModeProxy
		next.LastTransitionAt = now
		next.ProxyFailureCount = 0
		next.DirectSuccessCount = 0
		next.ProxySuccessCount = 0
		return Decision{
			ShouldTransition: true,
			NextMode:         ModeProxy,
			Reason:           "Proxy path recovered",
			NextState:        next,
		}
	}

	return Decision{
		ShouldTransition: false,
		NextMode:         next.Mode,
		Reason:           "",
		NextState:        next,
	}
}
