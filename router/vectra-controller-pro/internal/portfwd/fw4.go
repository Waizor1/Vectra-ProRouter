package portfwd

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"vectra-controller-pro/internal/uci"
)

// SectionPrefix names vctl's own redirects: vectra_pf_<id>. A section of any
// other name is never changed by vctl.
const SectionPrefix = "vectra_pf_"

// NamePrefix starts the name of vctl's redirects, so LuCI's list tells them
// apart from the owner's own: "Vectra: <device name or address>", or
// "Vectra: <preset> → <device name or address>".
const NamePrefix = "Vectra: "

// Firewall is what the router's fw4 config says about port forwards.
type Firewall struct {
	// Own are vctl's redirects, as written (file order).
	Own []Rule
	// foreign are the other DNAT redirects from the wan zone, reserved the
	// ports the router accepts from the WAN for itself: both only count in
	// the conflict check.
	foreign  []foreign
	reserved []reserved
	// sections are the names of vctl's sections — every one, also one whose
	// name carries no valid id, so an apply removes it too.
	sections []string
	// srcZone / destZone are the zones a redirect is written from and to:
	// the one zone whose networks include the wan interface, and the one
	// with lan ("" when there is none, or more than one — then a rule is
	// refused rather than written to a zone fw4 would not know).
	srcZone, destZone string
}

// zoneOf is the name of the one zone whose networks include network; "" when
// no zone does, or several do.
func zoneOf(f *uci.File, network string) string {
	found := ""
	for _, z := range f.OfType("zone") {
		name := z.Get("name")
		if name == "" {
			continue
		}
		for _, n := range words(z, "network") {
			if n != network {
				continue
			}
			if found != "" && found != name {
				return ""
			}
			found = name
		}
	}
	return found
}

// sectionName is what uci accepts as a section's name; only such names are
// ever written into a batch.
var sectionName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// wanZones are the firewall zones the internet comes in by: a zone named wan
// or wan6, or one whose networks include the wan or wan6 interface (a zone
// renamed in LuCI keeps its networks). "wan" always counts.
func wanZones(f *uci.File) map[string]bool {
	out := map[string]bool{"wan": true, "wan6": true}
	for _, z := range f.OfType("zone") {
		name := z.Get("name")
		if name == "" {
			continue
		}
		for _, n := range words(z, "network") {
			if n == "wan" || n == "wan6" {
				out[name] = true
			}
		}
	}
	return out
}

// ParseFirewall reads /etc/config/firewall as uci parses it.
//
// Ports the router itself takes from the WAN are read from fw4 rules that
// accept a port from a wan zone for the router (no dest zone). A rule like
// that without dest_port, or a wan zone whose input policy is ACCEPT, opens
// every port of the router: which ones a service listens on is then not
// known from the config, and refusing every forward would make the feature
// useless on such a router, so they are not taken as claims. A forward does
// take its port from the router there (DNAT is decided before input); the
// owner opened the whole router to the internet by hand.
func ParseFirewall(f *uci.File) Firewall {
	fw := Firewall{srcZone: zoneOf(f, "wan"), destZone: zoneOf(f, "lan")}
	wan := wanZones(f)
	for _, s := range f.Sections {
		if strings.HasPrefix(s.Name, SectionPrefix) {
			// vctl's name, whatever its type: removed at every apply, so a
			// rule can be written under it as a redirect.
			if sectionName.MatchString(s.Name) {
				fw.sections = append(fw.sections, s.Name)
			}
			if s.Type != "redirect" {
				continue
			}
		}
		switch {
		case s.Type == "redirect" && strings.HasPrefix(s.Name, SectionPrefix):
			id := strings.TrimPrefix(s.Name, SectionPrefix)
			if !ValidID(id) {
				continue // not one vctl wrote; removed at the next apply
			}
			// Hand-edited in LuCI into what a rule cannot say (two ports, no
			// address, icmp): not read as one — the UI and Vectra Connect
			// get only the canonical form — and removed at the next apply.
			// "a:b" is fw4's spelling of a range and reads as "a-b".
			ports := fw4Ports(strings.Join(words(s, "src_dport"), " "))
			dest, derr := netip.ParseAddr(s.Get("dest_ip"))
			mask := fw4ProtoMask(strings.Join(words(s, "proto"), " "))
			if len(ports) != 1 || len(words(s, "src_dport")) != 1 || derr != nil || !dest.Is4() || mask == 0 {
				continue
			}
			var preset *string
			if p := s.Get("vectra_preset"); ValidPreset(p) {
				preset = &p
			}
			fw.Own = append(fw.Own, Rule{
				ID:      id,
				Preset:  preset,
				DestIP:  dest.String(),
				Port:    ports[0].String(),
				Proto:   ruleProto(mask),
				Direct:  s.Get("vectra_direct") == "1",
				Enabled: enabled(s),
			})
		case s.Type == "redirect":
			target := s.Get("target")
			if target == "" {
				target = "DNAT" // fw4's default for a redirect
			}
			if !strings.EqualFold(target, "DNAT") || !wan[s.Get("src")] {
				continue
			}
			ports := fw4Ports(strings.Join(words(s, "src_dport"), " "))
			if len(words(s, "src_dport")) == 0 {
				// No external port: fw4 forwards every port of the protocol.
				ports = []portRange{{1, 65535}}
			}
			name := s.Get("name")
			if name == "" {
				name = s.Ref()
			}
			fw.foreign = append(fw.foreign, foreign{name: name, proto: fw4ProtoMask(strings.Join(words(s, "proto"), " ")),
				ports: ports, enabled: enabled(s)})
		case s.Type == "rule":
			// An input rule (no dest zone) accepting from the wan zone opens a
			// port of the router itself to the internet. IPv6-only rules do
			// not meet an IPv4 DNAT.
			if !wan[s.Get("src")] || s.Get("dest") != "" || !strings.EqualFold(s.Get("target"), "ACCEPT") ||
				!enabled(s) || s.Get("family") == "ipv6" {
				continue
			}
			mask := fw4ProtoMask(strings.Join(words(s, "proto"), " "))
			if mask == 0 {
				continue
			}
			name := s.Get("name")
			if name == "" {
				name = s.Ref()
			}
			for _, r := range fw4Ports(strings.Join(words(s, "dest_port"), " ")) {
				fw.reserved = append(fw.reserved, reserved{name: name, proto: mask, ports: r})
			}
		}
	}
	return fw
}

// words are an option's values: an option's words, or a list's items.
func words(s uci.Section, name string) []string {
	var out []string
	if v, ok := s.Options[name]; ok {
		out = append(out, strings.Fields(v)...)
	}
	for _, v := range s.Lists[name] {
		out = append(out, strings.Fields(v)...)
	}
	return out
}

// enabled reads fw4's `enabled`: on unless it says otherwise.
func enabled(s uci.Section) bool {
	switch strings.ToLower(strings.TrimSpace(s.Get("enabled"))) {
	case "0", "false", "no", "off", "disabled":
		return false
	}
	return true
}

// fw4ProtoMask reads an fw4 proto value: no value is fw4's default "tcp udp";
// "all", "*" and "tcpudp" cover both; anything else (icmp, esp) neither.
func fw4ProtoMask(p string) int {
	ws := strings.Fields(strings.ToLower(p))
	if len(ws) == 0 {
		return maskTCP | maskUDP
	}
	m := 0
	for _, w := range ws {
		switch w {
		case "tcp", "6":
			m |= maskTCP
		case "udp", "17":
			m |= maskUDP
		case "all", "*", "tcpudp":
			m |= maskTCP | maskUDP
		}
	}
	return m
}

// ruleProto is a mask as a rule's protocol; one vctl wrote is always tcp,
// udp or both ("tcp udp").
func ruleProto(m int) string {
	switch m {
	case maskTCP:
		return ProtoTCP
	case maskUDP:
		return ProtoUDP
	}
	return ProtoBoth
}

// fw4Ports reads fw4 port values: words of "n", "a-b" or "a:b". Words it
// cannot read are left out — a conflict check that guessed would refuse a
// rule for a port nothing holds.
func fw4Ports(s string) []portRange {
	var out []portRange
	for _, w := range strings.Fields(s) {
		if strings.HasPrefix(w, "!") {
			continue // a negation is not a claim
		}
		if r, ok := parsePorts(strings.ReplaceAll(w, ":", "-")); ok {
			out = append(out, r)
		}
	}
	return out
}

// uciQuote quotes a value for a uci batch line the way uci's parser reads it
// (internal/uci.Statements mirrors it): inside single quotes nothing is
// special, and a single quote itself is closed, written escaped (\') and
// opened again. Values never hold a newline: every one is validated (or, for
// a device's name, made printable) before it gets here.
func uciQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// fw4Proto is a rule's protocol as fw4 takes it.
func fw4Proto(p string) string {
	if p == ProtoBoth {
		return "tcp udp"
	}
	return p
}

// FirewallBatch is the uci batch that makes vctl's redirects exactly rules:
// every vctl section the config has is deleted, and each rule is written
// anew — src_dport and dest_port the same port, from the zone of the wan
// interface to the zone of lan (fw.srcZone, fw.destZone: Apply refuses a
// rule when either is unknown). No other section is named,
// so nothing else of the firewall changes. rules must have passed Validate;
// names gives a destination's device name for the section's name (the
// address when it has none).
func FirewallBatch(fw Firewall, rules []Rule, names func(ip string) *string) string {
	var b strings.Builder
	for _, name := range fw.sections {
		fmt.Fprintf(&b, "delete firewall.%s\n", name)
	}
	for _, r := range rules {
		sec := "firewall." + SectionPrefix + r.ID
		fmt.Fprintf(&b, "set %s=redirect\n", sec)
		set := func(opt, val string) { fmt.Fprintf(&b, "set %s.%s=%s\n", sec, opt, uciQuote(val)) }
		who := r.DestIP
		if names != nil {
			if n := names(r.DestIP); n != nil {
				who = *n
			}
		}
		if r.Preset != nil {
			who = *r.Preset + " → " + who
		}
		set("name", NamePrefix+who)
		set("src", fw.srcZone)
		set("dest", fw.destZone)
		set("target", "DNAT")
		set("proto", fw4Proto(r.Proto))
		set("src_dport", r.Port)
		set("dest_ip", r.DestIP)
		set("dest_port", r.Port)
		set("enabled", boolOpt(r.Enabled))
		set("reflection", "1")
		if r.Direct {
			set("vectra_direct", "1")
		}
		if r.Preset != nil {
			set("vectra_preset", *r.Preset)
		}
	}
	return b.String()
}

func boolOpt(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
