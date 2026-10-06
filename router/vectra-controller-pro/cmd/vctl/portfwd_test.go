package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/portfwd"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/uiapi"
)

const pfFirewall = `
config redirect 'vectra_pf_3fa1c09e'
	option name 'Vectra: gaming-pc'
	option src 'wan'
	option dest 'lan'
	option target 'DNAT'
	option proto 'tcp'
	option src_dport '25565'
	option dest_ip '192.168.1.50'
	option dest_port '25565'
	option enabled '1'
	option reflection '1'
	option vectra_preset 'minecraft'

config redirect 'vectra_pf_b7d204aa'
	option name 'Vectra: 192.168.1.60'
	option src 'wan'
	option dest 'lan'
	option target 'DNAT'
	option proto 'tcp udp'
	option src_dport '3478-3480'
	option dest_ip '192.168.1.60'
	option dest_port '3478-3480'
	option enabled '1'
	option reflection '1'
	option vectra_direct '1'

config redirect
	option name 'Allow-NAS'
	option src 'wan'
	option src_dport '5000'
	option dest_ip '192.168.1.10'
	option proto 'tcp'
`

// pfRouter points portfwdEnv at a router in a temp dir and records every
// command a change runs.
type pfRouter struct {
	mu    sync.Mutex
	calls []string
	env   portfwd.Env
}

func (r *pfRouter) ran() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func fakePortForwardRouter(t *testing.T) *pfRouter {
	t.Helper()
	dir := t.TempDir()
	r := &pfRouter{}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	r.env = portfwd.Env{
		FirewallConfig: write("firewall", pfFirewall),
		DHCPConfig:     write("dhcp", "config host\n\toption name 'gaming-pc'\n\toption ip '192.168.1.50'\n"),
		Leases:         write("dhcp.leases", "0 aa:bb:cc:dd:ee:02 192.168.1.60 * *\n0 aa:bb:cc:dd:ee:01 192.168.1.50 pc *\n"),
		RunDir:         filepath.Join(dir, "run"), UCISaveDir: filepath.Join(dir, "uci"), Lock: filepath.Join(dir, "pf.lock"),
		DirectStatus: filepath.Join(dir, "run", "portfwd-direct.json"),
		Run: func(_ context.Context, stdin io.Reader, name string, args ...string) error {
			if stdin != nil {
				_, _ = io.ReadAll(stdin)
			}
			r.mu.Lock()
			r.calls = append(r.calls, name+" "+args[len(args)-1])
			r.mu.Unlock()
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ubus" && strings.Join(args, " ") == "call network.interface.lan status" {
				return []byte(`{"ipv4-address":[{"address":"192.168.1.1","mask":24}]}`), nil
			}
			return nil, errors.New("unexpected")
		},
	}
	old := portfwdEnv
	portfwdEnv = func() portfwd.Env { return r.env }
	t.Cleanup(func() { portfwdEnv = old })
	return r
}

// The read is the contract's shape, built from a real fw4 config: the
// router's own rules only (LuCI's redirect is not listed), with their
// device's name; the devices without a MAC address.
func TestPortForwardsAnswerIsTheContract(t *testing.T) {
	r := fakePortForwardRouter(t)
	s := newLockStand(t)
	fakeUILock(t, "0", nil)
	// Before the daemon wrote the set, «past the VPN» is not in effect.
	if a := rpcdCall(context.Background(), s.cfg, "port_forwards", nil).(uiapi.PortForwards); a.DirectActive == nil || *a.DirectActive {
		t.Fatalf("directActive with no status = %v, want false", a.DirectActive)
	}
	if err := portfwd.WriteDirectStatus(r.env.DirectStatus, portfwd.DirectStatus{PID: os.Getpid(), Active: true, Addrs: []string{"192.168.1.60"}}); err != nil {
		t.Fatal(err)
	}
	out, ok := rpcdCall(context.Background(), s.cfg, "port_forwards", nil).(uiapi.PortForwards)
	if !ok {
		t.Fatalf("port_forwards = %T", out)
	}
	raw, _ := json.Marshal(out)
	var problems []string
	sameShape("", generic(t, readContract(t, "port_forwards.json")), generic(t, raw), &problems)
	if len(problems) > 0 {
		t.Fatalf("shape differs from the fixture:\n  %s\n%s", strings.Join(problems, "\n  "), raw)
	}
	want := `{"rules":[{"id":"3fa1c09e","preset":"minecraft","destIp":"192.168.1.50","deviceName":"gaming-pc","port":"25565","proto":"tcp","direct":false,"enabled":true},` +
		`{"id":"b7d204aa","preset":null,"destIp":"192.168.1.60","deviceName":null,"port":"3478-3480","proto":"both","direct":true,"enabled":true}],` +
		`"devices":[{"name":"gaming-pc","ip":"192.168.1.50"},{"name":null,"ip":"192.168.1.60"}],"cgnat":false,"directActive":true,"max":32}`
	if string(raw) != want {
		t.Fatalf("port_forwards =\n%s\nwant\n%s", raw, want)
	}
	if strings.Contains(string(raw), "aa:bb") || strings.Contains(string(raw), "Allow-NAS") {
		t.Fatalf("a MAC address or a foreign redirect in the answer: %s", raw)
	}
}

func TestSetPortForwardsAppliesAndAsksTheDaemonForTheSet(t *testing.T) {
	r := fakePortForwardRouter(t)
	s := newLockStand(t)
	fakeUILock(t, "0", nil)
	ctx := context.Background()
	for _, tc := range []struct{ params, code string }{
		{``, "invalid_params"},
		{`{}`, "invalid_params"},
		{`{"rules":null}`, "invalid_params"},
		{`{"rules":[],"extra":1}`, "invalid_params"},
		{`{"rules":[{"destIp":"192.168.1.50","port":"80","proto":"tcp","direct":false,"enabled":true,"label":"x"}]}`, "invalid_params"},
		{`{"rules":[{"destIp":"192.168.1.50","port":"80","proto":"tcp+udp","direct":false,"enabled":true}]}`, "invalid_params"},
		{`{"rules":[{"destIp":"192.168.2.50","port":"80","proto":"tcp","direct":false,"enabled":true}]}`, "dest_not_lan"},
		{`{"rules":[{"destIp":"192.168.1.1","port":"80","proto":"tcp","direct":false,"enabled":true}]}`, "dest_is_router"},
		{`{"rules":[{"destIp":"192.168.1.50","port":"5000","proto":"both","direct":false,"enabled":true}]}`, "port_conflict"},
		{`{"rules":[]} {}`, "invalid_params"},
		{`{"rules":[{"preset":"Mine Craft","destIp":"192.168.1.50","port":"80","proto":"tcp","direct":false,"enabled":true}]}`, "invalid_params"},
		{`{"rules":[{"preset":"` + strings.Repeat("a", 25) + `","destIp":"192.168.1.50","port":"80","proto":"tcp","direct":false,"enabled":true}]}`, "invalid_params"},
	} {
		a, ok := rpcdCall(ctx, s.cfg, "set_port_forwards", []byte(tc.params)).(uiapi.Action)
		if !ok || a.OK || a.Code != tc.code {
			t.Errorf("set_port_forwards %s = %+v, want %s", tc.params, a, tc.code)
		}
	}
	if len(r.ran()) != 0 || len(s.daemonRequests()) != 0 {
		t.Fatalf("a refused change ran %q, asked the daemon %+v", r.ran(), s.daemonRequests())
	}
	a := rpcdCall(ctx, s.cfg, "set_port_forwards", []byte(`{"rules":[{"id":"3fa1c09e","preset":"minecraft","destIp":"192.168.1.50","port":"25565","proto":"tcp","direct":true,"enabled":true}]}`)).(uiapi.Action)
	if !a.OK || a.Code != "port_forwards_set" || a.Detail != nil {
		t.Fatalf("set_port_forwards = %+v", a)
	}
	if got := strings.Join(r.ran(), "|"); got != "uci batch|uci firewall|/etc/init.d/firewall reload" {
		t.Fatalf("ran %s", got)
	}
	if reqs := s.daemonRequests(); len(reqs) != 1 || reqs[0].Op != localctl.OpSyncPortForwards {
		t.Fatalf("the daemon was asked %+v", reqs)
	}
}

// A daemon that is down does not undo a change fw4 already carries: the set
// follows when it runs.
func TestSetPortForwardsWithTheControllerDown(t *testing.T) {
	fakePortForwardRouter(t)
	s := newLockStand(t)
	fakeUILock(t, "0", nil)
	cfg := s.cfg
	cfg.UISocketPath = filepath.Join(t.TempDir(), "absent.sock")
	a := rpcdCall(context.Background(), cfg, "set_port_forwards", []byte(`{"rules":[]}`)).(uiapi.Action)
	if !a.OK || a.Code != "port_forwards_set" || a.Detail == nil {
		t.Fatalf("set_port_forwards with the controller down = %+v", a)
	}
}

func TestPortForwardsAreProOnly(t *testing.T) {
	r := fakePortForwardRouter(t)
	s := newLockStand(t)
	fakeUILock(t, "1", nil)
	for _, m := range []string{"port_forwards", "set_port_forwards"} {
		out, _ := json.Marshal(rpcdCall(context.Background(), s.cfg, m, []byte(`{"rules":[]}`)))
		if string(out) != lockedAnswer {
			t.Errorf("locked %s = %s", m, out)
		}
	}
	if len(r.ran()) != 0 || len(s.daemonRequests()) != 0 {
		t.Fatal("work was done under the lock")
	}
}

// The daemon writes the set from the firewall config: after a load (forced),
// when the config changed, on a request — and not again for nothing.
func TestTheDaemonSyncsThePortForwardsSet(t *testing.T) {
	r := fakePortForwardRouter(t)
	d, _ := directDaemon(t)
	var scripts []string
	fail := false
	d.pfLoad = func(_ context.Context, s string) error {
		if fail {
			return errors.New("nft: no such table")
		}
		scripts = append(scripts, s)
		return nil
	}
	ctx := context.Background()
	want := "flush set inet vctl vctl_pf_direct4\nadd element inet vctl vctl_pf_direct4 { 192.168.1.60 }\n"
	status := func() portfwd.DirectStatus {
		t.Helper()
		var st portfwd.DirectStatus
		raw, err := os.ReadFile(r.env.DirectStatus)
		if err != nil || json.Unmarshal(raw, &st) != nil {
			t.Fatalf("no status: %v %s", err, raw)
		}
		return st
	}
	if err := d.maybeSyncPortForwards(ctx, false); err != nil || len(scripts) != 1 || scripts[0] != want {
		t.Fatalf("first sync: %v %q", err, scripts)
	}
	if st := status(); !st.Active || st.PID != os.Getpid() || strings.Join(st.Addrs, ",") != "192.168.1.60" {
		t.Fatalf("status after a write = %+v", st)
	}
	_ = d.maybeSyncPortForwards(ctx, false)
	if len(scripts) != 1 {
		t.Fatalf("written again with nothing changed: %q", scripts)
	}
	_ = d.maybeSyncPortForwards(ctx, true)
	if len(scripts) != 2 {
		t.Fatal("a forced sync (after a load) was skipped")
	}
	// The owner turned the rule off: the set empties.
	raw, _ := os.ReadFile(r.env.FirewallConfig)
	if err := os.WriteFile(r.env.FirewallConfig, []byte(strings.Replace(string(raw), "option vectra_direct '1'", "option vectra_direct '0'", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp := d.syncPortForwardsNow(ctx); !resp.OK {
		t.Fatalf("syncPortForwardsNow = %+v", resp)
	}
	if last := scripts[len(scripts)-1]; last != "flush set inet vctl vctl_pf_direct4\n" {
		t.Fatalf("set not emptied: %q", last)
	}
	fail = true
	if resp := d.syncPortForwardsNow(ctx); resp.OK || resp.Code != "apply_failed" {
		t.Fatalf("a failed write = %+v", resp)
	}
	if st := status(); st.Active {
		t.Fatalf("a failed write recorded as in effect: %+v", st)
	}
	// Not loaded by this process, or in rescue's direct mode: nothing to do.
	fail = false
	n := len(scripts)
	d.fwProgrammed = nil
	if resp := d.syncPortForwardsNow(ctx); !resp.OK || len(scripts) != n {
		t.Fatalf("synced a table this process did not load: %+v", resp)
	}
	if st := status(); st.Active {
		t.Fatalf("no data plane, recorded as in effect: %+v", st)
	}
	programmed := ""
	d.fwProgrammed = &programmed
	// The kill switch armed: the rule is not in the table (the switch wins),
	// so nothing is written and the status reads not in effect.
	d.pfStatusKey = ""
	d.desired.Inbounds.Tproxy.KillSwitch = true
	if resp := d.syncPortForwardsNow(ctx); !resp.OK || len(scripts) != n {
		t.Fatalf("wrote the set under the kill switch: %+v %q", resp, scripts[n:])
	}
	if st := status(); st.Active {
		t.Fatalf("kill switch armed, recorded as in effect: %+v", st)
	}
	if got := portfwd.DirectActive([]portfwd.Rule{{DestIP: "192.168.1.60", Enabled: true, Direct: true}}, r.env.DirectStatus, portfwdAlive); got == nil || *got {
		t.Fatalf("directActive under the kill switch = %v, want false", got)
	}
	d.desired.Inbounds.Tproxy.KillSwitch = false
	d.st.Rescue.Mode = string(rescue.ModeDirect)
	_ = d.maybeSyncPortForwards(ctx, true)
	if len(scripts) != n {
		t.Fatal("synced in rescue's direct mode")
	}
	// Rescue's direct mode with the table down: everything goes out by the
	// kernel, the device asked for included. (The rule was turned off
	// above: put it back.)
	_ = os.WriteFile(r.env.FirewallConfig, raw, 0o644)
	d.fwProgrammed = nil
	_ = d.maybeSyncPortForwards(ctx, false)
	if st := status(); !st.Active || strings.Join(st.Addrs, ",") != "192.168.1.60" {
		t.Fatalf("rescue direct status = %+v", st)
	}
}

func TestConnectSetPortForwardsAction(t *testing.T) {
	d, results := connectTestDaemon(t)
	oldExec, oldGate := connectPortForwardsExecute, connectResourceBlocked
	t.Cleanup(func() { connectPortForwardsExecute, connectResourceBlocked = oldExec, oldGate })
	connectResourceBlocked = func(*daemon, string) bool { return false }
	var got []portfwd.Rule
	connectPortForwardsExecute = func(_ *daemon, _ context.Context, rules []portfwd.Rule) (string, bool) {
		got = rules
		return "port_conflict", false
	}
	job := connectTestJob("pf1", "set_port_forwards", map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"destIp": "192.168.1.50", "port": "25565", "proto": "tcp", "direct": true, "enabled": true},
	}})
	if err := d.executeJob(context.Background(), job, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DestIP != "192.168.1.50" || !got[0].Direct || got[0].ID != "" {
		t.Fatalf("executed with %+v", got)
	}
	last := (*results)[len(*results)-1]
	if last.Status != "failure" || last.Result["code"] != "port_conflict" {
		t.Fatalf("result %+v", last)
	}
	// A malformed list never reaches the router's configuration.
	got = nil
	bad := connectTestJob("pf2", "set_port_forwards", map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"destIp": "192.168.1.50", "port": "0", "proto": "tcp", "direct": true, "enabled": true},
	}})
	_ = d.executeJob(context.Background(), bad, controlplane.CheckInResponse{RouterID: d.st.RouterID})
	if got != nil || (*results)[len(*results)-1].Result["error"] != "invalid_params" {
		t.Fatalf("malformed list executed: %+v %+v", got, (*results)[len(*results)-1])
	}
}

// The real executor: applied through fw4, «applied» on success, a refusal's
// code otherwise.
func TestConnectPortForwardsExecutor(t *testing.T) {
	r := fakePortForwardRouter(t)
	d, _ := directDaemon(t)
	d.pfLoad = func(context.Context, string) error { return nil }
	code, ok := connectPortForwardsExecute(d, context.Background(), []portfwd.Rule{{DestIP: "192.168.1.50", Port: "8080", Proto: "tcp", Enabled: true}})
	if !ok || code != "applied" || len(r.ran()) != 3 {
		t.Fatalf("= %s %v, ran %q", code, ok, r.ran())
	}
	code, ok = connectPortForwardsExecute(d, context.Background(), []portfwd.Rule{{DestIP: "8.8.8.8", Port: "8080", Proto: "tcp", Enabled: true}})
	if ok || code != "dest_not_lan" {
		t.Fatalf("= %s %v", code, ok)
	}
	many := make([]portfwd.Rule, portfwd.MaxRules+1)
	for i := range many {
		many[i] = portfwd.Rule{DestIP: "192.168.1.50", Port: fmt.Sprint(10000 + i), Proto: "tcp", Enabled: true}
	}
	if code, ok = connectPortForwardsExecute(d, context.Background(), many); ok || code != "too_many" {
		t.Fatalf("33 rules = %s %v, want too_many", code, ok)
	}
}

func TestConnectAdvertisesPortForwardsWhereFw4Is(t *testing.T) {
	d, _ := servicesTestDaemon(t, localctl.Overrides{})
	if d.connectCapabilities()["set_port_forwards"] {
		t.Fatal("advertised on a router without fw4's config")
	}
	fakePortForwardRouter(t)
	if !d.connectCapabilities()["set_port_forwards"] {
		t.Fatal("not advertised where fw4 keeps its config")
	}
}

// The check-in carries the same simple shape, bounded, without a MAC.
func TestConnectTelemetryCarriesPortForwards(t *testing.T) {
	r := fakePortForwardRouter(t)
	if err := portfwd.WriteDirectStatus(r.env.DirectStatus, portfwd.DirectStatus{PID: os.Getpid(), Active: true, Addrs: []string{"192.168.1.60"}}); err != nil {
		t.Fatal(err)
	}
	d, _ := servicesTestDaemon(t, localctl.Overrides{})
	d.publishConnectTelemetry(context.Background(), map[string]bool{})
	inv := d.collector.Collect(context.Background(), supervisor.Status{}, 0, 0)
	if inv.Connect == nil || inv.Connect.PortForwards == nil {
		t.Fatalf("no port forwards reported: %+v", inv.Connect)
	}
	raw, _ := json.Marshal(inv.Connect.PortForwards)
	want := `{"rules":[{"id":"3fa1c09e","preset":"minecraft","destIp":"192.168.1.50","deviceName":"gaming-pc","port":"25565","proto":"tcp","direct":false,"enabled":true},` +
		`{"id":"b7d204aa","preset":null,"destIp":"192.168.1.60","deviceName":null,"port":"3478-3480","proto":"both","direct":true,"enabled":true}],` +
		`"devices":[{"name":"gaming-pc","ip":"192.168.1.50"},{"name":null,"ip":"192.168.1.60"}],"cgnat":false,"directActive":true}`
	if string(raw) != want {
		t.Fatalf("portForwards =\n%s\nwant\n%s", raw, want)
	}
	if pf := connectPortForwards(portfwd.State{}, "100.64.3.4", nil); !pf.CGNAT || pf.Rules == nil || pf.Devices == nil || pf.DirectActive != nil {
		t.Fatalf("empty state = %+v", pf)
	}
}
