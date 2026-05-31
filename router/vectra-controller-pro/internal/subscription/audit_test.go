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
