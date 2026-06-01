package xray

import (
	"context"
	"encoding/json"
	"testing"

	"vectra-controller-pro/internal/config"
)

// renderDoc renders c and unmarshals the result into a generic map for
// structural assertions. Fails the test on any error.
func renderDoc(t *testing.T, c *config.Config) map[string]any {
	t.Helper()
	out, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("rendered output is not valid JSON:\n%s", out)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

// ---- Item 1: Observatory / BurstObservatory ----

func TestRender_Observatory(t *testing.T) {
	c := p0Config()
	c.Observatory = &config.ObservatoryConfig{
		SubjectSelector:   []string{"node-"},
		ProbeURL:          "https://www.google.com/generate_204",
		ProbeInterval:     "10m",
		EnableConcurrency: true,
	}
	doc := renderDoc(t, c)
	obs, ok := doc["observatory"].(map[string]any)
	if !ok {
		t.Fatalf("expected observatory block, got %T (%v)", doc["observatory"], doc["observatory"])
	}
	if obs["probeURL"] != "https://www.google.com/generate_204" {
		t.Errorf("probeURL = %v", obs["probeURL"])
	}
	if obs["probeInterval"] != "10m" {
		t.Errorf("probeInterval = %v", obs["probeInterval"])
	}
	if obs["enableConcurrency"] != true {
		t.Errorf("enableConcurrency = %v", obs["enableConcurrency"])
	}
	sel, _ := obs["subjectSelector"].([]any)
	if len(sel) != 1 || sel[0] != "node-" {
		t.Errorf("subjectSelector = %v", obs["subjectSelector"])
	}
}

func TestRender_BurstObservatory(t *testing.T) {
	c := p0Config()
	c.BurstObservatory = &config.BurstObservatoryConfig{
		SubjectSelector: []string{"node-world"},
		PingConfig: &config.ObservatoryPing{
			Destination:   "https://connectivitycheck.gstatic.com/generate_204",
			Interval:      "5m",
			Timeout:       "30s",
			SamplingCount: 3,
		},
	}
	doc := renderDoc(t, c)
	bo, ok := doc["burstObservatory"].(map[string]any)
	if !ok {
		t.Fatalf("expected burstObservatory block, got %T", doc["burstObservatory"])
	}
	pc, ok := bo["pingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected pingConfig, got %T", bo["pingConfig"])
	}
	if pc["destination"] != "https://connectivitycheck.gstatic.com/generate_204" {
		t.Errorf("destination = %v", pc["destination"])
	}
	if pc["interval"] != "5m" || pc["timeout"] != "30s" {
		t.Errorf("interval/timeout = %v / %v", pc["interval"], pc["timeout"])
	}
	if pc["samplingCount"] != float64(3) {
		t.Errorf("samplingCount = %v", pc["samplingCount"])
	}
}

func TestRender_NoObservatoryWhenUnset(t *testing.T) {
	c := p0Config()
	doc := renderDoc(t, c)
	if _, present := doc["observatory"]; present {
		t.Error("observatory must be omitted when unset")
	}
	if _, present := doc["burstObservatory"]; present {
		t.Error("burstObservatory must be omitted when unset")
	}
}

// ---- Item 2: HTTP/2 ("http") transport ----

func TestRender_HTTP2Transport(t *testing.T) {
	c := p0Config()
	c.Nodes[0].Outbound.Stream = &config.StreamSettings{
		Transport: "http",
		Security:  "tls",
		HTTP:      &config.HTTPSettings{Host: []string{"a.example.com", "b.example.com"}, Path: "/v2"},
		TLS:       &config.TLSSettings{ServerName: "a.example.com"},
	}
	// Remove vless flow that requires raw tcp to keep the node valid-ish for render.
	doc := renderDoc(t, c)
	ob := findOutboundByTag(t, doc, "node-world")
	ss, ok := ob["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("expected streamSettings, got %T", ob["streamSettings"])
	}
	if ss["network"] != "http" {
		t.Errorf("network = %v, want http", ss["network"])
	}
	hs, ok := ss["httpSettings"].(map[string]any)
	if !ok {
		t.Fatalf("expected httpSettings, got %T (full ss=%v)", ss["httpSettings"], ss)
	}
	if hs["path"] != "/v2" {
		t.Errorf("path = %v", hs["path"])
	}
	host, _ := hs["host"].([]any)
	if len(host) != 2 || host[0] != "a.example.com" {
		t.Errorf("host = %v", hs["host"])
	}
}

func TestValidate_HTTPTransportAccepted(t *testing.T) {
	c := p0Config()
	c.Nodes[0].Outbound.Stream = &config.StreamSettings{
		Transport: "http",
		HTTP:      &config.HTTPSettings{Host: []string{"h"}, Path: "/"},
	}
	if err := config.Validate(c); err != nil {
		t.Fatalf("http transport must validate, got: %v", err)
	}
}

// ---- Item 3: REALITY inbound + inbound streamSettings ----

func TestRender_RealityInbound(t *testing.T) {
	c := p0Config()
	c.Inbounds.Reality = &config.RealityInbound{
		ListenIP: "0.0.0.0",
		Port:     8443,
		Protocol: "vless",
		Settings: map[string]any{
			"clients":    []any{map[string]any{"id": "uuid-here", "flow": "xtls-rprx-vision"}},
			"decryption": "none",
		},
		Stream: &config.StreamSettings{
			Transport: "tcp",
			Security:  "reality",
			REALITY: &config.REALITYSettings{
				PrivateKey:  "PRIV",
				Dest:        "www.microsoft.com:443",
				Xver:        0,
				ServerNames: []string{"www.microsoft.com"},
				ShortIDs:    []string{"", "0123abcd"},
			},
		},
		Tag: "reality-in",
	}
	doc := renderDoc(t, c)
	ib := findInboundByTag(t, doc, "reality-in")
	if ib["protocol"] != "vless" {
		t.Errorf("protocol = %v", ib["protocol"])
	}
	if ib["port"] != float64(8443) {
		t.Errorf("port = %v", ib["port"])
	}
	// settings passthrough
	st, ok := ib["settings"].(map[string]any)
	if !ok || st["decryption"] != "none" {
		t.Errorf("settings passthrough wrong: %v", ib["settings"])
	}
	ss, _ := ib["streamSettings"].(map[string]any)
	rs, ok := ss["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("expected realitySettings on inbound, got %v", ss)
	}
	if rs["privateKey"] != "PRIV" || rs["dest"] != "www.microsoft.com:443" {
		t.Errorf("server-side reality fields wrong: %v", rs)
	}
	sids, _ := rs["shortIds"].([]any)
	if len(sids) != 2 {
		t.Errorf("shortIds = %v", rs["shortIds"])
	}
	// client-only fields must NOT be emitted on the server-side block.
	if _, present := rs["publicKey"]; present {
		t.Error("server-side reality must not emit publicKey")
	}
	if _, present := rs["serverName"]; present {
		t.Error("server-side reality must not emit singular serverName")
	}
}

func TestRender_RealityOnlyInboundNotEmpty(t *testing.T) {
	// Correctness trap: a reality-only inbound config must render a non-empty
	// inbound list (previously it validated but rendered nothing).
	c := p0Config()
	c.Inbounds.Tproxy = nil // remove the default inbound; reality is the only one
	c.Inbounds.Reality = &config.RealityInbound{
		ListenIP: "0.0.0.0", Port: 8443, Protocol: "vless",
		Settings: map[string]any{"decryption": "none"},
		Tag:      "reality-in",
	}
	if err := config.Validate(c); err != nil {
		t.Fatalf("reality-only config should validate: %v", err)
	}
	doc := renderDoc(t, c)
	ibs, _ := doc["inbounds"].([]any)
	if len(ibs) == 0 {
		t.Fatal("reality-only inbound rendered an EMPTY inbound list (regression)")
	}
	findInboundByTag(t, doc, "reality-in")
}

func TestRender_SocksOverTLS(t *testing.T) {
	// SOCKS-over-TLS must be expressible via the inbound Stream field.
	c := p0Config()
	c.Inbounds.Socks = &config.SocksInbound{
		ListenIP: "127.0.0.1", Port: 1080, Auth: "noauth", Tag: "socks-in",
		Stream: &config.StreamSettings{
			Transport: "tcp", Security: "tls",
			TLS: &config.TLSSettings{ServerName: "local", Certificates: []config.TLSCertificate{
				{CertificateFile: "/etc/x/cert.pem", KeyFile: "/etc/x/key.pem"},
			}},
		},
	}
	doc := renderDoc(t, c)
	ib := findInboundByTag(t, doc, "socks-in")
	ss, ok := ib["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("socks inbound has no streamSettings: %v", ib)
	}
	if ss["security"] != "tls" {
		t.Errorf("security = %v", ss["security"])
	}
	if _, ok := ss["tlsSettings"].(map[string]any); !ok {
		t.Errorf("expected tlsSettings on socks inbound, got %v", ss)
	}
}

func TestRender_HTTPAndSSInboundStreamWiredThrough(t *testing.T) {
	c := p0Config()
	c.Inbounds.HTTP = &config.HTTPInbound{
		ListenIP: "127.0.0.1", Port: 8080, Tag: "http-in",
		Stream: &config.StreamSettings{Transport: "ws", WS: &config.WSSettings{Path: "/h"}},
	}
	c.Inbounds.Shadowsocks = &config.SSInbound{
		ListenIP: "0.0.0.0", Port: 8388, Method: "aes-128-gcm", Password: "pw", Tag: "ss-in",
		Stream: &config.StreamSettings{Transport: "ws", WS: &config.WSSettings{Path: "/s"}},
	}
	doc := renderDoc(t, c)
	for _, tag := range []string{"http-in", "ss-in"} {
		ib := findInboundByTag(t, doc, tag)
		ss, ok := ib["streamSettings"].(map[string]any)
		if !ok {
			t.Fatalf("%s: expected streamSettings, got %v", tag, ib)
		}
		if ss["network"] != "ws" {
			t.Errorf("%s: network = %v", tag, ss["network"])
		}
	}
}

// ---- Item 6: ruleTag + metrics inbound auto-synthesis ----

func TestRender_RuleTagEmitted(t *testing.T) {
	c := p0Config()
	c.Routing.Rules = []config.RoutingRule{
		{Domain: []string{"geosite:cn"}, OutboundTag: "direct", Tag: "cn-direct"},
		{OutboundTag: "node-world"},
	}
	doc := renderDoc(t, c)
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	found := false
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if rm["ruleTag"] == "cn-direct" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a rule with ruleTag=cn-direct, rules=%v", rules)
	}
}

func TestRender_MetricsInboundSynthesizedWhenListenSet(t *testing.T) {
	c := p0Config()
	c.Metrics = &config.MetricsConfig{Tag: "metrics_in", Listen: "127.0.0.1:11111"}
	doc := renderDoc(t, c)
	// metrics block present
	mb, ok := doc["metrics"].(map[string]any)
	if !ok || mb["tag"] != "metrics_in" {
		t.Fatalf("metrics block missing/wrong: %v", doc["metrics"])
	}
	// matching dokodemo inbound present
	ib := findInboundByTag(t, doc, "metrics_in")
	if ib["protocol"] != "dokodemo-door" {
		t.Errorf("metrics inbound protocol = %v", ib["protocol"])
	}
	if ib["port"] != float64(11111) {
		t.Errorf("metrics inbound port = %v", ib["port"])
	}
	// matching routing rule present
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	found := false
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		inb, _ := rm["inboundTag"].([]any)
		if len(inb) == 1 && inb[0] == "metrics_in" && rm["outboundTag"] == "metrics_in" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected synthesized metrics_in routing rule, rules=%v", rules)
	}
}

func TestRender_MetricsInboundNotSynthesizedWithoutListen(t *testing.T) {
	c := p0Config()
	c.Metrics = &config.MetricsConfig{Tag: "metrics_in"} // no Listen
	doc := renderDoc(t, c)
	if _, ok := doc["metrics"].(map[string]any); !ok {
		t.Fatal("metrics block should still be emitted")
	}
	ibs, _ := doc["inbounds"].([]any)
	for _, ib := range ibs {
		ibm, _ := ib.(map[string]any)
		if ibm["tag"] == "metrics_in" {
			t.Error("metrics inbound must NOT be synthesized without a listen address")
		}
	}
}

// ---- Item 4: ForceFingerprint through the engine Render path ----

func TestRender_ForceFingerprint_OverridesAndDoesNotMutateCaller(t *testing.T) {
	c := p0Config()
	// p0Config node[0] reality fp = "firefox"; node[1] reality fp = "chrome".
	c.Normalization = config.Normalization{ForceFingerprint: true, FingerprintValue: "edge"}
	doc := renderDoc(t, c)
	for _, tag := range []string{"node-world", "node-pl-disc"} {
		ob := findOutboundByTag(t, doc, tag)
		ss, _ := ob["streamSettings"].(map[string]any)
		rs, _ := ss["realitySettings"].(map[string]any)
		if rs["fingerprint"] != "edge" {
			t.Errorf("%s: reality fingerprint = %v, want edge", tag, rs["fingerprint"])
		}
	}
	// The caller's config must be untouched (Render clones before normalizing).
	if got := c.Nodes[0].Outbound.Stream.REALITY.Fingerprint; got != "firefox" {
		t.Errorf("caller config mutated by Render: node[0] reality fp = %q, want firefox", got)
	}
}

func TestRender_ForceFingerprintOff_PreservesOperatorValue(t *testing.T) {
	c := p0Config() // Normalization zero-valued -> off
	doc := renderDoc(t, c)
	ob := findOutboundByTag(t, doc, "node-world")
	ss, _ := ob["streamSettings"].(map[string]any)
	rs, _ := ss["realitySettings"].(map[string]any)
	if rs["fingerprint"] != "firefox" {
		t.Errorf("fingerprint = %v, want operator-set firefox (no normalization)", rs["fingerprint"])
	}
}

// ---- helpers ----

func findOutboundByTag(t *testing.T, doc map[string]any, tag string) map[string]any {
	t.Helper()
	obs, _ := doc["outbounds"].([]any)
	for _, o := range obs {
		om, _ := o.(map[string]any)
		if om["tag"] == tag {
			return om
		}
	}
	t.Fatalf("outbound tag %q not found", tag)
	return nil
}

func findInboundByTag(t *testing.T, doc map[string]any, tag string) map[string]any {
	t.Helper()
	ibs, _ := doc["inbounds"].([]any)
	for _, ib := range ibs {
		ibm, _ := ib.(map[string]any)
		if ibm["tag"] == tag {
			return ibm
		}
	}
	t.Fatalf("inbound tag %q not found", tag)
	return nil
}
