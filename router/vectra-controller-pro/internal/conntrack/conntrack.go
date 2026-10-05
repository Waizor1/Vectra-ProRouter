// Package conntrack reads the kernel's connection table — through ctnetlink
// (netlink.go), or /proc/net/nf_conntrack on a kernel without it: enough of
// each tcp/udp entry to tell where the original direction went and whether it
// was ever answered. Entries it cannot read are skipped.
package conntrack

import (
	"bufio"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Path is where the kernel shows the table as text (a variable for tests).
var Path = "/proc/net/nf_conntrack"

// Entry is one connection's ORIGINAL direction.
type Entry struct {
	Proto        string // tcp | udp
	State        string // tcp only, e.g. SYN_SENT, ESTABLISHED
	Src, Dst     netip.Addr
	SPort, DPort uint16
	// Replied is false while the line carries [UNREPLIED]: nothing ever came
	// back the other way.
	Replied bool
	// ReplySrc and ReplySPort are where the answers come from: the original
	// destination, unless a NAT rule took the connection elsewhere (a
	// redirect to a loopback port answers from 127.0.0.1 and that port).
	ReplySrc   netip.Addr
	ReplySPort uint16
}

// readProc parses the kernel's table as text.
func readProc() ([]Entry, error) {
	f, err := os.Open(Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f), nil
}

// Parse reads a table in /proc/net/nf_conntrack's format.
func Parse(r io.Reader) []Entry {
	var out []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		if e, ok := parseLine(sc.Text()); ok {
			out = append(out, e)
		}
	}
	return out
}

// A line: family, family number, protocol, protocol number, timeout,
// [state (tcp)], the original tuple and counters, [UNREPLIED], the reply
// tuple, flags, mark, zone, use.
func parseLine(line string) (Entry, bool) {
	f := strings.Fields(line)
	if len(f) < 6 || (f[0] != "ipv4" && f[0] != "ipv6") {
		return Entry{}, false
	}
	e := Entry{Proto: f[2], Replied: true}
	if e.Proto != "tcp" && e.Proto != "udp" {
		return Entry{}, false
	}
	rest := f[5:]
	if e.Proto == "tcp" && !strings.Contains(f[5], "=") {
		e.State, rest = f[5], f[6:]
	}
	seen := map[string]bool{}
	reply := false
	for _, tok := range rest {
		if tok == "[UNREPLIED]" {
			e.Replied = false
			continue
		}
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue // a flag
		}
		if seen[k] {
			// The reply direction repeats the keys: a line that got this
			// far is whole; of the reply, where the answers come from.
			reply = true
			if !seen["reply "+k] {
				seen["reply "+k] = true
				switch k {
				case "src":
					e.ReplySrc, _ = netip.ParseAddr(v)
				case "sport":
					p, _ := strconv.ParseUint(v, 10, 16)
					e.ReplySPort = uint16(p)
				}
			}
			continue
		}
		seen[k] = true
		switch k {
		case "src":
			e.Src, _ = netip.ParseAddr(v)
		case "dst":
			e.Dst, _ = netip.ParseAddr(v)
		case "sport":
			p, _ := strconv.ParseUint(v, 10, 16)
			e.SPort = uint16(p)
		case "dport":
			p, _ := strconv.ParseUint(v, 10, 16)
			e.DPort = uint16(p)
		}
	}
	if !reply || !e.Src.IsValid() || !e.Dst.IsValid() || e.DPort == 0 {
		return Entry{}, false
	}
	return e, true
}

// RedirectedTo are the connections a NAT redirect sent to a loopback port:
// to port 53 originally, answered from loopback:port. They keep that
// binding for as long as they live — a UDP one that was answered, three
// minutes from its last packet — whatever the ruleset says by then.
func RedirectedTo(es []Entry, port uint16) []Entry {
	var out []Entry
	for _, e := range es {
		if e.DPort == 53 && e.ReplySPort == port && e.ReplySrc.IsLoopback() {
			out = append(out, e)
		}
	}
	return out
}

// DNSFrom are the connections own() addresses opened to port 53 that go
// where they were sent — no NAT took them anywhere — on a non-loopback
// address.
func DNSFrom(es []Entry, own func(netip.Addr) bool) []Entry {
	var out []Entry
	for _, e := range es {
		if e.DPort == 53 && e.ReplySPort == 53 && e.ReplySrc == e.Dst && !e.Dst.IsLoopback() && own(e.Src) {
			out = append(out, e)
		}
	}
	return out
}
