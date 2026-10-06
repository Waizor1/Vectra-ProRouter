// Package portfwd is the router side of «Проброс портов»: the owner's port
// forwards, kept as native fw4 redirects, and the «past the VPN» mode of the
// device behind one.
//
// Why fw4 and not a table of vctl's own: a port forward has to work whether
// vctl runs or not, LuCI must show it, and fw4 already does DNAT, hairpin NAT
// (reflection) and the matching forward accept. So a rule is a `config
// redirect` section of /etc/config/firewall named vectra_pf_<id>, and vctl
// touches only those sections. Anybody else's redirects — made in LuCI, by
// PassWall, by hand — are never changed, and never shown either; they only
// count when two rules would claim the same port (port_conflict).
//
// The owner asked for it simple (06.10): a rule is a device, a port (or a
// range) that is the same outside and on the device, a protocol, «through the
// VPN or past it», on or off. Nothing else.
//
// Inbound through the VPN is not possible and nothing here pretends it is:
// the exits are the provider's, and they forward no port to us. A port
// forward reaches the router's own WAN address only; when that address is the
// provider's shared one or a private one (CGNAT), nothing from the internet
// reaches it, and the UI says so in one line.
//
// Everything that reaches the router's configuration is validated here first
// — the router is the authority, whatever the UI or Vectra Connect already
// checked — and it reaches uci only through a batch script whose every value
// is quoted (uciQuote), so a device's name is text and never a statement.
package portfwd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxRules is how many port forwards of its own the router keeps. A home
// router forwards a handful; the bound keeps the batch, the telemetry and the
// UI's list small.
const MaxRules = 32

// MaxDevices bounds the device list the UI picks a destination from.
const MaxDevices = 64

// The codes a change is refused with (ui/contract/README.md, "Port
// forwards"); the UI and Vectra Connect speak them in the person's language.
const (
	CodeInvalidParams = "invalid_params"
	CodeTooMany       = "too_many"
	CodeDestNotLAN    = "dest_not_lan"
	CodeDestIsRouter  = "dest_is_router"
	CodePortConflict  = "port_conflict"
	CodeBusy          = "busy"
	CodeApplyFailed   = "apply_failed"
	CodeInternal      = "internal"
)

// Error is a change the router refuses, by code. Detail is for a person
// reading logs, never shown as the UI's sentence.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func refuse(code, format string, a ...interface{}) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, a...)}
}

// Protocols a rule forwards.
const (
	ProtoTCP  = "tcp"
	ProtoUDP  = "udp"
	ProtoBoth = "both"
)

// Rule is one of the owner's port forwards, as the UI and Vectra Connect send
// it and as the router keeps it.
type Rule struct {
	// ID is 8 lowercase hex digits; the section is vectra_pf_<ID>. Empty in a
	// request = a new rule, and the router picks one.
	ID string `json:"id"`
	// Preset is the UI's tag for what the forward is for ("minecraft",
	// "playstation"), or nil. The UI owns the catalogue; the router only
	// checks the tag's form and keeps it (option vectra_preset).
	Preset *string `json:"preset"`
	DestIP string  `json:"destIp"`
	// Port is "25565" or a range "3478-3480": the router's WAN port and the
	// device's port alike.
	Port  string `json:"port"`
	Proto string `json:"proto"`
	// Direct: the device's own new connections go out by the kernel, «past
	// the VPN» (firewall.SetPortForwardDirect4) — FakeDNS addresses excepted.
	Direct  bool `json:"direct"`
	Enabled bool `json:"enabled"`
}

// foreign is a redirect of somebody else's: only its claim on ports is kept.
type foreign struct {
	name    string
	proto   int // protoMask
	ports   []portRange
	enabled bool
}

// reserved is a port the router itself takes from the WAN: an fw4 rule
// accepting it on the wan zone for the router (dropbear or uhttpd opened to
// the internet, the DHCP client's renewals). A DNAT on it would take those
// connections away from the router.
type reserved struct {
	name  string
	proto int
	ports portRange
}

// protoMask: which of tcp and udp a rule covers.
const (
	maskTCP = 1 << iota
	maskUDP
)

func ruleProtoMask(p string) int {
	switch p {
	case ProtoTCP:
		return maskTCP
	case ProtoUDP:
		return maskUDP
	case ProtoBoth:
		return maskTCP | maskUDP
	}
	return 0
}

// portRange is an inclusive port range; a single port has lo == hi.
type portRange struct{ lo, hi int }

func (r portRange) overlaps(o portRange) bool { return r.lo <= o.hi && o.lo <= r.hi }

func (r portRange) String() string {
	if r.lo == r.hi {
		return strconv.Itoa(r.lo)
	}
	return fmt.Sprintf("%d-%d", r.lo, r.hi)
}

var portSyntax = regexp.MustCompile(`^([0-9]{1,5})(?:-([0-9]{1,5}))?$`)

// parsePorts reads a rule's port: "n" or "a-b", 1-65535, a <= b. "a-a" is
// the port a.
func parsePorts(s string) (portRange, bool) {
	m := portSyntax.FindStringSubmatch(s)
	if m == nil {
		return portRange{}, false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	if lo < 1 || hi > 65535 || lo > hi {
		return portRange{}, false
	}
	return portRange{lo, hi}, true
}

var idSyntax = regexp.MustCompile(`^[0-9a-f]{8}$`)

var presetSyntax = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)

// ValidPreset reports whether p is a preset tag: 1-24 of a-z, 0-9 and -.
func ValidPreset(p string) bool { return presetSyntax.MatchString(p) }

// ValidID reports whether id is a rule id: 8 lowercase hex digits.
func ValidID(id string) bool { return idSyntax.MatchString(id) }

// NewID is a fresh rule id.
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on Linux
	}
	return hex.EncodeToString(b[:])
}

// CheckSyntax checks what a rule says on its own — not where it points, nor
// what it collides with (Validate): an id when it has one, a preset tag of
// the right form when it has one, a known protocol,
// a port or range within 1-65535, an IPv4 destination. It is what Vectra
// Connect's parser holds a request to before the router looks at its own
// configuration.
func CheckSyntax(r Rule) *Error {
	if r.ID != "" && !ValidID(r.ID) {
		return refuse(CodeInvalidParams, "id must be 8 lowercase hex digits")
	}
	if r.Preset != nil && !ValidPreset(*r.Preset) {
		return refuse(CodeInvalidParams, "preset must be 1-24 of a-z, 0-9 and -")
	}
	if ruleProtoMask(r.Proto) == 0 {
		return refuse(CodeInvalidParams, "proto must be tcp, udp or both")
	}
	if _, ok := parsePorts(r.Port); !ok {
		return refuse(CodeInvalidParams, "port must be a port or a range a-b within 1-65535")
	}
	if a, err := netip.ParseAddr(r.DestIP); err != nil || !a.Is4() {
		return refuse(CodeInvalidParams, "destIp must be an IPv4 address")
	}
	return nil
}

// LAN is where the router's LAN is: its IPv4 subnets (netifd's lan
// interface) and the router's own addresses in them.
type LAN struct {
	Subnets []netip.Prefix
	Router  []netip.Addr
}

// Context is everything a change is checked against besides itself.
type Context struct {
	LAN      LAN
	foreign  []foreign
	reserved []reserved
	// NewID picks the id of a new rule (NewID when nil; tests fix it).
	NewID func() string
}

// Validate checks the owner's whole list as it is to replace what the router
// has, and returns it as it will be kept: ids given to new rules, ports and
// addresses in their canonical form. The first refusal stops it.
//
// Two rules conflict when both are enabled and they share a protocol and a
// port: fw4 would install both DNATs and the first would win in silence.
// Disabled rules never conflict, so an owner can keep two devices for one
// port and switch between them. The same holds against the router's other
// redirects and the ports the router takes from the WAN itself.
func Validate(rules []Rule, c Context) ([]Rule, *Error) {
	if len(rules) > MaxRules {
		return nil, refuse(CodeTooMany, "%d rules; at most %d", len(rules), MaxRules)
	}
	newID := c.NewID
	if newID == nil {
		newID = NewID
	}
	out := make([]Rule, 0, len(rules))
	seen := map[string]bool{}
	for i, r := range rules {
		r.ID = strings.TrimSpace(r.ID)
		r.Proto = strings.ToLower(strings.TrimSpace(r.Proto))
		if err := CheckSyntax(r); err != nil {
			err.Detail = fmt.Sprintf("rules[%d]: %s", i, err.Detail)
			return nil, err
		}
		if r.ID == "" {
			for r.ID = newID(); seen[r.ID]; r.ID = newID() {
			}
		}
		if seen[r.ID] {
			return nil, refuse(CodeInvalidParams, "rules[%d]: id %s twice", i, r.ID)
		}
		seen[r.ID] = true
		ports, _ := parsePorts(r.Port)
		r.Port = ports.String()
		a := netip.MustParseAddr(r.DestIP)
		r.DestIP = a.String()
		if err := checkLAN(a, c.LAN); err != nil {
			err.Detail = fmt.Sprintf("rules[%d]: %s", i, err.Detail)
			return nil, err
		}
		out = append(out, r)
	}
	if err := checkConflicts(out, c.foreign, c.reserved); err != nil {
		return nil, err
	}
	return out, nil
}

// checkLAN: the destination is a host of the LAN — inside one of its
// subnets, not the router, not a subnet's network or broadcast address.
func checkLAN(a netip.Addr, lan LAN) *Error {
	if len(lan.Subnets) == 0 {
		return refuse(CodeDestNotLAN, "the LAN's subnet could not be read")
	}
	for _, r := range lan.Router {
		if r == a {
			return refuse(CodeDestIsRouter, "%s is the router itself", a)
		}
	}
	for _, p := range lan.Subnets {
		if !p.Contains(a) {
			continue
		}
		if p.Bits() <= 30 {
			if a == p.Masked().Addr() {
				return refuse(CodeDestNotLAN, "%s is the network address of %s", a, p)
			}
			if a == lastAddr(p) {
				return refuse(CodeDestNotLAN, "%s is the broadcast address of %s", a, p)
			}
		}
		return nil
	}
	return refuse(CodeDestNotLAN, "%s is outside the LAN", a)
}

// lastAddr is the last address of an IPv4 prefix (its broadcast address).
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= uint32(1)<<(32-p.Bits()) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func checkConflicts(own []Rule, others []foreign, res []reserved) *Error {
	type claim struct {
		who   string
		proto int
		ports portRange
	}
	var claims []claim
	for _, r := range own {
		if !r.Enabled {
			continue
		}
		ports, _ := parsePorts(r.Port)
		mine := claim{who: r.DestIP + " " + r.Proto + "/" + r.Port, proto: ruleProtoMask(r.Proto), ports: ports}
		for _, c := range claims {
			if c.proto&mine.proto != 0 && c.ports.overlaps(mine.ports) {
				return refuse(CodePortConflict, "%s and %s share a port", c.who, mine.who)
			}
		}
		for _, f := range others {
			if !f.enabled || f.proto&mine.proto == 0 {
				continue
			}
			for _, fr := range f.ports {
				if fr.overlaps(mine.ports) {
					return refuse(CodePortConflict, "%s and the router's redirect %q share port %s", mine.who, f.name, fr)
				}
			}
		}
		for _, s := range res {
			if s.proto&mine.proto != 0 && s.ports.overlaps(mine.ports) {
				return refuse(CodePortConflict, "%s takes port %s, which the router itself accepts from the WAN (%q)", mine.who, s.ports, s.name)
			}
		}
		claims = append(claims, mine)
	}
	return nil
}

// DirectAddrs are the destinations of the enabled rules «past the VPN»: what
// firewall.SetPortForwardDirect4 is to hold, each once, sorted.
func DirectAddrs(rules []Rule) []netip.Addr {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, r := range rules {
		if !r.Enabled || !r.Direct {
			continue
		}
		a, err := netip.ParseAddr(r.DestIP)
		if err != nil || !a.Is4() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}
