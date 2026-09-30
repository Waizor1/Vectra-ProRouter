package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/rescue"
)

// toServer sends every request to srv: what the control plane's marked path
// reaches while the tunnel does not.
type toServer struct{ srv *httptest.Server }

func (s toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.srv.URL)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host, r2.Host = u.Scheme, u.Host, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// The tunnel dead, the line alive: the rescue leaves proxy mode by itself.
// Its "can we go direct" was the tunnel's own answer again, false exactly
// when the tunnel was dead, so this never happened.
func TestRescueGoesDirectWhenOnlyTheTunnelIsDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	// Only the marked client reaches it; the tunnel's plain client cannot
	// even resolve the name.
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.Cooldown = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})

	inv := controlplane.RouterInventory{}
	var last rescue.Decision
	for i := 0; i < d.rescuePolicy.TriggerFailureCount; i++ {
		_, last = d.evaluateHealth(context.Background(), &inv)
	}
	if !last.ShouldTransition || last.NextMode != rescue.ModeDirect {
		t.Fatalf("after %d dead-tunnel loops with the line up: %+v", d.rescuePolicy.TriggerFailureCount, last)
	}
}

// The tunnel dead AND the line dead: nothing to gain by going direct.
func TestRescueStaysWhenTheLineIsDeadToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.Cooldown = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+1; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("went %s with the line dead too: %+v", dec.NextMode, dec)
		}
	}
}

// Under the kill switch the rescue never opens the router: fail closed.
func TestRescueNeverGoesDirectUnderTheKillSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.Cooldown = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})
	d.desired = &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1, KillSwitch: true}}}

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+2; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("the kill switch is armed and the rescue went %s: %+v", dec.NextMode, dec)
		}
	}
}

// A start in direct mode leaves the data plane down: the mode was chosen on
// purpose and survives the restart; loading the table would run the LAN into
// the dead tunnel while the router says "direct".
func TestAStartInDirectModeLoadsNothing(t *testing.T) {
	dir := t.TempDir()
	confirm := filepath.Join(dir, "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirm, nil)
	d.desired = &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1}}}
	d.dnsAnswers = func(context.Context, int) bool { return false }

	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect}, "")
	if err := os.WriteFile(confirm, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.restoreDataPlane(context.Background())
	if _, err := os.Stat(confirm); err != nil {
		t.Fatal("direct mode, and the firewall was programmed anyway (the apply cleared the confirm sentinel)")
	}

	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy}, "")
	d.restoreDataPlane(context.Background())
	if _, err := os.Stat(confirm); err == nil {
		t.Fatal("proxy mode, and the firewall was not programmed")
	}
}
