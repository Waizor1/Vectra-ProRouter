package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/firewall"
)

// renderWithBridgeQUIC: the DNS and FakeDNS of a real render, the Russian
// bridge's member with the udp443 flow, a main exit on plain Vision, and two
// names the bridge routes.
const renderWithBridgeQUIC = `{"inbounds":[{"tag":"tproxy-in","listen":"0.0.0.0","port":12345,"protocol":"dokodemo-door"},` +
	`{"tag":"vctl-dns-in","listen":"127.0.0.1","port":10053,"protocol":"dokodemo-door"}],` +
	`"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}],` +
	`"outbounds":[{"tag":"bridge-ru-tcp","protocol":"vless","settings":{"vnext":[{"address":"ru.example","port":40051,"users":[{"id":"x","flow":"xtls-rprx-vision-udp443"}]}]}},` +
	`{"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"de.example","port":443,"users":[{"id":"x","flow":"xtls-rprx-vision"}]}]}}],` +
	`"routing":{"rules":[{"domain":["geosite:youtube","domain:googlevideo.com"],"balancerTag":"BL-RU"},` +
	`{"domain":["domain:nnmclub.to","domain:ru"],"balancerTag":"BL-RU"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}],` +
	`"balancers":[{"tag":"BL-RU","selector":["bridge-ru-tcp"],"fallbackTag":"stage-main"},{"tag":"BL-MAIN","selector":["bridge-de5"]}]}}`

type bridgeQUICStand struct {
	*steer
	dirs     []string
	routes   map[string]string
	routeErr error
	asked    []api.RouteQuery
	restarts int
	flushes  int
}

func bridgeQUICDaemon(t *testing.T, render string) *bridgeQUICStand {
	t.Helper()
	s := &bridgeQUICStand{steer: steerDaemon(t, render)}
	root := t.TempDir()
	for _, inst := range []string{"cfg01411c", "guest"} {
		dir := filepath.Join(root, "dnsmasq."+inst+".d")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		s.dirs = append(s.dirs, dir)
	}
	defer func(g string) { t.Cleanup(func() { dnsmasqConfDirGlob = g }) }(dnsmasqConfDirGlob)
	dnsmasqConfDirGlob = filepath.Join(root, "dnsmasq.*.d")
	s.routes = map[string]string{"googlevideo.com": "bridge-ru-tcp", "nnmclub.to": "bridge-ru-tcp"}
	d := s.d
	d.supStarted = true
	// A fixed clock: the recheck window must not turn between two looks.
	fixed := time.Date(2026, 10, 26, 3, 1, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixed }
	d.testRoute = func(_ context.Context, server string, q []api.RouteQuery) ([]string, error) {
		if server != api.DefaultAPIServer {
			t.Errorf("asked %s, want the render's API", server)
		}
		s.asked = append(s.asked, q...)
		if s.routeErr != nil {
			return nil, s.routeErr
		}
		out := make([]string, len(q))
		for i, x := range q {
			out[i] = s.routes[x.Domain]
		}
		return out, nil
	}
	d.restartResolver = func(context.Context) error { s.restarts++; return nil }
	d.resolverAccepts = func(context.Context, string) error { return nil }
	defer func(w time.Duration) { t.Cleanup(func() { resolverSteadyAfter = w }) }(resolverSteadyAfter)
	resolverSteadyAfter = 20 * time.Millisecond
	d.runFirewallCmd = func(_ context.Context, name string, args ...string) error {
		if name == "nft" && strings.Join(args, " ") == "flush set inet vctl "+firewall.SetQUICPass4 {
			s.flushes++
		}
		return nil
	}
	return s
}

func (s *bridgeQUICStand) fragments(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range s.dirs {
		b, err := os.ReadFile(filepath.Join(dir, bridgeQUICFragment))
		if errors.Is(err, os.ErrNotExist) {
			out = append(out, "")
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

func TestTheBridgesNamesReachDnsmasqWhenXraySendsThemToTheBridge(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	if d.fwSpec == nil || !d.fwSpec.QUICPass {
		t.Fatalf("the table does not pass QUIC to the set: %+v", d.fwSpec)
	}

	d.syncBridgeQUIC(ctx)
	gv := "\nnftset=/googlevideo.com/4#inet#vctl#vctl_quic_pass4\n"
	nnm := "\nnftset=/nnmclub.to/4#inet#vctl#vctl_quic_pass4\n"
	for i, f := range s.fragments(t) {
		if !strings.Contains(f, gv) || !strings.Contains(f, nnm) {
			t.Fatalf("instance %d fragment = %q, want a line for each name", i, f)
		}
	}
	for _, q := range s.asked {
		if !q.UDP || q.Port != 443 || q.InboundTag != "tproxy-in" {
			t.Fatalf("asked %+v: QUIC from the LAN is UDP to 443 on the tproxy inbound", q)
		}
		if q.Domain == "ru" || q.Domain == "youtube" {
			t.Fatalf("asked about %q: not a name", q.Domain)
		}
	}
	if s.restarts != 1 || s.flushes != 1 {
		t.Fatalf("restarts %d, flushes %d; want one each for a new fragment", s.restarts, s.flushes)
	}

	// The same start again: nothing to do.
	d.syncBridgeQUIC(ctx)
	if s.restarts != 1 {
		t.Fatalf("the same start restarted dnsmasq again (%d)", s.restarts)
	}

	// The owner sent nnmclub.to through a country: xray says so at its next
	// start, and the name leaves.
	s.routes["nnmclub.to"] = "bridge-de5"
	s.started = time.Now().Add(-bridgeQUICSettle)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, gv) || strings.Contains(f, nnm) {
		t.Fatalf("fragment = %q, want only googlevideo.com", f)
	}
	// One flush for the new xray (its FakeDNS addresses are handed out
	// anew), one for the name that left.
	if s.restarts != 2 || s.flushes != 3 {
		t.Fatalf("restarts %d, flushes %d; the set must be emptied when a name leaves", s.restarts, s.flushes)
	}

	// The data plane goes: so does the fragment.
	d.tearDownFirewall(ctx)
	for i, f := range s.fragments(t) {
		if f != "" {
			t.Fatalf("instance %d keeps its fragment after the teardown: %q", i, f)
		}
	}
	if s.restarts != 3 {
		t.Fatalf("dnsmasq not restarted after the fragment left (%d)", s.restarts)
	}
}

func TestNoNameLandsOnTheBridgeNoFragment(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	s.routes = map[string]string{"googlevideo.com": "bridge-de5"} // e.g. YouTube on a country
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	for i, f := range s.fragments(t) {
		if f != "" {
			t.Fatalf("instance %d got a fragment though nothing goes to the bridge: %q", i, f)
		}
	}
	if s.restarts != 0 {
		t.Fatalf("dnsmasq restarted for nothing (%d)", s.restarts)
	}
}

func TestAnUnansweredAPIIsAskedAgainAndChangesNothing(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	s.routeErr = errors.New("connection refused")
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" || s.restarts != 0 {
		t.Fatalf("fragment %q, restarts %d: nothing may change without an answer", f, s.restarts)
	}
	s.routeErr = nil
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatalf("the next look did not ask again: %q", f)
	}
}

func TestQuicBridgeOffKeepsR30sRefusal(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	d.cfg.NoQUICBridge = true
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	if d.fwSpec == nil || d.fwSpec.QUICPass {
		t.Fatalf("quic_bridge '0' and the table passes QUIC: %+v", d.fwSpec)
	}
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" || len(s.asked) != 0 {
		t.Fatalf("fragment %q, asked %v: nothing with quic_bridge '0'", f, s.asked)
	}
}

func TestAPlainVisionBridgePassesNoQUIC(t *testing.T) {
	render := strings.Replace(renderWithBridgeQUIC, `"flow":"xtls-rprx-vision-udp443"`, `"flow":"xtls-rprx-vision"`, 1)
	s := bridgeQUICDaemon(t, render)
	d := s.d
	d.programFirewall(context.Background(), d.desired)
	if d.fwSpec == nil || d.fwSpec.QUICPass {
		t.Fatalf("a bridge on plain Vision drops UDP/443, and the table passes QUIC: %+v", d.fwSpec)
	}
}

// BL-RU with no member alive sends its names to its fallback (the main path,
// plain Vision). That is no reason to restart dnsmasq: the names keep their
// pass for bridgeQUICHold, lose it after, and come back with the bridge.
func TestABridgeOnItsFallbackKeepsItsNamesForAWhile(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatalf("fragment %q", f)
	}
	restarts := s.restarts

	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	asked := len(s.asked)
	now = now.Add(time.Minute)
	d.syncBridgeQUIC(ctx)
	if len(s.asked) != asked {
		t.Fatal("asked again before bridgeQUICRecheck")
	}
	now = now.Add(bridgeQUICRecheck)
	d.syncBridgeQUIC(ctx)
	now = now.Add(bridgeQUICHold - time.Minute)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") || s.restarts != restarts {
		t.Fatalf("fragment %q, restarts %d -> %d: the fallback within bridgeQUICHold changes nothing", f, restarts, s.restarts)
	}
	if d.bridgeQUICWaiting != bridgeQUICOnFallback {
		t.Fatalf("waiting = %q, want %q (asked again every bridgeQUICRetry)", d.bridgeQUICWaiting, bridgeQUICOnFallback)
	}
	now = now.Add(time.Minute)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" || s.restarts != restarts+1 {
		t.Fatalf("fragment %q, restarts %d: a bridge down for bridgeQUICHold gives its names up", f, s.restarts-restarts)
	}

	s.routes = map[string]string{"googlevideo.com": "bridge-ru-tcp", "nnmclub.to": "bridge-ru-tcp"}
	now = now.Add(bridgeQUICRetry)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatalf("the bridge is back and its names are not, %s later: %q", bridgeQUICRetry, f)
	}
}

// A fresh xray sends BL-RU's names to the fallback until the observatory's
// first round (leastLoad knows no member alive before it): a restart of xray
// leaves dnsmasq alone.
func TestAnXrayRestartLeavesDnsmasqAlone(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	restarts := s.restarts

	s.started = s.started.Add(time.Hour - bridgeQUICSettle)
	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	now = now.Add(time.Second)
	d.syncBridgeQUIC(ctx)
	s.routes = map[string]string{"googlevideo.com": "bridge-ru-tcp", "nnmclub.to": "bridge-ru-tcp"}
	now = now.Add(bridgeQUICRetry)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") || s.restarts != restarts {
		t.Fatalf("fragment %q, restarts %d -> %d: an xray restart must not take the names out and back", f, restarts, s.restarts)
	}
	if d.bridgeQUICWaiting != "" {
		t.Fatalf("waiting = %q with the names through", d.bridgeQUICWaiting)
	}
}

// The first start: the names reach dnsmasq within bridgeQUICRetry of the
// observatory seeing the bridge, not at the next bridgeQUICRecheck.
func TestNamesAFreshBalancerHoldsBackArriveWithinTheRetry(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" || s.restarts != 0 {
		t.Fatalf("fragment %q, restarts %d while the balancer has no member alive", f, s.restarts)
	}
	if d.bridgeQUICWaiting != bridgeQUICOnFallback {
		t.Fatalf("waiting = %q, want %q", d.bridgeQUICWaiting, bridgeQUICOnFallback)
	}
	asked := len(s.asked)
	now = now.Add(time.Second)
	d.syncBridgeQUIC(ctx)
	if len(s.asked) != asked {
		t.Fatal("asked again within the same retry window")
	}

	s.routes = map[string]string{"googlevideo.com": "bridge-ru-tcp", "nnmclub.to": "bridge-ru-tcp"}
	now = now.Add(bridgeQUICRetry)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") || s.restarts != 1 {
		t.Fatalf("fragment %q, restarts %d: the bridge is alive %s after the start and its names are not through", f, s.restarts, bridgeQUICRetry+time.Second)
	}
	if d.bridgeQUICWaiting != "" {
		t.Fatalf("waiting = %q with the names through", d.bridgeQUICWaiting)
	}
}

func TestTheFragmentIsWrittenOnlyWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dnsmasq.a.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(g string) { dnsmasqConfDirGlob = g }(dnsmasqConfDirGlob)
	dnsmasqConfDirGlob = filepath.Join(dir, "dnsmasq.*.d")
	for i, tc := range []struct {
		names []string
		want  bool
	}{{[]string{"b.example", "a.example"}, true}, {[]string{"a.example", "b.example"}, false}, {nil, true}, {nil, false}} {
		got, err := writeBridgeQUICFragment(tc.names, "vctl")
		if err != nil || got != tc.want {
			t.Fatalf("step %d (%v): changed=%v err=%v, want changed=%v", i, tc.names, got, err, tc.want)
		}
	}
	// No instance directory: nothing written, nothing changed.
	dnsmasqConfDirGlob = filepath.Join(dir, "none.*.d")
	if got, err := writeBridgeQUICFragment([]string{"a.example"}, "vctl"); got || err != nil {
		t.Fatalf("changed=%v err=%v without a directory", got, err)
	}
}

// dnsmasq reads a config line in at most 1024 bytes and does not start with
// the rest of a longer one; the provider's names on one line came to 1.6 KB.
func TestEveryNameHasALineOfItsOwn(t *testing.T) {
	var names []string
	for i := 0; i < 200; i++ {
		names = append(names, fmt.Sprintf("a-rather-long-name-%03d.some-provider-site.example", i))
	}
	frag := bridgeQUICLines(names, "vctl")
	lines := strings.Split(strings.TrimSuffix(frag, "\n"), "\n")
	if len(lines) != len(names)+1 {
		t.Fatalf("%d lines for %d names", len(lines), len(names))
	}
	for _, l := range lines {
		if len(l) >= 1000 {
			t.Fatalf("a line of %d bytes: %q", len(l), l)
		}
	}
}

// No FakeDNS (russia_direct, dns_tunnel or direct_bypass '0'): no pool, no
// refusal, and no set in the table for dnsmasq to name — so no pass either.
func TestNoFakeDNSNoQUICPass(t *testing.T) {
	render := strings.Replace(renderWithBridgeQUIC, `"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}],`, "", 1)
	if render == renderWithBridgeQUIC {
		t.Fatal("fixture changed: no fakedns section to take out")
	}
	s := bridgeQUICDaemon(t, render)
	d := s.d
	d.programFirewall(context.Background(), d.desired)
	if d.fwSpec == nil || d.fwSpec.QUICPass {
		t.Fatalf("QUICPass without a FakeDNS pool: %+v", d.fwSpec)
	}
	d.syncBridgeQUIC(context.Background())
	if f := s.fragments(t)[0]; f != "" {
		t.Fatalf("a fragment naming a set the table does not have: %q", f)
	}
}

// A dnsmasq that does not come back with the fragment loses it and starts
// without it, rather than leave the LAN without DNS and DHCP; the fragment
// stays out for the rest of the run.
func TestADnsmasqThatDoesNotComeBackLosesTheFragment(t *testing.T) {
	defer func(w time.Duration) { resolverBackWithin = w }(resolverBackWithin)
	resolverBackWithin = 50 * time.Millisecond
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	alive, gone := d.procDir, t.TempDir()
	d.restartResolver = func(context.Context) error {
		s.restarts++
		if s.restarts == 1 {
			d.procDir = gone // dnsmasq refuses the fragment
		} else {
			d.procDir = alive // and starts without it
		}
		return nil
	}
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" {
		t.Fatalf("the fragment dnsmasq did not start with is still there: %q", f)
	}
	if s.restarts != 2 || !d.bridgeQUICBroken {
		t.Fatalf("restarts %d, broken %v: dnsmasq must be started again without the fragment", s.restarts, d.bridgeQUICBroken)
	}
	s.started = s.started.Add(time.Minute)
	d.syncBridgeQUIC(ctx)
	d.programFirewall(ctx, d.desired)
	if f := s.fragments(t)[0]; f != "" || d.fwSpec.QUICPass {
		t.Fatalf("fragment %q, QUICPass %v: it stays out until vctl starts again", f, d.fwSpec.QUICPass)
	}
}

// A dnsmasq that does not take the fragment (built without nftset) is never
// restarted on it: no fragment, QUIC refused as in r30.
func TestAFragmentDnsmasqRefusesIsNeverWritten(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	var checked string
	d.resolverAccepts = func(_ context.Context, frag string) error {
		checked = frag
		return errors.New("dnsmasq: recompile with HAVE_NFTSET defined to enable nftset directives")
	}
	d.programFirewall(context.Background(), d.desired)
	d.syncBridgeQUIC(context.Background())
	if !strings.Contains(checked, "nftset=/googlevideo.com/") {
		t.Fatalf("checked %q, want the fragment it would write", checked)
	}
	if f := s.fragments(t)[0]; f != "" || s.restarts != 0 || !d.bridgeQUICBroken {
		t.Fatalf("fragment %q, restarts %d, broken %v", f, s.restarts, d.bridgeQUICBroken)
	}
}

// dnsmasq is not changed under a fresh xray, which resolves its nodes' names
// through it (the stand's tunnel went down to a restart 3 s after xray's).
func TestDnsmasqWaitsForXrayToSettle(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	s.started = time.Now().Add(-bridgeQUICSettle + 300*time.Millisecond)
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" || s.restarts != 0 || len(s.asked) != 0 {
		t.Fatalf("fragment %q, restarts %d, asked %d under an xray younger than %s", f, s.restarts, len(s.asked), bridgeQUICSettle)
	}
	time.Sleep(400 * time.Millisecond)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") || s.restarts != 1 {
		t.Fatalf("fragment %q, restarts %d once xray has settled", f, s.restarts)
	}
}

// A dnsmasq that is not running (the owner's own resolver) is not started:
// it reads the fragment whenever it does.
func TestADnsmasqThatIsNotRunningIsNotStarted(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	d.procDir = t.TempDir()
	d.programFirewall(context.Background(), d.desired)
	d.syncBridgeQUIC(context.Background())
	if s.restarts != 0 {
		t.Fatalf("dnsmasq started (%d) though it was not running", s.restarts)
	}
}

// Going direct takes the table out first, then restarts dnsmasq and waits for
// it: the rescue's flush of the resolver's cache, next, must not land in the
// middle of the restart.
func TestTheTeardownTakesTheTableFirstThenRestartsDnsmasq(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	var events []string
	d.runFirewallCmd = func(_ context.Context, name string, args ...string) error {
		events = append(events, name+" "+strings.Join(args, " "))
		return nil
	}
	waited := false
	d.restartResolver = func(c context.Context) error {
		events = append(events, "restart dnsmasq")
		waited = c.Err() == nil
		return nil
	}
	d.tearDownFirewall(ctx)
	if len(events) < 2 || events[len(events)-1] != "restart dnsmasq" {
		t.Fatalf("events %q: the table goes before dnsmasq restarts", events)
	}
	if !waited {
		t.Fatal("the teardown did not wait for dnsmasq's restart")
	}
}

// /etc/init.d/dnsmasq's comm is "dnsmasq" too: only the resolver itself is
// one — a SIGHUP for its cache once killed the init script mid-restart.
func TestResolverPIDsAreTheResolversNotItsInitScript(t *testing.T) {
	root := fakeProc(t, map[string][2]string{
		"11": {"dnsmasq", "453\t453\t453\t453"},
		"12": {"dnsmasq", "0\t0\t0\t0"},
		"13": {"dnsmasq", "453\t453\t453\t453"},
		"14": {"dnsmasq", "453\t453\t453\t453"},
	})
	for pid, exe := range map[string]string{"11": "/usr/sbin/dnsmasq", "12": "/bin/busybox", "13": "/usr/sbin/dnsmasq (deleted)"} {
		if err := os.Symlink(exe, filepath.Join(root, pid, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	got := resolverPIDs(root)
	if fmt.Sprint(got) != "[11 13 14]" {
		t.Fatalf("resolverPIDs = %v, want [11 13 14] (14 has no exe to read: by comm)", got)
	}
}

// A new xray hands its FakeDNS addresses out anew: the set is emptied at its
// start, while the fragment, and dnsmasq, stay.
func TestANewXrayEmptiesThePassSet(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	restarts, flushes := s.restarts, s.flushes
	s.started = s.started.Add(time.Minute)
	d.syncBridgeQUIC(ctx)
	if s.flushes != flushes+1 || s.restarts != restarts {
		t.Fatalf("flushes %d -> %d, restarts %d -> %d: one flush, no restart", flushes, s.flushes, restarts, s.restarts)
	}
}

// The hold runs on across xray's starts: a bridge down for hours, under an
// xray restarted every few minutes, still gives its names up.
func TestTheHoldRunsOnAcrossXrayStarts(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	for i := 0; i < 4; i++ {
		now = now.Add(10 * time.Minute)
		s.started = s.started.Add(10 * time.Minute)
		d.syncBridgeQUIC(ctx)
	}
	if f := s.fragments(t)[0]; f != "" {
		t.Fatalf("40 min on the fallback over four xray starts, and the names keep their pass: %q", f)
	}
}

// vctl's own restart of dnsmasq is no resolver that changed user: the poll
// must not reload the table for it.
func TestVctlsOwnDnsmasqRestartIsNoStaleRedirect(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.programFirewall(context.Background(), d.desired)
	d.procDir = t.TempDir() // dnsmasq between its stop and its start
	if !d.dnsRedirectStale(context.Background()) {
		t.Fatal("fixture: a dnsmasq gone should read as stale without the grace")
	}
	d.resolverRestartedAt = now.Add(-time.Second)
	if d.dnsRedirectStale(context.Background()) {
		t.Fatal("vctl's own restart of dnsmasq read as a stale redirect")
	}
	now = now.Add(resolverRestartGrace)
	if !d.dnsRedirectStale(context.Background()) {
		t.Fatal("past the grace, a dnsmasq still gone is stale")
	}
}
