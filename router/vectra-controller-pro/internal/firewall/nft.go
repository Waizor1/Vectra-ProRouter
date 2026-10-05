// Package firewall emits an nftables ruleset for transparent-proxy TPROXY
// + fwmark + ip-rule/route plumbing. The renderer is OS-independent
// (produces nft script text); apply uses `nft -f -` and is Linux-only.
//
// Design notes:
//   - One table `inet vctl` owns everything; this avoids collisions with
//     any other tables on the router.
//   - Sets are named consistently so dnsmasq nftset hooks can populate them
//     (vctl_direct4 / vctl_direct6 / vctl_proxy4 / vctl_proxy6 / vctl_block).
//   - Reserved ports (DHCP, etc.) and local destinations bypass TPROXY.
//   - A "policy: accept" fallback means any rule typo doesn't black-hole the
//     router — operator must explicitly choose to drop.
package firewall

import (
	"bytes"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"vectra-controller-pro/internal/config"
)

// DefaultControlMark is the SO_MARK the controller stamps on its OWN
// control-plane sockets (vctl -> panel). The output chain returns on it before
// any other rule, so controller traffic is never routed by the provider's
// rules. Without this the router's own egress is tproxy'd into Xray: if the
// provider chain is down the panel becomes unreachable and the firewall
// commit-confirm deadman flaps the ruleset every 90s.
//
// 0x5643 is "VC" in ASCII — distinct from the tproxy mark (0x1 by default) and
// from anything OpenWrt/PassWall2 uses.
const DefaultControlMark = 0x5643

// Named counters attached to the three rules that decide whether the data plane
// is alive. They are objects (not anonymous counters) so they can be read by
// name without parsing rule text:
//
//	nft list counters table inet vctl
//
// What each one tells an operator — and why it exists:
//
//   - CounterTproxyHits stays at 0 when client traffic never reaches TPROXY.
//     That is exactly what a missing `vctl firewall routing` looks like from the
//     outside: rules applied, xray up, `xray -test` happy, zero packets carried.
//   - CounterEgressExempt counts xray's OWN outbound packets being returned on
//     their fwmark. 0 here while traffic flows means xray's sockets are not
//     carrying sockopt.mark.
//   - CounterOutputMarked counts local egress that fell through to the marking
//     rule. On a healthy router it barely moves; if it climbs while xray dials
//     out, xray's egress is being steered by the policy route back into its own
//     TPROXY socket — the loop that presents as "io: read/write on closed pipe".
//
// test/dataplane asserts on all three; they are the instrument that would have
// caught both shipped defects.
const (
	CounterTproxyHits   = "vctl_tproxy_hits"
	CounterEgressExempt = "vctl_egress_exempt"
	CounterOutputMarked = "vctl_output_marked"
)

// CounterKillSwitchDrops counts every tcp/udp packet the kill-switch refused
// to let out unproxied — at BOTH drop points (the prerouting fall-through and
// the forward guard). Everything else it drops is counted apart, in
// CounterUnproxiedOther. It is emitted only when Spec.KillSwitch is on.
//
// Reading it:
//
//	nft list counter inet vctl vctl_killswitch_drops
//
// Non-zero means client traffic WOULD have left the WAN in the clear and did
// not. It climbing steadily while clients complain of no internet is the
// signature of "xray is down and the kill-switch is holding" — which is the
// intended behaviour, not a firewall bug.
const CounterKillSwitchDrops = "vctl_killswitch_drops"

// CounterUnproxiedOther counts, at the same two points and with the same
// verdict as the kill-switch instrument, the packets that are NOT tcp/udp: a
// LAN client's ping, ICMP errors, GRE. TPROXY carries only tcp/udp, so these
// are never proxied, by design — counted in the instrument, every ping read as
// a leak (switch off) or as a kill-switch drop (switch on).
const CounterUnproxiedOther = "vctl_unproxied_other"

// CounterTproxyEscaped counts forwarded packets that carry the tproxy FwMark:
// TPROXY captured and accepted them, and then the kernel routed them out the
// WAN instead of into xray's socket, because the fwmark policy route is
// missing (leak path 2 in Spec.KillSwitch). That never happens by design.
// The rule has no verdict: the packet goes on to the forward guard's other
// rules, which leak it (switch off) or drop it (switch on) and count it there
// too.
const CounterTproxyEscaped = "vctl_tproxy_escaped"

// CounterKillSwitchShadow is the same instrument with the switch OFF: the exact
// same rules, at the exact same two points, ending in bare counters instead of
// `counter ... drop`. Nothing is dropped; the packets are counted and then
// leave exactly as they do today. Like CounterKillSwitchDrops it counts only
// tcp/udp; the rest goes to CounterUnproxiedOther.
//
// Reading it:
//
//	nft list counter inet vctl vctl_would_leak
//
// It exists because the OFF default was justified by "the guard counts what it
// would have dropped, so exposure is measurable per router before committing
// fleet-wide" — and that was not true as written. With the switch off there was
// no counter, no forward chain and a prerouting policy of accept, so measuring
// the exposure required first taking the risk being measured. With the shadow
// count the rollout gate is actually executable: turn nothing on, run a week,
// read this counter, then decide per router.
//
// A non-zero reading is the number of client tcp/udp packets that DID leave the
// WAN unproxied — the leak, quantified. Zero for a week is the evidence that
// enabling Spec.KillSwitch on that router costs nothing.
const CounterKillSwitchShadow = "vctl_would_leak"

// Spec is everything the renderer needs to emit a ruleset.
type Spec struct {
	TproxyPort int // Xray tproxy listen port
	FwMark     int // mark applied to tproxied packets
	CtlMark    int // mark on the controller's own control-plane sockets
	// SockMark is the mark on XRAY's own sockets (tproxy inbound replies and
	// every dialling outbound). It has to be distinct from FwMark, because the
	// fwmark policy route resolves to `local ... dev lo` and would loop Xray's
	// own packets straight back into TPROXY. Kept in lockstep with
	// config.DefaultXraySockMark, which is what the splice stamps.
	SockMark    int
	TableName   string // default "vctl"
	RtTable     int    // routing table id for fwmark route (default 100)
	IPv6Enabled bool
	// RefuseIPv6: the LAN's IPv6 to the outside is refused at once — a reset
	// for TCP, admin-prohibited for the rest — never captured nor let past.
	// The owner, 2026-09-30: «IPv6 заблокирован у нас на серверах — выключи
	// его и у нас». Captured, a connection over IPv6 is accepted by xray at
	// once and hangs at an exit that carries no IPv6: a page that never
	// loads, no fallback, since TCP connected. Refused, the device takes
	// IPv4. The LAN's own IPv6 (bypass6) is untouched. Needs IPv6Enabled.
	RefuseIPv6 bool
	// LANDevices: the router's LAN-side devices (the daemon's lanDevices).
	// Under RefuseIPv6, IPv6 forwarded INTO them — from the internet to a
	// LAN device, between the LAN's own networks — is left to fw4 as without
	// vctl; IPv6 to anywhere else is refused. With none known the refusal
	// stays whole: a device misread as the LAN's would let IPv6 out. xray
	// may not dial into them either (CounterLANDial; "br-lan" when none).
	LANDevices []string

	// KillSwitch makes forwarded LAN-client traffic fail CLOSED, so a client
	// packet can never leave the WAN unproxied while the user believes it is
	// proxied. It closes TWO distinct leak paths, which is why it is not just a
	// chain policy:
	//
	//  1. Fall-through. When xray is down the TPROXY socket lookup fails,
	//     nft_tproxy issues NFT_BREAK, and the rest of that rule (mark + accept)
	//     never runs. Evaluation continues to the terminal counted drops at the
	//     bottom of PREROUTING — tcp/udp counted in CounterKillSwitchDrops, the
	//     rest in CounterUnproxiedOther — which are emitted only in this mode.
	//     This is the path the data-plane stand caught: one 60-byte SYN,
	//     forwarded in the clear, in the two-hundred-millisecond window while the
	//     supervisor was restarting xray over the RSS soft cap.
	//
	//     The drop is those RULES and never the chain policy. Both prerouting and
	//     forward keep `policy accept`, armed or not.
	//
	//     That is load-bearing, not stylistic. In an nftables BASE chain
	//     `return` with an empty jump stack falls through to the chain POLICY —
	//     it does not accept. `policy drop` therefore turned every exemption in
	//     PREROUTING into a drop: the router's own inbound traffic
	//     (`fib daddr type local`), the LAN (`@bypass4`/`@bypass6`) and every
	//     split-DNS direct destination. Measured end to end on the data-plane
	//     stand (test/dataplane, MODE=killswitch): with the switch armed and
	//     xray healthy, client packets were tproxied and dialled out
	//     (vctl_egress_exempt +297) and not one reply survived prerouting;
	//     flipping those five verdicts to accept, and nothing else, carried the
	//     same request. It was invisible too — a policy drop increments no
	//     counter, so vctl_killswitch_drops read 0 throughout, which is the
	//     exact failure that counter exists to prevent.
	//
	//  2. Explicit accept. When the socket lookup SUCCEEDS the rule ends in
	//     `accept`, and delivery to xray then depends entirely on the fwmark
	//     policy route installed by `vctl firewall routing`. Those `ip rule`s are
	//     not part of the nft transaction and do not survive a netifd reload. If
	//     they are missing, the kernel routes the marked packet normally and
	//     forwards it out the WAN — with an explicit accept verdict, so the
	//     PREROUTING policy is never consulted and cannot see it. The ruleset
	//     looks perfect and vctl_tproxy_hits counts every leaking packet (the
	//     stand's no-routing mode measured 43,241 hits while carrying nothing).
	//
	// Path 2 is why the guard lives at the FORWARD hook: prerouting runs before
	// the routing decision, so `fib daddr type local` is only an approximation of
	// "this is not going out the WAN". By the time a packet reaches FORWARD the
	// kernel has already ruled it is not for us, so "was this proxied?" is
	// finally decidable. It also means the router's OWN traffic — the
	// controller->panel control plane, xray's egress, the local DNS service —
	// never traverses the guard at all (that is INPUT/OUTPUT), so the kill-switch
	// is structurally incapable of costing us remote management. The OUTPUT chain
	// stays `accept` unconditionally.
	//
	// Off by default: see the KillSwitch note on config.TproxyInbound for why
	// that is a deliberate choice and not an oversight.
	KillSwitch bool

	// Static bypass nets — typical LAN-side ranges that must NEVER be proxied.
	BypassV4 []string
	BypassV6 []string

	// Source bypass: don't proxy traffic FROM these addresses. Default: empty.
	SourceBypassV4 []string
	SourceBypassV6 []string

	// Hook into existing fakedns / split DNS: nftsets dnsmasq populates.
	DirectSetV4 string // dnsmasq's nftset name for "direct" domains, ipv4
	DirectSetV6 string
	ProxySetV4  string
	ProxySetV6  string

	// DirectCtMark, when set, makes the direct sets route in the kernel for
	// the life of a connection. A LAN connection whose FIRST packet is to an
	// address in DirectSetV4/V6 is marked with this conntrack bit and never
	// enters xray; every later packet of it follows the bit, not the set. So
	// the sets can be reloaded, emptied by a reprogram or changed under live
	// traffic: a connection xray already carries stays with xray (its packets
	// are not new), and one the kernel carries stays with the kernel. Tested
	// against the set instead, each change would move live connections from
	// one to the other mid-stream — and a TCP connection that changes hands
	// is reset. ("New" is conntrack's: a UDP flow nothing has answered yet is
	// still new, and can move — QUIC rides that out.)
	//
	// The sets hold what the routing sends straight out by address anyway
	// (xray.DirectBypass): PassWall2 keeps them out of xray the same way, and
	// every connection xray does not carry is ~38 KB of its heap and two
	// sockets the router does not spend.
	DirectCtMark uint32

	// GuardPorts are loopback TCP ports only root may reach, and xray's own
	// sockets never: xray's gRPC API and metrics endpoint, which listen on
	// 127.0.0.1 WITHOUT authentication, and the exit probe — an HTTP proxy
	// onto every exit whose password anyone can read (internal/coreengine/
	// xray, exit_check.go).
	//
	// Loopback is not a boundary on its own. (1) Not everything on an OpenWrt
	// router is root — dnsmasq runs as its own user and parses LAN input. (2) A
	// LAN client can make xray dial loopback for it: sniffing turns
	// "Host: 127.0.0.1" into the destination, the provider routes
	// geoip:private to DIRECT, and freedom connects to the router's own
	// 127.0.0.1:10086 — /debug/vars and pprof, handed to the LAN. Every socket
	// xray dials carries SockMark, so (2) is decidable here; (1) by uid.
	GuardPorts []int

	// Peak load (1111, 2026-09-30): a torrent client's tracker storm through
	// xray to one node got the router cut off from that node, and its peers —
	// connections the provider sends DIRECT anyway — carried through xray took
	// the router to 15 MB of free memory: two sockets and their buffers each.
	//
	// P2PBypass: a LAN device opening new connections to non-web ports faster
	// than a person does (a torrent client, Delivery Optimization) is a P2P
	// host for five minutes, and its connections to non-web ports go out by
	// the kernel (DirectCtMark), not through xray. Web ports, push and chat
	// (5222-5223, 5228), STUN and Discord's voice range stay with xray: the
	// services a VPN is for. Needs DirectCtMark.
	P2PBypass bool
	// AdmitRate/AdmitBurst: a device's new connections per second into xray,
	// and the burst it may open at once. More wait at the door — dropped
	// before xray; TCP asks again in a second — so a storm is spread over
	// time instead of choking xray, the node and the connection table.
	// 0 = off.
	AdmitRate, AdmitBurst int
	// AdmitTotalRate/AdmitTotalBurst: new connections per second into xray
	// from every device together, and the burst — the router's whole door,
	// sized to what its CPU carries (each is a Reality handshake to a node).
	// Checked after each device's own door, so a device held there spends
	// none of it. 0 = off (the default: set from what the router measures).
	AdmitTotalRate, AdmitTotalBurst int
	// PaceRate/PaceBurst: xray's own new TCP connections per second to one
	// address and port (a node), and the burst. More are held back by the
	// kernel (the SYN dropped here is sent again): hundreds of handshakes a
	// second to one foreign address is what got it cut off. DNS is not
	// paced. 0 = off.
	PaceRate, PaceBurst int
	// DNSRate/DNSBurst: a device's DNS queries per second to the router's
	// resolver, and the burst (r35). Every query dnsmasq forwards is a session
	// in xray (DNSRedirectPort): 100 unique names a second from one device
	// grew xray by 2 MB a second on 1111. More are dropped at the router's
	// door — the device's resolver asks again. Only the LAN's (@bypass4/6)
	// are counted: never the router's own lookups, never the internet's.
	// TCP counts its connections. 0 = off.
	DNSRate, DNSBurst int

	// DNSRedirectPort, when set, sends the router's resolver through the
	// tunnel: dnsmasq's upstream queries — the ones it sends as DNSResolverUIDs
	// to a public address on port 53 — are redirected to xray's loopback DNS
	// inbound (internal/coreengine/xray, dns_steer.go). dnsmasq still answers
	// the LAN and keeps its local names and its cache; only where it asks
	// changes. Over IPv6 its upstream queries are refused instead (the inbound
	// is IPv4), so it asks its IPv4 servers.
	//
	// Measured on the test router: the ISP's filter forges NXDOMAIN for
	// Instagram and YouTube in flight, for queries to 8.8.8.8 and 1.1.1.1 as
	// much as to its own resolver. Asked over the open path, those names never
	// resolve, whatever the tunnel carries.
	//
	// It is part of this table on purpose. Whatever takes the data plane down
	// — the commit-confirm deadman, the rescue's direct mode, a stop, the
	// cron dead-man — takes the redirect with it, and dnsmasq asks its own
	// servers again. The caller sets it only when the running render HAS that
	// inbound (xray.RenderDNSListen): redirected to a closed port, every
	// lookup on the router would fail.
	DNSRedirectPort int
	// DNSResolverUIDs are the uids dnsmasq runs as. Root is never one: xray
	// and vctl are root, and their own lookups must not come back here.
	DNSResolverUIDs []int
	// CarriedV4 are ranges taken out of BypassV4 on purpose: FakeDNS pools,
	// whose addresses stand for domains only xray knows (PassWall-compatible
	// routing). Informational: BypassV4 already lacks them.
	CarriedV4 []string
	// DNSRejectV6 refuses dnsmasq's IPv6 upstream queries while it steers,
	// so it asks its IPv4 servers (redirected). Set only when it has one: with
	// IPv6 servers alone, refusing them would leave the router without DNS.
	DNSRejectV6 bool
	// DNSUpstreamV4 are the WAN's own IPv4 resolvers (netifd's resolv.conf.auto)
	// that sit in a bypass4 range — a provider's box in front of the router
	// hands out its LAN address (192.168.x.1) as the resolver. The redirect
	// above skips private addresses, so dnsmasq kept asking that one over the
	// open path, beside the redirected public ones, and cached whichever
	// answered first: measured on artem-lutfulin 2026-10-05 (r19), the
	// ISP's forged NXDOMAIN for www.instagram.com and real addresses for
	// youtube and chatgpt, mixed with FakeDNS, changing minute to minute.
	// These are redirected too; a server= of the owner's on the LAN is not
	// in resolv.conf.auto and is asked as before.
	DNSUpstreamV4 []string
	// DNSUpstreamV6: the same over IPv6 (a link-local or ULA resolver on the
	// WAN), refused with the public ones while DNSRejectV6 holds.
	DNSUpstreamV6 []string
	// HijackDNS answers the LAN's DNS queries to PUBLIC resolvers — a TV or a
	// phone with 8.8.8.8 set by hand — with the router's own resolver: from a
	// LAN source (@bypass4, @bypass6) to port 53 of a public address that is
	// not the router's, redirected to the router's port 53 (chain dns_hijack;
	// the prerouting chain lets them past TPROXY for it). PassWall2 did the same
	// (dns_redirect). Without it such a query is ordinary traffic to xray, the
	// routing sends it out over the open path, and the ISP forges the answer
	// for the names it blocks — the tunnel carries the site, but the name never
	// becomes its address. The caller sets it only while something on the
	// router answers on port 53: redirected to nothing, those devices would
	// lose DNS altogether.
	HijackDNS bool
}

// CounterDNSRedirected counts dnsmasq's upstream queries sent into the tunnel.
const CounterDNSRedirected = "vctl_dns_redirected"

// CounterDNSHijacked counts the LAN's queries to other resolvers answered by
// the router's own (Spec.HijackDNS): one per query over UDP, one per
// connection over TCP.
const CounterDNSHijacked = "vctl_dns_hijacked"

// steersDNS: the spec redirects dnsmasq's upstream (see DNSRedirectPort).
func (s Spec) steersDNS() bool {
	if s.DNSRedirectPort < 1 || s.DNSRedirectPort > 65535 || len(s.DNSResolverUIDs) == 0 {
		return false
	}
	for _, u := range s.DNSResolverUIDs {
		if u <= 0 {
			return false
		}
	}
	return true
}

// CounterLocalGuard counts connections the loopback guard refused.
const CounterLocalGuard = "vctl_local_guard"

// CounterInboundGuard counts packets dropped on their way into xray's TPROXY
// port that TPROXY did not put there: sent to the router's own address on
// that port (from the LAN, from the router itself, or from the WAN past
// fw4). xray's tproxy inbound listens on 0.0.0.0 — a TPROXY socket must, to
// take traffic for every address — and dokodemo-door with followRedirect
// takes such a connection's destination, the router itself, for the one to
// dial: xray proxies into itself, over and over, until it runs out of file
// descriptors. One connection was enough.
const CounterInboundGuard = "vctl_inbound_guard"

// CounterLANDial counts xray's own dials into the router's LAN that were
// dropped. xray never has a reason to: the LAN is bypassed before TPROXY, so
// nothing the router carries is for it. A dial there is a LAN client's
// "Host: 192.168.1.1" sniffed into a destination, or a provider document
// sending a connection there — xray as a door into the LAN. xray's answers
// to the LAN's own connections leave from the address the client asked for
// (TPROXY keeps it), never from the router's, so they are not this.
const CounterLANDial = "vctl_lan_dial"

// DefaultDirectCtMark is the conntrack bit of connections the kernel routes
// straight out (Spec.DirectCtMark): high, clear of the fwmark vctl stamps as
// a whole ct mark on the router's own flows, of mwan3's 0x3f00 and of the low
// bits qosify and fw4 use.
const DefaultDirectCtMark = 0x10000000

// CounterDirectNew counts LAN connections routed straight out by the kernel
// (Spec.DirectCtMark): one packet per connection, its first.
const CounterDirectNew = "vctl_direct_new"

// Load guards (Spec.P2PBypass, AdmitRate, PaceRate): a P2P host's
// connections sent out by the kernel, a device's new connections held at the
// door, xray's dials to one node held back.
const (
	CounterP2PDirect = "vctl_p2p_direct"
	CounterAdmitHeld = "vctl_admit_held"
	// CounterAdmitTotalHeld counts new connections held at the router's
	// whole door (Spec.AdmitTotalRate).
	CounterAdmitTotalHeld = "vctl_admit_total_held"
	CounterPaced          = "vctl_paced"
	// CounterDNSHeld counts the DNS queries a device sent over its rate
	// (Spec.DNSRate).
	CounterDNSHeld = "vctl_dns_held"
	// CounterIPv6Refused counts the LAN's IPv6 refused (Spec.RefuseIPv6).
	CounterIPv6Refused = "vctl_ipv6_refused"
	// A device is let less than a node takes, so one device can never fill a
	// node's bucket: the watchdog's confirm and the rescue's probes dial the
	// same node (review of r23).
	defaultAdmitRate  = 15
	defaultAdmitBurst = 60
	defaultPaceRate   = 40
	defaultPaceBurst  = 160
	// A device asks A, AAAA and HTTPS for every name: a heavy page is a few
	// hundred queries at once, a restored browser session a thousand and more,
	// and a Pi-hole asks for a whole network. Only a storm waits.
	defaultDNSRate  = 100
	defaultDNSBurst = 2000
)

// DefaultSpec returns a sensible baseline matching the project's fleet contour.
func DefaultSpec(tproxyPort, fwmark int) Spec {
	return Spec{
		TproxyPort:  tproxyPort,
		FwMark:      fwmark,
		CtlMark:     DefaultControlMark,
		SockMark:    config.DefaultXraySockMark,
		TableName:   "vctl",
		RtTable:     100,
		IPv6Enabled: true,
		BypassV4: []string{
			"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
			"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
			"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
			"203.0.113.0/24", "224.0.0.0/3",
		},
		BypassV6: []string{
			"::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
		},
		DirectSetV4:  "vctl_direct4",
		DirectSetV6:  "vctl_direct6",
		ProxySetV4:   "vctl_proxy4",
		ProxySetV6:   "vctl_proxy6",
		DirectCtMark: DefaultDirectCtMark,
		P2PBypass:    true,
		AdmitRate:    defaultAdmitRate,
		AdmitBurst:   defaultAdmitBurst,
		PaceRate:     defaultPaceRate,
		PaceBurst:    defaultPaceBurst,
		DNSRate:      defaultDNSRate,
		DNSBurst:     defaultDNSBurst,
		// xray's API and metrics (xray.DefaultAPIListen / DefaultMetricsListen;
		// cmd/vctl tests keep the two in lockstep).
		GuardPorts: []int{10085, 10086, 10087},
	}
}

// Block form: the whole table is declared in a single transaction. nft -f -
// runs everything as one netlink batch — atomic replace, no race window where
// rules are missing.
const tmplText = `# Generated by vctl — do not edit by hand.
# table: {{ .TableName }} (inet)
# tproxy port: {{ .TproxyPort }}, fwmark: 0x{{ printf "%x" .FwMark }}

# REPLACE the table, never add to it. Declared into a table that exists, the
# block below ADDS: every chain gets its rules appended again and nothing is
# ever removed — a reprogram left the old rules in force behind the new ones
# (a DNS redirect a render no longer serves among them). Declaring, deleting
# and declaring again is one nft transaction: the old table and the new one
# are never both, or neither, in force.
table inet {{ .TableName }}
{{- if .DirectSetV4 }}
# The direct sets are flushed first: nft reads every interval set it is not
# told is being emptied back from the kernel before it applies a script like
# this one, and a loaded direct set is 15k ranges — 24 MB of nft, 3 MB with
# the flush (measured). vctl loads them again after the table is in.
add set inet {{ .TableName }} {{ .DirectSetV4 }} { type ipv4_addr; flags interval; auto-merge; }
flush set inet {{ .TableName }} {{ .DirectSetV4 }}
{{- end }}
{{- if and .IPv6Enabled .DirectSetV6 }}
add set inet {{ .TableName }} {{ .DirectSetV6 }} { type ipv6_addr; flags interval; auto-merge; }
flush set inet {{ .TableName }} {{ .DirectSetV6 }}
{{- end }}
delete table inet {{ .TableName }}

table inet {{ .TableName }} {
  counter {{ .CounterTproxyHits }} { }
  counter {{ .CounterEgressExempt }} { }
  counter {{ .CounterOutputMarked }} { }
  counter {{ .KillCounter }} { }
  counter {{ .CounterUnproxiedOther }} { }
{{- if .FwMark }}
  counter {{ .CounterTproxyEscaped }} { }
{{- end }}
{{- if .GuardPorts }}
  counter {{ .CounterLocalGuard }} { }
{{- end }}
{{- if .TproxyPort }}
  counter {{ .CounterInboundGuard }} { }
{{- end }}
{{- if .SockMark }}
  counter {{ .CounterLANDial }} { }
{{- end }}
{{- if .SteersDNS }}
  counter {{ .CounterDNSRedirected }} { }
{{- end }}
{{- if .HijackDNS }}
  counter {{ .CounterDNSHijacked }} { }
{{- end }}
{{- if .DirectCtMark }}
  counter {{ .CounterDirectNew }} { }
{{- end }}
{{- if .P2P }}
  counter {{ .CounterP2PDirect }} { }
  set vctl_p2p4 { type ipv4_addr; flags dynamic, timeout; timeout 5m; size 1024; }
  set vctl_p2p_rate4 { type ipv4_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- if .IPv6Enabled }}
  set vctl_p2p6 { type ipv6_addr; flags dynamic, timeout; timeout 5m; size 1024; }
  set vctl_p2p_rate6 { type ipv6_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- end }}
{{- end }}
{{- if .RefuseV6 }}
  counter {{ .CounterIPv6Refused }} { }
{{- end }}
{{- if .AdmitRate }}
  counter {{ .CounterAdmitHeld }} { }
  set vctl_admit4 { type ipv4_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- if .IPv6Enabled }}
  set vctl_admit6 { type ipv6_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- end }}
{{- end }}
{{- if .AdmitTotalRate }}
  counter {{ .CounterAdmitTotalHeld }} { }
{{- end }}
{{- if .DNSRate }}
  counter {{ .CounterDNSHeld }} { }
  set vctl_dns4 { type ipv4_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- if .IPv6Enabled }}
  set vctl_dns6 { type ipv6_addr; flags dynamic, timeout; timeout 1m; size 1024; }
{{- end }}
{{- end }}
{{- if and .PaceRate .SockMark }}
  counter {{ .CounterPaced }} { }
  set vctl_pace4 { type ipv4_addr . inet_service; flags dynamic, timeout; timeout 30s; size 1024; }
{{- if .IPv6Enabled }}
  set vctl_pace6 { type ipv6_addr . inet_service; flags dynamic, timeout; timeout 30s; size 1024; }
{{- end }}
{{- end }}

  set {{ .DirectSetV4 }} { type ipv4_addr; flags interval; auto-merge; }
  set {{ .ProxySetV4 }}  { type ipv4_addr; flags interval; auto-merge; }
{{- if .IPv6Enabled }}
  set {{ .DirectSetV6 }} { type ipv6_addr; flags interval; auto-merge; }
  set {{ .ProxySetV6 }}  { type ipv6_addr; flags interval; auto-merge; }
{{- end }}

  set bypass4 { type ipv4_addr; flags interval; auto-merge;
    elements = { {{ join .BypassV4 ", " }} }; }
{{- if .IPv6Enabled }}
  set bypass6 { type ipv6_addr; flags interval; auto-merge;
    elements = { {{ join .BypassV6 ", " }} }; }
{{- end }}

  # The policy is accept in BOTH modes. The kill-switch's verdict is the counted
  # drops at the bottom of this chain, the last of which matches unconditionally,
  # so nothing can fall past them to the policy.
  #
  # This is not a softening. In an nftables BASE chain, "return" with an empty
  # jump stack does NOT accept — nft_do_chain falls through to the chain policy.
  # Under "policy drop" every exemption below (the router's own address, the LAN,
  # the split-DNS direct sets) therefore became an UNCOUNTED drop. See the
  # KillSwitch doc comment in nft.go for the measurement.
  chain prerouting {
    type filter hook prerouting priority mangle; policy accept;
    fib daddr type { local, broadcast, multicast } return
    # REPLIES ARE NOT THE LAN'S TRAFFIC. A packet in a connection's reply
    # direction answers something that came from outside: a port forward's
    # server answering the WAN, a LAN host answering inbound IPv6. Every
    # connection xray carries is opened by its client (original direction),
    # and xray answers from the router itself (OUTPUT), so nothing here is
    # xray's. Captured, a forwarded server's SYN-ACK reached xray's listener,
    # which reset it: every port forward on the router was dead.
    ct direction reply return
{{- if .HijackDNS }}
    # The LAN's queries to other resolvers are the router's to answer
    # (Spec.HijackDNS): past TPROXY here, redirected in dns_hijack — a new
    # flow, and later the flows it redirected. A flow older than the hijack
    # (never redirected) stays with TPROXY, as it was: let past, it would go
    # out unproxied, or into the kill switch.
    meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ct state new return
    meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ct status dnat return
{{- if .IPv6Enabled }}
    meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ct state new return
    meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ct status dnat return
{{- end }}
{{- end }}
    ip  daddr @bypass4 return
{{- if .IPv6Enabled }}
    ip6 daddr @bypass6 return
{{- end }}
{{- if .RefuseV6 }}
    # The LAN's IPv6 to the outside is refused in forward (Spec.RefuseIPv6):
    # never captured, never marked direct.
    meta nfproto ipv6 return
{{- end }}
{{- if .DirectCtMark }}
    # Straight out by the kernel, for the whole connection (Spec.DirectCtMark):
    # decided on its first packet, followed by its conntrack bit after that.
    ct mark and 0x{{ printf "%x" .DirectCtMark }} == 0x{{ printf "%x" .DirectCtMark }} return
    ct state new ip  daddr @{{ .DirectSetV4 }} ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterDirectNew }}" return
{{- if .IPv6Enabled }}
    ct state new ip6 daddr @{{ .DirectSetV6 }} ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterDirectNew }}" return
{{- end }}
{{- else }}
    ip  daddr @{{ .DirectSetV4 }} return
{{- if .IPv6Enabled }}
    ip6 daddr @{{ .DirectSetV6 }} return
{{- end }}
{{- end }}
{{- if .P2P }}
    # A P2P host's peers go out by the kernel (Spec.P2PBypass): its new
    # connections to non-web ports while it is one, and the connections that
    # show it is one — faster than a person opens them. A connection is judged
    # on its first packet only (a UDP flow is "new" until it is answered, and
    # must never change hands mid-stream), and never the router's own (lo).
    iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    # UDP 50000-65535 stays with xray for voice; a known P2P host's uTP SYN
    # (0x41) or DHT message ("d1:") on those ports is a peer, not a call.
    iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto udp udp dport 50000-65535 @th,64,8 0x41 update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto udp udp dport 50000-65535 @th,64,24 0x64313a update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p_rate4 { ip saddr limit rate over 4/second burst 24 packets } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p_rate4 { ip saddr limit rate over 4/second burst 24 packets } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
{{- if .IPv6Enabled }}
    iif != "lo" ct state new ct original packets 1 ip6 saddr @vctl_p2p6 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 ip6 saddr @vctl_p2p6 meta l4proto udp udp dport 50000-65535 @th,64,8 0x41 update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 ip6 saddr @vctl_p2p6 meta l4proto udp udp dport 50000-65535 @th,64,24 0x64313a update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 ip6 saddr @vctl_p2p6 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p_rate6 { ip6 saddr limit rate over 4/second burst 24 packets } update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
    iif != "lo" ct state new ct original packets 1 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p_rate6 { ip6 saddr limit rate over 4/second burst 24 packets } update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x{{ printf "%x" .DirectCtMark }} counter name "{{ .CounterP2PDirect }}" return
{{- end }}
{{- end }}
{{- if .AdmitRate }}
    # A device's storm waits at the door (Spec.AdmitRate).
    iif != "lo" ct state new ct original packets 1 meta l4proto { tcp, udp } update @vctl_admit4 { ip saddr limit rate over {{ .AdmitRate }}/second burst {{ .AdmitBurst }} packets } counter name "{{ .CounterAdmitHeld }}" drop
{{- if .IPv6Enabled }}
    iif != "lo" ct state new ct original packets 1 meta l4proto { tcp, udp } update @vctl_admit6 { ip6 saddr limit rate over {{ .AdmitRate }}/second burst {{ .AdmitBurst }} packets } counter name "{{ .CounterAdmitHeld }}" drop
{{- end }}
{{- end }}
{{- if .AdmitTotalRate }}
    # Every device together (Spec.AdmitTotalRate): one rule for both
    # families, so they share the rate.
    iif != "lo" ct state new ct original packets 1 meta l4proto { tcp, udp } limit rate over {{ .AdmitTotalRate }}/second burst {{ .AdmitTotalBurst }} packets counter name "{{ .CounterAdmitTotalHeld }}" drop
{{- end }}
    meta l4proto { tcp, udp } counter name "{{ .CounterTproxyHits }}" tproxy to :{{ .TproxyPort }} meta mark set 0x{{ printf "%x" .FwMark }} accept
{{- if .KillSwitch }}
    # Fail CLOSED, and COUNTED. Anything reaching here was not captured by
    # TPROXY: xray is down (the socket lookup NFT_BREAKs out of the rule above,
    # skipping its mark+accept) or the packet is not tcp/udp. These two rules
    # ARE the kill-switch's drop — the second matches unconditionally, so
    # nothing can fall past them to the policy — and every dropped packet lands
    # in a counter instead of an invisible policy decision that presents to the
    # operator as "the internet is broken and nothing moved". tcp/udp and the
    # rest are counted apart: TPROXY never carries anything but tcp/udp, so a
    # LAN ping landing here is by design, not a sign of trouble.
{{- else }}
    # Shadow count (kill-switch OFF). Same rules, same place, no verdict: the
    # packet is counted and then falls through to the chain's accept policy,
    # exactly as it does without these rules. The first measures the
    # fall-through leak — tcp/udp TPROXY did not capture because xray is down;
    # the second what is never proxied by design (not tcp/udp).
{{- end }}
    meta l4proto { tcp, udp } counter name "{{ .KillCounter }}"{{ if .KillSwitch }} drop{{ end }}
    counter name "{{ .CounterUnproxiedOther }}"{{ if .KillSwitch }} drop{{ end }}
  }

  # Kill-switch guard. The PREROUTING policy alone does NOT close the leak: the
  # TPROXY rule above ends in an explicit accept, so a packet that WAS captured
  # but is then routed normally — because the fwmark policy route is missing
  # ("vctl firewall routing" skipped or failed, or "ip rule" flushed by a netifd
  # reload) — never reaches that policy and is forwarded out the WAN in the
  # clear. FORWARD is the first hook where the kernel has already decided the
  # packet is not for us, so it is the only place "this was not proxied" is
  # decidable.
  #
  # This chain CANNOT strand the router: locally-generated traffic (controller
  # -> panel, xray's egress, the router's own DNS answers) traverses
  # INPUT/OUTPUT and never enters FORWARD. The two mark returns below are
  # defence in depth, not load-bearing.
  #
  # The tproxy FwMark is deliberately NOT exempt here. A forwarded packet
  # carrying it is precisely leak path 2 above; returning on it would re-open
  # the hole this chain exists to close.
  #
  # priority mangle (-150) puts the verdict ahead of fw4's forward chain
  # (filter, 0), so the kill-switch decides first and deterministically.
{{- if not .KillSwitch }}
  #
  # THE SWITCH IS OFF, so this chain is emitted in SHADOW form: byte-for-byte
  # the same exemptions, with the terminal drops replaced by bare counters. It
  # issues no verdict, so every packet continues to the accept policy and the
  # forwarding behaviour is identical to having no chain at all. The counters
  # are the point: they make the leak measurable per router BEFORE anyone takes
  # the risk of failing closed. See CounterKillSwitchShadow.
{{- end }}
  chain forward {
    type filter hook forward priority mangle; policy accept;
{{- if .CtlMark }}
    meta mark 0x{{ printf "%x" .CtlMark }} return
{{- end }}
{{- if .SockMark }}
    meta mark 0x{{ printf "%x" .SockMark }} return
{{- end }}
    fib daddr type { local, broadcast, multicast } return
    # Answers to connections from outside (see prerouting): a port forward's
    # server answering the WAN is not the LAN's traffic leaking.
    ct direction reply return
    udp dport { 67, 68, 546, 547 } return
    ip  daddr @bypass4 return
{{- if .IPv6Enabled }}
    ip6 daddr @bypass6 return
{{- end }}
{{- if .RefuseV6 }}
{{- if .LANDevs }}
    # Into the LAN's own devices (Spec.LANDevices) — from the internet, or
    # between the LAN's networks — is fw4's to judge, as without vctl.
    meta nfproto ipv6 oifname { {{ .LANDevs }} } return
{{- end }}
    # Refused at once (Spec.RefuseIPv6): the device takes IPv4 — before the
    # direct returns below, so no IPv6 leaves by the kernel either.
    meta nfproto ipv6 meta l4proto tcp counter name "{{ .CounterIPv6Refused }}" reject with tcp reset
    meta nfproto ipv6 counter name "{{ .CounterIPv6Refused }}" reject with icmpv6 type admin-prohibited
{{- end }}
{{- if .DirectCtMark }}
    ct mark and 0x{{ printf "%x" .DirectCtMark }} == 0x{{ printf "%x" .DirectCtMark }} return
{{- end }}
    ip  daddr @{{ .DirectSetV4 }} return
{{- if .IPv6Enabled }}
    ip6 daddr @{{ .DirectSetV6 }} return
{{- end }}
{{- if .FwMark }}
    # Captured by TPROXY — it carries the tproxy mark — and still forwarded:
    # leak path 2, the fwmark policy route is missing. Counted, no verdict; the
    # rules below decide, as for anything else that got this far.
    meta mark 0x{{ printf "%x" .FwMark }} counter name "{{ .CounterTproxyEscaped }}"
{{- end }}
    meta l4proto { tcp, udp } counter name "{{ .KillCounter }}"{{ if .KillSwitch }} drop{{ end }}
    counter name "{{ .CounterUnproxiedOther }}"{{ if .KillSwitch }} drop{{ end }}
  }

  chain output {
    type route hook output priority mangle; policy accept;
    # FIRST rule: the controller's own control-plane sockets carry this mark
    # (SO_MARK, see internal/controlplane). Returning here keeps vctl->panel
    # traffic off the provider's routing, so a dead provider chain can never
    # cost us remote management.
{{- if .CtlMark }}
    meta mark 0x{{ printf "%x" .CtlMark }} return
{{- end }}
{{- if .SockMark }}
    # Xray's OWN sockets (tproxy inbound replies + every dialling outbound).
    # They must escape before the marking rule at the bottom, and they must NOT
    # carry FwMark: the fwmark policy route resolves to "local 0.0.0.0/0 dev lo"
    # and would loop them straight back into TPROXY.
    meta mark 0x{{ printf "%x" .SockMark }} counter name "{{ .CounterEgressExempt }}" return
{{- end }}
    meta mark 0x{{ printf "%x" .FwMark }} return
    # REPLIES ARE NOT EGRESS. Anything the router sends on a connection that
    # already exists is an answer to something that came IN — a LAN client
    # talking to SSH, LuCI, dnsmasq — and must go back the way it arrived. Only
    # connections the router ORIGINATES (ct state new) are its own traffic and
    # belong on the provider's routing.
    #
    # Without this the marking rule at the bottom stamps those replies with the
    # tproxy FwMark, and the fwmark policy route resolves that to
    # "local ::/0 dev lo": the answer never leaves the box.
    #
    # It only ever showed on IPv6. Every RFC1918 LAN is inside @bypass4, so a v4
    # reply returns two rules below and the bug is invisible; @bypass6 covers
    # only ::1/128, fc00::/7, fe80::/10 and ff00::/8, so a LAN with
    # ISP-DELEGATED GLOBAL addresses — the normal case when the ISP delegates a
    # prefix — is not covered and the router silently stops answering its own
    # clients over v6. Measured on the data-plane stand (MODE=ipv6-killswitch):
    # a LAN client's request reached a service on the router's v6 address and
    # not one reply came back, with vctl_output_marked +7 and
    # vctl_killswitch_drops 0 — the marking rule, not the kill switch.
    ct direction reply return
    # A flow the ROUTER opened goes into xray with every packet, not only its
    # first: the marking rule below tags the connection (ct mark) along with
    # the packet, and this sends the rest of it the same way. Returning on
    # "established" alone let only the SYN reach xray — it answered as the
    # origin — while the ACK and the data left by the WAN to the real origin,
    # which reset them: every connection the router opened failed at once,
    # the daemon's own health probes among them (measured on the data-plane
    # stand, one curl from the router, packet by packet).
    ct mark 0x{{ printf "%x" .FwMark }} meta mark set 0x{{ printf "%x" .FwMark }} return
    # Older flows, not ours — open before this table was loaded: untouched.
    ct state established,related return
    # The router's OWN DNS and NTP go out directly, never through the proxy.
    # dnsmasq answers the whole LAN from its upstream, vctl finds its panel by
    # name through dnsmasq, and TLS needs a clock NTP has set: none of that may
    # wait for a proxy that is starting, still probing its balancers, or down.
    # Measured on the test router: the provider's catch-all rule sent UDP 53
    # to its balancer, the first minute's lookups timed out, the check-in with
    # them, and the commit-confirm deadman took the whole data plane back down.
    # A LAN client's own DNS is not this: it is forwarded, and TPROXY takes it.
    udp dport { 53, 123 } return
    tcp dport 53 return
    fib daddr type { local, broadcast, multicast } return
    ip  daddr @bypass4 return
{{- if .IPv6Enabled }}
    ip6 daddr @bypass6 return
{{- end }}
    ip  daddr @{{ .DirectSetV4 }} return
{{- if .IPv6Enabled }}
    ip6 daddr @{{ .DirectSetV6 }} return
{{- end }}
    meta l4proto { tcp, udp } counter name "{{ .CounterOutputMarked }}" meta mark set 0x{{ printf "%x" .FwMark }} ct mark set 0x{{ printf "%x" .FwMark }}
  }
{{- if and .PaceRate .SockMark }}

  # xray's own dials to one node are paced (Spec.PaceRate). Only the SYN of
  # a new connection, only xray's sockets, never DNS.
  chain pace {
    type filter hook output priority filter; policy accept;
    meta mark 0x{{ printf "%x" .SockMark }} meta l4proto tcp tcp flags & (syn | ack) == syn tcp dport != 53 update @vctl_pace4 { ip daddr . tcp dport limit rate over {{ .PaceRate }}/second burst {{ .PaceBurst }} packets } counter name "{{ .CounterPaced }}" drop
{{- if .IPv6Enabled }}
    meta mark 0x{{ printf "%x" .SockMark }} meta l4proto tcp tcp flags & (syn | ack) == syn tcp dport != 53 update @vctl_pace6 { ip6 daddr . tcp dport limit rate over {{ .PaceRate }}/second burst {{ .PaceBurst }} packets } counter name "{{ .CounterPaced }}" drop
{{- end }}
  }
{{- end }}
{{- if .GuardPorts }}

  # The loopback guard (see Spec.GuardPorts). A filter chain of its own, so it
  # decides nothing about routing: it only refuses xray's own sockets and
  # non-root processes on xray's unauthenticated API and metrics ports.
  chain local_guard {
    type filter hook output priority filter; policy accept;
{{- if .SockMark }}
    meta mark 0x{{ printf "%x" .SockMark }} ip daddr 127.0.0.0/8 tcp dport { {{ joinInts .GuardPorts ", " }} } counter name "{{ .CounterLocalGuard }}" reject with tcp reset
{{- if .IPv6Enabled }}
    meta mark 0x{{ printf "%x" .SockMark }} ip6 daddr ::1 tcp dport { {{ joinInts .GuardPorts ", " }} } counter name "{{ .CounterLocalGuard }}" reject with tcp reset
{{- end }}
{{- end }}
    oifname "lo" tcp dport { {{ joinInts .GuardPorts ", " }} } meta skuid != 0 counter name "{{ .CounterLocalGuard }}" reject with tcp reset
  }
{{- end }}
{{- if .TproxyPort }}

  # The inbound guard (see CounterInboundGuard). What TPROXY delivers keeps
  # the address it was sent to — never the router's own — and carries the
  # tproxy mark; a connection to the router's own address on the TPROXY port
  # is someone speaking to xray's listener directly, and is dropped. Ahead of
  # fw4's input chain, so the LAN's accept there never reaches it. Only a
  # connection's original direction: an answer to one of the router's own
  # sockets whose local port happens to be the TPROXY port (dnsmasq asks
  # from random ports) is not one.
  chain inbound_guard {
    type filter hook input priority filter - 10; policy accept;
    ct direction original meta l4proto { tcp, udp } th dport {{ .TproxyPort }} fib daddr type local meta mark != 0x{{ printf "%x" .FwMark }} counter name "{{ .CounterInboundGuard }}" drop
  }
{{- end }}
{{- if .SockMark }}

  # The LAN egress guard (see CounterLANDial): xray's own sockets carry
  # SockMark; one that leaves from the router's own address into a LAN-side
  # device is xray dialling into the LAN, and is dropped. Its answers to the
  # LAN's connections leave from the address the client asked for, which is
  # not the router's, and pass.
  chain lan_egress_guard {
    type filter hook output priority filter; policy accept;
    meta mark 0x{{ printf "%x" .SockMark }} oifname { {{ .EgressLANDevs }} } fib saddr type local counter name "{{ .CounterLANDial }}" drop
  }
{{- end }}
{{- if .DNSRate }}

  # A device's DNS storm waits at the router's door (see Spec.DNSRate): its
  # queries to the router's resolver, and those the hijack sends there. Ahead
  # of fw4's input chain; the router's own lookups (lo) and the internet's
  # (not @bypass) are never counted.
  chain dns_guard {
    type filter hook input priority filter - 5; policy accept;
    iif != "lo" ip saddr @bypass4 udp dport 53 update @vctl_dns4 { ip saddr limit rate over {{ .DNSRate }}/second burst {{ .DNSBurst }} packets } counter name "{{ .CounterDNSHeld }}" drop
    iif != "lo" ip saddr @bypass4 tcp dport 53 tcp flags & (syn | ack) == syn update @vctl_dns4 { ip saddr limit rate over {{ .DNSRate }}/second burst {{ .DNSBurst }} packets } counter name "{{ .CounterDNSHeld }}" drop
{{- if .IPv6Enabled }}
    iif != "lo" ip6 saddr @bypass6 udp dport 53 update @vctl_dns6 { ip6 saddr limit rate over {{ .DNSRate }}/second burst {{ .DNSBurst }} packets } counter name "{{ .CounterDNSHeld }}" drop
    iif != "lo" ip6 saddr @bypass6 tcp dport 53 tcp flags & (syn | ack) == syn update @vctl_dns6 { ip6 saddr limit rate over {{ .DNSRate }}/second burst {{ .DNSBurst }} packets } counter name "{{ .CounterDNSHeld }}" drop
{{- end }}
  }
{{- end }}
{{- if .SteersDNS }}

  # The router's resolver asks through the tunnel (see Spec.DNSRedirectPort).
  # Only dnsmasq's own upstream queries, by uid, and only to public addresses:
  # a server= on the LAN or on loopback is asked as before. The LAN's queries
  # to dnsmasq itself are local and never pass here.
  chain dns_steer {
    type nat hook output priority dstnat; policy accept;
    meta skuid { {{ joinInts .DNSResolverUIDs ", " }} } ip daddr != @bypass4 meta l4proto { tcp, udp } th dport 53 counter name "{{ .CounterDNSRedirected }}" redirect to :{{ .DNSRedirectPort }}
{{- if .SteerUpstreamV4 }}
    # The WAN's own resolvers, at private addresses too (Spec.DNSUpstreamV4).
    meta skuid { {{ joinInts .DNSResolverUIDs ", " }} } ip daddr { {{ join .SteerUpstreamV4 ", " }} } meta l4proto { tcp, udp } th dport 53 counter name "{{ .CounterDNSRedirected }}" redirect to :{{ .DNSRedirectPort }}
{{- end }}
  }
{{- if and .IPv6Enabled .DNSRejectV6 }}

  # Over IPv6 the inbound is not there to redirect to: refused, so dnsmasq
  # asks its IPv4 servers, which are redirected above.
  chain dns_steer6 {
    type filter hook output priority filter; policy accept;
    meta skuid { {{ joinInts .DNSResolverUIDs ", " }} } ip6 daddr != @bypass6 meta l4proto { tcp, udp } th dport 53 reject
{{- if .SteerUpstreamV6 }}
    meta skuid { {{ joinInts .DNSResolverUIDs ", " }} } ip6 daddr { {{ join .SteerUpstreamV6 ", " }} } meta l4proto { tcp, udp } th dport 53 reject
{{- end }}
  }
{{- end }}
{{- end }}
{{- if .HijackDNS }}

  # The LAN's DNS queries to public resolvers go to the router's own (see
  # Spec.HijackDNS). Not those to the router itself, which reach dnsmasq as
  # they are, and not those to a private address: a resolver of the LAN's own
  # (a Pi-hole another subnet asks through the router) keeps its clients.
  # Ahead of fw4's dstnat chain, so a port forward's DNAT never meets a query
  # this has already taken.
  chain dns_hijack {
    type nat hook prerouting priority dstnat - 5; policy accept;
    meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ip daddr != @bypass4 fib daddr type != local counter name "{{ .CounterDNSHijacked }}" redirect to :53
{{- if .IPv6Enabled }}
    meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ip6 daddr != @bypass6 fib daddr type != local counter name "{{ .CounterDNSHijacked }}" redirect to :53
{{- end }}
  }
{{- end }}
}

# Routing companion commands (run separately by 'vctl firewall routing'):
#   ip rule add fwmark 0x{{ printf "%x" .FwMark }} lookup {{ .RtTable }} pref 100
#   ip route add local 0.0.0.0/0 dev lo table {{ .RtTable }}
{{- if .IPv6Enabled }}
#   ip -6 rule add fwmark 0x{{ printf "%x" .FwMark }} lookup {{ .RtTable }} pref 100
#   ip -6 route add local ::/0 dev lo table {{ .RtTable }}
{{- end }}
`

// tmplData is Spec plus the counter object names, so the template can name the
// counters without them becoming operator-settable Spec fields (the names are a
// stable contract: scripts and the data-plane stand read them).
type tmplData struct {
	Spec
	CounterTproxyHits     string
	CounterEgressExempt   string
	CounterOutputMarked   string
	CounterLocalGuard     string
	CounterUnproxiedOther string
	CounterTproxyEscaped  string
	CounterDNSRedirected  string
	CounterDNSHijacked    string
	CounterDirectNew      string
	CounterP2PDirect      string
	CounterAdmitHeld      string
	CounterAdmitTotalHeld string
	CounterPaced          string
	CounterDNSHeld        string
	CounterIPv6Refused    string
	CounterInboundGuard   string
	CounterLANDial        string
	// EgressLANDevs: the LAN-side devices xray may not dial into, quoted for
	// an nft set — Spec.LANDevices, or "br-lan" when none is known.
	EgressLANDevs string
	// P2P: Spec.P2PBypass is in force — it rides DirectCtMark.
	P2P bool
	// RefuseV6: Spec.RefuseIPv6 is in force (it needs IPv6Enabled).
	RefuseV6 bool
	// LANDevs: Spec.LANDevices quoted for an nft set ("" when none).
	LANDevs string
	// SteersDNS: Spec.DNSRedirectPort is in force (Spec.steersDNS).
	SteersDNS bool
	// SteerUpstreamV4/V6: Spec.DNSUpstreamV4/V6, each a plain address of its
	// family (steerAddrs) — nothing else reaches the script.
	SteerUpstreamV4, SteerUpstreamV6 []string
	// KillCounter is the kill-switch instrument's name for THIS spec:
	// CounterKillSwitchDrops when the switch is on, CounterKillSwitchShadow when
	// it is off. Two names rather than one because a counter that means "packets
	// dropped" and a counter that means "packets that leaked" must never be
	// confused when read off a router, and because `nft list counter inet vctl
	// vctl_killswitch_drops` returning a number is then proof the switch is on.
	KillCounter string
}

// Render returns the nft script text for the given spec.
func Render(s Spec) (string, error) {
	if s.TableName == "" {
		s.TableName = "vctl"
	}
	if s.RtTable == 0 {
		s.RtTable = 100
	}
	t := template.Must(template.New("nft").Funcs(template.FuncMap{
		"join": strings.Join,
		"joinInts": func(xs []int, sep string) string {
			out := make([]string, len(xs))
			for i, x := range xs {
				out[i] = strconv.Itoa(x)
			}
			return strings.Join(out, sep)
		},
	}).Parse(tmplText))
	var buf bytes.Buffer
	killCounter := CounterKillSwitchShadow
	if s.KillSwitch {
		killCounter = CounterKillSwitchDrops
	}
	data := tmplData{
		Spec:                  s,
		CounterTproxyHits:     CounterTproxyHits,
		CounterEgressExempt:   CounterEgressExempt,
		CounterOutputMarked:   CounterOutputMarked,
		CounterLocalGuard:     CounterLocalGuard,
		CounterUnproxiedOther: CounterUnproxiedOther,
		CounterTproxyEscaped:  CounterTproxyEscaped,
		CounterDNSRedirected:  CounterDNSRedirected,
		CounterDNSHijacked:    CounterDNSHijacked,
		CounterDirectNew:      CounterDirectNew,
		CounterP2PDirect:      CounterP2PDirect,
		CounterAdmitHeld:      CounterAdmitHeld,
		CounterAdmitTotalHeld: CounterAdmitTotalHeld,
		CounterPaced:          CounterPaced,
		CounterDNSHeld:        CounterDNSHeld,
		CounterIPv6Refused:    CounterIPv6Refused,
		CounterInboundGuard:   CounterInboundGuard,
		CounterLANDial:        CounterLANDial,
		EgressLANDevs:         egressLANDevices(s.LANDevices),
		P2P:                   s.P2PBypass && s.DirectCtMark != 0 && !s.KillSwitch,
		RefuseV6:              s.RefuseIPv6 && s.IPv6Enabled,
		LANDevs:               quotedDevices(s.LANDevices),
		SteersDNS:             s.steersDNS(),
		SteerUpstreamV4:       steerAddrs(s.DNSUpstreamV4, false),
		SteerUpstreamV6:       steerAddrs(s.DNSUpstreamV6, true),
		KillCounter:           killCounter,
	}
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// steerAddrs are the resolver addresses of one family, each once, as net.IP
// prints them; loopback, unspecified and anything unparsable left out (a
// resolver on the router itself is not the WAN's).
func steerAddrs(addrs []string, v6 bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		ip := net.ParseIP(strings.TrimSpace(a))
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || (ip.To4() == nil) != v6 {
			continue
		}
		if k := ip.String(); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// RoutingCommands returns the ip rule/route commands needed alongside nft.
func RoutingCommands(s Spec) []string {
	cmds := []string{
		fmt.Sprintf("ip rule add fwmark 0x%x lookup %d pref 100", s.FwMark, s.RtTable),
		fmt.Sprintf("ip route add local 0.0.0.0/0 dev lo table %d", s.RtTable),
	}
	if s.IPv6Enabled {
		cmds = append(cmds,
			fmt.Sprintf("ip -6 rule add fwmark 0x%x lookup %d pref 100", s.FwMark, s.RtTable),
			fmt.Sprintf("ip -6 route add local ::/0 dev lo table %d", s.RtTable),
		)
	}
	return cmds
}

// RevertCommands returns the commands to undo what we set up. Operator runs these
// (or our --apply tooling does) to clean up an applied ruleset on shutdown.
func RevertCommands(s Spec) []string {
	cmds := []string{
		"nft flush table inet " + s.TableName,
		"nft delete table inet " + s.TableName,
		fmt.Sprintf("ip rule del fwmark 0x%x lookup %d", s.FwMark, s.RtTable),
		fmt.Sprintf("ip route del local 0.0.0.0/0 dev lo table %d", s.RtTable),
	}
	if s.IPv6Enabled {
		cmds = append(cmds,
			fmt.Sprintf("ip -6 rule del fwmark 0x%x lookup %d", s.FwMark, s.RtTable),
			fmt.Sprintf("ip -6 route del local ::/0 dev lo table %d", s.RtTable),
		)
	}
	return cmds
}

// quotedDevices is the devices safe for an nft set, quoted and joined; one a
// Linux interface name cannot be is dropped.
func quotedDevices(devs []string) string {
	var q []string
	for _, d := range devs {
		if !ifaceName.MatchString(d) {
			continue
		}
		q = append(q, `"`+d+`"`)
	}
	return strings.Join(q, ", ")
}

// egressLANDevices is the LAN egress guard's devices: the LAN's as netifd
// reports them, or the LAN bridge every OpenWrt router has. Never a guess
// wider than that — a WAN in the set would cut xray off from everything.
func egressLANDevices(devs []string) string {
	if q := quotedDevices(devs); q != "" {
		return q
	}
	return `"br-lan"`
}

var ifaceName = regexp.MustCompile(`^[A-Za-z0-9._@:+-]{1,15}$`)
