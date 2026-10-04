package main

import (
 "vectra-controller-pro/internal/vault"
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
)

// DNS through the tunnel, the daemon's side (the why is in
// internal/coreengine/xray/dns_steer.go): every render gets the DNS inbound,
// and the firewall redirects dnsmasq's upstream to it only while the RUNNING
// render has one and dnsmasq runs as its own user.

// dnsOptions are the DNS options every render is made with; nil when the
// router is told to resolve over the open path (UCI dns_tunnel '0').
func (d *daemon) dnsOptions() *xray.DNSOptions {
	if d.cfg.NoDNSTunnel {
		return nil
	}
	return &xray.DNSOptions{
		Listen: xray.DefaultDNSListen,
		// PassWall's routing answers its lists' domains with FakeDNS; the
		// data plane then carries those addresses (carryFakeDNS).
		AllowFakeDNS:    d.passwallMode(),
		DirectResolvers: directResolvers(d.etcRoot()),
		// The panel's names, and NTP's: the clock TLS needs must not wait
		// for the tunnel either.
		DirectDomains: append(controlPlaneDomains(d.cfg.ControlURL, d.cfg.PanelURL), "domain:pool.ntp.org"),
		// The router refuses the LAN's IPv6 unless the nodes carry it.
		IPv4Only: !d.cfg.IPv6,
	}
}

// withDNS sets the DNS through the tunnel on opts, and FakeDNS for the names
// the rules proxy where the kernel may take the Russian networks: the
// provider's Russian bridge sent direct, DNS through the tunnel, the kernel's
// direct routing on (spec decision 8).
func (d *daemon) withDNS(opts xray.SpliceOptions) xray.SpliceOptions {
	opts.DNS = d.dnsOptions()
	opts.FakeDNS = opts.DNS != nil && opts.RussiaDirect && !d.cfg.NoDirectBypass
	return opts
}

// directResolvers are the public ones, then the WAN's own IPv4 resolvers
// (netifd's resolv.conf.auto): the last to be asked, and the most likely to
// be reachable where a network keeps port 53 to the outside closed. Without
// any of them answering, the nodes' names could only be asked through the
// tunnel they are the way into.
func directResolvers(root string) []string {
	out := append([]string(nil), xray.DefaultDirectResolvers...)
	seen := map[string]bool{}
	for _, r := range out {
		seen[r] = true
	}
	for _, f := range []string{"tmp/resolv.conf.d/resolv.conf.auto", "tmp/resolv.conf.auto"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "nameserver" || !isIPv4(fields[1]) || seen[fields[1]] {
				continue
			}
			if ip := net.ParseIP(fields[1]); ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			seen[fields[1]] = true
			out = append(out, fields[1])
			if len(out) >= len(xray.DefaultDirectResolvers)+2 {
				return out
			}
		}
	}
	return out
}

// controlPlaneDomains are the panel's names, resolved directly: vctl must
// reach its panel, and fetch its updates, with every node dead.
func controlPlaneDomains(urls ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		h := strings.ToLower(u.Hostname())
		if seen[h] || net.ParseIP(h) != nil || !strings.Contains(h, ".") {
			continue // an address, or not a name xray resolves
		}
		seen[h] = true
		out = append(out, "full:"+h)
	}
	return out
}

// dnsCandidate is what the firewall could redirect: the running render's DNS
// inbound port and dnsmasq's uids. ok is false when either is missing — then
// nothing is redirected and dnsmasq asks its own servers, as without vctl.
func (d *daemon) dnsCandidate() (port int, uids []int, ok bool) {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return 0, nil, false
	}
	port, ok = xray.RenderDNSListen(raw)
	if !ok {
		return 0, nil, false
	}
	uids = resolverUIDs(d.procRoot())
	if len(uids) == 0 {
		return 0, nil, false
	}
	return port, uids, true
}

func (d *daemon) procRoot() string {
	if d.procDir != "" {
		return d.procDir
	}
	return "/proc"
}

// answers asks xray's DNS inbound whether it is there (dnsInboundAnswers, or
// the tests' stand-in).
func (d *daemon) answers(ctx context.Context, port int) bool {
	if d.dnsAnswers != nil {
		return d.dnsAnswers(ctx, port)
	}
	return dnsInboundAnswers(ctx, port)
}

// dnsRedirectWait is how long programming the firewall waits for a just
// started xray to answer on its DNS inbound before leaving the redirect out
// (the loop adds it once xray answers).
const dnsRedirectWait = 10 * time.Second

// addDNSRedirect puts the redirect in spec when the render has the inbound,
// dnsmasq runs as its own user AND xray answers on the inbound — waiting up to
// wait for that — and says in the log when it cannot. The redirect is never
// installed towards a port nothing answers on: every lookup on the router,
// the panel's among them, would fail.
func (d *daemon) addDNSRedirect(ctx context.Context, spec *firewall.Spec, wait time.Duration) {
	port, uids, ok := d.dnsCandidate()
	if !ok {
		if d.cfg.NoDNSTunnel {
			return
		}
		raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
		if _, has := xray.RenderDNSListen(raw); err == nil && has {
			logging.L().Warn("DNS through the tunnel is off: no dnsmasq runs as its own user; the router's resolver keeps asking over the open path")
		}
		return
	}
	deadline := time.Now().Add(wait)
	for !d.answers(ctx, port) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			logging.L().Warn("xray does not answer on its DNS inbound; the router's resolver asks over the open path until it does", "port", port)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	spec.DNSRedirectPort, spec.DNSResolverUIDs = port, uids
	spec.DNSRejectV6 = hasIPv4Upstream(d.etcRoot())
	spec.HijackDNS = d.hijacksDNS()
}

// hijacksDNS: the LAN's queries to public resolvers are answered by the
// router's own (firewall.Spec.HijackDNS) — while its resolver asks through the
// tunnel (the caller's condition), unless the router is told not to (UCI
// dns_hijack '0'), and only while something on the router answers on port 53.
// Sent to a port nothing serves, those devices would have no DNS at all.
func (d *daemon) hijacksDNS() bool {
	owned := d.ownsAddr
	if owned == nil {
		owned = routerOwns
	}
	return !d.cfg.NoDNSHijack && dnsServed(d.procRoot(), owned)
}

// routerOwns: ip is one of the router's own addresses.
func routerOwns(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// dnsServed: a UDP socket on the router serves port 53 to the LAN (dnsmasq,
// or whatever serves DNS in its place), by /proc/net/udp and udp6: bound to
// all addresses, or to one of the router's own (owned) that is not loopback —
// the hijack redirects to the address the query came in on, which a resolver
// on 127.0.0.1 alone does not answer. Not any socket on port 53: xray answers
// every UDP flow it carries from a transparent socket bound to the flow's
// original destination, so a LAN client asking 8.8.8.8 puts an
// "8.8.8.8:53" socket on the router — which serves nothing.
func dnsServed(procRoot string, owned func(net.IP) bool) bool {
	for _, f := range []string{"net/udp", "net/udp6"} {
		b, err := os.ReadFile(filepath.Join(procRoot, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fields := strings.Fields(line)
			// sl local_address rem_address st ...: 0035 is port 53, 07 unconnected.
			if len(fields) < 4 || fields[3] != "07" {
				continue
			}
			addr, port, ok := strings.Cut(fields[1], ":")
			if !ok || port != "0035" {
				continue
			}
			ip := procNetIP(addr)
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.IsUnspecified() || owned(ip) {
				return true
			}
		}
	}
	return false
}

// procNetIP reads /proc/net's hex address: 32-bit words in the host's byte
// order (little-endian on every router this runs on).
func procNetIP(h string) net.IP {
	if len(h) != 8 && len(h) != 32 {
		return nil
	}
	raw, err := hex.DecodeString(h)
	if err != nil {
		return nil
	}
	ip := make(net.IP, len(raw))
	for w := 0; w < len(raw); w += 4 {
		ip[w], ip[w+1], ip[w+2], ip[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	return ip
}

func (d *daemon) etcRoot() string {
	if d.rootDir != "" {
		return d.rootDir
	}
	return "/"
}

// dnsRedirectKey fingerprints what the firewall redirects ("" = nothing).
func dnsRedirectKey(spec firewall.Spec) string {
	if spec.DNSRedirectPort == 0 || len(spec.DNSResolverUIDs) == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%v/v6reject=%t/hijack=%t", spec.DNSRedirectPort, spec.DNSResolverUIDs, spec.DNSRejectV6, spec.HijackDNS)
}

// dnsWatchEvery is how often, between the polls, the loop asks xray's DNS
// inbound whether it still answers while the data plane sends the router's
// lookups to it (dnsWatchDue). At the polls' pace alone a crashed xray cost the
// LAN every name for dnsDeadAfter minutes, while TPROXY itself falls through to
// the open path within a second: measured on 1111 on 2026-10-04, xray killed
// in a loop, 90 s without a single lookup before the redirect went.
const dnsWatchEvery = 3 * time.Second

// dnsPutBackRetry is how long a put-back that did not get the redirect in
// waits before the next.
const dnsPutBackRetry = 30 * time.Second

// dnsFastDeadAfter is how many of those checks in a row xray may not answer
// before the redirect goes: about ten seconds, longer than a reload or a
// supervised restart takes.
const dnsFastDeadAfter = 3

// dnsWatchDue: the loaded data plane is to be programmed again now, between
// the polls — the redirect it holds leads to an inbound that has not answered
// dnsFastDeadAfter checks in a row, or this watch took it out and xray answers
// again. Cheap while nothing changes: one query to the inbound it redirects
// to, or nothing at all.
func (d *daemon) dnsWatchDue(ctx context.Context) bool {
	if d.desired == nil || !d.supStarted || d.fwProgrammed == nil {
		return false
	}
	// Direct mode has no data plane to program: loaded again here, it would
	// run the LAN back into the tunnel the rescue left. Under the kill switch
	// the router's lookups must not leave the tunnel either; the polls decide
	// there, as before.
	if d.rescueState().Mode == rescue.ModeDirect || d.killSwitchArmed() {
		return false
	}
	if port, ok := redirectPort(*d.fwProgrammed); ok {
		if d.answers(ctx, port) {
			d.dnsFastMisses = 0
			return false
		}
		d.dnsFastMisses++
		if d.dnsFastMisses < dnsFastDeadAfter {
			return false
		}
		d.dnsFastMisses = 0
		d.dnsFailedOpen = true
		logging.L().Warn("xray stopped answering on its DNS inbound; the router's resolver asks over the open path until it answers again", "port", port)
		return true
	}
	// Back only once xray has run xraySettle: a crash loop answers for a
	// second at each start, and each put-back costs two loads of the whole
	// table (and its direct sets) on a 234 MB router.
	if !d.dnsFailedOpen || !d.xraySettled(time.Now()) || time.Now().Before(d.dnsPutBackAfter) {
		return false
	}
	port, _, ok := d.dnsCandidate()
	if !ok {
		d.dnsFailedOpen = false
		return false
	}
	if !d.answers(ctx, port) {
		return false
	}
	d.dnsFailedOpen = false
	logging.L().Info("xray answers on its DNS inbound again; the router's resolver asks through the tunnel", "port", port)
	return true
}

// redirectPort is the port a dnsRedirectKey redirects to; ok is false when it
// redirects nothing.
func redirectPort(key string) (int, bool) {
	head, _, found := strings.Cut(key, "/")
	if !found {
		return 0, false
	}
	port, err := strconv.Atoi(head)
	return port, err == nil && port > 0
}

// dnsDeadAfter is how many loops in a row xray may not answer on its DNS
// inbound before the redirect is taken out: one miss can be a restart.
const dnsDeadAfter = 2

// dnsRedirectStale: the loaded data plane redirects other than what the
// router calls for now — a render with or without the inbound, dnsmasq as
// another user, xray answering again, or xray no longer answering (a crash
// loop, a restart that did not come back). A redirect to a port nothing
// answers on costs the router every lookup, the panel's included, and with the
// kill switch off a dead xray otherwise leaves the LAN online; so it is taken
// out after dnsDeadAfter loops, and put back as soon as xray answers.
func (d *daemon) dnsRedirectStale(ctx context.Context) bool {
	if d.desired == nil || !d.supStarted || d.fwProgrammed == nil {
		return false
	}
	want := ""
	if port, uids, ok := d.dnsCandidate(); ok {
		if d.answers(ctx, port) {
			d.dnsMisses = 0
			hijack := d.hijacksDNS()
			if hijack {
				d.hijackMisses = 0
			} else if strings.Contains(*d.fwProgrammed, "/hijack=true") {
				// One loop without dnsmasq's socket is a restart, most
				// likely: not worth two reprograms.
				d.hijackMisses++
				hijack = d.hijackMisses < dnsDeadAfter
			}
			want = dnsRedirectKey(firewall.Spec{DNSRedirectPort: port, DNSResolverUIDs: uids, DNSRejectV6: hasIPv4Upstream(d.etcRoot()), HijackDNS: hijack})
		} else {
			d.dnsMisses++
			if *d.fwProgrammed != "" && d.dnsMisses < dnsDeadAfter {
				return false
			}
		}
	}
	var fake firewall.Spec
	d.carryFakeDNS(&fake)
	return want+fakeDNSKey(fake) != *d.fwProgrammed
}

// carryFakeDNS takes the running render's FakeDNS pools out of the bypass
// sets, so connections to those addresses reach xray, which knows the domain
// each stands for. Bypassed (198.18.0.0/15 is in the default set), a client
// handed one would connect nowhere.
func (d *daemon) carryFakeDNS(spec *firewall.Spec) {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	pools := xray.RenderFakeDNSPools(raw)
	if len(pools) == 0 {
		return
	}
	spec.CarriedV4 = pools
	kept := spec.BypassV4[:0:0]
	for _, b := range spec.BypassV4 {
		if !overlapsAny(b, pools) {
			kept = append(kept, b)
		}
	}
	spec.BypassV4 = kept
}

// fakeDNSKey fingerprints the FakeDNS pools a spec carries ("" = none).
func fakeDNSKey(spec firewall.Spec) string {
	if len(spec.CarriedV4) == 0 {
		return ""
	}
	return ";fakedns=" + strings.Join(spec.CarriedV4, ",")
}

func overlapsAny(cidr string, pools []string) bool {
	_, a, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	for _, p := range pools {
		_, b, err := net.ParseCIDR(p)
		if err != nil {
			continue
		}
		if a.Contains(b.IP) || b.Contains(a.IP) {
			return true
		}
	}
	return false
}

// hasIPv4Upstream: dnsmasq has an IPv4 server to ask — the WAN's (netifd's
// resolv.conf.auto) or a general server= of its own. Only then are its IPv6
// upstream queries refused while DNS goes through the tunnel (the inbound is
// IPv4): with nothing but IPv6 servers, refusing them would leave the router
// no DNS at all, so they are left alone.
func hasIPv4Upstream(root string) bool {
	for _, f := range []string{"tmp/resolv.conf.d/resolv.conf.auto", "tmp/resolv.conf.auto"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "nameserver" && isIPv4(fields[1]) {
				return true
			}
		}
	}
	confs, _ := filepath.Glob(filepath.Join(root, "var/etc/dnsmasq.conf.*"))
	for _, f := range confs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), "server=")
			if !ok || strings.HasPrefix(v, "/") {
				continue // a server for some names only
			}
			if i := strings.IndexAny(v, "#@"); i >= 0 {
				v = v[:i]
			}
			if isIPv4(v) {
				return true
			}
		}
	}
	return false
}

func isIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}

// dnsInboundAnswers sends xray's DNS inbound a TXT query and waits for any
// answer. The DNS outbound answers a non-address query at once, itself —
// without the tunnel and without the network — so an answer means exactly
// "xray is up and its DNS inbound serves".
func dnsInboundAnswers(ctx context.Context, port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for attempt := 0; attempt < 2 && ctx.Err() == nil; attempt++ {
		c, err := net.DialTimeout("udp", addr, time.Second)
		if err != nil {
			return false
		}
		id := uint16(time.Now().UnixNano()) ^ uint16(attempt<<8)
		_ = c.SetDeadline(time.Now().Add(1500 * time.Millisecond))
		ok := false
		if _, err := c.Write(dnsTXTQuery(id, "vctl-probe.invalid")); err == nil {
			buf := make([]byte, 512)
			if n, err := c.Read(buf); err == nil && n >= 12 && binary.BigEndian.Uint16(buf) == id && buf[2]&0x80 != 0 {
				ok = true
			}
		}
		c.Close()
		if ok {
			return true
		}
	}
	return false
}

// dnsTXTQuery is a one-question DNS query for name's TXT records.
func dnsTXTQuery(id uint16, name string) []byte {
	b := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(b[0:], id)
	b[2] = 0x01 // RD
	binary.BigEndian.PutUint16(b[4:], 1)
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0, 0, 16, 0, 1) // root, TXT, IN
	return b
}

// resolverUIDs are the uids the running dnsmasq processes act as — what
// their upstream sockets carry. Root is left out: xray and vctl are root, and
// a redirect by root's uid would bring their own lookups back to xray.
func resolverUIDs(procRoot string) []int {
	dirs, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, e := range dirs {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "dnsmasq" {
			continue
		}
		uid, ok := effectiveUID(filepath.Join(procRoot, e.Name(), "status"))
		if !ok || uid == 0 || seen[uid] {
			continue
		}
		seen[uid] = true
		out = append(out, uid)
	}
	sort.Ints(out)
	return out
}

// resolverPIDs are the dnsmasq processes, by /proc/<pid>/comm.
func resolverPIDs(procRoot string) []int {
	dirs, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range dirs {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(comm)) == "dnsmasq" {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// resolverFlushDedupe: a flush for the same reason this soon after the last
// is not repeated.
const resolverFlushDedupe = 5 * time.Second

// flushResolverCache empties dnsmasq's cache — SIGHUP: it drops the cache and
// reads its hosts files again; DHCP and the leases are untouched. What xray
// answered while it carried the router's lookups leads nowhere once the
// tunnel is out of the way: the sites that go through it got FakeDNS
// addresses (198.18.x), and dnsmasq kept handing those out until they
// expired — Instagram and YouTube dead on the LAN with the internet up.
//
// The same reason again within resolverFlushDedupe is not repeated: one
// change of path, one flush, however many steps carry it out.
func (d *daemon) flushResolverCache(reason string) {
	now := time.Now()
	if reason == d.flushedFor && !d.flushedAt.IsZero() && now.Sub(d.flushedAt) < resolverFlushDedupe {
		return
	}
	d.flushedFor, d.flushedAt = reason, now
	hup := d.hupResolver
	if hup == nil {
		hup = func(pid int) error { return syscall.Kill(pid, syscall.SIGHUP) }
	}
	n := 0
	for _, pid := range resolverPIDs(d.procRoot()) {
		if hup(pid) == nil {
			n++
		}
	}
	if n > 0 {
		logging.L().Info("emptied the router's resolver cache", "reason", reason, "processes", n)
	}
}

// effectiveUID reads the effective uid from a /proc/<pid>/status
// ("Uid:	real	effective	saved	fs").
func effectiveUID(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 3 && fields[0] == "Uid:" {
			uid, err := strconv.Atoi(fields[2])
			return uid, err == nil
		}
	}
	return 0, false
}
