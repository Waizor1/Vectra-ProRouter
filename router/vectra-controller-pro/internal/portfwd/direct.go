package portfwd

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
)

// DirectStatus is what the daemon last made of the «past the VPN» devices,
// kept in a file (Env.DirectStatus) so `vctl rpcd` and the telemetry can say
// whether a rule's direct flag is in effect — a flag the router reads back as
// true while nothing carries it out would be a silent no-op.
//
// The daemon writes it after every attempt to write the data plane's set:
// Active with the addresses the set now holds; not Active when the write
// failed, when the data plane is not loaded by vctl (stopped, Vectra off),
// or when it has no direct conntrack bit. In rescue's direct mode everything
// goes out by the kernel, so every device is past the VPN: Active, with the
// addresses that were asked for. PID is the daemon's: a status whose daemon
// is gone says nothing any more.
type DirectStatus struct {
	PID    int      `json:"pid"`
	Active bool     `json:"active"`
	Addrs  []string `json:"addrs"`
}

// WriteDirectStatus replaces the status file.
func WriteDirectStatus(path string, st DirectStatus) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'))
}

// DirectActive is the port_forwards answer's directActive: nil when no
// enabled rule asks for «past the VPN»; true when the status file's daemon
// runs (alive) and the set holds every address asked for; false otherwise —
// some rule's direct flag is not in effect.
func DirectActive(rules []Rule, statusPath string, alive func(pid int) bool) *bool {
	want := DirectAddrs(rules)
	if len(want) == 0 {
		return nil
	}
	no := false
	raw, err := os.ReadFile(statusPath)
	if err != nil || len(raw) > 64<<10 {
		return &no
	}
	var st DirectStatus
	if json.Unmarshal(raw, &st) != nil || !st.Active || st.PID <= 0 || alive == nil || !alive(st.PID) {
		return &no
	}
	have := map[netip.Addr]bool{}
	for _, a := range st.Addrs {
		if p, err := netip.ParseAddr(a); err == nil {
			have[p] = true
		}
	}
	for _, a := range want {
		if !have[a] {
			return &no
		}
	}
	yes := true
	return &yes
}
