package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// The router's own clock face. OpenWrt keeps its time zone as a POSIX TZ
// string (/etc/TZ, a link to /tmp/TZ: "MSK-3", "<+03>-3",
// "CET-1CEST,M3.5.0,M10.5.0/3") and no zoneinfo, so Go reads the time in UTC.
// A schedule the operator wrote as local time — PassWall's "0:00" for the
// subscription, busybox cron's 06:00 for the geo files — is local time, and
// is read in routerLocation.

// tzPaths are where the router's TZ string is; tests replace them.
var tzPaths = []string{"/tmp/TZ", "/etc/TZ"}

// routerLocation is the router's standard time as a fixed zone; UTC when it
// has none. Summer time (the part after the first comma) is not followed:
// the fleet's zone (Moscow) has none, and elsewhere a schedule then runs an
// hour off half the year, never not at all.
func routerLocation() *time.Location {
	for _, p := range tzPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if loc, ok := parsePosixTZ(strings.TrimSpace(string(b))); ok {
			return loc
		}
	}
	return time.UTC
}

// parsePosixTZ reads a POSIX TZ string's standard zone: a name ("MSK", or
// quoted "<+03>") and its offset in hours WEST of UTC ("-3" is UTC+3), with
// optional minutes and seconds.
func parsePosixTZ(s string) (*time.Location, bool) {
	s, _, _ = strings.Cut(s, ",")
	var name string
	if strings.HasPrefix(s, "<") {
		end := strings.Index(s, ">")
		if end < 0 {
			return nil, false
		}
		name, s = s[1:end], s[end+1:]
	} else {
		i := 0
		for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
			i++
		}
		name, s = s[:i], s[i:]
	}
	if len(name) < 1 || s == "" {
		return nil, false
	}
	sign := 1
	switch s[0] {
	case '-':
		sign, s = -1, s[1:]
	case '+':
		s = s[1:]
	}
	// The offset ends where the summer zone's name begins.
	j := 0
	for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ':') {
		j++
	}
	parts := strings.Split(s[:j], ":")
	if parts[0] == "" || len(parts) > 3 {
		return nil, false
	}
	secs := 0
	for k, unit := range []int{3600, 60, 1} {
		if k >= len(parts) {
			break
		}
		v, err := strconv.Atoi(parts[k])
		if err != nil || v < 0 || (k == 0 && v > 24) || (k > 0 && v > 59) {
			return nil, false
		}
		secs += v * unit
	}
	return time.FixedZone(name, -sign*secs), true
}
