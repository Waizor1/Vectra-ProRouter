package uiapi

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxLogLines bounds what one `logs` call returns.
const MaxLogLines = 500

// xray: "2026/09/27 16:33:18.749926 [Warning] core: Xray 26.3.27 started"
var xrayLine = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})(?:\.\d+)? \[(\w+)\] (.*)$`)

// logread: "Sat Sep 27 16:33:18 2026 daemon.info vctl[1234]: <message>"
var logreadLine = regexp.MustCompile(`^\w{3} (\w{3} +\d+ \d{2}:\d{2}:\d{2} \d{4}) \w+\.(\w+) ([^:\[]+)(?:\[\d+\])?: (.*)$`)

// slog text: `time=2026-09-27T16:33:18.123Z level=INFO msg="check-in ok" ...`
var slogTime = regexp.MustCompile(`(?:^|\s)time=(\S+)`)
var slogLevel = regexp.MustCompile(`(?:^|\s)level=(\w+)`)

// ParseXrayLine turns one xray log line into a LogLine; loc is the router's
// zone, which xray's timestamps are in.
func ParseXrayLine(s string, loc *time.Location) LogLine {
	src := "xray"
	m := xrayLine.FindStringSubmatch(s)
	if m == nil {
		return LogLine{Level: "unknown", Source: &src, Message: s}
	}
	line := LogLine{Level: normLevel(m[2]), Source: &src, Message: m[3]}
	if t, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], loc); err == nil {
		line.Time = timePtr(t)
	}
	return line
}

// ControllerTag reports whether a syslog tag is the controller's own. Only
// such lines are the controller's: anyone who can put one line into syslog —
// a failed LuCI login's username, a DHCP hostname — must not be able to write
// "vctl" ERROR lines into the journal a customer copies to support.
func ControllerTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	return tag == "vctl" || tag == "vectra-controller-pro" || strings.HasPrefix(tag, "vectra-controller-pro.")
}

// ParseLogreadLine turns one logread line into a LogLine. ok is false unless
// the line was logged under the controller's own tag.
func ParseLogreadLine(s string, loc *time.Location) (LogLine, bool) {
	m := logreadLine.FindStringSubmatch(s)
	if m == nil || !ControllerTag(m[3]) {
		return LogLine{}, false
	}
	src := "vctl"
	msg := m[4]
	line := LogLine{Level: normLevel(m[2]), Source: &src, Message: msg}
	if t, err := time.ParseInLocation("Jan _2 15:04:05 2006", m[1], loc); err == nil {
		line.Time = timePtr(t)
	}
	// vctl logs through slog: its own time and level are the precise ones.
	if tm := slogTime.FindStringSubmatch(msg); tm != nil {
		if t, err := time.Parse(time.RFC3339Nano, tm[1]); err == nil {
			line.Time = timePtr(t)
		}
	}
	if lm := slogLevel.FindStringSubmatch(msg); lm != nil {
		line.Level = normLevel(lm[1])
	}
	return line, true
}

// xray prints a two-line banner on every start; after a few restarts it is
// most of what a short journal would show.
var xrayBanner = regexp.MustCompile(`^(Xray \d+\.\d+\.\d+ \(|A unified platform for anti-censorship)`)

func isXrayBanner(s string) bool { return xrayBanner.MatchString(s) }

func normLevel(s string) string {
	switch strings.ToLower(s) {
	case "debug":
		return "debug"
	case "info", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "err", "error", "crit", "alert", "emerg":
		return "error"
	}
	return "unknown"
}

// tailLines returns up to n last lines of path.
func tailLines(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// routerLocation is the zone xray and logread print in: `date +%z` answers
// with the offset OpenWrt's /etc/TZ gives, which Go cannot parse from a POSIX
// TZ string itself.
func routerLocation(ctx context.Context) *time.Location {
	out, err := exec.CommandContext(ctx, "date", "+%z").Output()
	if err != nil {
		return time.UTC
	}
	s := strings.TrimSpace(string(out))
	if len(s) != 5 {
		return time.UTC
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil {
		return time.UTC
	}
	off := (h*60 + m) * 60
	if s[0] == '-' {
		off = -off
	}
	return time.FixedZone(s, off)
}

// GatherLogs merges the newest n lines of xray's log and the controller's
// syslog lines, oldest first.
func GatherLogs(ctx context.Context, xrayLog string, n int) Logs {
	if n <= 0 {
		n = 200
	}
	if n > MaxLogLines {
		n = MaxLogLines
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	loc := routerLocation(c)

	var lines []LogLine
	raw := append(tailLines(xrayLog+".1", n), tailLines(xrayLog, n)...)
	for _, s := range raw {
		if strings.TrimSpace(s) != "" && !isXrayBanner(s) {
			lines = append(lines, ParseXrayLine(s, loc))
		}
	}
	// No `-e`: ubox logread compiles it as a BASIC regex, where "a|b" is a
	// literal and would match nothing. The tag is checked here instead.
	if out, err := exec.CommandContext(c, "logread", "-l", strconv.Itoa(n*8)).Output(); err == nil {
		for _, s := range strings.Split(string(bytes.TrimRight(out, "\n")), "\n") {
			if l, ok := ParseLogreadLine(s, loc); ok {
				lines = append(lines, l)
			}
		}
	}
	sortLogLines(lines)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if lines == nil {
		lines = []LogLine{}
	}
	return Logs{Lines: lines}
}

// sortLogLines orders lines by time. A line without a timestamp (a
// continuation, a banner) keeps the time of the line before it in its own
// stream, so it stays where it was printed instead of comparing "equal" to
// everything and pinning older lines behind it.
func sortLogLines(lines []LogLine) {
	keys := make([]string, len(lines))
	last := map[string]string{}
	for i, l := range lines {
		src := ""
		if l.Source != nil {
			src = *l.Source
		}
		if l.Time != nil {
			last[src] = *l.Time
		}
		keys[i] = last[src]
	}
	idx := make([]int, len(lines))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	sorted := make([]LogLine, len(lines))
	for i, j := range idx {
		sorted[i] = lines[j]
	}
	copy(lines, sorted)
}
