package main

import (
	"context"
	"errors"
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
	`"balancers":[{"tag":"BL-RU","selector":["bridge-ru-tcp"]},{"tag":"BL-MAIN","selector":["bridge-de5"]}]}}`

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
	want := "nftset=/googlevideo.com/nnmclub.to/4#inet#vctl#vctl_quic_pass4\n"
	for i, f := range s.fragments(t) {
		if !strings.HasSuffix(f, want) {
			t.Fatalf("instance %d fragment = %q, want the line %q", i, f, want)
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
	s.started = time.Now()
	d.syncBridgeQUIC(ctx)
	want = "nftset=/googlevideo.com/4#inet#vctl#vctl_quic_pass4\n"
	if f := s.fragments(t)[0]; !strings.HasSuffix(f, want) {
		t.Fatalf("fragment = %q, want only googlevideo.com", f)
	}
	if s.restarts != 2 || s.flushes != 2 {
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

// BL-RU with no live member falls back to the main path (plain Vision): xray
// is asked again every bridgeQUICRecheck, the names leave while the fallback
// carries them and come back with the bridge.
func TestABridgeOnItsFallbackLosesItsNamesAtTheNextLook(t *testing.T) {
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

	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	now = now.Add(time.Minute)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatal("asked again before bridgeQUICRecheck")
	}
	now = now.Add(bridgeQUICRecheck)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" {
		t.Fatalf("the fallback carries the names and they keep their pass: %q", f)
	}

	s.routes = map[string]string{"googlevideo.com": "bridge-ru-tcp", "nnmclub.to": "bridge-ru-tcp"}
	now = now.Add(bridgeQUICRecheck)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatalf("the bridge is back and its names are not: %q", f)
	}
}

// Right after xray starts BL-RU may still send its names to the fallback,
// until the observatory has seen the bridge alive: xray is asked again within
// bridgeQUICRetry, not at the next bridgeQUICRecheck.
func TestNamesAFreshBalancerHoldsBackArriveWithinTheRetry(t *testing.T) {
	s := bridgeQUICDaemon(t, renderWithBridgeQUIC)
	d := s.d
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	s.routes = map[string]string{"googlevideo.com": "stage-main", "nnmclub.to": "stage-main"}
	d.programFirewall(ctx, d.desired)
	d.syncBridgeQUIC(ctx)
	if f := s.fragments(t)[0]; f != "" {
		t.Fatalf("fragment %q while the balancer sends the names elsewhere", f)
	}
	if d.bridgeQUICWaiting != "stage-main" {
		t.Fatalf("waiting = %q, want where xray sent the names", d.bridgeQUICWaiting)
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
	if f := s.fragments(t)[0]; !strings.Contains(f, "googlevideo.com") {
		t.Fatalf("the bridge is alive %s after the start and its names are not through: %q", bridgeQUICRetry+time.Second, f)
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
