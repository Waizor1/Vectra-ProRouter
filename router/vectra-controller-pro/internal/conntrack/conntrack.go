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
			// The reply direction repeats the keys: only the original
			// direction counts, and a line that got this far is whole.
			reply = true
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
