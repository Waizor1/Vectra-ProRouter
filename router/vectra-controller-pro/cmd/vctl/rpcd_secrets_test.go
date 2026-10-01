package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
)

// The router UI is any LuCI session's: whatever `vectra` answers, the person
// at that browser has. No answer may carry a credential of the VPN the router
// runs, the subscription it fetches or the panel it answers to (the live
// router's `logs` once carried 7 UUIDs and 119 subscription and panel URLs).
// These tests run every method against a router whose every file holds
// credentials — fake ones, each distinct — and look for each of them in the
// bytes `vctl rpcd` prints.

// Every credential below is made up: shaped like the real thing, valid for
// nothing.
const (
	guardSubURL     = "https://sub.provider.invalid/api/sub/bm90LWEtcmVhbC1zdWItdG9rZW4?client=json&hwid=1"
	guardAgentToken = "bm90LWEtcmVhbC1hZ2VudC10b2tlbjAx"
	guardHWID       = "f00df00df00df00df00df00df00df00df00df00df00df00df00df00df00df00d"
	guardRouterID   = "7a3e5c1d-2b4f-4e6a-9c8d-0f1e2d3c4b5a"
	guardJobID      = "5c1d2e3f-4a5b-4c6d-8e7f-a0b1c2d3e4f5"
	guardClaimQR    = "VECTRA:R1:AZFLppw9fsgETyBtq5qzT43OPUfl6UEWmgHCO0ZPVhJgEq4creWHa5nRCfK8DH2zuw8OetHTPah97-EzlDN_bmXcvVbkf9XEVfw2nIYih"
)

// guardCreds is every credential the router holds in this test, by kind.
type guardCreds map[string]string

func (g guardCreds) add(kind, v string) { g[kind] = v }

// first is the value of the first credential of a key ("id", "password"…) in
// the provider document — the one whose prefix it has, when one is given.
func (g guardCreds) first(key, prefix string) string {
	for n := 1; n <= len(g); n++ {
		if v, ok := g[fmt.Sprintf("%s#%d", key, n)]; ok && strings.HasPrefix(v, prefix) {
			return v
		}
	}
	return ""
}

// guardProvider is the real provider document with credentials of its own —
// fake, each distinct — and a trojan and a shadowsocks node beside its VLESS
// and Hysteria ones.
func guardProvider(t *testing.T, creds guardCreds) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(providerEntry(t), &doc); err != nil {
		t.Fatal(err)
	}
	outs, _ := doc["outbounds"].([]any)
	outs = append(outs,
		map[string]any{"tag": "trojan-tr5", "protocol": "trojan", "settings": map[string]any{
			"servers": []any{map[string]any{"address": "tr.provider.invalid", "port": 443, "password": "not-a-real-trojan-pass-5"}}}},
		map[string]any{"tag": "ss-nl5", "protocol": "shadowsocks", "settings": map[string]any{
			"servers": []any{map[string]any{"address": "ss.provider.invalid", "port": 8388, "method": "2022-blake3-aes-128-gcm",
				"password": "bm90LWEtcmVhbC1zcy1wYXNz"}}}},
	)
	doc["outbounds"] = outs
	n := 0
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys) // numbered in the same order every run
			for _, k := range keys {
				e := x[k]
				s, isString := e.(string)
				switch {
				case !isString:
					walk(e)
					continue
				case k == "id" || (k == "auth" && s != "noauth" && s != "password"):
					n++
					x[k] = fmt.Sprintf("0badc0de-feed-4000-8000-%012d", n)
				case k == "publicKey":
					n++
					x[k] = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("not-a-real-reality-public-key-%02d", n)))
				case k == "shortId":
					n++
					x[k] = fmt.Sprintf("5eed5eed5eed%04d", n)
				case k == "password":
					n++
					x[k] = s + fmt.Sprintf("-%02d", n)
				default:
					continue
				}
				creds.add(fmt.Sprintf("%s#%d", k, n), x[k].(string))
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// guardRouter is a router whose files and daemon hold credentials: the
// operator config with the subscription's URL, the provider document, xray's
// render of it, the state with the panel token and device key, xray's log and
// the controller's syslog quoting them as real errors do.
type guardRouter struct {
	cfg   agentcfg.Config
	creds guardCreds
}

func newGuardRouter(t *testing.T) *guardRouter {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "vgr") // a unix socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	g := &guardRouter{creds: guardCreds{}}
	cfg, err := agentcfg.Parse([]byte(`{"controlUrl":"https://api.vectra-pro.net"}`))
	if err != nil {
		t.Fatal(err)
	}
	for p, name := range map[*string]string{
		&cfg.StatePath: "state.json", &cfg.XrayConfigPath: "xray-desired.json", &cfg.ProviderConfigPath: "provider-config.json",
		&cfg.XrayRenderPath: "xray.json", &cfg.OverridesPath: "local-overrides.json", &cfg.EntriesPath: "provider-entries.json.gz",
		&cfg.EntriesIndexPath: "provider-entries.index.json", &cfg.UISocketPath: "ui.sock", &cfg.StatusPath: "status.json",
	} {
		*p = filepath.Join(dir, name)
	}
	g.cfg = cfg

	// The subscription: the URL whole, and every secret-bearing part of it.
	u, _ := url.Parse(guardSubURL)
	g.creds.add("subscription url", guardSubURL)
	g.creds.add("subscription path", u.EscapedPath())
	g.creds.add("subscription query", u.RawQuery)
	g.creds.add("subscription token", strings.TrimPrefix(u.Path, "/api/sub/"))
	g.creds.add("panel token", guardAgentToken)
	g.creds.add("hwid", guardHWID)
	devKey := base64.StdEncoding.EncodeToString([]byte("not-a-real-ed25519-device-key-seed-and-public-half-for-a-test!!"))
	g.creds.add("device key", devKey)

	golden, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "panel", "operator-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	operator := bytes.Replace(golden, []byte("https://subscription.example.test/api/sub/GOLDEN_TOKEN"), []byte(guardSubURL), 1)
	if _, err := config.Unmarshal(operator); err != nil || bytes.Equal(operator, golden) {
		t.Fatalf("the operator config does not carry the subscription: %v", err)
	}
	mustWrite(t, cfg.XrayConfigPath, operator)

	provider := guardProvider(t, g.creds)
	mustWrite(t, cfg.ProviderConfigPath, provider)
	// xray's API and metrics answer nothing: the answers say so, as they do
	// with xray down.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	tproxy := &config.TproxyInbound{ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, UDPEnabled: true, Tag: "tproxy-in"}
	render, _, err := xray.Splice(provider, tproxy, xray.SpliceOptions{APIListen: ln.Addr().String(), MetricsListen: ln.Addr().String(), NoAccessLog: true})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, cfg.XrayRenderPath, render)

	state, _ := json.Marshal(map[string]any{"router_id": guardRouterID, "agent_token": guardAgentToken,
		"device_identifier": "vectra-0123456789ab", "device_private_key": devKey, "bot_username": "VectraBot"})
	mustWrite(t, cfg.StatePath, state)
	idx, _ := json.Marshal(localctl.EntriesIndex{SubscriptionID: "primary", FetchedAt: time.Now().UTC(), Entries: []localctl.EntrySummary{
		{Index: 0, Remark: "🇷🇺🇪🇺 Авто Самый стабильный", NodeCount: 31, BalancerCount: 7}, {Index: 1, Remark: "🇩🇪 Германия", NodeCount: 4},
	}})
	mustWrite(t, cfg.EntriesIndexPath, idx)
	ov, _ := json.Marshal(localctl.Overrides{Pins: map[string]string{"BL-MAIN": "bridge-de5"}, Direct: []string{"sberbank.ru"}, Proxy: []string{"example.org"}})
	mustWrite(t, cfg.OverridesPath, ov)

	// A daemon that answers the runtime, and refuses every change with the
	// error a failed fetch gives: the subscription's URL, a user id, a password.
	uuid, trojan := g.creds.first("id", ""), g.creds.first("password", "not-a-real-trojan")
	if uuid == "" || trojan == "" {
		t.Fatalf("the provider document lost its user id or its trojan password: %v", g.creds)
	}
	running := time.Now().Add(-time.Hour)
	reach := true
	rt := &localctl.Runtime{ControllerPID: 3012, StartedAt: running, Version: "0.6.0-r37", XrayVersion: "26.3.27", RouterID: guardRouterID,
		PanelReachable: &reach, LastCheckIn: &running,
		Engine: localctl.Engine{State: "running", PID: 4127, StartedAt: running, Restarts: 2, LastExitAt: running.Add(-time.Minute), LastExitCode: 23,
			LastExitErr: `infra/conf: failed to build outbound config with tag bridge-de5 > invalid "id": ` + uuid + ` in {"password":"` + trojan + `"}`},
		Entry:          &localctl.Entry{Index: 0, Remark: "🇷🇺🇪🇺 Авто Самый стабильный", Count: 26, FetchedAt: running},
		Route:          &localctl.Route{Nodes: []string{"bridge-de5"}},
		LastApplyError: "subscription.Fetch: Get \"" + guardSubURL + "\": context deadline exceeded"}
	refusal := "fetch " + guardSubURL + ": x509: certificate signed by unknown authority; user " + uuid +
		`; {"password":"` + trojan + `"} x-vectra-router-token: ` + guardAgentToken
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = localctl.Serve(ctx, cfg.UISocketPath, func(_ context.Context, req localctl.SocketRequest) localctl.SocketResponse {
			if req.Op == localctl.OpRuntime {
				return localctl.SocketResponse{OK: true, Runtime: rt}
			}
			return localctl.SocketResponse{OK: false, Code: "apply_failed", Detail: refusal}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; ; i++ {
		if fi, err := os.Stat(cfg.UISocketPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		if i > 200 {
			t.Fatal("the fake daemon never listened")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// xray's log and the controller's syslog, as the real ones quote
	// credentials: in errors, in config fragments, bare.
	pbk, sid, hy2 := g.creds.first("publicKey", ""), g.creds.first("shortId", ""), g.creds.first("auth", "")
	day := time.Now().Format("2006/01/02")
	xlog := filepath.Join(dir, "xray.log")
	mustWrite(t, xlog+".1", []byte(day+" 10:00:01.000000 [Warning] core: Xray 26.3.27 started\n"+
		day+" 10:00:02.000000 [Info] [1731] proxy/vless/outbound: tunneling request to tcp:www.instagram.com:443 via ru11.provider.invalid:40052\n"))
	mustWrite(t, xlog, []byte(day+" 10:00:03.000000 [Error] infra/conf: failed to build outbound config with tag bridge-de5 > invalid \"id\": "+uuid+
		" in {\"address\":\"ru11.provider.invalid\",\"port\":40052,\"users\":[{\"id\":\""+uuid+"\"}]}\n"+
		day+" 10:00:04.000000 [Warning] [1732] transport/internet/reality: REALITY: processed invalid connection with publicKey "+pbk+" shortId "+sid+"\n"+
		day+" 10:00:05.000000 [Warning] [1733] proxy/hysteria: auth "+hy2+" rejected by ru4.provider.invalid:50052\n"+
		day+" 10:00:06.000000 [Info] [1734] app/dispatcher: taking detour [bridge-de5] for [tcp:www.instagram.com:443]\n"))
	stamp := time.Now().Format("Mon Jan _2 15:04:05 2006")
	syslog := filepath.Join(dir, "syslog")
	mustWrite(t, syslog, []byte(
		stamp+` daemon.warn vctl[1234]: time=2026-09-30T10:00:07Z level=WARN msg="the subscription's own refresh failed; what runs keeps running" err="subscription.Fetch: Get \"`+guardSubURL+`\": context deadline exceeded" retryIn=5m0s`+"\n"+
			stamp+` daemon.err vctl[1234]: time=2026-09-30T10:00:08Z level=ERROR msg="job failed" jobId=`+guardJobID+` type=run_terminal_command err="request failed: x-vectra-router-token=`+guardAgentToken+`"`+"\n"+
			stamp+` daemon.info vctl[1234]: time=2026-09-30T10:00:09Z level=INFO msg="fetch" hwid=`+guardHWID+` x-hwid: `+guardHWID+` key `+devKey+"\n"+
			stamp+` daemon.info vctl[1234]: time=2026-09-30T10:00:10Z level=INFO msg="node trojan://`+trojan+`@tr.provider.invalid:443#TR and vless://`+uuid+`@ru11.provider.invalid:40052?pbk=`+pbk+`&sid=`+sid+`"`+"\n"+
			stamp+` daemon.info vctl[1234]: time=2026-09-30T10:00:11Z level=INFO msg="registered; awaiting operator approval" routerId=`+guardRouterID+"\n"+
			stamp+` daemon.info vctl[1234]: time=2026-09-30T10:00:12Z level=WARN msg="failover: moved off a node that stopped answering" from=bridge-de5 to=bridge-nl5 via=ru8.provider.invalid:40055`+"\n"))
	bin := filepath.Join(dir, "bin")
	mustWrite(t, filepath.Join(bin, "logread"), []byte("#!/bin/sh\ncat '"+syslog+"'\n"))
	if err := os.Chmod(filepath.Join(bin, "logread"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	prevL, prevP, prevG := rpcdGatherLogs, rpcdPower, rpcdGather
	rpcdGather = uiapi.Gather
	rpcdGatherLogs = func(ctx context.Context, _ string, n int) uiapi.Logs { return uiapi.GatherLogs(ctx, xlog, n) }
	rpcdPower = func(context.Context, bool, bool) power.Facts {
		return power.Facts{UCI: true, Boot: true, Running: true, Carrying: true, Owed: power.PassWall}
	}
	t.Cleanup(func() { rpcdGatherLogs, rpcdPower, rpcdGather = prevL, prevP, prevG })
	fakeUILock(t, "0", nil)
	return g
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// guardParams are the params the UI sends each method; a method not named
// here is called without any.
var guardParams = map[string]string{
	"logs":               `{"lines":200}`,
	"select_entry":       `{"index":0}`,
	"pin_balancer":       `{"balancer":"BL-MAIN","node":"bridge-de5"}`,
	"unpin_balancer":     `{"balancer":"BL-MAIN"}`,
	"set_probe_interval": `{"seconds":600}`,
	"set_rules":          `{"direct":["sberbank.ru"],"proxy":["example.org"]}`,
	"set_service":        `{"id":"youtube","country":"DE"}`,
	"set_wifi":           `{"radios":{"radio1":{"ssid":"Home-5G"}}}`,
	"optimize_wifi":      `{"channels":{"radio1":36}}`,
	"set_power":          `{"on":true}`,
	"set_remote_shell":   `{"on":false}`,
}

func TestNoAnswerCarriesACredential(t *testing.T) {
	w := newWizardRouter(t) // the setup wizard's router: its commands faked
	w.write(t, w.env.WirelessConfig, wizardWireless)
	prevChecker := rpcdChecker
	rpcdChecker = func(agentcfg.Config) setup.Checker {
		return setup.Checker{PanelURL: "https://127.0.0.1:1", ProbeURL: "http://127.0.0.1:1/generate_204", ProbeHost: "127.0.0.1", Budget: time.Second}
	}
	pr := newPowerRouter(t)
	prevPower := rpcdPowerEnv
	rpcdPowerEnv = func() power.Env { return pr.env }
	t.Cleanup(func() { rpcdChecker, rpcdPowerEnv = prevChecker, prevPower })
	g := newGuardRouter(t)

	methods := make([]string, 0, len(rpcdSignatures))
	for m := range rpcdSignatures {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	kinds := make([]string, 0, len(g.creds))
	for k := range g.creds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if len(kinds) < 30 {
		t.Fatalf("only %d credentials in the fixtures; the router is not the one these tests stand for", len(kinds))
	}

	// Anti-vacuity: unscrubbed, the answers do carry them — the fixtures reach
	// the answers, and the scrub is what keeps them out.
	leaked := map[string]bool{}
	for _, m := range methods {
		var raw bytes.Buffer
		_ = writeJSON(&raw, rpcdCall(context.Background(), g.cfg, m, []byte(guardParams[m])))
		for _, k := range kinds {
			if bytes.Contains(raw.Bytes(), []byte(g.creds[k])) {
				leaked[k] = true
			}
		}
	}
	for _, k := range []string{"subscription url", "subscription token", "panel token", "hwid", "device key", "id#1", "publicKey#2", "shortId#3"} {
		if !leaked[k] {
			t.Errorf("unscrubbed, no answer carries the %s: the fixtures do not reach the answers", k)
		}
	}

	var logs []byte
	for _, m := range methods {
		var out bytes.Buffer
		if err := writeJSON(&out, rpcdAnswer(context.Background(), g.cfg, m, []byte(guardParams[m]))); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if !json.Valid(out.Bytes()) || !bytes.HasPrefix(bytes.TrimSpace(out.Bytes()), []byte("{")) {
			t.Fatalf("%s answered what rpcd refuses: %s", m, out.Bytes())
		}
		for _, k := range kinds {
			if bytes.Contains(out.Bytes(), []byte(g.creds[k])) {
				t.Errorf("%s answers the %s", m, k)
			}
		}
		if m == "logs" {
			logs = out.Bytes()
		}
	}

	// What the diagnosis needs stays: node tags, hosts and ports, verdicts.
	for _, want := range []string{"bridge-de5", "ru11.provider.invalid:40052", "ru8.provider.invalid:40055", "https://sub.provider.invalid/<redacted>", "context deadline exceeded"} {
		if !bytes.Contains(logs, []byte(want)) {
			t.Errorf("the journal lost %q:\n%s", want, logs)
		}
	}
}

// Some strings are not credentials, however they look, and the UI needs them
// exactly: the router's own id (support asks for it; it opens nothing
// without the panel token), the claim code, its QR and bot link (ADR-0006:
// shown to link the router), the owner's own sites (a list the UI edits and
// sends back whole) and Wi-Fi names.
func TestTheScrubKeepsWhatTheUIShowsVerbatim(t *testing.T) {
	claim := func() *uiapi.Setup {
		code := "7ZKNPGS6"
		qr, bot := guardClaimQR, "https://t.me/VectraBot?start=rt_7ZKNPGS6"
		ssid := "token=not-a-real-looking-ssid"
		return &uiapi.Setup{Wifi: uiapi.SetupWifi{Radios: []uiapi.SetupRadio{{Device: "radio0", SSID: &ssid}}},
			Vectra: uiapi.SetupVectra{Claim: &uiapi.SetupClaim{State: "unclaimed", Code: code, QR: &qr, BotURL: &bot}}}
	}
	id := guardRouterID
	for _, c := range []struct {
		method string
		answer interface{}
		keep   []string
	}{
		{"status", uiapi.Status{ControlPlane: uiapi.ControlPlane{RouterID: &id}}, []string{guardRouterID}},
		{"setup", claim(), []string{guardClaimQR, "https://t.me/VectraBot?start=rt_7ZKNPGS6", "7ZKNPGS6", "token=not-a-real-looking-ssid"}},
		{"rules", uiapi.Rules{Direct: []string{"0badc0de-feed-4000-8000-000000000001.example.org"}, Proxy: []string{"a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4.example.org"}},
			[]string{"0badc0de-feed-4000-8000-000000000001.example.org", "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4.example.org"}},
	} {
		out, err := json.Marshal(rpcdScrub(c.method, c.answer, nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range c.keep {
			if !bytes.Contains(out, []byte(`"`+k+`"`)) {
				t.Errorf("%s lost %q:\n%s", c.method, k, out)
			}
		}
	}
	// The same strings anywhere else are scrubbed.
	out, _ := json.Marshal(rpcdScrub("entries", uiapi.Entries{Entries: []uiapi.Entry{{Remark: guardRouterID + " " + guardClaimQR}}}, nil))
	if bytes.Contains(out, []byte(guardRouterID)) {
		t.Errorf("a UUID outside status.controlPlane.routerId was kept: %s", out)
	}
}
