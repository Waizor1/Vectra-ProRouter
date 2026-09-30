package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/logging"
)

const failoverRender = `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService"]},
"metrics":{"tag":"metrics","listen":"127.0.0.1:10086"},
"outbounds":[
{"tag":"bridge-nl5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":50055,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"bridge-pl5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"DIRECT","protocol":"freedom"}],
"routing":{"rules":[{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["bridge-"],"strategy":{"type":"leastPing"}}]},
"burstObservatory":{"subjectSelector":["bridge-"],"pingConfig":{"destination":"http://probe.example/generate_204","interval":"300s","timeout":"10s"}}}`

type failoverFakes struct {
	entries     []conntrack.Entry
	ctErr       error
	info        map[string]api.BalancerInfo
	overrides   []string
	overrideErr []error // consumed in order: the API refusing
	confirmed   int
	confirmAt   string
	obs         map[string]api.Observation // the observatory; nil: every node alive
}

func (f *failoverFakes) install(t *testing.T) {
	t.Helper()
	oc, ob, oo, om, of, ol := failoverConntrack, failoverBalancers, failoverOverride, failoverMetrics, failoverConfirm, failoverLocalAddrs
	t.Cleanup(func() {
		failoverConntrack, failoverBalancers, failoverOverride, failoverMetrics, failoverConfirm, failoverLocalAddrs = oc, ob, oo, om, of, ol
	})
	failoverConntrack = func() ([]conntrack.Entry, error) { return f.entries, f.ctErr }
	failoverLocalAddrs = func() (map[netip.Addr]bool, error) {
		return map[netip.Addr]bool{netip.MustParseAddr("198.51.100.7"): true}, nil
	}
	failoverBalancers = func(_ context.Context, _ string, _ []string) (map[string]api.BalancerInfo, error) {
		return f.info, nil
	}
	failoverOverride = func(_ context.Context, _, balancer, target string) error {
		f.overrides = append(f.overrides, balancer+"→"+target)
		if len(f.overrideErr) > 0 {
			err := f.overrideErr[0]
			f.overrideErr = f.overrideErr[1:]
			return err
		}
		return nil
	}
	failoverMetrics = func(_ context.Context, _ string) (*api.Metrics, error) {
		if f.obs != nil {
			return &api.Metrics{Observatory: f.obs}, nil
		}
		return &api.Metrics{Observatory: map[string]api.Observation{
			"bridge-nl5": {Alive: true, DelayMs: 40}, "bridge-pl5": {Alive: true, DelayMs: 60}, "bridge-de5": {Alive: true, DelayMs: 70}}}, nil
	}
	failoverConfirm = func(_ context.Context, url string) bool { f.confirmed++; f.confirmAt = url; return true }
}

func unanswered(sport uint16, dst string) conntrack.Entry {
	ap := netip.MustParseAddrPort(dst)
	return conntrack.Entry{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: ap.Addr(), SPort: sport, DPort: ap.Port()}
}

func failoverDaemon(t *testing.T) *daemon {
	t.Helper()
	dir := t.TempDir()
	render := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(render, []byte(failoverRender), 0o644); err != nil {
		t.Fatal(err)
	}
	return &daemon{cfg: agentcfg.Config{XrayRenderPath: render, OverridesPath: filepath.Join(dir, "overrides.json")},
		incidents: incident.NewRecorder(incident.Dir, time.Minute)}
}

func TestTheWatchdogMovesTheMainBalancerOffADeadNode(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:    map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if len(f.overrides) != 1 || f.overrides[0] != "BL-MAIN→bridge-pl5" || f.confirmed != 1 {
		t.Fatalf("overrides %v confirmed %d", f.overrides, f.confirmed)
	}
	found := false
	for _, p := range incident.Read(incident.Dir) {
		found = found || (p.Code == "PROXY_FAILOVER" && strings.Contains(p.Key, "bridge-nl5"))
	}
	if !found {
		t.Fatal("no PROXY_FAILOVER incident")
	}
}

func TestTheWatchdogSwitchedOffDoesNothing(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:    map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	d.cfg.NoFailoverWatchdog = true
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if len(f.overrides) != 0 {
		t.Fatalf("switched off, still moved: %v", f.overrides)
	}
}

// A new render (another entry, a provider refresh) restarts xray without
// overrides: the watchdog forgets its own and never re-points a balancer at a
// node the new render does not have.
func TestTheWatchdogForgetsWhatANewRenderLeftBehind(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:    map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	next := strings.ReplaceAll(strings.ReplaceAll(failoverRender, "bridge-nl5", "bridge-de5"), "203.0.113.5", "203.0.113.9")
	next = strings.Replace(next, `"routing":{`, `"routing":{"domainStrategy":"AsIs",`, 1)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	f.entries = nil
	f.info = map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-de5"}}}
	f.overrides = nil
	d.failoverTick(context.Background(), w, t0.Add(4*time.Second))
	if len(f.overrides) != 0 {
		t.Fatalf("re-pointed after the new render: %v", f.overrides)
	}
}

// The one request that confirms a move asks what the provider's own
// observatory asks of a node — its destination, in the render.
func TestTheWatchdogConfirmsWithTheProvidersOwnProbe(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:    map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if f.confirmed != 1 || f.confirmAt != "http://probe.example/generate_204" {
		t.Fatalf("confirmed %d at %q", f.confirmed, f.confirmAt)
	}
}

// Like the observatory, any answer that came back through the tunnel is the
// path working; a connection closed without one is not.
func TestAnyAnswerThroughTheTunnelConfirms(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if !confirmThroughTheTunnel(context.Background(), ok.URL+"/probe") {
		t.Fatal("a 200 through the tunnel did not confirm")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if confirmThroughTheTunnel(context.Background(), "http://"+ln.Addr().String()+"/probe") {
		t.Fatal("a connection closed without an answer confirmed")
	}
}

// Only the router's own connections are xray's dials: the owner's phone
// reaching the dead node's address through the router is answered by xray's
// transparent proxy, and must not hide the node's death.
func TestTheWatchdogTakesOnlyTheRoutersOwnConnectionsAsEvidence(t *testing.T) {
	phone := unanswered(9, "203.0.113.5:50055")
	phone.Src, phone.Replied, phone.State = netip.MustParseAddr("192.168.1.50"), true, "ESTABLISHED"
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:    map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	f.entries = append(f.entries, phone)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if len(f.overrides) != 1 || f.overrides[0] != "BL-MAIN→bridge-pl5" {
		t.Fatalf("overrides %v", f.overrides)
	}
}

// xray refused the move: nothing is confirmed or reported, and the next look
// asks again.
func TestAnOverrideXrayRefusedIsAskedAgain(t *testing.T) {
	f := &failoverFakes{
		entries:     []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info:        map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
		overrideErr: []error{errors.New("rpc error: context deadline exceeded")},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if f.confirmed != 0 {
		t.Fatalf("confirmed a move xray refused")
	}
	d.failoverTick(context.Background(), w, t0.Add(4*time.Second))
	if len(f.overrides) != 2 || f.overrides[1] != "BL-MAIN→bridge-pl5" || f.confirmed != 1 {
		t.Fatalf("overrides %v confirmed %d", f.overrides, f.confirmed)
	}
}

// A watchdog that cannot see says so — once, not every 2 s — and says when
// it sees again.
func TestTheWatchdogSaysOnceWhyItCannotSee(t *testing.T) {
	var buf bytes.Buffer
	prev := logging.L()
	logging.SetDefault(logging.New("info", &buf, "text"))
	t.Cleanup(func() { logging.SetDefault(prev) })
	f := &failoverFakes{
		ctErr: errors.New("no ctnetlink and no /proc/net/nf_conntrack"),
		info:  map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	w := newFailoverWatch()
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		d.failoverTick(context.Background(), w, t0.Add(time.Duration(2*i)*time.Second))
	}
	if n := strings.Count(buf.String(), "cannot read the connection table"); n != 1 {
		t.Fatalf("said %d times:\n%s", n, buf.String())
	}
	f.ctErr = nil
	d.failoverTick(context.Background(), w, t0.Add(6*time.Second))
	if !strings.Contains(buf.String(), "failover watchdog sees again") {
		t.Fatalf("recovery not said:\n%s", buf.String())
	}
}

// The «Авто» entry as served on 2026-09-30: BL-MAIN is one node, its
// fallback the entry's own stage. The watchdog puts the balancer on that
// stage at once — the router 1111's drill showed xray itself staying on the
// dead node for as long as the observatory still held it alive.
const failoverRenderOneNode = `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService"]},
"metrics":{"tag":"metrics","listen":"127.0.0.1:10086"},
"outbounds":[
{"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":50055,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"bridge-pl5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"stage-bridge","protocol":"loopback","settings":{"inboundTag":"STAGE_BRIDGE"}},
{"tag":"DIRECT","protocol":"freedom"}],
"routing":{"rules":[{"inboundTag":["STAGE_BRIDGE"],"balancerTag":"BL-BRIDGE"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["sticky-de5"],"strategy":{"type":"leastPing"},"fallbackTag":"stage-bridge"},
{"tag":"BL-BRIDGE","selector":["bridge-pl5"],"strategy":{"type":"leastPing"},"fallbackTag":"DIRECT"}]},
"burstObservatory":{"subjectSelector":["sticky-","bridge-"],"pingConfig":{"destination":"https://cp.cloudflare.com/generate_204","interval":"300s","timeout":"10s"}}}`

func TestTheWatchdogPutsAOneNodeBalancerOnItsFallbackStage(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.5:50055"), unanswered(2, "203.0.113.5:50055")},
		info: map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"sticky-de5"}},
			"BL-BRIDGE": {Principle: []string{"bridge-pl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(failoverRenderOneNode), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newFailoverWatch()
	t0 := time.Now()
	d.failoverTick(context.Background(), w, t0)
	d.failoverTick(context.Background(), w, t0.Add(2*time.Second))
	if len(f.overrides) != 1 || f.overrides[0] != "BL-MAIN→stage-bridge" || f.confirmed != 1 {
		t.Fatalf("overrides %v confirmed %d", f.overrides, f.confirmed)
	}
}

// The stand's PassWall race (pw_race_restart), 30.09: a balancer whose lone
// node looked failing was pointed at its fallback DIRECT at once, and nothing
// dialled the node again to bring it back — the LAN left through no node. A
// fallback that carries nothing through a node (direct, a black hole) is
// xray's own last resort once its observatory holds every node dead, never
// the watchdog's to force on connection evidence.
func TestTheWatchdogNeverParksABalancerOnDirect(t *testing.T) {
	f := &failoverFakes{
		entries: []conntrack.Entry{unanswered(1, "203.0.113.7:443"), unanswered(2, "203.0.113.7:443")},
		info: map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"sticky-de5"}},
			"BL-BRIDGE": {Principle: []string{"bridge-pl5"}}},
	}
	f.install(t)
	d := failoverDaemon(t)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(failoverRenderOneNode), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newFailoverWatch()
	t0 := time.Now()
	for i := 0; i < 4; i++ {
		d.failoverTick(context.Background(), w, t0.Add(time.Duration(i)*2*time.Second))
	}
	if len(f.overrides) != 0 {
		t.Fatalf("the watchdog parked a balancer on a plain outbound: %v", f.overrides)
	}
}

// 1111, 2026-09-30 04:00: the entry's German node and its German bridge stop
// answering the router (a tracker storm got the address cut off), the
// observatory holds both dead, and BL-MAIN has nowhere to go — while the
// entry's Belarusian node carries TikTok fine. The watchdog lends it to
// BL-MAIN; never the Russian exit, faster as it is: it reaches nothing a VPN
// is for.
const failoverRenderBorrow = `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService"]},
"metrics":{"tag":"metrics","listen":"127.0.0.1:10086"},
"outbounds":[
{"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.6","port":40052,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"sticky-by5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"bridge-ru-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":40051,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"stage-bridge","protocol":"loopback","settings":{"inboundTag":"STAGE_BRIDGE"}},
{"tag":"DIRECT","protocol":"freedom"}],
"routing":{"rules":[{"inboundTag":["STAGE_BRIDGE"],"balancerTag":"BL-BRIDGE"},
{"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},
{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["sticky-de5"],"strategy":{"type":"leastPing"},"fallbackTag":"stage-bridge"},
{"tag":"BL-BRIDGE","selector":["bridge-de5"],"strategy":{"type":"leastPing"}},
{"tag":"BL-TK","selector":["sticky-by5"],"strategy":{"type":"leastPing"}},
{"tag":"BL-RU","selector":["bridge-ru-tcp"],"strategy":{"type":"leastPing"}}]},
"burstObservatory":{"subjectSelector":["sticky-","bridge-"],"pingConfig":{"destination":"https://cp.cloudflare.com/generate_204","interval":"300s","timeout":"10s"}}}`

func TestTheWatchdogLendsTheMainBalancerAnotherCountryWhenItsWholePathIsDead(t *testing.T) {
	f := &failoverFakes{
		info: map[string]api.BalancerInfo{"BL-MAIN": {}, "BL-BRIDGE": {},
			"BL-TK": {Principle: []string{"sticky-by5"}}, "BL-RU": {Principle: []string{"bridge-ru-tcp"}}},
		obs: map[string]api.Observation{"sticky-de5": {Alive: false}, "bridge-de5": {Alive: false},
			"sticky-by5": {Alive: true, DelayMs: 359}, "bridge-ru-tcp": {Alive: true, DelayMs: 116}},
	}
	f.install(t)
	d := failoverDaemon(t)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(failoverRenderBorrow), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newFailoverWatch()
	d.failoverTick(context.Background(), w, time.Now())
	if len(f.overrides) != 1 || f.overrides[0] != "BL-MAIN→sticky-by5" || f.confirmed != 1 {
		t.Fatalf("overrides %v confirmed %d", f.overrides, f.confirmed)
	}
	// The owner's server card is told (spec decision 6).
	if r := d.route.Load(); r == nil || r.Balancer != "BL-MAIN" || r.Override != "sticky-by5" || r.MovedFrom != "sticky-de5" || r.Reason != "borrowed" {
		t.Fatalf("route %+v", r)
	}
	found := false
	for _, p := range incident.Read(incident.Dir) {
		found = found || (p.Code == "PROXY_FAILOVER" && strings.Contains(p.Title, "sticky-by5"))
	}
	if !found {
		t.Fatal("no PROXY_FAILOVER incident naming the lent node")
	}
}

// The exit check found the entry's American exit unfit (1111, 2026-09-30:
// no blocked site through it). Faster than the Belarusian one, it is still
// never what the watchdog lends BL-MAIN; and the owner's server card names
// it, because no balancer of the render selects it any more.
const failoverRenderBorrowUnfit = `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService"]},
"metrics":{"tag":"metrics","listen":"127.0.0.1:10086"},
"outbounds":[
{"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"sticky-us5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"sticky-by5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
{"tag":"DIRECT","protocol":"freedom"}],
"routing":{"rules":[{"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["sticky-de5"],"strategy":{"type":"leastPing"}},
{"tag":"BL-TK","selector":["sticky-by5"],"strategy":{"type":"leastPing"}}]},
"burstObservatory":{"subjectSelector":["sticky-"],"pingConfig":{"destination":"https://cp.cloudflare.com/generate_204","interval":"300s","timeout":"10s"}}}`

func TestTheWatchdogNeverLendsAnExitTheCheckFoundUnfit(t *testing.T) {
	f := &failoverFakes{
		info: map[string]api.BalancerInfo{"BL-MAIN": {}, "BL-TK": {Principle: []string{"sticky-by5"}}},
		obs: map[string]api.Observation{"sticky-de5": {Alive: false}, "sticky-us5": {Alive: true, DelayMs: 90},
			"sticky-by5": {Alive: true, DelayMs: 359}},
	}
	f.install(t)
	d := failoverDaemon(t)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(failoverRenderBorrowUnfit), 0o644); err != nil {
		t.Fatal(err)
	}
	d.exits.Restore(map[string]time.Time{"sticky-us5": time.Now()})
	w := newFailoverWatch()
	d.failoverTick(context.Background(), w, time.Now())
	if len(f.overrides) != 1 || f.overrides[0] != "BL-MAIN→sticky-by5" {
		t.Fatalf("overrides %v", f.overrides)
	}
	if r := d.route.Load(); r == nil || !reflect.DeepEqual(r.Unfit, []string{"sticky-us5"}) {
		t.Fatalf("route %+v", r)
	}
}

// Review of r26 (deferred minor, done): with every node of the main balancer
// unfit none can leave it (a balancer is never emptied), and the card said
// nothing while blocked sites kept failing. It names them as kept.
func TestTheCardNamesUnfitExitsTheMainBalancerMustKeep(t *testing.T) {
	f := &failoverFakes{info: map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}}}}
	f.install(t)
	d := failoverDaemon(t)
	d.exits.Restore(map[string]time.Time{"bridge-nl5": time.Now(), "bridge-pl5": time.Now()})
	w := newFailoverWatch()
	d.failoverTick(context.Background(), w, time.Now())
	r := d.route.Load()
	if r == nil || len(r.Unfit) != 0 || !reflect.DeepEqual(r.UnfitKept, []string{"bridge-nl5", "bridge-pl5"}) {
		t.Fatalf("route %+v", r)
	}
}

// The review of r29–r31: with every server of the main balancer unfit AND the
// main traffic moved to another server (the watchdog's, or the owner's pin),
// the card said both «идёт через Y» and «другого сервера нет» — the second
// untrue while the move holds. Kept is what the main traffic goes through now:
// only the server it is held on, if that one is unfit.
func TestTheCardKeepsNoUnfitExitTheMainTrafficWasMovedOff(t *testing.T) {
	f := &failoverFakes{info: map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}, Override: "bridge-de5"}}}
	f.install(t)
	d := failoverDaemon(t)
	d.exits.Restore(map[string]time.Time{"bridge-nl5": time.Now(), "bridge-pl5": time.Now()})
	d.failoverTick(context.Background(), newFailoverWatch(), time.Now())
	if r := d.route.Load(); r == nil || len(r.UnfitKept) != 0 {
		t.Fatalf("moved off the unfit servers, the card still keeps %+v", r)
	}
	// Held on an unfit one (all there is): that one is kept.
	f.info = map[string]api.BalancerInfo{"BL-MAIN": {Principle: []string{"bridge-nl5"}, Override: "bridge-pl5"}}
	d.failoverTick(context.Background(), newFailoverWatch(), time.Now())
	if r := d.route.Load(); r == nil || !reflect.DeepEqual(r.UnfitKept, []string{"bridge-pl5"}) {
		t.Fatalf("held on an unfit server, kept %+v", r)
	}
}
