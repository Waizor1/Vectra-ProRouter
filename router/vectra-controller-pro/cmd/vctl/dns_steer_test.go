package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
)

// fakeProc writes /proc/<pid>/{comm,status} for each process.
func fakeProc(t *testing.T, procs map[string][2]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "comm"), []byte(p[0]+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\t"+p[0]+"\nUid:\t"+p[1]+"\n"), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(root, "self"), 0o755)
	return root
}

// dnsmasq's upstream sockets carry the uid it runs as. Root never counts: a
// redirect by root's uid would bring xray's and vctl's own lookups back to
// xray. PassWall's own instance and anything else are not dnsmasq.
func TestResolverUIDsAreDnsmasqsOwnNeverRoot(t *testing.T) {
	root := fakeProc(t, map[string][2]string{
		"101": {"dnsmasq", "453\t453\t453\t453"},
		"102": {"dnsmasq", "0\t0\t0\t0"},
		"103": {"dnsmasq_default", "453\t454\t454\t454"},
		"104": {"xray", "0\t0\t0\t0"},
		"105": {"dnsmasq", "455\t455\t455\t455"},
		"106": {"dnsmasq", "453\t453\t453\t453"},
	})
	if got := resolverUIDs(root); !reflect.DeepEqual(got, []int{453, 455}) {
		t.Fatalf("uids = %v", got)
	}
	if got := resolverUIDs(filepath.Join(root, "absent")); got != nil {
		t.Fatalf("no /proc: %v", got)
	}
}

func TestControlPlaneDomainsAreThePanelsNames(t *testing.T) {
	got := controlPlaneDomains("https://api.vectra-pro.net", "https://Router.Vectra-Pro.net/x", "https://api.vectra-pro.net/y", "https://192.0.2.1", "http://[2001:db8::1]:80", "::bad")
	want := []string{"full:api.vectra-pro.net", "full:router.vectra-pro.net"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("domains = %v", got)
	}
}

func dnsDaemon(t *testing.T, render string, procs map[string][2]string) *daemon {
	t.Helper()
	dir := t.TempDir()
	d := &daemon{cfg: agentcfg.Config{
		ControlURL:     "https://api.vectra-pro.net",
		PanelURL:       "https://router.vectra-pro.net",
		XrayRenderPath: filepath.Join(dir, "xray.json"),
	}}
	if render != "" {
		if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(render)); err != nil {
			t.Fatal(err)
		}
	}
	d.procDir = fakeProc(t, procs)
	d.rootDir = t.TempDir()
	d.dnsAnswers = func(context.Context, int) bool { return true }
	// The router's own LAN address, as far as the tests are concerned.
	d.ownsAddr = func(ip net.IP) bool { return ip.Equal(net.IPv4(192, 168, 1, 1)) }
	return d
}

// withWANResolver writes netifd's resolv.conf.auto under d.rootDir.
func withWANResolver(t *testing.T, d *daemon, nameserver string) {
	t.Helper()
	dir := filepath.Join(d.rootDir, "tmp", "resolv.conf.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resolv.conf.auto"), []byte("# Interface wan\nnameserver "+nameserver+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const renderWithDNS = `{"inbounds":[{"tag":"tproxy-in","listen":"0.0.0.0","port":12345,"protocol":"dokodemo-door"},` +
	`{"tag":"vctl-dns-in","listen":"127.0.0.1","port":10053,"protocol":"dokodemo-door"}]}`
const renderWithoutDNS = `{"inbounds":[{"tag":"tproxy-in","listen":"0.0.0.0","port":12345,"protocol":"dokodemo-door"}]}`

var dnsmasqAs453 = map[string][2]string{"7": {"dnsmasq", "453\t453\t453\t453"}}

// The firewall redirects dnsmasq's upstream only to an inbound the RUNNING
// render has, and only by dnsmasq's own uid; otherwise nothing, and dnsmasq
// asks its own servers as it would without vctl.
func TestTheRedirectNeedsTheRunningInboundAndDnsmasqsUID(t *testing.T) {
	for name, c := range map[string]struct {
		render string
		procs  map[string][2]string
		off    bool
		want   string
	}{
		"steered":                {renderWithDNS, dnsmasqAs453, false, "10053/[453]/v6reject=false/hijack=false"},
		"render without inbound": {renderWithoutDNS, dnsmasqAs453, false, ""},
		"no render":              {"", dnsmasqAs453, false, ""},
		"dnsmasq as root":        {renderWithDNS, map[string][2]string{"7": {"dnsmasq", "0\t0\t0\t0"}}, false, ""},
		"no dnsmasq":             {renderWithDNS, nil, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := dnsDaemon(t, c.render, c.procs)
			d.cfg.NoDNSTunnel = c.off
			spec := firewall.DefaultSpec(12345, 1)
			d.addDNSRedirect(context.Background(), &spec, 0)
			if got := dnsRedirectKey(spec); got != c.want {
				t.Fatalf("redirect = %q, want %q", got, c.want)
			}
		})
	}
}

// Turned off in UCI (dns_tunnel '0'), no render gets the inbound at all.
func TestDNSTunnelSwitchedOff(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	if o := d.dnsOptions(); o == nil || o.Listen != xray.DefaultDNSListen ||
		!reflect.DeepEqual(o.DirectDomains, []string{"full:api.vectra-pro.net", "full:router.vectra-pro.net", "domain:pool.ntp.org"}) ||
		!reflect.DeepEqual(o.DirectResolvers, []string{"8.8.8.8", "77.88.8.8"}) {
		t.Fatalf("options = %+v", o)
	}
	d.cfg.NoDNSTunnel = true
	if o := d.dnsOptions(); o != nil {
		t.Fatalf("switched off, still %+v", o)
	}
}

// A loaded data plane that redirects DNS to a port the running render no
// longer listens on (or not at all, now that it does) is programmed again.
func TestAStaleRedirectIsProgrammedAgain(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	d.desired = &config.Config{}
	d.supStarted = true
	ctx := context.Background()
	if d.dnsRedirectStale(ctx) {
		t.Fatal("stale before anything was programmed by this process")
	}
	none := ""
	d.fwProgrammed = &none
	if !d.dnsRedirectStale(ctx) {
		t.Fatal("the render has the inbound and nothing is redirected: not stale?")
	}
	cur := "10053/[453]/v6reject=false/hijack=false"
	d.fwProgrammed = &cur
	if d.dnsRedirectStale(ctx) {
		t.Fatal("stale while the redirect is exactly what is called for")
	}
	withWANResolver(t, d, "77.37.251.33")
	if !d.dnsRedirectStale(ctx) {
		t.Fatal("an IPv4 upstream appeared and IPv6 is still not refused: not stale?")
	}
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(renderWithoutDNS)); err != nil {
		t.Fatal(err)
	}
	if !d.dnsRedirectStale(ctx) {
		t.Fatal("redirecting to an inbound the render dropped: not stale?")
	}
	d.supStarted = false
	if d.dnsRedirectStale(ctx) {
		t.Fatal("stale with xray down: the data plane is not this check's business then")
	}
}

// xray that stops answering on its DNS inbound — a crash loop, a restart
// that does not come back — loses the redirect after dnsDeadAfter loops, not
// at the first miss; and gets it back at the first answer.
func TestADeadDNSInboundLosesTheRedirect(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	d.desired = &config.Config{}
	d.supStarted = true
	ctx := context.Background()
	answering := false
	d.dnsAnswers = func(context.Context, int) bool { return answering }
	cur := "10053/[453]/v6reject=false/hijack=false"
	d.fwProgrammed = &cur
	if d.dnsRedirectStale(ctx) {
		t.Fatal("one miss took the redirect out")
	}
	if !d.dnsRedirectStale(ctx) {
		t.Fatalf("%d misses in a row and the redirect stays", dnsDeadAfter)
	}
	none := ""
	d.fwProgrammed = &none
	if d.dnsRedirectStale(ctx) {
		t.Fatal("still dead, and yet stale without the redirect")
	}
	answering = true
	if !d.dnsRedirectStale(ctx) {
		t.Fatal("xray answers again and the redirect is not put back")
	}

	// Programming never installs it towards a port nothing answers on.
	answering = false
	spec := firewall.DefaultSpec(12345, 1)
	d.addDNSRedirect(ctx, &spec, 0)
	if dnsRedirectKey(spec) != "" {
		t.Fatalf("redirected to a dead inbound: %s", dnsRedirectKey(spec))
	}
}

// IPv6 upstream queries are refused only when dnsmasq has an IPv4 server to
// ask instead — the WAN's, or a general server= of its own.
func TestIPv6RefusedOnlyWithAnIPv4Upstream(t *testing.T) {
	root := t.TempDir()
	if hasIPv4Upstream(root) {
		t.Fatal("nothing at all, and yet an IPv4 upstream")
	}
	etc := filepath.Join(root, "var", "etc")
	_ = os.MkdirAll(etc, 0o755)
	_ = os.WriteFile(filepath.Join(etc, "dnsmasq.conf.cfg01411c"), []byte("server=/lan/192.168.1.5\nserver=2001:db8::53\n"), 0o644)
	if hasIPv4Upstream(root) {
		t.Fatal("a per-domain server and an IPv6 one are not an IPv4 upstream")
	}
	_ = os.WriteFile(filepath.Join(etc, "dnsmasq.conf.cfg01411c"), []byte("server=8.8.8.8#53\n"), 0o644)
	if !hasIPv4Upstream(root) {
		t.Fatal("server=8.8.8.8#53 not seen")
	}
	root2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root2, "tmp", "resolv.conf.d"), 0o755)
	_ = os.WriteFile(filepath.Join(root2, "tmp", "resolv.conf.d", "resolv.conf.auto"), []byte("nameserver 2001:db8::1\n"), 0o644)
	if hasIPv4Upstream(root2) {
		t.Fatal("an IPv6-only WAN is not an IPv4 upstream")
	}
}

// The probe is a real DNS exchange: a TXT query, any answer with its id.
func TestTheDNSInboundProbeTalksDNS(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if n < 16 || q[n-3] != 16 || !strings.Contains(string(q), "vctl-probe") {
				continue // not the TXT probe: no answer
			}
			resp := append([]byte(nil), q...)
			resp[2] |= 0x80 // QR
			resp[3] = 5     // REFUSED, as xray 26.3.27 answers a non-address query
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	if !dnsInboundAnswers(context.Background(), port) {
		t.Fatal("a DNS server that answers was not seen")
	}
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	dead.Close()
	if dnsInboundAnswers(context.Background(), deadPort) {
		t.Fatal("a closed port answered")
	}
}

// The WAN's own IPv4 resolvers come last among the direct ones: where port 53
// to the outside is closed, they are what still resolves the nodes' names.
func TestDirectResolversEndWithTheWANs(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	withWANResolver(t, d, "77.37.251.33")
	got := d.dnsOptions().DirectResolvers
	want := []string{"8.8.8.8", "77.88.8.8", "77.37.251.33"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolvers = %v, want %v", got, want)
	}
	withWANResolver(t, d, "8.8.8.8")
	if got := d.dnsOptions().DirectResolvers; !reflect.DeepEqual(got, []string{"8.8.8.8", "77.88.8.8"}) {
		t.Fatalf("a repeat was added: %v", got)
	}
}

// withDNSServed writes a /proc/net/<file> under d.procDir with one socket:
// port 53 (0035) when serving, 5353 (14E9) when not.
func withDNSServed(t *testing.T, d *daemon, file string, serving bool) {
	t.Helper()
	port := "14E9"
	if serving {
		port = "0035"
	}
	local := "0101A8C0:" + port // 192.168.1.1
	if file == "udp6" {
		local = "00000000000000000000000000000000:" + port
	}
	if err := os.MkdirAll(filepath.Join(d.procDir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n" +
		"  150: " + local + " 00000000:0000 07 00000000:00000000 00:00000000 00000000   453        0 4242 2 0000000000000000 0\n" +
		"  151: 0100007F:0035 08080808:0035 01 00000000:00000000 00:00000000 00000000   453        0 4243 2 0000000000000000 0\n"
	if err := os.WriteFile(filepath.Join(d.procDir, "net", file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The LAN's queries to public resolvers are hijacked to the router's own
// while its resolver asks through the tunnel — only while something serves
// port 53 on the router (an unconnected UDP socket on it; a connected one to
// some :53 is a client), and not when told not to.
func TestTheLANsDNSIsHijackedOnlyWhileTheRouterServesIt(t *testing.T) {
	for name, c := range map[string]struct {
		file    string
		serving bool
		off     bool
		want    string
	}{
		"dnsmasq on 53":    {"udp", true, false, "10053/[453]/v6reject=false/hijack=true"},
		"on 53 over v6":    {"udp6", true, false, "10053/[453]/v6reject=false/hijack=true"},
		"dnsmasq on 5353":  {"udp", false, false, "10053/[453]/v6reject=false/hijack=false"},
		"switched off":     {"udp", true, true, "10053/[453]/v6reject=false/hijack=false"},
		"no /proc/net/udp": {"", false, false, "10053/[453]/v6reject=false/hijack=false"},
	} {
		t.Run(name, func(t *testing.T) {
			d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
			d.cfg.NoDNSHijack = c.off
			if c.file != "" {
				withDNSServed(t, d, c.file, c.serving)
			}
			spec := firewall.DefaultSpec(12345, 1)
			d.addDNSRedirect(context.Background(), &spec, 0)
			if got := dnsRedirectKey(spec); got != c.want {
				t.Fatalf("redirect = %q, want %q", got, c.want)
			}
			if spec.HijackDNS != strings.HasSuffix(c.want, "hijack=true") {
				t.Fatalf("HijackDNS = %v", spec.HijackDNS)
			}
		})
	}
	// Without the tunnel there is nothing better to hand them: no hijack.
	d := dnsDaemon(t, renderWithoutDNS, dnsmasqAs453)
	withDNSServed(t, d, "udp", true)
	spec := firewall.DefaultSpec(12345, 1)
	d.addDNSRedirect(context.Background(), &spec, 0)
	if spec.HijackDNS {
		t.Fatal("hijacked while the router's resolver asks over the open path")
	}
}

// dnsmasq starting or stopping to serve port 53 makes the loaded hijack stale.
func TestAHijackToANoLongerServedPortIsProgrammedAgain(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	d.desired = &config.Config{}
	d.supStarted = true
	ctx := context.Background()
	withDNSServed(t, d, "udp", true)
	cur := "10053/[453]/v6reject=false/hijack=true"
	d.fwProgrammed = &cur
	if d.dnsRedirectStale(ctx) {
		t.Fatal("stale while the hijack is exactly what is called for")
	}
	withDNSServed(t, d, "udp", false)
	if d.dnsRedirectStale(ctx) {
		t.Fatal("one loop without dnsmasq's socket (a restart) reprogrammed the table")
	}
	if !d.dnsRedirectStale(ctx) {
		t.Fatalf("port 53 not served for %d loops and the hijack stays", dnsDeadAfter)
	}
	withDNSServed(t, d, "udp", true)
	if d.dnsRedirectStale(ctx) {
		t.Fatal("served again: the loaded hijack is right")
	}
}

// A resolver on loopback alone does not serve the LAN: the hijack redirects
// to the address the query came in on.
func TestALoopbackOnlyResolverIsNotHijackedTo(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	if err := os.MkdirAll(filepath.Join(d.procDir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	loopback := "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n" +
		"  150: 0100007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   453        0 4242 2 0000000000000000 0\n"
	v6loop := "  sl  local_address                         remote_address                        st\n" +
		"  1: 00000000000000000000000001000000:0035 00000000000000000000000000000000:0000 07\n"
	_ = os.WriteFile(filepath.Join(d.procDir, "net", "udp"), []byte(loopback), 0o644)
	_ = os.WriteFile(filepath.Join(d.procDir, "net", "udp6"), []byte(v6loop), 0o644)
	if dnsServed(d.procDir, d.ownsAddr) {
		t.Fatal("127.0.0.1:53 and [::1]:53 alone count as serving the LAN")
	}
	// xray's transparent answer socket for a LAN client's 8.8.8.8:53 flow.
	fake := loopback + "  151: 08080808:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 4243 2 0000000000000000 0\n"
	_ = os.WriteFile(filepath.Join(d.procDir, "net", "udp"), []byte(fake), 0o644)
	if dnsServed(d.procDir, d.ownsAddr) {
		t.Fatal("xray's socket on 8.8.8.8:53 counts as the router serving DNS")
	}
	wild := loopback + "  152: 00000000:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 4244 2 0000000000000000 0\n"
	_ = os.WriteFile(filepath.Join(d.procDir, "net", "udp"), []byte(wild), 0o644)
	if !dnsServed(d.procDir, d.ownsAddr) {
		t.Fatal("a resolver on all addresses does not count")
	}
	for addr, want := range map[string]string{"0100007F": "127.0.0.1", "0101A8C0": "192.168.1.1", "00000000": "0.0.0.0", "00000000000000000000000001000000": "::1"} {
		if got := procNetIP(addr).String(); got != want {
			t.Errorf("%s -> %s, want %s", addr, got, want)
		}
	}
}

// The names the rules proxy are answered with FakeDNS addresses where the
// kernel may take the Russian networks: the provider's Russian bridge sent
// direct, DNS through the tunnel, the kernel's direct routing on (spec
// decision 8). Switching either off takes FakeDNS off with it.
func TestFakeDNSWhereTheKernelTakesTheRussianNetworks(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	withBridge := []byte(`{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}],"routing":{"rules":[],"balancers":[{"tag":"BL-RU","selector":["b"]}]}}`)
	o, _ := spliceOptionsFor(withBridge, localctl.Overrides{}, true)
	if o = d.withDNS(o); !o.FakeDNS || o.DNS == nil {
		t.Fatalf("FakeDNS off: %+v", o)
	}
	d.cfg.NoDirectBypass = true
	if o = d.withDNS(o); o.FakeDNS {
		t.Fatal("FakeDNS with the kernel's direct routing off")
	}
	d.cfg.NoDirectBypass, d.cfg.NoDNSTunnel = false, true
	if o = d.withDNS(o); o.FakeDNS {
		t.Fatal("FakeDNS without DNS through the tunnel")
	}
	d.cfg.NoDNSTunnel = false
	o, _ = spliceOptionsFor(withBridge, localctl.Overrides{}, false)
	if o = d.withDNS(o); o.FakeDNS {
		t.Fatal("FakeDNS with the Russian networks left to the provider")
	}
}
