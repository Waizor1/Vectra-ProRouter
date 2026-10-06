package portfwd

import (
	"bufio"
	"bytes"
	"net"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"

	"vectra-controller-pro/internal/uci"
)

// Device is a LAN device the UI offers as a destination: its name and
// address. Its MAC address never leaves the router — not in this answer, not
// in Vectra Connect's telemetry.
type Device struct {
	Name *string `json:"name"`
	IP   string  `json:"ip"`
}

// parseLeases reads dnsmasq's /tmp/dhcp.leases
// ("<expiry> <mac> <ip> <hostname|*> <client-id|*>"): each IPv4 address's
// name ("" for none; the last line of an address wins, as dnsmasq rewrites
// it). A line it cannot read is skipped: one bad line must not hide every
// device. The MAC is only checked, never kept.
func parseLeases(raw []byte) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		if mac, err := net.ParseMAC(f[1]); err != nil || len(mac) != 6 {
			continue
		}
		a, err := netip.ParseAddr(f[2])
		if err != nil || !a.Is4() {
			continue
		}
		name := f[3]
		if name == "*" {
			name = ""
		}
		out[a] = name
	}
	return out
}

// parseStaticHosts reads the dhcp config's static leases (`config host`):
// each IPv4 address's name.
func parseStaticHosts(f *uci.File) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	if f == nil {
		return out
	}
	for _, s := range f.OfType("host") {
		for _, ip := range words(s, "ip") {
			if a, err := netip.ParseAddr(ip); err == nil && a.Is4() {
				out[a] = s.Get("name")
			}
		}
	}
	return out
}

// displayName is a device name fit to show and to put in a section's name:
// printable, at most 63 characters; nil when there is none.
func displayName(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 63 {
		return nil
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return nil
		}
	}
	return &s
}

// deviceNames merges the leases' names and the static hosts' (which win),
// keeping only the names fit to show.
func deviceNames(leases, hosts map[netip.Addr]string) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	for _, m := range []map[netip.Addr]string{leases, hosts} {
		for a, n := range m {
			if d := displayName(n); d != nil {
				out[a] = *d
			}
		}
	}
	return out
}

// devices is one row per IPv4 address the leases and static hosts know,
// inside the LAN's subnets (all of them when those are unknown), sorted by
// address, at most MaxDevices.
func devices(leases, hosts, names map[netip.Addr]string, lan LAN) []Device {
	seen := map[netip.Addr]bool{}
	var addrs []netip.Addr
	for _, m := range []map[netip.Addr]string{leases, hosts} {
		for a := range m {
			if !seen[a] && inLAN(a, lan) {
				seen[a] = true
				addrs = append(addrs, a)
			}
		}
	}
	sortAddrs(addrs)
	out := []Device{}
	for _, a := range addrs {
		if len(out) == MaxDevices {
			break
		}
		d := Device{IP: a.String()}
		if n, ok := names[a]; ok {
			d.Name = &n
		}
		out = append(out, d)
	}
	return out
}

func inLAN(a netip.Addr, lan LAN) bool {
	if len(lan.Subnets) == 0 {
		return true
	}
	for _, p := range lan.Subnets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func sortAddrs(as []netip.Addr) {
	for i := 1; i < len(as); i++ {
		for j := i; j > 0 && as[j].Less(as[j-1]); j-- {
			as[j], as[j-1] = as[j-1], as[j]
		}
	}
}
