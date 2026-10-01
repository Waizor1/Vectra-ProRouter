package retire

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// App is PassWall2 itself.
const App = "luci-app-passwall2"

// candidates are all that may go with PassWall2, and nothing else ever does:
// its translations, itself, and the helpers it brought — in the order opkg
// removes them (a package another installed one still needs is refused).
// Not what it pulled in that the router needs anyway: xray-core,
// dnsmasq-full, curl, ip-full, coreutils, Lua, LuCI, kernel modules.
var candidates = []string{"luci-i18n-passwall2-*", App, "chinadns-ng", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"}

// guarded are never removed, whatever the list or a status file says: a
// second fence around what the router needs (see TestTheListNeverNamesWhat
// TheRouterNeeds). PassWall2's own LuCI packages are the list's, not these.
var guarded = []string{"xray-core*", "dnsmasq*", "curl", "libcurl*", "ip-full", "ip-tiny", "coreutils*", "kmod-*",
	"lua*", "liblua*", "libuci-lua", "luci-*", "ca-*", "unzip", "vectra-*", "firewall*", "nftables*", "uci", "opkg", "procd*"}

// neverRemove: name is one of the guarded, and not PassWall2's own.
func neverRemove(name string) bool {
	if name == App || strings.HasPrefix(name, "luci-i18n-passwall2-") {
		return false
	}
	for _, g := range guarded {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// listed: name is on the list of what may go.
func listed(name string) bool {
	for _, c := range candidates {
		if ok, _ := path.Match(c, name); ok {
			return true
		}
	}
	return false
}

// Package is an installed package as opkg's status file has it: what it
// needs — every name its Depends and Pre-Depends name, alternatives and all,
// versions dropped — and the names it provides.
type Package struct {
	Name     string
	Depends  []string
	Provides []string
}

// Installed reads the status file: what opkg has installed.
func (e Env) Installed() (map[string]Package, error) {
	b, err := os.ReadFile(e.StatusFile)
	if err != nil {
		return nil, err
	}
	return ParseStatus(b), nil
}

// ParseStatus reads /usr/lib/opkg/status: its stanzas whose state is
// installed (or unpacked — opkg counts both when it refuses a removal).
// Read here rather than asked of `opkg whatdepends`, which loads every feed
// list (megabytes on a 234 MB router) to answer what this file already says.
func ParseStatus(b []byte) map[string]Package {
	out := map[string]Package{}
	var p Package
	var state string
	flush := func() {
		if p.Name != "" && (state == "installed" || state == "unpacked") {
			out[p.Name] = p
		}
		p, state = Package{}, ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // a continuation (Conffiles)
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Package":
			p.Name = val
		case "Depends", "Pre-Depends":
			p.Depends = append(p.Depends, names(val)...)
		case "Provides":
			p.Provides = append(p.Provides, names(val)...)
		case "Status":
			if f := strings.Fields(val); len(f) == 3 {
				state = f[2]
			}
		}
	}
	flush()
	return out
}

// names are the package names of a Depends-like field: "a (>= 1), b | c".
func names(field string) []string {
	var out []string
	for _, part := range strings.Split(field, ",") {
		for _, alt := range strings.Split(part, "|") {
			n := strings.TrimSpace(alt)
			if i := strings.IndexAny(n, " ("); i >= 0 {
				n = n[:i]
			}
			n, _, _ = strings.Cut(n, ":")
			if n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// Plan is what goes, in the order opkg is to remove it, and what stays of
// the list, with why.
type Plan struct {
	Remove []string
	Keep   map[string]string
}

// plain is a package name opkg cannot read as a pattern: it takes its
// arguments to fnmatch, and "a*" would name more than a.
var plain = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)

// MakePlan is what of the list goes from a router with these packages.
// protect names what stays whatever needs it (the caller's reasons: vctl
// reads v2ray-geoip's file). Anything else stays only while an installed
// package that does not go needs it — by its name or one it provides — so
// PassWall2 needed by something stays with everything it needs. A name that
// is not plain is never on the plan: it stays as any other package does.
func MakePlan(installed map[string]Package, protect map[string]string) Plan {
	var set []string
	for _, c := range candidates {
		var match []string
		for name := range installed {
			if ok, _ := path.Match(c, name); ok && plain.MatchString(name) && !neverRemove(name) && !contains(set, name) {
				match = append(match, name)
			}
		}
		sort.Strings(match)
		set = append(set, match...)
	}
	keep := map[string]string{}
	for _, n := range set {
		if why, ok := protect[n]; ok {
			keep[n] = why
		}
	}
	goes := func(n string) bool { return contains(set, n) && keep[n] == "" }
	for changed := true; changed; {
		changed = false
		for _, m := range set {
			if keep[m] != "" {
				continue
			}
			for _, user := range dependents(installed, m) {
				if !goes(user) {
					keep[m] = "needed by " + user
					changed = true
					break
				}
			}
		}
	}
	var remove []string
	for _, n := range set {
		if keep[n] == "" {
			remove = append(remove, n)
		}
	}
	return Plan{Remove: order(installed, remove), Keep: keep}
}

// dependents are the installed packages that need m, by its name or by one
// it provides, sorted.
func dependents(installed map[string]Package, m string) []string {
	want := append([]string{m}, installed[m].Provides...)
	var out []string
	for name, p := range installed {
		if name == m {
			continue
		}
		for _, d := range p.Depends {
			if contains(want, d) {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// order puts every package before what it needs among them, and otherwise
// keeps the list's order. A cycle (none is known) keeps the rest as listed.
func order(installed map[string]Package, remove []string) []string {
	left := append([]string(nil), remove...)
	var out []string
	for len(left) > 0 {
		picked := -1
		for i, m := range left {
			needed := false
			for _, user := range dependents(installed, m) {
				if user != m && contains(left, user) {
					needed = true
					break
				}
			}
			if !needed {
				picked = i
				break
			}
		}
		if picked < 0 {
			return append(out, left...)
		}
		out = append(out, left[picked])
		left = append(left[:picked], left[picked+1:]...)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// describe is a plan's kept packages for a log line: "tcping (needed by x)".
func describe(keep map[string]string) string {
	var out []string
	for n, why := range keep {
		out = append(out, fmt.Sprintf("%s (%s)", n, why))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
