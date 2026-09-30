// Package bugreport is the reporter's (vectra-reporter, ADR-0007): reports
// made of incidents, redacted before they reach flash, kept in a capped
// spool, and sent signed to Vectra Connect (docs/CONNECT-BUG-REPORTS.md).
package bugreport

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"vectra-controller-pro/internal/incident"
)

// Caps of the contract.
const (
	MaxStack  = 16 << 10
	MaxLog    = 80
	MaxKernel = 40
	MaxBody   = 32 << 10
	MaxTitle  = 300
)

// Report is one report: schema 1 of the contract.
type Report struct {
	Schema      int       `json:"schema"`
	ReportID    string    `json:"reportId"`
	Code        string    `json:"code"`
	Severity    string    `json:"severity"`
	Fingerprint string    `json:"fingerprint"`
	Title       string    `json:"title"`
	FirstAt     time.Time `json:"firstAt"`
	LastAt      time.Time `json:"lastAt"`
	Count       int       `json:"count"`
	Router      Router    `json:"router"`
	Evidence    Evidence  `json:"evidence"`
}

// Router is what every report says about the router.
type Router struct {
	DeviceID        string `json:"deviceId"`
	Model           string `json:"model,omitempty"`
	Arch            string `json:"arch,omitempty"`
	OpenWrt         string `json:"openwrt,omitempty"`
	Vctl            string `json:"vctl"`
	Xray            string `json:"xray,omitempty"`
	Reporter        string `json:"reporter,omitempty"`
	RouteSource     string `json:"routeSource,omitempty"`
	KillSwitch      *bool  `json:"killSwitch,omitempty"`
	UptimeSec       int64  `json:"uptimeSec,omitempty"`
	MemTotalMiB     uint64 `json:"memTotalMiB,omitempty"`
	MemAvailableMiB uint64 `json:"memAvailableMiB,omitempty"`
}

// Evidence is what the report shows of the failure.
type Evidence struct {
	Stack   string         `json:"stack,omitempty"`
	Log     []string       `json:"log,omitempty"`
	Kernel  []string       `json:"kernel,omitempty"`
	Memory  map[string]any `json:"memory,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

var severities = map[string]string{
	"VCTL_PANIC": "critical", "VCTL_CRASH_LOOP": "critical", "DATAPLANE_WITHOUT_XRAY": "critical",
	"VCTL_DOWN": "high", "XRAY_CRASH": "high", "XRAY_RESTART_STORM": "high", "OOM_KILL": "high",
	"UNEXPECTED_REBOOT": "high", "HANDBACK": "high", "APPLY_REFUSED": "high",
	"KERNEL_ERROR": "medium", "RESCUE_DIRECT": "medium", "SUBSCRIPTION_REFUSED": "medium",
	"GEO_REFUSED": "medium", "MEMORY_GROWTH": "medium",
	"REPORTER_ERROR": "low", "TEST": "low", "MANUAL": "low",
}

// DefaultSeverity is a code's severity; an unknown code's is medium.
func DefaultSeverity(code string) string {
	if s, ok := severities[code]; ok {
		return s
	}
	return "medium"
}

// SeverityRank orders severities: low 0 … critical 3; unknown as medium.
func SeverityRank(s string) int {
	switch s {
	case "low":
		return 0
	case "high":
		return 2
	case "critical":
		return 3
	}
	return 1
}

// Fingerprint is what makes two reports one problem: the code and the
// incident's key.
func Fingerprint(code, key string) string {
	h := sha256.Sum256([]byte(code + "\n" + key))
	return hex.EncodeToString(h[:])
}

// NewID is a random UUID (version 4): a report's id, the same through every
// retry, so the server counts it once.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// FromIncident makes a report of in: its own words, redacted and capped.
func FromIncident(in incident.Incident, r Router, now time.Time) Report {
	at := in.At
	if at.IsZero() {
		at = now
	}
	sev := in.Severity
	if _, ok := map[string]bool{"low": true, "medium": true, "high": true, "critical": true}[sev]; !ok {
		sev = DefaultSeverity(in.Code)
	}
	rep := Report{
		Schema: 1, ReportID: NewID(), Code: in.Code, Severity: sev,
		Fingerprint: Fingerprint(in.Code, in.Key), Title: capRunes(Redact(in.Title), MaxTitle),
		FirstAt: at.UTC(), LastAt: at.UTC(), Count: 1, Router: r,
	}
	if len(in.Details) > 0 {
		rep.Evidence.Details, _ = RedactValue(in.Details).(map[string]any)
	}
	rep.Evidence.Log = tail(RedactLines(in.Log), MaxLog)
	rep.Evidence.Kernel = tail(RedactLines(in.Kernel), MaxKernel)
	if in.Stack != "" {
		rep.Evidence.Stack = capBytes(Redact(in.Stack), MaxStack)
	}
	return rep
}

// Encode is the report's body, at most MaxBody: the log gives way first,
// then the kernel's lines, then the stack, then the details.
func (r Report) Encode() ([]byte, error) {
	r.Title = capRunes(r.Title, MaxTitle)
	r.Evidence.Stack = capBytes(r.Evidence.Stack, MaxStack)
	r.Evidence.Log = tail(r.Evidence.Log, MaxLog)
	r.Evidence.Kernel = tail(r.Evidence.Kernel, MaxKernel)
	for step := 0; ; step++ {
		b, err := json.Marshal(r)
		if err != nil || len(b) <= MaxBody {
			return b, err
		}
		switch {
		case len(r.Evidence.Log) > 0:
			r.Evidence.Log = r.Evidence.Log[(len(r.Evidence.Log)+1)/2:]
		case len(r.Evidence.Kernel) > 0:
			r.Evidence.Kernel = r.Evidence.Kernel[(len(r.Evidence.Kernel)+1)/2:]
		case len(r.Evidence.Stack) > 512:
			r.Evidence.Stack = capBytes(r.Evidence.Stack, len(r.Evidence.Stack)/2)
		case r.Evidence.Details != nil || r.Evidence.Memory != nil:
			r.Evidence.Details, r.Evidence.Memory = nil, nil
		default:
			return b, nil // the facts alone; nothing more to give
		}
	}
}

func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n…"
}

func isRuneStart(b byte) bool { return b&0xc0 != 0x80 }

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
