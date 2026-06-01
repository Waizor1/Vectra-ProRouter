package xray

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
)

// Helper: a minimal config covering the fleet's P0 surface (VLESS+REALITY+Vision/gRPC+tproxy+shunt+DoH).
func p0Config() *config.Config {
	c := &config.Config{
		Schema:   1,
		Instance: config.Instance{Name: "t", LogLevel: "warning"},
		Process: config.Process{
			XrayBinary: "/usr/bin/xray", WorkDir: "/var/run/vctl",
			OOMScoreAdj: -100,
			RestartBackoff: config.Backoff{InitialMs: 500, Factor: 2, MaxMs: 60000, Reset: "60s"},
		},
		Inbounds: config.Inbounds{
			Tproxy: &config.TproxyInbound{
				ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, UDPEnabled: true, Tag: "tproxy-in",
				Sniffing: config.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
			},
		},
		DNS: config.DNS{
			QueryStrategy: "UseIPv4",
			Servers: []config.DNSServer{
				{Address: "https://dns.google/dns-query", QueryStrategy: "UseIPv4"},
				{Address: "8.8.8.8"},
			},
			Hosts: map[string]string{"dns.google": "8.8.8.8"},
		},
		Nodes: []config.Node{
			{
				ID: "world", Tag: "node-world", Enabled: true,
				Outbound: config.Outbound{
					Protocol: "vless", Server: "de1.example", Port: 443,
					Settings: config.ProtocolSettings{VLESS: &config.VLESSSettings{
						UUID: "11111111-2222-3333-4444-555555555555",
						Flow: "xtls-rprx-vision", Encryption: "none",
					}},
					Stream: &config.StreamSettings{
						Transport: "tcp", Security: "reality",
						REALITY: &config.REALITYSettings{
							ServerName: "www.cloudflare.com", PublicKey: "PUBKEY",
							ShortID: "abcdef", SpiderX: "/", Fingerprint: "firefox",
						},
					},
				},
			},
			{
				ID: "pl-disc", Tag: "node-pl-disc", Enabled: true,
				Outbound: config.Outbound{
					Protocol: "vless", Server: "pl1.example", Port: 443,
					Settings: config.ProtocolSettings{VLESS: &config.VLESSSettings{
						UUID: "11111111-2222-3333-4444-555555555555",
						Encryption: "none",
					}},
					Stream: &config.StreamSettings{
						Transport: "grpc", Security: "reality",
						GRPC: &config.GRPCSettings{ServiceName: "voice"},
						REALITY: &config.REALITYSettings{
							ServerName: "www.microsoft.com", PublicKey: "PUBKEY",
							ShortID: "00", Fingerprint: "chrome",
						},
					},
					Mux: &config.MuxSettings{Enabled: true, Concurrency: -1, XUDPConcurrency: 16, PacketEncoding: "xudp"},
				},
			},
		},
		Routing: config.Routing{
			DomainStrategy: "IPIfNonMatch", DomainMatcher: "hybrid",
			Rules: []config.RoutingRule{
				{Domain: []string{"geosite:cn"}, OutboundTag: "direct"},
				{Network: "udp", Port: "19294-19344,50000-50100", OutboundTag: "node-pl-disc"},
				{OutboundTag: "node-world"},
			},
		},
		Geo: config.Geo{AssetDir: "/usr/share/xray"},
	}
	return c
}

func TestRender_P0_NoSilentNormalization(t *testing.T) {
	c := p0Config()
	out, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// Parse for assertions; we don't want a brittle whole-byte golden,
	// we want behavioral checks plus a snapshot for visual diffing.
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// First: every operator-set value must reach the JSON unchanged.
	str := string(out)
	for _, must := range []string{
		`"fingerprint": "firefox"`,
		`"fingerprint": "chrome"`,
		`"flow": "xtls-rprx-vision"`,
		`"serviceName": "voice"`,
		`"shortId": "abcdef"`,
		`"shortId": "00"`,
		`"xudpConcurrency": 16`,
		`"packetEncoding": "xudp"`,
		`"tproxy": "tproxy"`,
		`"mark": 1`,
	} {
		if !strings.Contains(str, must) {
			t.Errorf("expected output to contain %q\n--- got ---\n%s", must, str)
		}
	}
	// Outbound count = 3 synthetic (direct/block/dns-out) + 2 real
	outbounds, _ := doc["outbounds"].([]any)
	if len(outbounds) != 5 {
		t.Errorf("expected 5 outbounds, got %d", len(outbounds))
	}
	// Inbounds = tproxy
	inbounds, _ := doc["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Errorf("expected 1 inbound, got %d", len(inbounds))
	}
	// Routing rules = 3 operator + 0 synthesized (no DNS/API inbounds here)
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	if len(rules) != 3 {
		t.Errorf("expected 3 routing rules, got %d", len(rules))
	}
}

func TestRender_DeterministicOutput(t *testing.T) {
	c := p0Config()
	a, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("render not deterministic")
	}
}

func TestRender_DNSOutboundSynthesizedWhenInboundPresent(t *testing.T) {
	c := p0Config()
	c.Inbounds.DNS = &config.DNSInbound{ListenIP: "127.0.0.1", Port: 5353, Network: "tcp,udp", Tag: "dns-in"}
	out, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range doc.Routing.Rules {
		if r["outboundTag"] != "dns-out" {
			continue
		}
		inb, _ := r["inboundTag"].([]any)
		if len(inb) == 1 && inb[0] == "dns-in" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected synthesized dns-in -> dns-out rule, got rules=%v", doc.Routing.Rules)
	}
}

func TestRender_APIInboundSynthesizedWhenListenSet(t *testing.T) {
	c := p0Config()
	c.API = &config.APIConfig{Tag: "api", Services: []string{"HandlerService", "StatsService"}, Listen: "127.0.0.1:10085"}
	out, err := New().Render(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"tag": "api"`) || !strings.Contains(s, `"listen": "127.0.0.1"`) {
		t.Errorf("expected API inbound synthesized:\n%s", s)
	}
}

func TestRender_RealityFingerprintPreserved(t *testing.T) {
	// Critical: must NOT rewrite operator-set fingerprint.
	c := p0Config()
	c.Nodes[0].Outbound.Stream.REALITY.Fingerprint = "edge_77"
	out, _ := New().Render(context.Background(), c)
	if !strings.Contains(string(out), `"fingerprint": "edge_77"`) {
		t.Fatalf("fingerprint not preserved as-is — silent normalization detected")
	}
}
