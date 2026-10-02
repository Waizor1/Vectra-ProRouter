package redact

import (
	"strings"
	"testing"
)

// Credentials takes out what is a credential by its shape and keeps what a
// diagnosis needs: hosts, addresses, ports, node tags, times.
func TestCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"node vless://00000000-0000-4000-8000-000000000001@ru1.provider.invalid:443?security=reality&pbk=AbC#DE": "node <link>",
		"trojan://not-a-real-password@h.invalid:443 and hy2://x@h.invalid:1":                                     "<link> and <link>",
		"ss://YWVzLTI1Ni1nY206eA@h.invalid:8388#x":                                                               "<link>",
		"happ://crypt5/AbCdEf0123456789":                                                                         "<link>",
		"fetch https://sub.provider.invalid/api/sub/AbCdEf?x=1 failed":                                           "fetch https://sub.provider.invalid/<redacted> failed",
		`Get "https://sub.provider.invalid:8443/s/tok": EOF`:                                                     `Get "https://sub.provider.invalid:8443/<redacted>": EOF`,
		"https://user:pw@h.invalid/":                                                                             "https://h.invalid/<redacted>",
		"panel https://api.vectra-pro.net and https://api.vectra-pro.net/":                                       "panel https://api.vectra-pro.net and https://api.vectra-pro.net/",
		"(see https://h.invalid/a/b).":                                                                           "(see https://h.invalid/<redacted>).",
		"user 00000000-0000-4000-8000-000000000001 logged":                                                       "user <uuid> logged",
		`{"password":"not-a-real-password","port":443}`:                                                          `{"password":"<redacted>","port":443}`,
		`"publicKey": "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA", "shortId":"0123abcd"`:                       `"publicKey":"<redacted>", "shortId":"<redacted>"`,
		`{"user":"u","pass":"not-a-real-one"}`:                                                                   `{"user":"u","pass":"<redacted>"}`,
		"pbk=AbC sid=12ab uuid=x":                                                                                "pbk=<redacted> sid=<redacted> uuid=<redacted>",
		"x-vectra-router-token: tok123 x-hwid: 4f8e":                                                             "x-vectra-router-token: <redacted> x-hwid: <redacted>",
		"password=hunter2 token: abc api_key=zzz":                                                                "password=<redacted> token: <redacted> api_key=<redacted>",
		// What a diagnosis needs stays.
		"dial tcp 203.0.113.9:443: i/o timeout":                   "dial tcp 203.0.113.9:443: i/o timeout",
		"lookup ru12.provider.invalid: no such host":              "lookup ru12.provider.invalid: no such host",
		"bridge-de5 dead, moved to whitelist-lv3-2 (BL-MAIN)":     "bridge-de5 dead, moved to whitelist-lv3-2 (BL-MAIN)",
		"2026/09/27 16:33:18.749926 [Warning] core: Xray 26.3.27": "2026/09/27 16:33:18.749926 [Warning] core: Xray 26.3.27",
		"p2p_bypass=1 dns_rate=40":                                "p2p_bypass=1 dns_rate=40",
		// A long run is not a credential by its shape alone: Text takes those.
		"hwid 4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7": "hwid 4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7",
	} {
		if got := Credentials(in); got != want {
			t.Errorf("Credentials(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// Text is Credentials for free text, where a key or a token is printed bare.
func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"hwid 4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7": "hwid <secret>",
		"pbk AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA ok":                    "pbk <secret> ok",
		"agent ZGVhZGJlZWYtbm90LXJlYWwtdG9rZW4tMTIzNDU2":                        "agent <secret>",
		"fetch https://sub.provider.invalid/api/sub/AbCdEf failed":              "fetch https://sub.provider.invalid/<redacted> failed",
		"bridge-de5 dead, moved to whitelist-lv3-2 at 16:33:18":                 "bridge-de5 dead, moved to whitelist-lv3-2 at 16:33:18",
		"vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)":   "vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// Known values are replaced wherever they appear, whatever their shape; a
// value too short to be one cannot shred the text.
func TestKnown(t *testing.T) {
	k := NewKnown("not-a-real-password", "abc", "", "0123456789abcdef", "not-a-real-password")
	if len(k) != 2 {
		t.Fatalf("known = %q, want the two long values once each", k)
	}
	got := k.Redact("auth not-a-real-password failed; sid 0123456789abcdef; abc stays")
	if want := "auth <redacted> failed; sid <redacted>; abc stays"; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Longest first: a value inside another leaves no remnant.
	k = NewKnown("abcdefgh", "xxabcdefghyy")
	if got := k.Redact("xxabcdefghyy"); got != "<redacted>" {
		t.Fatalf("got %q", got)
	}
}

// UCI writes a secret as `option name 'value'`: PassWall2's nodes and Wi-Fi's
// key. Neither = nor : — the key=value rule missed them.
func TestUCIOptionsLoseTheirSecrets(t *testing.T) {
	in := "config nodes 'n1'\n\toption address 'pl5.example.net'\n\toption uuid 'synthetic-uuid-value'\n\toption password 'synthetic-node-pass'\n\toption public_key 'synthetic-reality-pbk'\n\toption short_id 'abcd'\nwifi-iface\n\toption key 'synthetic-wifi-key'\n\toption ssid 'Home'\n"
	out := Credentials(in)
	for _, secret := range []string{"synthetic-uuid-value", "synthetic-node-pass", "synthetic-reality-pbk", "synthetic-wifi-key", "'abcd'"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q left in %q", secret, out)
		}
	}
	for _, kept := range []string{"pl5.example.net", "'Home'", "config nodes"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("%q lost from %q", kept, out)
		}
	}
}
