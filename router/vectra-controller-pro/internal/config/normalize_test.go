package config

import "testing"

func fpNode(id, tlsFP, realityFP string) Node {
	st := &StreamSettings{Transport: "tcp"}
	if tlsFP != "" {
		st.Security = "tls"
		st.TLS = &TLSSettings{ServerName: "s", Fingerprint: tlsFP}
	}
	if realityFP != "" {
		st.Security = "reality"
		st.REALITY = &REALITYSettings{ServerName: "s", PublicKey: "K", Fingerprint: realityFP}
	}
	return Node{
		ID: id, Tag: "node-" + id, Enabled: true,
		Outbound: Outbound{Protocol: "vless", Server: "h", Port: 443,
			Settings: ProtocolSettings{VLESS: &VLESSSettings{UUID: "u"}},
			Stream:   st},
	}
}

// TestApplyNormalization_OffByDefault: a zero Normalization block changes
// nothing and fingerprints round-trip untouched.
func TestApplyNormalization_OffByDefault(t *testing.T) {
	c := &Config{
		Nodes: []Node{fpNode("a", "firefox", ""), fpNode("b", "", "chrome")},
		// Normalization left zero-valued -> ForceFingerprint=false.
	}
	changes := ApplyNormalization(c)
	if len(changes) != 0 {
		t.Fatalf("expected no changes with ForceFingerprint off, got %v", changes)
	}
	if c.Nodes[0].Outbound.Stream.TLS.Fingerprint != "firefox" {
		t.Errorf("tls fingerprint mutated while off: %q", c.Nodes[0].Outbound.Stream.TLS.Fingerprint)
	}
	if c.Nodes[1].Outbound.Stream.REALITY.Fingerprint != "chrome" {
		t.Errorf("reality fingerprint mutated while off: %q", c.Nodes[1].Outbound.Stream.REALITY.Fingerprint)
	}
}

// TestApplyNormalization_ForceFingerprintOverrides: when on, every TLS and
// REALITY fingerprint is overridden and each change is reported.
func TestApplyNormalization_ForceFingerprintOverrides(t *testing.T) {
	c := &Config{
		Nodes: []Node{
			fpNode("a", "firefox", ""),
			fpNode("b", "", "safari"),
			fpNode("c", "chrome", ""), // already differs from target
		},
		Normalization: Normalization{ForceFingerprint: true, FingerprintValue: "chrome"},
	}
	changes := ApplyNormalization(c)
	// node a (tls firefox->chrome) and node b (reality safari->chrome) change;
	// node c is already "chrome" so it is left untouched and not logged.
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d: %v", len(changes), changes)
	}
	if c.Nodes[0].Outbound.Stream.TLS.Fingerprint != "chrome" {
		t.Errorf("node a tls fp = %q, want chrome", c.Nodes[0].Outbound.Stream.TLS.Fingerprint)
	}
	if c.Nodes[1].Outbound.Stream.REALITY.Fingerprint != "chrome" {
		t.Errorf("node b reality fp = %q, want chrome", c.Nodes[1].Outbound.Stream.REALITY.Fingerprint)
	}
	if c.Nodes[2].Outbound.Stream.TLS.Fingerprint != "chrome" {
		t.Errorf("node c tls fp = %q, want chrome", c.Nodes[2].Outbound.Stream.TLS.Fingerprint)
	}
}

// TestApplyNormalization_ForceFingerprintEmptyValue: ForceFingerprint with an
// empty FingerprintValue clears non-empty fingerprints (operator explicitly
// asked to force "no fingerprint"). Still logged.
func TestApplyNormalization_ForceFingerprintEmptyValue(t *testing.T) {
	c := &Config{
		Nodes:         []Node{fpNode("a", "firefox", "")},
		Normalization: Normalization{ForceFingerprint: true, FingerprintValue: ""},
	}
	changes := ApplyNormalization(c)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %v", changes)
	}
	if c.Nodes[0].Outbound.Stream.TLS.Fingerprint != "" {
		t.Errorf("expected fingerprint cleared, got %q", c.Nodes[0].Outbound.Stream.TLS.Fingerprint)
	}
}

func TestApplyNormalization_NilSafe(t *testing.T) {
	if got := ApplyNormalization(nil); got != nil {
		t.Errorf("ApplyNormalization(nil) = %v, want nil", got)
	}
}
