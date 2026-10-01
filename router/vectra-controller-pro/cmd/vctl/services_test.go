package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/vault"
)

// A server per service: `vctl rpcd` answers what the running entry offers
// and checks a choice before anything reaches the daemon; the daemon keeps it
// once a render with it runs.

// svcRunning is a render of the «Авто» entry's shape (2026-09-30) with
// TikTok already through Germany.
const svcRunning = `{"outbounds":[
 {"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443}]}},
 {"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.6","port":40052}]}},
 {"tag":"sticky-by5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443}]}},
 {"tag":"bridge-ru-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":40051}]}},
 {"tag":"whitelist-lv1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443}]}},
 {"tag":"vctl-svc-tiktok","protocol":"loopback","settings":{"inboundTag":"vctl-svc-tiktok"}},
 {"tag":"DIRECT","protocol":"freedom"}],
 "routing":{"rules":[
  {"inboundTag":["vctl-svc-tiktok"],"balancerTag":"BL-TK"},
  {"inboundTag":["tproxy-in"],"domain":["geosite:tiktok"],"balancerTag":"VCTL-SVC-TIKTOK"},
  {"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},
  {"domain":["geosite:telegram"],"balancerTag":"BL-TK"},
  {"ip":["geoip:telegram"],"balancerTag":"BL-TK"},
  {"domain":["geosite:youtube"],"balancerTag":"BL-RU"},
  {"network":"tcp,udp","balancerTag":"BL-MAIN"}],
 "balancers":[
  {"tag":"BL-MAIN","selector":["sticky-de5"]},
  {"tag":"BL-TK","selector":["sticky-by5"]},
  {"tag":"BL-RU","selector":["bridge-ru-tcp"]},
  {"tag":"VCTL-SVC-TIKTOK","selector":["sticky-de5","bridge-de5"],"fallbackTag":"vctl-svc-tiktok"}]}}`

func svcStand(t *testing.T) *lockStand {
	t.Helper()
	s := newLockStand(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	if err := vault.WriteFile(s.cfg.XrayRenderPath, []byte(svcRunning)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetServiceIsCheckedBeforeTheDaemonIsAsked(t *testing.T) {
	s := svcStand(t)
	ctx := context.Background()
	for _, tc := range []struct{ params, code string }{
		{`{"id":"netflix","country":"DE"}`, "unknown_service"},
		{`{"id":"tiktok","country":"NL"}`, "unknown_country"},
		{`{"id":"tiktok","country":"LV"}`, "unknown_country"}, // a whitelist level, no country
		{`{"id":"tiktok"}`, "invalid_params"},
		{`{"country":"DE"}`, "invalid_params"},
		{`{"id":"tiktok","country":1}`, "invalid_params"},
		{`["tiktok"]`, "invalid_params"},
	} {
		a, ok := rpcdCall(ctx, s.cfg, "set_service", []byte(tc.params)).(uiapi.Action)
		if !ok || a.OK || a.Code != tc.code {
			t.Errorf("set_service %s = %+v, want %s", tc.params, a, tc.code)
		}
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Fatalf("the daemon was asked %d time(s) for refused choices", n)
	}
	a := rpcdCall(ctx, s.cfg, "set_service", []byte(`{"id":"tiktok","country":"de"}`)).(uiapi.Action)
	if !a.OK || a.Code != "service_set" {
		t.Fatalf("set_service = %+v", a)
	}
	a = rpcdCall(ctx, s.cfg, "set_service", []byte(`{"id":"youtube","country":""}`)).(uiapi.Action)
	if !a.OK || a.Code != "service_set" {
		t.Fatalf("back to the default = %+v", a)
	}
	reqs := s.daemonRequests()
	if len(reqs) != 2 || reqs[0].Op != localctl.OpReapply || reqs[0].Change == nil || reqs[0].Change.SetService == nil {
		t.Fatalf("the daemon was asked %+v", reqs)
	}
	if got := *reqs[0].Change.SetService; got != (localctl.ServiceChoice{ID: "tiktok", Country: "DE"}) {
		t.Errorf("the daemon was handed %+v", got)
	}
	if got := *reqs[1].Change.SetService; got != (localctl.ServiceChoice{ID: "youtube"}) {
		t.Errorf("the daemon was handed %+v", got)
	}
	if _, err := os.Stat(s.cfg.OverridesPath); !os.IsNotExist(err) {
		t.Errorf("rpcd wrote the overrides itself (%v)", err)
	}
}

// A router that routes by the operator's policy (native, PassWall) has no
// subscription entry to choose countries from.
func TestServicesAreUnavailableOutsideTheSubscriptionsEngine(t *testing.T) {
	s := svcStand(t)
	s.cfg.RouteSource = "native"
	ctx := context.Background()
	if got := rpcdCall(ctx, s.cfg, "services", nil).(uiapi.Services); got.Available || len(got.Services) != 0 {
		t.Fatalf("services = %+v", got)
	}
	if a := rpcdCall(ctx, s.cfg, "set_service", []byte(`{"id":"tiktok","country":"DE"}`)).(uiapi.Action); a.OK || a.Code != "unavailable" {
		t.Fatalf("set_service = %+v", a)
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Fatalf("the daemon was asked %d time(s)", n)
	}
}

func TestTheServicesAnswerIsWhatTheRouterRuns(t *testing.T) {
	s := svcStand(t)
	if _, err := localctl.UpdateOverrides(s.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Services = map[string]string{"tiktok": "DE", "telegram": "NL"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := rpcdCall(context.Background(), s.cfg, "services", nil).(uiapi.Services)
	if !got.Available || len(got.Services) != 3 {
		t.Fatalf("services = %+v", got)
	}
	str := func(p *string) string {
		if p == nil {
			return "∅"
		}
		return *p
	}
	var rows []string
	for _, sv := range got.Services {
		rows = append(rows, strings.Join([]string{sv.ID, str(sv.Choice), str(sv.DefaultCountry),
			strings.Join(sv.Countries, "/"), map[bool]string{true: "active", false: "-"}[sv.Active],
			map[bool]string{true: "stale", false: "-"}[sv.Stale]}, " "))
	}
	want := []string{
		"youtube ∅ RU BY/DE/RU - -",
		"tiktok DE BY BY/DE/RU active -",
		"telegram NL BY BY/DE/RU - stale",
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

// svcNoTikTok is a render of an entry with German outbounds and no rule of
// its own for TikTok: TikTok rides the catch-all, and there is no path of
// TikTok's own for a country to fall back to.
const svcNoTikTok = `{"outbounds":[
 {"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443}]}},
 {"tag":"sticky-by5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443}]}},
 {"tag":"DIRECT","protocol":"freedom"}],
 "routing":{"rules":[
  {"domain":["geosite:telegram"],"balancerTag":"BL-TK"},
  {"network":"tcp,udp","balancerTag":"BL-MAIN"}],
 "balancers":[
  {"tag":"BL-MAIN","selector":["sticky-de5"]},
  {"tag":"BL-TK","selector":["sticky-by5"]}]}}`

// A choice the running render does not carry is stale, whatever the reason —
// here the entry has Germany but no path of TikTok's own — and a service with
// no path of its own is offered no country: the UI must never show a country
// that is not running.
func TestAChoiceTheRouterDoesNotRunIsStale(t *testing.T) {
	s := svcStand(t)
	if err := vault.WriteFile(s.cfg.XrayRenderPath, []byte(svcNoTikTok)); err != nil {
		t.Fatal(err)
	}
	if _, err := localctl.UpdateOverrides(s.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Services = map[string]string{"tiktok": "DE"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := rpcdCall(context.Background(), s.cfg, "services", nil).(uiapi.Services)
	var tk *uiapi.ServiceInfo
	for i := range got.Services {
		if got.Services[i].ID == "tiktok" {
			tk = &got.Services[i]
		}
	}
	if tk == nil || tk.Choice == nil || *tk.Choice != "DE" || tk.Active || !tk.Stale || len(tk.Countries) != 0 {
		t.Fatalf("tiktok = %+v", tk)
	}
	a := rpcdCall(context.Background(), s.cfg, "set_service", []byte(`{"id":"tiktok","country":"DE"}`)).(uiapi.Action)
	if a.OK || a.Code != "unknown_country" {
		t.Fatalf("set_service for a service the entry has no path for = %+v", a)
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Fatalf("the daemon was asked %d time(s)", n)
	}
}
