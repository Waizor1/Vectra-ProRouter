// Package connecttelemetry emits bounded actual router observations. It
// performs no network probes, and never derives country from node labels.
package connecttelemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"vectra-controller-pro/internal/controlplane"
)

const MaxFileBytes = 1 << 20

// Snapshot supplies daemon observations and applied settings. Verdict and
// country expire together; heartbeat and process state cannot create them.
type Snapshot struct {
	Telemetry  controlplane.RouterConnectTelemetry
	ObservedAt time.Time
}

const MaxObservationAge = 2 * time.Minute

func Build(read func(string) ([]byte, error), now time.Time, snapshot Snapshot) *controlplane.RouterConnectTelemetry {
	// Clone so callers cannot mutate a report while it is being serialized.
	raw, _ := json.Marshal(snapshot.Telemetry)
	out := new(controlplane.RouterConnectTelemetry)
	_ = json.Unmarshal(raw, out)
	if snapshot.ObservedAt.IsZero() || now.Before(snapshot.ObservedAt) || now.Sub(snapshot.ObservedAt) > MaxObservationAge {
		out.Verdict = ""
		out.ExitCountry = nil
	}
	switch out.Verdict {
	case "ok", "reserve", "down", "direct", "leak", "stopped":
	default:
		out.Verdict = ""
	}
	if out.ExitCountry != nil && !validCountry(*out.ExitCountry) {
		out.ExitCountry = nil
	}
	// The caller cannot substitute guessed LAN/uptime values for readings.
	out.UptimeSec = nil
	out.LanClients = nil
	if raw, err := read("/proc/uptime"); err == nil && len(raw) <= MaxFileBytes {
		f := strings.Fields(string(raw))
		if len(f) > 0 {
			if n, e := strconv.ParseFloat(f[0], 64); e == nil && n >= 0 && !math.IsInf(n, 0) && !math.IsNaN(n) && n < float64(math.MaxInt64) {
				v := int64(n)
				out.UptimeSec = &v
			}
		}
	}
	if raw, err := read("/tmp/dhcp.leases"); err == nil && len(raw) <= MaxFileBytes {
		out.LanClients = LeaseCount(raw, now)
	}
	if out.Entries != nil && len(*out.Entries) > 300 {
		out.Entries = nil
	}
	if out.Services != nil && len(*out.Services) > 100 {
		out.Services = nil
	}
	if out.Wifi != nil {
		for i := range *out.Wifi {
			(*out.Wifi)[i].Password = ""
		}
	}
	if out.Wifi != nil && len(*out.Wifi) > 8 {
		out.Wifi = nil
	}
	if out.Sites != nil && (len(out.Sites.Direct) > 300 || len(out.Sites.VPN) > 300) {
		out.Sites = nil
	}
	if out.Location != nil && out.Location.Mode != "auto" && out.Location.Mode != "entry" {
		out.Location = nil
	}
	if out.Entries != nil {
		for _, entry := range *out.Entries {
			if entry.ID == "" {
				out.Entries = nil
				break
			}
		}
		if out.Entries != nil {
			for i := range *out.Entries {
				entry := &(*out.Entries)[i]
				if entry.Country != nil && !validCountry(*entry.Country) {
					entry.Country = nil
				}
			}
		}
	}
	if out.Sites != nil {
		if out.Sites.Direct == nil {
			out.Sites.Direct = []string{}
		}
		if out.Sites.VPN == nil {
			out.Sites.VPN = []string{}
		}
	}
	if out.Capabilities != nil && len(*out.Capabilities) > 64 {
		out.Capabilities = nil
	}
	return out
}
func validCountry(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// LeaseCount counts distinct active DHCP clients, not historic/expired leases.
// A malformed or oversized source remains unavailable rather than zero.
func LeaseCount(raw []byte, now time.Time) *int {
	if len(raw) > MaxFileBytes {
		return nil
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 5 {
			return nil
		}
		expiry, e := strconv.ParseInt(f[0], 10, 64)
		if e != nil || expiry < 0 {
			return nil
		}
		mac, e := net.ParseMAC(f[1])
		if e != nil || len(mac) != 6 || net.ParseIP(f[2]) == nil {
			return nil
		}
		if expiry != 0 && expiry <= now.Unix() {
			continue
		}
		seen[mac.String()] = true
	}
	if sc.Err() != nil {
		return nil
	}
	n := len(seen)
	return &n
}
