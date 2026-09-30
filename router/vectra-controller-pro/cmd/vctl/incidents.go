package main

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/incident"
)

// What vctl sees going other than meant goes to the reporter's inbox
// (internal/incident, ADR-0007): at most one of a code every 10 minutes.

var reKeyNumber = regexp.MustCompile(`\d+`)

// incident records one; without a recorder (tests, CLI commands) nothing.
func (d *daemon) incident(code, key, title string, details map[string]any) bool {
	return d.incidents.Record(incident.Incident{Code: code, Key: key, Title: title, Source: "vctl", Details: details})
}

// noteApplyErr: `xray -test` refused a render vctl made — not for want of
// memory, which is tried again and says nothing of the config.
func (d *daemon) noteApplyErr(err error) bool {
	if !errors.Is(err, apply.ErrRefused) || errors.Is(err, xray.ErrLowMemory) {
		return false
	}
	msg := err.Error()
	if i := strings.Index(msg, apply.ErrRefused.Error()); i >= 0 {
		msg = strings.TrimPrefix(msg[i+len(apply.ErrRefused.Error()):], ": ")
	}
	first := strings.SplitN(msg, "\n", 2)[0]
	return d.incident("APPLY_REFUSED", reKeyNumber.ReplaceAllString(first, "N"),
		"xray -test refused the config vctl made: "+first, map[string]any{"routeSource": d.cfg.RouteSource})
}

// xrayLogPath is where the supervisor keeps xray's own output (capped).
var xrayLogPath = "/var/log/vectra-controller-pro/xray.log"

// lastLines is the last n lines of path, read from its last 16 KB.
func lastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 16<<10 {
		_, _ = f.Seek(-16<<10, io.SeekEnd)
	}
	b, _ := io.ReadAll(io.LimitReader(f, 16<<10))
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// noteXrayExit: xray exited unasked — with its own last lines, why; five in
// ten minutes are a storm.
func (d *daemon) noteXrayExit(code int, err error, ran time.Duration, now time.Time) {
	reason := "exit"
	if err != nil {
		reason = reKeyNumber.ReplaceAllString(err.Error(), "N")
	}
	d.incidents.Record(incident.Incident{Code: "XRAY_CRASH", Key: reason, Title: "xray exited without being asked to",
		Source: "vctl", Details: map[string]any{"exitCode": code, "ranSec": int(ran.Seconds())}, Log: lastLines(xrayLogPath, 20)})
	d.xrayExitsMu.Lock() // the supervisor's goroutine calls this
	defer d.xrayExitsMu.Unlock()
	d.xrayExits = append(d.xrayExits, now)
	for len(d.xrayExits) > 0 && now.Sub(d.xrayExits[0]) > 10*time.Minute {
		d.xrayExits = d.xrayExits[1:]
	}
	if len(d.xrayExits) >= 5 {
		if d.incident("XRAY_RESTART_STORM", "xray restart storm", "xray keeps exiting: 5 times in 10 minutes", map[string]any{"exits": len(d.xrayExits)}) {
			d.xrayExits = nil
		}
	}
}
