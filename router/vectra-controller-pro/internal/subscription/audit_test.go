package subscription

import "testing"

// These tests cover the DEGRADED link-list path only. It is off the default
// apply path since the provider-JSON pivot, but the parsers must still refuse
// to invent or hide values.
//
// The allowInsecure ENFORCEMENT used to live in the URI->config.Node adapter,
// which is gone along with the config builder. Enforcement is now a read-only
// scan over the raw provider document: see
// internal/coreengine/xray.ScanAllowInsecure and its tests.

// TestNoSilentNormalization_Trojan asserts that when the Trojan parser fills in
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

// TestAllowInsecureSurfacedByParser: the parser must SURFACE an upstream
// allowInsecure request rather than swallow it, so the caller can refuse.
func TestAllowInsecureSurfacedByParser(t *testing.T) {
	u := "vless://uu@h:443?type=tcp&security=tls&sni=s&allowInsecure=1#hostile"
	p, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Stream.AllowInsec {
		t.Fatal("parser must record allowInsecure=1 from the URI so it can be refused upstream")
	}
}

func TestAllowInsecureNotSetWhenUpstreamDidNotAskForIt(t *testing.T) {
	u := "vless://uu@h:443?type=tcp&security=tls&sni=s#clean"
	p, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stream.AllowInsec {
		t.Error("allowInsecure must remain false when the URI never set it")
	}
}
