// Package rescue is a pragmatic connectivity-based mode evaluator for the
// xray-direct controller. It is a deliberately smaller subset of the legacy
// agent's rescue+recovery machine: enough to detect "proxy path is down" and
// recommend a fallback to direct (and recovery back to proxy), with a cooldown
// to prevent flapping. The deep auto-reboot recovery phases are deferred — on
// a canary, PassWall2 remains installed as the instant rollback.
//
// Evaluate is pure (no I/O) so the policy is fully unit-tested; the daemon
// performs the probes (ProbeAny) and acts on the Decision.
package rescue

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// Mode is the controller's routing posture.
type Mode string

const (
	ModeProxy  Mode = "proxy"  // traffic flows through Xray
	ModeDirect Mode = "direct" // Xray routing bypassed; plain internet
)

// State is the rescue state carried across loops.
type State struct {
	Mode               Mode
	ProxyFailureCount  int
	DirectSuccessCount int
	ProxySuccessCount  int
	LastTransitionAt   time.Time
	// FirstFailureAt is when the current run of failed probes began: the
	// failures must span MinFailSpan before the router leaves the tunnel.
	FirstFailureAt time.Time
	// FailedRetries counts the returns to the proxy in a row that failed
	// within RetryWindow: each doubles the cooldown of the next (up to 16×),
	// so a tunnel the observatory calls alive but that carries nothing does
	// not swing the LAN every few minutes. A proxy that holds for StableAfter
	// clears it.
	FailedRetries int
}

// RetryWindow: a proxy that fails this soon after the router entered it was a
// failed return. StableAfter: a proxy that works this long clears the count.
const (
	RetryWindow = 5 * time.Minute
	StableAfter = 10 * time.Minute
)

// MinFailSpan is the default Policy.MinFailSpan: how long failed probes must
// span before the router goes direct. Rechecked every few seconds, three fast
// failures (a reset refused at once) would otherwise beat the failover
// watchdog, which moves off a dead node within seconds and may already have
// healed the tunnel.
const MinFailSpan = 20 * time.Second

// maxBackoffShift caps the cooldown's doubling (2 min << 4 = 32 min).
const maxBackoffShift = 4

// CooldownAfter is the cooldown on the way back after failed returns to the
// proxy in a row: p.Cooldown, doubled for each, up to 16×.
func CooldownAfter(p Policy, failed int) time.Duration {
	if failed > maxBackoffShift {
		failed = maxBackoffShift
	}
	if failed < 0 {
		failed = 0
	}
	return p.Cooldown << failed
}

// Policy tunes the evaluator.
type Policy struct {
	HealthURLs []string
	// TraceURLs are asked through the tunnel (see TraceURLs).
	TraceURLs            []string
	TriggerFailureCount  int           // consecutive proxy failures before going direct
	RecoverySuccessCount int           // consecutive direct successes before retrying proxy
	Cooldown             time.Duration // minimum gap between transitions
	MinFailSpan          time.Duration // failures must span this before going direct
}

// DefaultPolicy returns sane defaults (mirrors the agent's thresholds).
func DefaultPolicy() Policy {
	return Policy{
		HealthURLs:           []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"},
		TraceURLs:            TraceURLs,
		TriggerFailureCount:  3,
		RecoverySuccessCount: 2,
		// The way back to the proxy only (the way out never waits), and
		// only once a node lives (Input.TunnelDead): two minutes keep a
		// flapping tunnel from swinging the LAN, without holding a working
		// one off for long.
		Cooldown:    2 * time.Minute,
		MinFailSpan: MinFailSpan,
	}
}

// Input is the per-loop observation handed to Evaluate.
type Input struct {
	CurrentState    State
	PublicReachable bool // could we reach the public health URLs this loop?
	ProxyConclusive bool // in proxy mode, was the failure conclusive (not a transient probe error)?
	DirectReachable bool // is a direct path available (gate before switching to direct)?
	// TunnelDead: xray's observatory holds every node the main traffic can
	// take dead. In direct mode the proxy is not retried then: retried, it
	// carried nothing and the LAN sat offline until the rescue left it again.
	TunnelDead bool
	Now        time.Time
}

// Decision is the evaluator's recommendation.
type Decision struct {
	ShouldTransition bool
	NextMode         Mode
	NextState        State
	Reason           string
}

// Evaluate decides whether to transition modes given the current observation.
func Evaluate(in Input, p Policy) Decision {
	if p.TriggerFailureCount <= 0 {
		p.TriggerFailureCount = DefaultPolicy().TriggerFailureCount
	}
	if p.RecoverySuccessCount <= 0 {
		p.RecoverySuccessCount = DefaultPolicy().RecoverySuccessCount
	}
	st := in.CurrentState
	if st.Mode == "" {
		st.Mode = ModeProxy
	}
	d := Decision{NextMode: st.Mode, NextState: st}

	cooldownOK := st.LastTransitionAt.IsZero() || in.Now.Sub(st.LastTransitionAt) >= CooldownAfter(p, st.FailedRetries)

	switch st.Mode {
	case ModeProxy:
		if in.PublicReachable {
			d.NextState.ProxyFailureCount = 0
			d.NextState.FirstFailureAt = time.Time{}
			d.NextState.ProxySuccessCount = st.ProxySuccessCount + 1
			if st.FailedRetries > 0 && !st.LastTransitionAt.IsZero() && in.Now.Sub(st.LastTransitionAt) >= StableAfter {
				d.NextState.FailedRetries = 0
			}
			return d
		}
		if !in.ProxyConclusive {
			return d // transient probe failure — don't count it
		}
		d.NextState.ProxyFailureCount = st.ProxyFailureCount + 1
		if st.ProxyFailureCount == 0 || st.FirstFailureAt.IsZero() {
			d.NextState.FirstFailureAt = in.Now
		}
		// No cooldown on the way out: the internet must not wait for it. The
		// way back keeps it, so a flapping tunnel still swings at most once
		// a cooldown.
		if d.NextState.ProxyFailureCount >= p.TriggerFailureCount && in.DirectReachable &&
			in.Now.Sub(d.NextState.FirstFailureAt) >= p.MinFailSpan {
			d.ShouldTransition = true
			d.NextMode = ModeDirect
			d.NextState.Mode = ModeDirect
			d.NextState.DirectSuccessCount = 0
			d.NextState.FirstFailureAt = time.Time{}
			if !st.LastTransitionAt.IsZero() && in.Now.Sub(st.LastTransitionAt) < RetryWindow {
				d.NextState.FailedRetries = st.FailedRetries + 1
			}
			d.NextState.LastTransitionAt = in.Now
			d.Reason = "proxy path unreachable; falling back to direct"
		}
		return d

	case ModeDirect:
		if in.PublicReachable {
			d.NextState.DirectSuccessCount = st.DirectSuccessCount + 1
			if d.NextState.DirectSuccessCount >= p.RecoverySuccessCount && cooldownOK && !in.TunnelDead {
				d.ShouldTransition = true
				d.NextMode = ModeProxy
				d.NextState.Mode = ModeProxy
				d.NextState.ProxyFailureCount = 0
				d.NextState.LastTransitionAt = in.Now
				d.Reason = "direct path stable; retrying proxy"
			}
		}
		return d
	}
	return d
}

// ProbeAny returns true if ANY url answers with a < 400 status. A nil client
// uses a short-timeout default. Used by the daemon to fill Input.
//
// The urls are asked at once, and the first answer ends the others: asked in
// turn, two dead urls cost two full timeouts in every loop of the daemon —
// with the internet down, loops long enough to slow down everything else the
// loop keeps in order (the data plane, the DNS redirect).
func ProbeAny(ctx context.Context, client *http.Client, urls []string) bool {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	if len(urls) == 0 {
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan bool, len(urls))
	for _, u := range urls {
		go func(u string) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				results <- false
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				results <- false
				return
			}
			_ = resp.Body.Close()
			results <- resp.StatusCode < 400
		}(u)
	}
	for range urls {
		if <-results {
			return true
		}
	}
	return false
}

// ProbeAnyWithin is ProbeAny bounded by d as a whole, whatever the client's
// own timeout.
func ProbeAnyWithin(ctx context.Context, client *http.Client, urls []string, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return ProbeAny(ctx, client, urls)
}

// TraceURLs answer with the address the request came from ("ip=" in
// Cloudflare's /cdn-cgi/trace): asked through the tunnel, an answer from the
// router's own WAN address went around it. A plain 2xx proves nothing there —
// www.gstatic.com resolves to a Google cache inside the ISP, whose address the
// kernel sends straight out, so the probe "worked" with every node dead
// (1111, 2026-10-04).
var TraceURLs = []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://cp.cloudflare.com/cdn-cgi/trace"}

// ProbeTrace asks the trace urls at once, within d, and returns the address
// the first answer saw the request come from.
func ProbeTrace(ctx context.Context, client *http.Client, urls []string, d time.Duration) (string, bool) {
	ips := probeTrace(ctx, client, urls, d, true, "")
	if len(ips) == 0 {
		return "", false
	}
	return ips[0], true
}

// ProbeTraceAll asks the trace urls at once, within d, and returns the address
// each answer saw — all of them: through the tunnel one url may go out by the
// kernel (an address in the direct sets) while another takes the tunnel, and
// only the second says the tunnel works (1111, 2026-10-04: cp.cloudflare.com
// answered from the WAN while www.cloudflare.com came through Poland).
//
// around, when not empty, is the router's own address: the first answer from
// any other ends the asking — the tunnel is proven, the rest need not time out.
func ProbeTraceAll(ctx context.Context, client *http.Client, urls []string, d time.Duration, around string) []string {
	return probeTrace(ctx, client, urls, d, false, around)
}

func probeTrace(ctx context.Context, client *http.Client, urls []string, d time.Duration, first bool, around string) []string {
	if client == nil {
		client = &http.Client{Timeout: d}
	}
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	results := make(chan string, len(urls))
	for _, u := range urls {
		go func(u string) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				results <- ""
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				results <- ""
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if resp.StatusCode >= 400 {
				results <- ""
				return
			}
			for _, line := range strings.Split(string(body), "\n") {
				if ip, found := strings.CutPrefix(strings.TrimSpace(line), "ip="); found && ip != "" {
					results <- ip
					return
				}
			}
			results <- ""
		}(u)
	}
	var ips []string
	for range urls {
		if ip := <-results; ip != "" {
			ips = append(ips, ip)
			if first || (around != "" && ip != around) {
				return ips
			}
		}
	}
	return ips
}
