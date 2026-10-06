package portfwd

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/uci"
)

var homeLAN = LAN{
	Subnets: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")},
	Router:  []netip.Addr{netip.MustParseAddr("192.168.1.1")},
}

func rule(id, proto, port, ip string) Rule {
	return Rule{ID: id, Proto: proto, Port: port, DestIP: ip, Enabled: true}
}

func off(r Rule) Rule { r.Enabled = false; return r }

func withPreset(r Rule, p string) Rule { r.Preset = &p; return r }

func counterIDs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("%08x", 0xaaaa0000+n) }
}

func TestParsePorts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"25565", "25565", true}, {"1", "1", true}, {"65535", "65535", true},
		{"3478-3480", "3478-3480", true}, {"80-80", "80", true},
		{"0", "", false}, {"65536", "", false}, {"100-99", "", false}, {"1-65536", "", false},
		{"", "", false}, {" 80", "", false}, {"80 ", "", false}, {"80:90", "", false},
		{"-80", "", false}, {"80-", "", false}, {"abc", "", false}, {"123456", "", false},
		{"80,443", "", false}, {"+80", "", false}, {"80\n", "", false},
	} {
		r, ok := parsePorts(tc.in)
		if ok != tc.ok || (ok && r.String() != tc.want) {
			t.Errorf("parsePorts(%q) = %v %v, want %q %v", tc.in, r, ok, tc.want, tc.ok)
		}
	}
}

func TestValidateRefusesByCode(t *testing.T) {
	ctx := Context{LAN: homeLAN, NewID: counterIDs()}
	many := make([]Rule, MaxRules+1)
	for i := range many {
		many[i] = rule("", "tcp", fmt.Sprint(1000+i), "192.168.1.50")
	}
	for _, tc := range []struct {
		name  string
		rules []Rule
		code  string
	}{
		{"too many", many, CodeTooMany},
		{"bad id", []Rule{rule("XYZ", "tcp", "80", "192.168.1.50")}, CodeInvalidParams},
		{"upper-case id", []Rule{rule("ABCDEF12", "tcp", "80", "192.168.1.50")}, CodeInvalidParams},
		{"bad proto", []Rule{rule("", "icmp", "80", "192.168.1.50")}, CodeInvalidParams},
		{"preset with a space", []Rule{withPreset(rule("", "tcp", "80", "192.168.1.50"), "mine craft")}, CodeInvalidParams},
		{"preset upper-case", []Rule{withPreset(rule("", "tcp", "80", "192.168.1.50"), "Minecraft")}, CodeInvalidParams},
		{"preset too long", []Rule{withPreset(rule("", "tcp", "80", "192.168.1.50"), strings.Repeat("a", 25))}, CodeInvalidParams},
		{"preset empty", []Rule{withPreset(rule("", "tcp", "80", "192.168.1.50"), "")}, CodeInvalidParams},
		{"preset with a quote", []Rule{withPreset(rule("", "tcp", "80", "192.168.1.50"), "x'y")}, CodeInvalidParams},
		{"the old spelling", []Rule{rule("", "tcp+udp", "80", "192.168.1.50")}, CodeInvalidParams},
		{"bad port", []Rule{rule("", "tcp", "0", "192.168.1.50")}, CodeInvalidParams},
		{"a port with a statement", []Rule{rule("", "tcp", "80'\nset firewall.x=y", "192.168.1.50")}, CodeInvalidParams},
		{"ipv6 dest", []Rule{rule("", "tcp", "80", "fd00::5")}, CodeInvalidParams},
		{"mapped dest", []Rule{rule("", "tcp", "80", "::ffff:192.168.1.50")}, CodeInvalidParams},
		{"hostname dest", []Rule{rule("", "tcp", "80", "nas.lan")}, CodeInvalidParams},
		{"duplicate ids", []Rule{rule("0000000a", "tcp", "80", "192.168.1.50"), rule("0000000a", "tcp", "81", "192.168.1.50")}, CodeInvalidParams},
		{"outside the LAN", []Rule{rule("", "tcp", "80", "10.0.0.5")}, CodeDestNotLAN},
		{"public dest", []Rule{rule("", "tcp", "80", "8.8.8.8")}, CodeDestNotLAN},
		{"network address", []Rule{rule("", "tcp", "80", "192.168.1.0")}, CodeDestNotLAN},
		{"broadcast address", []Rule{rule("", "tcp", "80", "192.168.1.255")}, CodeDestNotLAN},
		{"the router", []Rule{rule("", "tcp", "80", "192.168.1.1")}, CodeDestIsRouter},
		{"own conflict", []Rule{rule("", "tcp", "8000-8010", "192.168.1.50"), rule("", "both", "8010", "192.168.1.60")}, CodePortConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.rules, ctx)
			if err == nil || err.Code != tc.code {
				t.Fatalf("Validate = %v, want %s", err, tc.code)
			}
		})
	}
	if _, err := Validate(nil, Context{}); err != nil {
		t.Errorf("an empty list (every forward removed) must be accepted even when the LAN is unknown: %v", err)
	}
	if _, err := Validate([]Rule{rule("", "tcp", "80", "192.168.1.50")}, Context{}); err == nil || err.Code != CodeDestNotLAN {
		t.Errorf("an unknown LAN must refuse, not guess: %v", err)
	}
}

func TestValidateKeepsTheCanonicalForm(t *testing.T) {
	ids := []string{"0000000b", "0000000b", "0000000c"} // a collision is picked again
	next := 0
	ctx := Context{LAN: homeLAN, NewID: func() string { next++; return ids[next-1] }}
	in := []Rule{
		{ID: "0000000b", Proto: " TCP ", Port: "25565-25565", DestIP: "192.168.1.50", Enabled: true},
		withPreset(Rule{Proto: "both", Port: "3478-3480", DestIP: "192.168.1.60", Enabled: true, Direct: true}, "some-future-game-2"),
	}
	got, err := Validate(in, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{ID: "0000000b", Proto: "tcp", Port: "25565", DestIP: "192.168.1.50", Enabled: true},
		withPreset(Rule{ID: "0000000c", Proto: "both", Port: "3478-3480", DestIP: "192.168.1.60", Enabled: true, Direct: true}, "some-future-game-2"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestConflicts(t *testing.T) {
	others := []foreign{
		{name: "Allow-NAS", proto: maskTCP, ports: []portRange{{5000, 5000}}, enabled: true},
		{name: "Old", proto: maskTCP | maskUDP, ports: []portRange{{6000, 6000}}, enabled: false},
		{name: "Games", proto: maskUDP, ports: []portRange{{7000, 7010}, {7100, 7100}}, enabled: true},
	}
	res := []reserved{{name: "Allow-DHCP-Renew", proto: maskUDP, ports: portRange{68, 68}}, {name: "ssh-wan", proto: maskTCP, ports: portRange{22, 22}}}
	ctx := Context{LAN: homeLAN, foreign: others, reserved: res}
	for _, tc := range []struct {
		name     string
		rules    []Rule
		conflict bool
	}{
		{"tcp beside udp", []Rule{rule("00000001", "tcp", "9000", "192.168.1.50"), rule("00000002", "udp", "9000", "192.168.1.60")}, false},
		{"both over udp", []Rule{rule("00000001", "both", "9000", "192.168.1.50"), rule("00000002", "udp", "9000", "192.168.1.60")}, true},
		{"ranges touching", []Rule{rule("00000001", "tcp", "9000-9009", "192.168.1.50"), rule("00000002", "tcp", "9010-9019", "192.168.1.60")}, false},
		{"ranges overlapping", []Rule{rule("00000001", "tcp", "9000-9010", "192.168.1.50"), rule("00000002", "tcp", "9010-9019", "192.168.1.60")}, true},
		{"one disabled", []Rule{rule("00000001", "tcp", "9000", "192.168.1.50"), off(rule("00000002", "tcp", "9000", "192.168.1.60"))}, false},
		{"a foreign redirect's port", []Rule{rule("00000001", "tcp", "5000", "192.168.1.50")}, true},
		{"a foreign redirect's other proto", []Rule{rule("00000001", "udp", "5000", "192.168.1.50")}, false},
		{"a disabled foreign redirect", []Rule{rule("00000001", "tcp", "6000", "192.168.1.50")}, false},
		{"a foreign redirect's second range", []Rule{rule("00000001", "udp", "7100", "192.168.1.50")}, true},
		{"inside a foreign range", []Rule{rule("00000001", "both", "7005", "192.168.1.50")}, true},
		{"the router's DHCP renewals", []Rule{rule("00000001", "udp", "60-70", "192.168.1.50")}, true},
		{"ssh opened on the wan", []Rule{rule("00000001", "tcp", "22", "192.168.1.50")}, true},
		{"ssh over udp", []Rule{rule("00000001", "udp", "22", "192.168.1.50")}, false},
		{"a disabled rule on a reserved port", []Rule{off(rule("00000001", "tcp", "22", "192.168.1.50"))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.rules, ctx)
			if tc.conflict {
				if err == nil || err.Code != CodePortConflict {
					t.Fatalf("Validate = %v, want port_conflict", err)
				}
			} else if err != nil {
				t.Fatalf("Validate = %v, want accepted", err)
			}
		})
	}
}

func TestCheckLANEdges(t *testing.T) {
	lan := LAN{Subnets: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/30"), netip.MustParsePrefix("172.16.5.4/31"), netip.MustParsePrefix("192.168.8.0/22")},
		Router: []netip.Addr{netip.MustParseAddr("192.168.8.1")}}
	for ip, code := range map[string]string{
		"10.0.0.1": "", "10.0.0.2": "", "10.0.0.0": CodeDestNotLAN, "10.0.0.3": CodeDestNotLAN,
		"172.16.5.4": "", "172.16.5.5": "", // a /31 has no network or broadcast address
		"192.168.9.255": "", "192.168.11.255": CodeDestNotLAN, "192.168.8.0": CodeDestNotLAN,
		"192.168.8.1": CodeDestIsRouter, "192.168.12.1": CodeDestNotLAN,
	} {
		err := checkLAN(netip.MustParseAddr(ip), lan)
		if (code == "" && err != nil) || (code != "" && (err == nil || err.Code != code)) {
			t.Errorf("checkLAN(%s) = %v, want %q", ip, err, code)
		}
	}
}

const fw4Config = `
config defaults
	option input 'REJECT'

config rule
	option name 'Allow-DHCP-Renew'
	option src 'wan'
	option proto 'udp'
	option dest_port '68'
	option target 'ACCEPT'
	option family 'ipv4'

config rule
	option name 'Allow-DHCPv6'
	option src 'wan'
	option proto 'udp'
	option dest_port '546'
	option family 'ipv6'
	option target 'ACCEPT'

config rule
	option name 'Allow-ISAKMP'
	option src 'wan'
	option dest 'lan'
	option dest_port '500'
	option proto 'udp'
	option target 'ACCEPT'

config rule
	option name 'ssh-wan'
	option src 'wan'
	option dest_port '22 2222'
	option target 'ACCEPT'

config rule
	option name 'off'
	option src 'wan'
	option dest_port '8443'
	option target 'ACCEPT'
	option enabled '0'

config redirect
	option name 'Allow-NAS'
	option src 'wan'
	option src_dport '5000'
	option dest 'lan'
	option dest_ip '192.168.1.10'
	option dest_port '5000'
	option proto 'tcp'

config redirect
	option target 'SNAT'
	option src 'lan'
	option src_dport '6000'

config redirect 'vectra_pf_3fa1c09e'
	option name 'Vectra: minecraft → gaming-pc'
	option vectra_preset 'minecraft'
	option src 'wan'
	option dest 'lan'
	option target 'DNAT'
	option proto 'tcp'
	option src_dport '25565'
	option dest_ip '192.168.1.50'
	option dest_port '25565'
	option enabled '1'
	option reflection '1'

config redirect 'vectra_pf_b7d204aa'
	option name 'Vectra: 192.168.1.60'
	option src 'wan'
	option dest 'lan'
	option target 'DNAT'
	option proto 'tcp udp'
	option src_dport '3478-3480'
	option dest_ip '192.168.1.60'
	option dest_port '3478-3480'
	option enabled '0'
	option reflection '1'
	option vectra_direct '1'
	option vectra_preset 'Not A Tag'

config redirect 'vectra_pf_junk'
	option src 'wan'

config redirect
	option src 'wan'
	option dest_ip '192.168.1.20'
	list proto 'udp'
	option src_dport '7000:7010'
`

func parseFW(t *testing.T) Firewall {
	t.Helper()
	f, err := uci.Parse(fw4Config)
	if err != nil {
		t.Fatal(err)
	}
	return ParseFirewall(f)
}

var minecraft = "minecraft"

func TestParseFirewall(t *testing.T) {
	fw := parseFW(t)
	wantOwn := []Rule{
		{ID: "3fa1c09e", Preset: &minecraft, Proto: "tcp", Port: "25565", DestIP: "192.168.1.50", Enabled: true},
		{ID: "b7d204aa", Proto: "both", Port: "3478-3480", DestIP: "192.168.1.60", Direct: true}, // a malformed tag is none
	}
	if !reflect.DeepEqual(fw.Own, wantOwn) {
		t.Errorf("own = %+v", fw.Own)
	}
	wantForeign := []foreign{
		{name: "Allow-NAS", proto: maskTCP, ports: []portRange{{5000, 5000}}, enabled: true},
		{name: "@redirect[5]", proto: maskUDP, ports: []portRange{{7000, 7010}}, enabled: true},
	}
	if !reflect.DeepEqual(fw.foreign, wantForeign) {
		t.Errorf("foreign = %+v", fw.foreign)
	}
	wantReserved := []reserved{
		{name: "Allow-DHCP-Renew", proto: maskUDP, ports: portRange{68, 68}},
		{name: "ssh-wan", proto: maskTCP | maskUDP, ports: portRange{22, 22}},
		{name: "ssh-wan", proto: maskTCP | maskUDP, ports: portRange{2222, 2222}},
	}
	if !reflect.DeepEqual(fw.reserved, wantReserved) {
		t.Errorf("reserved = %+v", fw.reserved)
	}
	if !reflect.DeepEqual(fw.sections, []string{"vectra_pf_3fa1c09e", "vectra_pf_b7d204aa", "vectra_pf_junk"}) {
		t.Errorf("sections = %v", fw.sections)
	}
	// A foreign redirect without an external port claims them all.
	f, _ := uci.Parse("config redirect\n\toption src 'wan'\n\toption dest_ip '192.168.1.9'\n")
	if got := ParseFirewall(f).foreign; len(got) != 1 || got[0].ports[0] != (portRange{1, 65535}) {
		t.Errorf("no src_dport = %+v", got)
	}
}

// The batch deletes every vctl section and writes the rules anew, the same
// port outside and on the device; it names no other section, and every value
// — a hostile device name above all — reads back as exactly one word through
// uci's own quoting rules.
func TestFirewallBatch(t *testing.T) {
	fw := parseFW(t)
	hostile := `x' ; delete firewall.@defaults[0] ; set firewall.y='z \"q\" \\ $(reboot)`
	rules := []Rule{
		{ID: "3fa1c09e", Preset: &minecraft, Proto: "tcp", Port: "25565", DestIP: "192.168.1.50", Enabled: true},
		{ID: "0badc0de", Proto: "both", Port: "3478-3480", DestIP: "192.168.1.60", Direct: true},
	}
	names := func(ip string) *string {
		if ip == "192.168.1.60" {
			return &hostile
		}
		return nil
	}
	batch := FirewallBatch(fw, rules, names)
	stmts, err := uci.Statements(batch)
	if err != nil {
		t.Fatalf("the batch does not parse: %v\n%s", err, batch)
	}
	var lines []string
	for _, s := range stmts {
		if len(s) != 2 {
			t.Fatalf("a statement is not exactly a command and one argument: %q", s)
		}
		lines = append(lines, s[0]+" "+s[1])
	}
	want := []string{
		"delete firewall.vectra_pf_3fa1c09e",
		"delete firewall.vectra_pf_b7d204aa",
		"delete firewall.vectra_pf_junk",
		"set firewall.vectra_pf_3fa1c09e=redirect",
		"set firewall.vectra_pf_3fa1c09e.name=Vectra: minecraft → 192.168.1.50",
		"set firewall.vectra_pf_3fa1c09e.src=wan",
		"set firewall.vectra_pf_3fa1c09e.dest=lan",
		"set firewall.vectra_pf_3fa1c09e.target=DNAT",
		"set firewall.vectra_pf_3fa1c09e.proto=tcp",
		"set firewall.vectra_pf_3fa1c09e.src_dport=25565",
		"set firewall.vectra_pf_3fa1c09e.dest_ip=192.168.1.50",
		"set firewall.vectra_pf_3fa1c09e.dest_port=25565",
		"set firewall.vectra_pf_3fa1c09e.enabled=1",
		"set firewall.vectra_pf_3fa1c09e.reflection=1",
		"set firewall.vectra_pf_3fa1c09e.vectra_preset=minecraft",
		"set firewall.vectra_pf_0badc0de=redirect",
		"set firewall.vectra_pf_0badc0de.name=Vectra: " + hostile,
		"set firewall.vectra_pf_0badc0de.src=wan",
		"set firewall.vectra_pf_0badc0de.dest=lan",
		"set firewall.vectra_pf_0badc0de.target=DNAT",
		"set firewall.vectra_pf_0badc0de.proto=tcp udp",
		"set firewall.vectra_pf_0badc0de.src_dport=3478-3480",
		"set firewall.vectra_pf_0badc0de.dest_ip=192.168.1.60",
		"set firewall.vectra_pf_0badc0de.dest_port=3478-3480",
		"set firewall.vectra_pf_0badc0de.enabled=0",
		"set firewall.vectra_pf_0badc0de.reflection=1",
		"set firewall.vectra_pf_0badc0de.vectra_direct=1",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("batch:\n%s\nreads as:\n%s", batch, strings.Join(lines, "\n"))
	}
	if n := strings.Count(batch, "\n"); n != len(want) {
		t.Fatalf("the batch has %d lines, want %d: a value broke a line", n, len(want))
	}
	if FirewallBatch(Firewall{}, nil, nil) != "" {
		t.Fatal("nothing to remove and nothing to write must be an empty batch")
	}
}

const leasesFile = `1759752000 aa:bb:cc:dd:ee:01 192.168.1.50 gaming-pc 01:aa:bb:cc:dd:ee:01
1759752000 aa:bb:cc:dd:ee:02 192.168.1.60 * *
0 aa:bb:cc:dd:ee:03 192.168.1.70 phone *
broken line
1759752000 not-a-mac 192.168.1.80 x *
1759752000 aa:bb:cc:dd:ee:05 fd00::5 v6 *
1759752000 aa:bb:cc:dd:ee:06 10.9.9.9 guest-phone *
`

const dhcpConfig = `
config dnsmasq
	option domainneeded '1'

config host
	option name 'nas'
	option mac 'aa:bb:cc:dd:ee:10'
	option ip '192.168.1.10'

config host
	option name 'Игровой ПК'
	option mac 'aa:bb:cc:dd:ee:01'
	option ip '192.168.1.50'
`

func TestDevicesNeverCarryAMAC(t *testing.T) {
	f, err := uci.Parse(dhcpConfig)
	if err != nil {
		t.Fatal(err)
	}
	leases, hosts := parseLeases([]byte(leasesFile)), parseStaticHosts(f)
	devs := devices(leases, hosts, deviceNames(leases, hosts), homeLAN)
	name := func(s string) *string { return &s }
	want := []Device{
		{Name: name("nas"), IP: "192.168.1.10"},
		{Name: name("Игровой ПК"), IP: "192.168.1.50"}, // the static host's name wins
		{Name: nil, IP: "192.168.1.60"},
		{Name: name("phone"), IP: "192.168.1.70"},
	}
	if !reflect.DeepEqual(devs, want) {
		t.Fatalf("devices = %s", fmt.Sprint(devs))
	}
	if strings.Contains(fmt.Sprintf("%+v", devs), "aa:bb") {
		t.Fatal("a MAC address left the router")
	}
	var many strings.Builder
	for i := 2; i < 2+MaxDevices+10; i++ {
		fmt.Fprintf(&many, "0 aa:bb:cc:dd:%02x:%02x 192.168.1.%d d%d *\n", i/256, i%256, i, i)
	}
	ml := parseLeases([]byte(many.String()))
	if n := len(devices(ml, nil, nil, homeLAN)); n != MaxDevices {
		t.Fatalf("%d devices, want %d", n, MaxDevices)
	}
	if displayName("bell\a") != nil || displayName(strings.Repeat("x", 64)) != nil || displayName("  ") != nil {
		t.Fatal("a name unfit to show was kept")
	}
}

func TestCGNAT(t *testing.T) {
	for in, want := range map[string]bool{
		"93.184.216.34": false, "93.184.216.34/24": false, "100.64.0.1": true, "100.127.255.254": true,
		"100.128.0.1": false, "192.168.0.10": true, "10.1.2.3/8": true, "172.20.0.2": true,
		"": false, "0.0.0.0": false, "fe80::1": false, "garbage": false,
	} {
		if got := CGNAT(in); got != want {
			t.Errorf("CGNAT(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseLAN(t *testing.T) {
	lan := parseLAN([]byte(`{"up":true,"ipv4-address":[{"address":"192.168.1.1","mask":24},{"address":"192.168.1.2","mask":24},{"address":"10.0.0.1","mask":8},{"address":"bad","mask":24},{"address":"10.1.1.1","mask":0}]}`))
	want := LAN{
		Subnets: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("10.0.0.0/8")},
		Router:  []netip.Addr{netip.MustParseAddr("192.168.1.1"), netip.MustParseAddr("192.168.1.2"), netip.MustParseAddr("10.0.0.1")},
	}
	if !reflect.DeepEqual(lan, want) {
		t.Fatalf("lan = %+v", lan)
	}
	if l := parseLAN([]byte("not json")); len(l.Subnets) != 0 {
		t.Fatal("garbage gave a LAN")
	}
}

func TestDirectAddrs(t *testing.T) {
	r := func(ip string, enabled, direct bool) Rule { return Rule{DestIP: ip, Enabled: enabled, Direct: direct} }
	got := DirectAddrs([]Rule{r("192.168.1.60", true, true), r("192.168.1.50", true, true), r("192.168.1.60", true, true),
		r("192.168.1.70", false, true), r("192.168.1.80", true, false), r("bad", true, true)})
	want := []netip.Addr{netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("192.168.1.60")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// fakeRouter is a router in a temp dir: the configs, the leases, and a Run
// that records every command and the batch it was handed.
type fakeRouter struct {
	env     Env
	calls   []string
	batches []string
	failOn  string
	// failFirst: only the first failFirst calls matching failOn fail (0 = all).
	failFirst int
	failed    int
	lanReply  string
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	dir := t.TempDir()
	fr := &fakeRouter{lanReply: `{"ipv4-address":[{"address":"192.168.1.1","mask":24}]}`}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fr.env = Env{
		FirewallConfig: write("firewall", fw4Config),
		DHCPConfig:     write("dhcp", dhcpConfig),
		Leases:         write("dhcp.leases", leasesFile),
		RunDir:         filepath.Join(dir, "run"),
		UCISaveDir:     filepath.Join(dir, "uci"),
		Lock:           filepath.Join(dir, "lock", "portfwd.lock"),
		NewID:          counterIDs(),
		Run: func(_ context.Context, stdin io.Reader, name string, args ...string) error {
			call := strings.Join(append([]string{name}, args...), " ")
			if len(args) >= 2 && args[0] == "-t" {
				call = name + " -t DIR " + strings.Join(args[2:], " ")
			}
			fr.calls = append(fr.calls, call)
			if stdin != nil {
				b, _ := io.ReadAll(stdin)
				fr.batches = append(fr.batches, string(b))
			}
			if fr.failOn != "" && strings.Contains(call, fr.failOn) && (fr.failFirst == 0 || fr.failed < fr.failFirst) {
				fr.failed++
				return fmt.Errorf("exit status 1")
			}
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ubus" && strings.Join(args, " ") == "call network.interface.lan status" {
				return []byte(fr.lanReply), nil
			}
			return nil, fmt.Errorf("unexpected %s %v", name, args)
		},
	}
	return fr
}

func TestApply(t *testing.T) {
	fr := newFakeRouter(t)
	kept, err := Apply(context.Background(), fr.env, []Rule{rule("3fa1c09e", "tcp", "25565", "192.168.1.50"), rule("", "udp", "9000", "192.168.1.60")})
	if err != nil {
		t.Fatal(err)
	}
	if kept[1].ID != "aaaa0001" {
		t.Fatalf("the new rule got no id: %+v", kept)
	}
	if !reflect.DeepEqual(fr.calls, []string{"uci -t DIR batch", "uci -t DIR commit firewall", "/etc/init.d/firewall reload"}) {
		t.Fatalf("calls = %q", fr.calls)
	}
	b := fr.batches[0]
	for _, want := range []string{
		"set firewall.vectra_pf_3fa1c09e.name='Vectra: Игровой ПК'\n",   // the device's name
		"set firewall.vectra_pf_aaaa0001.name='Vectra: 192.168.1.60'\n", // no name: the address
		"delete firewall.vectra_pf_b7d204aa\n",                          // left out: removed
	} {
		if !strings.Contains(b, want) {
			t.Errorf("batch lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "Allow-NAS") || strings.Contains(b, "dhcp.") {
		t.Errorf("the batch touches what is not vctl's:\n%s", b)
	}
	if entries, _ := os.ReadDir(fr.env.RunDir); len(entries) != 0 {
		t.Fatalf("the private save directories were left behind: %v", entries)
	}
}

func TestApplyRefusesBeforeChangingAnything(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(fr *fakeRouter)
		rules []Rule
		code  string
	}{
		{"a conflict with a foreign redirect", nil, []Rule{rule("", "tcp", "5000", "192.168.1.50")}, CodePortConflict},
		{"the router's ssh on the wan", nil, []Rule{rule("", "tcp", "2222", "192.168.1.50")}, CodePortConflict},
		{"the LAN unknown", func(fr *fakeRouter) { fr.lanReply = "{}" }, []Rule{rule("", "tcp", "80", "192.168.1.50")}, CodeDestNotLAN},
		{"uncommitted firewall edits", func(fr *fakeRouter) {
			_ = os.MkdirAll(fr.env.UCISaveDir, 0o755)
			_ = os.WriteFile(filepath.Join(fr.env.UCISaveDir, "firewall"), []byte("firewall.x.y='z'\n"), 0o600)
		}, nil, CodeBusy},
		{"no firewall config", func(fr *fakeRouter) { _ = os.Remove(fr.env.FirewallConfig) }, nil, CodeApplyFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRouter(t)
			if tc.setup != nil {
				tc.setup(fr)
			}
			_, err := Apply(context.Background(), fr.env, tc.rules)
			if err == nil || err.Code != tc.code {
				t.Fatalf("Apply = %v, want %s", err, tc.code)
			}
			if len(fr.calls) != 0 {
				t.Fatalf("a refused change ran %q", fr.calls)
			}
		})
	}
	// Uncommitted edits of another config do not hold it up.
	fr := newFakeRouter(t)
	_ = os.MkdirAll(fr.env.UCISaveDir, 0o755)
	_ = os.WriteFile(filepath.Join(fr.env.UCISaveDir, "dhcp"), []byte("dhcp.x.y='z'\n"), 0o600)
	if _, err := Apply(context.Background(), fr.env, nil); err != nil {
		t.Fatalf("dhcp edits refused a firewall change: %v", err)
	}
}

func TestApplyFailures(t *testing.T) {
	for _, fail := range []string{"batch", "commit firewall"} {
		fr := newFakeRouter(t)
		fr.failOn = fail
		if _, err := Apply(context.Background(), fr.env, []Rule{rule("", "udp", "9000", "192.168.1.60")}); err == nil || err.Code != CodeApplyFailed {
			t.Errorf("%s failing: %v, want apply_failed", fail, err)
		}
		for _, c := range fr.calls {
			if strings.Contains(c, "reload") {
				t.Errorf("%s failing: fw4 was reloaded anyway: %q", fail, fr.calls)
			}
		}
	}
}

// A reload that fails after the commit must not leave the new redirects
// committed to go live later: the file goes back as it was, fw4 reloads it,
// and apply_failed means the router runs the forwards it had. The commit is
// simulated by rewriting the file, as uci commit would.
func TestApplyRestoresTheConfigWhenTheReloadFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failFirst int
		code      string
	}{
		{"the restore's reload works", 1, CodeApplyFailed},
		{"the restore's reload fails too", 0, CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRouter(t)
			before, _ := os.ReadFile(fr.env.FirewallConfig)
			run := fr.env.Run
			fr.env.Run = func(ctx context.Context, stdin io.Reader, name string, args ...string) error {
				if len(args) > 0 && args[len(args)-1] == "firewall" && args[len(args)-2] == "commit" {
					_ = os.WriteFile(fr.env.FirewallConfig, []byte("config redirect 'vectra_pf_aaaa0001'\n"), 0o644)
				}
				return run(ctx, stdin, name, args...)
			}
			fr.failOn, fr.failFirst = "firewall reload", tc.failFirst
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := Apply(ctx, fr.env, []Rule{rule("", "udp", "9000", "192.168.1.60")})
			if err == nil || err.Code != tc.code {
				t.Fatalf("Apply = %v, want %s", err, tc.code)
			}
			after, _ := os.ReadFile(fr.env.FirewallConfig)
			if string(after) != string(before) {
				t.Fatalf("the firewall config was not restored:\n%s", after)
			}
			if n := strings.Count(strings.Join(fr.calls, "|"), "/etc/init.d/firewall reload"); n != 2 {
				t.Fatalf("%d reloads, want the failed one and the restore's: %q", n, fr.calls)
			}
		})
	}
}

// The restore runs even when the caller's time has run out — at least
// restoreFloor — so a deadline never strands a committed, unloaded change.
func TestApplyRestoresPastTheDeadline(t *testing.T) {
	fr := newFakeRouter(t)
	before, _ := os.ReadFile(fr.env.FirewallConfig)
	run := fr.env.Run
	fr.env.Run = func(ctx context.Context, stdin io.Reader, name string, args ...string) error {
		if name == "/etc/init.d/firewall" && strings.Count(strings.Join(fr.calls, "|"), "reload") == 0 {
			_ = run(ctx, stdin, name, args...)
			<-ctx.Done() // a reload that hangs past its share
			return ctx.Err()
		}
		if len(args) > 1 && args[len(args)-2] == "commit" {
			_ = os.WriteFile(fr.env.FirewallConfig, []byte("# committed\n"), 0o644)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run(ctx, stdin, name, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Apply(ctx, fr.env, nil); err == nil || err.Code != CodeApplyFailed {
		t.Fatalf("Apply = %v, want apply_failed", err)
	}
	if after, _ := os.ReadFile(fr.env.FirewallConfig); string(after) != string(before) {
		t.Fatal("not restored")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestApplyIsOneAtATime(t *testing.T) {
	fr := newFakeRouter(t)
	unlock, err := lock(fr.env)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, aerr := Apply(context.Background(), fr.env, nil); aerr == nil || aerr.Code != CodeBusy {
		t.Fatalf("Apply under the lock = %v, want busy", aerr)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("ran %q", fr.calls)
	}
}

func TestRead(t *testing.T) {
	fr := newFakeRouter(t)
	st := Read(context.Background(), fr.env)
	if len(st.Rules) != 2 || len(st.Devices) != 4 {
		t.Fatalf("state = %+v", st)
	}
	if n := st.DeviceName("192.168.1.50"); n == nil || *n != "Игровой ПК" {
		t.Errorf("name = %v", n)
	}
	if st.DeviceName("192.168.1.60") != nil || st.DeviceName("bad") != nil {
		t.Errorf("a name for a nameless device")
	}
	own, err := LoadOwn(fr.env.FirewallConfig)
	if err != nil || len(own) != 2 {
		t.Fatalf("LoadOwn = %v %v", own, err)
	}
	// Nothing readable: empty, never a failure.
	empty := Read(context.Background(), Env{FirewallConfig: "/nonexistent", DHCPConfig: "/nonexistent", Leases: "/nonexistent"})
	if len(empty.Rules) != 0 || len(empty.Devices) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

// A zone renamed in LuCI is still the WAN when it carries the wan network;
// a redirect from a LAN-side zone claims nothing on the WAN.
func TestForeignRedirectsFromAnyWANZone(t *testing.T) {
	f, err := uci.Parse(`
config zone
	option name 'internet'
	list network 'wan'
	list network 'wan6'

config zone
	option name 'guest'
	list network 'guest'

config redirect
	option src 'internet'
	option src_dport '8080'
	option proto 'tcp'

config redirect
	option src 'guest'
	option src_dport '9090'
	option proto 'tcp'

config rule
	option src 'internet'
	option dest_port '2222'
	option target 'ACCEPT'

config rule
	option name 'Allow-All-WAN'
	option src 'wan'
	option proto 'tcp'
	option target 'ACCEPT'
`)
	if err != nil {
		t.Fatal(err)
	}
	fw := ParseFirewall(f)
	if len(fw.foreign) != 1 || fw.foreign[0].ports[0] != (portRange{8080, 8080}) {
		t.Fatalf("foreign = %+v", fw.foreign)
	}
	// A rule without a port (the whole router opened) is not a claim; see
	// ParseFirewall.
	if len(fw.reserved) != 1 || fw.reserved[0].ports != (portRange{2222, 2222}) {
		t.Fatalf("reserved = %+v", fw.reserved)
	}
}

// Every section named vectra_pf_* goes, whatever its type, so the redirect
// can be written under its name; a name uci could not take is never put in
// a batch.
func TestBatchClearsEveryVctlNamedSection(t *testing.T) {
	fw := ParseFirewall(&uci.File{Sections: []uci.Section{
		{Type: "rule", Name: "vectra_pf_3fa1c09e"},
		{Type: "redirect", Name: "vectra_pf_bad-name;x"},
		{Type: "redirect", Name: "lan_thing"},
	}})
	b := FirewallBatch(fw, []Rule{{ID: "3fa1c09e", Proto: "tcp", Port: "80", DestIP: "192.168.1.50", Enabled: true}}, nil)
	if !strings.HasPrefix(b, "delete firewall.vectra_pf_3fa1c09e\nset firewall.vectra_pf_3fa1c09e=redirect\n") {
		t.Fatalf("batch:\n%s", b)
	}
	if strings.Contains(b, "bad-name") || strings.Contains(b, "lan_thing") {
		t.Fatalf("batch names what it must not:\n%s", b)
	}
	if len(fw.Own) != 0 {
		t.Fatalf("a rule section was read as a forward: %+v", fw.Own)
	}
}

func TestDirectActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run", "portfwd-direct.json")
	alive := func(pid int) bool { return pid == 42 }
	direct := []Rule{{DestIP: "192.168.1.60", Enabled: true, Direct: true}, {DestIP: "192.168.1.70", Enabled: true, Direct: true}}
	str := func(b *bool) string {
		if b == nil {
			return "null"
		}
		return fmt.Sprint(*b)
	}
	if got := DirectActive([]Rule{{DestIP: "192.168.1.60", Enabled: true}, {DestIP: "192.168.1.70", Direct: true}}, path, alive); got != nil {
		t.Fatalf("no enabled direct rule: %s, want null", str(got))
	}
	if got := str(DirectActive(direct, path, alive)); got != "false" {
		t.Fatalf("no status file: %s", got)
	}
	for _, tc := range []struct {
		name string
		st   DirectStatus
		want string
	}{
		{"all in the set", DirectStatus{PID: 42, Active: true, Addrs: []string{"192.168.1.70", "192.168.1.60", "192.168.1.9"}}, "true"},
		{"one missing", DirectStatus{PID: 42, Active: true, Addrs: []string{"192.168.1.60"}}, "false"},
		{"not active", DirectStatus{PID: 42, Active: false, Addrs: []string{"192.168.1.60", "192.168.1.70"}}, "false"},
		{"its daemon is gone", DirectStatus{PID: 7, Active: true, Addrs: []string{"192.168.1.60", "192.168.1.70"}}, "false"},
	} {
		if err := WriteDirectStatus(path, tc.st); err != nil {
			t.Fatal(err)
		}
		if got := str(DirectActive(direct, path, alive)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	_ = os.WriteFile(path, []byte("garbage"), 0o644)
	if got := str(DirectActive(direct, path, alive)); got != "false" {
		t.Errorf("garbage: %s", got)
	}
}
