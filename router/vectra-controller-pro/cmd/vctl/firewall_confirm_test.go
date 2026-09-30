package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
)

// A daemon with no panel wiring beyond what the confirm path needs. The
// firewall itself is never applied here — this is about which SOCKET the
// post-apply reachability probe goes out on, which is decidable without nft.
func confirmDaemon(t *testing.T, controlURL, confirmPath string, hc *http.Client) *daemon {
	t.Helper()
	dir := t.TempDir()
	agentJSON := `{"controlUrl":` + quoteJSON(controlURL) + `,` +
		`"statePath":` + quoteJSON(filepath.Join(dir, "state.json")) + `,` +
		`"statusPath":` + quoteJSON(filepath.Join(dir, "status.json")) + `,` +
		`"xrayConfigPath":` + quoteJSON(filepath.Join(dir, "operator.json")) + `,` +
		`"xrayRenderPath":` + quoteJSON(filepath.Join(dir, "xray.json")) + `,` +
		`"xrayBinary":` + quoteJSON(writeFakeXray(t, dir)) + `,` +
		`"legacyStatePath":` + quoteJSON(filepath.Join(dir, "absent.json")) + `}`
	agentPath := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(agentPath, []byte(agentJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := agentcfg.Load(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The point of the test: swap in a client whose transport is observable.
	// programFirewall must reach the panel through THIS one.
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: controlURL, HTTPClient: hc})
	d.confirmer = firewall.NewCommitConfirmer(confirmPath, time.Minute)
	return d
}

func quoteJSON(s string) string { return `"` + s + `"` }

// THE REGRESSION. The post-apply confirm used to probe on a fresh
// &http.Client{}. Once the vctl ruleset is loaded, the output chain stamps
// FwMark on unmarked local tcp/udp egress and the fwmark policy route resolves
// that to `local ... dev lo`, so such a probe never leaves the router: the
// branch was unreachable on every real unit. For the daemon that only cost a
// fast path — the next check-in confirms anyway. For `vctl apply-local`, which
// programs the firewall and exits, it would mean the deadman reverting a good
// ruleset 90 seconds later.
func TestConfirmProbesThroughTheControlPlaneClient(t *testing.T) {
	var hits int64
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer panel.Close()

	// A transport that ONLY this client has. If the confirm path built its own
	// http.Client, the counter below would stay at zero.
	var throughClient int64
	hc := &http.Client{
		Timeout: 4 * time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			atomic.AddInt64(&throughClient, 1)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}

	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, panel.URL, confirmPath, hc)

	if !d.confirmIfPanelReachable(context.Background()) {
		t.Fatal("the panel was reachable and the deadman was not confirmed")
	}
	if atomic.LoadInt64(&throughClient) == 0 {
		t.Error("the probe did not go through the control-plane client — it built its own, " +
			"which the loaded ruleset black-holes")
	}
	if !fileExists(confirmPath) {
		t.Error("no confirm sentinel was written; the deadman would revert")
	}
}

// The other direction: an unreachable panel must NOT confirm, or the deadman
// stops being a deadman and a ruleset that severed the control plane stays.
func TestConfirmDoesNotFireWhenThePanelIsUnreachable(t *testing.T) {
	hc := &http.Client{Timeout: time.Second}
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirmPath, hc)

	if d.confirmIfPanelReachable(context.Background()) {
		t.Fatal("confirmed with the panel unreachable")
	}
	if fileExists(confirmPath) {
		t.Error("a confirm sentinel was written despite the panel being unreachable")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
