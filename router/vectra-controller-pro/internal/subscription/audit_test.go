package subscription

import "testing"

// TestNoSilentNormalization_Trojan asserts that when Trojan parser fills in
// the protocol-required "security=tls" the choice is recorded in ParserDefaults
// (audit trail), NOT silently applied.
func TestNoSilentNormalization_Trojan(t *testing.T) {
	// No "security=" in URI; parser must default to "tls" AND record it.
	u := "trojan://pwd@srv:443?sni=srv#t"
	n, err := parseTrojan(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Stream.Security != "tls" {
		t.Fatalf("expected security=tls, got %q", n.Stream.Security)
	}
	if n.ParserDefaults()["stream.security"] == "" {
		t.Errorf("expected ParserDefaults to record stream.security default; got %v", n.ParserDefaults())
	}
}

// TestNoSilentNormalization_VLESS: when upstream sets security explicitly,
// nothing is recorded in ParserDefaults.
func TestNoSilentNormalization_VLESSExplicit(t *testing.T) {
	u := "vless://uu@h:443?type=tcp&security=reality&sni=s&pbk=K&sid=I&fp=firefox#one"
	n, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if pd := n.ParserDefaults(); pd != nil {
		t.Errorf("vless URI was fully explicit; no ParserDefaults expected, got %v", pd)
	}
}

// TestAllowInsecureStrippedFromSubscription asserts the security gate: a
// subscription URI requesting allowInsecure=1 must NOT yield a node with TLS
// verification disabled, and the strip must be recorded in the audit trail.
func TestAllowInsecureStrippedFromSubscription(t *testing.T) {
	u := "vless://uu@h:443?type=tcp&security=tls&sni=s&allowInsecure=1#hostile"
	p, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Stream.AllowInsec {
		t.Fatalf("precondition: parser should record allowInsecure=1 from URI")
	}
	nodes := ToConfigNodes([]ParsedNode{p}, SubscriptionRef{ID: "sub1", URL: "https://feed"})
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Outbound.Stream == nil || n.Outbound.Stream.TLS == nil {
		t.Fatalf("expected a TLS stream on the imported node, got %#v", n.Outbound.Stream)
	}
	if n.Outbound.Stream.TLS.AllowInsecure {
		t.Error("SECURITY: allowInsecure must be stripped from a subscription-sourced node")
	}
	// Audit trail must record the strip.
	if n.Origin == nil || n.Origin.ParserDefaults["stream.tls.allowInsecure"] == "" {
		t.Errorf("expected audit entry for stripped allowInsecure, got Origin=%#v", n.Origin)
	}
}

// TestAllowInsecureNotRecordedWhenUpstreamDidNotSetIt: when the URI never
// requested allowInsecure, no strip-audit entry is added and TLS verify stays on.
func TestAllowInsecureNotRecordedWhenUpstreamDidNotSetIt(t *testing.T) {
	u := "vless://uu@h:443?type=tcp&security=tls&sni=s#clean"
	p, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	nodes := ToConfigNodes([]ParsedNode{p}, SubscriptionRef{ID: "sub1", URL: "https://feed"})
	n := nodes[0]
	if n.Outbound.Stream.TLS.AllowInsecure {
		t.Error("allowInsecure must remain false")
	}
	if n.Origin != nil && n.Origin.ParserDefaults["stream.tls.allowInsecure"] != "" {
		t.Errorf("no strip-audit expected when URI never set allowInsecure, got %v", n.Origin.ParserDefaults)
	}
}
