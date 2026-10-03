package xray_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// The provider's log block is the router's: whatever it says — a file for the
// access or error log, debug level, the DNS log — xray runs with the router's
// own, in every spelling xray takes for "log".
func TestSpliceReplacesTheProvidersWholeLog(t *testing.T) {
	for _, key := range []string{"log", "Log", "LOG"} {
		doc := `{"` + key + `":{"access":"/overlay/a.log","error":"/etc/passwd","loglevel":"debug","dnsLog":true},"outbounds":[{"tag":"d","protocol":"freedom"}]}`
		for _, noAccess := range []bool{false, true} {
			out, _, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{NoAccessLog: noAccess})
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			for _, leaked := range []string{"/overlay/a.log", "/etc/passwd", "debug", "dnsLog"} {
				if strings.Contains(string(out), leaked) {
					t.Fatalf("%s (noAccess=%t): the provider's %q survived: %s", key, noAccess, leaked, out)
				}
			}
			want := `{"loglevel":"warning"}`
			if noAccess {
				want = `{"access":"none","loglevel":"warning"}`
			}
			if got := string(topLevel(t, out)[key]); got != want {
				t.Fatalf("%s (noAccess=%t): log = %s, want %s", key, noAccess, got, want)
			}
		}
	}
	// Anti-vacuity: the real fixture's log is already the router's, byte for
	// byte, so the provider's documents render as before.
	out, _, err := xray.SpliceInbounds(providerFixture(t), testTproxy())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(topLevel(t, out)["log"]); got != `{"loglevel":"warning"}` {
		t.Fatalf("fixture log = %s", got)
	}
}

// A tag with a newline would write a line of the provider's choosing into the
// router's log; an absurdly long one is no name. Either refuses the document.
func TestSpliceRefusesControlCharactersAndAbsurdLengthsInIdentifiers(t *testing.T) {
	long := strings.Repeat("a", 300)
	for name, doc := range map[string]string{
		"outbound tag newline":    `{"outbounds":[{"tag":"ok\nvctl: all good","protocol":"freedom"}]}`,
		"outbound tag escape":     `{"outbounds":[{"tag":"ok\u001b[2J","protocol":"freedom"}]}`,
		"line separator":          `{"outbounds":[{"tag":"ok` + " " + `x","protocol":"freedom"}]}`,
		"balancer tag":            `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"balancers":[{"tag":"b\r","selector":["a"]}]}}`,
		"balancer selector":       `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"balancers":[{"tag":"b","selector":["a\n"]}]}}`,
		"rule outboundTag":        `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"rules":[{"outboundTag":"a\t","domain":["x.test"]}]}}`,
		"rule ruleTag":            `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"rules":[{"ruleTag":"r\n","outboundTag":"a"}]}}`,
		"dialerProxy":             `{"outbounds":[{"tag":"a","protocol":"freedom","streamSettings":{"sockopt":{"dialerProxy":"x\n"}}}]}`,
		"observatory selector":    `{"outbounds":[{"tag":"a","protocol":"freedom"}],"burstObservatory":{"subjectSelector":["a\n"]}}`,
		"key with control":        `{"outbounds":[{"tag":"a","protocol":"freedom","x\ny":1}]}`,
		"absurdly long tag":       `{"outbounds":[{"tag":"` + long + `","protocol":"freedom"}]}`,
		"absurdly long balancer":  `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"balancers":[{"tag":"` + long + `","selector":["a"]}]}}`,
		"Tag spelled differently": `{"outbounds":[{"Tag":"a\n","protocol":"freedom"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions()); err == nil {
				t.Fatalf("spliced %s", doc)
			}
		})
	}
	// Anti-vacuity: tags as the provider writes them, emoji and Cyrillic
	// included, pass.
	ok := `{"outbounds":[{"tag":"bridge-pl5 🇵🇱 Польша","protocol":"freedom"}],"routing":{"balancers":[{"tag":"stage-main-backup","selector":["bridge-"]}]}}`
	if _, _, err := xray.Splice([]byte(ok), testTproxy(), routerOptions()); err != nil {
		t.Fatalf("a clean document was refused: %v", err)
	}
}

// Nothing in a provider document makes xray read or write a file of its
// choosing on the router.
func TestSpliceRefusesFilesNamedByTheProvider(t *testing.T) {
	for name, doc := range map[string]string{
		"certificate file": `{"outbounds":[{"tag":"a","protocol":"vless","streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/etc/uhttpd.crt","keyFile":"/etc/uhttpd.key"}]}}}]}`,
		"key file alone":   `{"outbounds":[{"tag":"a","protocol":"vless","streamSettings":{"tlsSettings":{"certificates":[{"keyFile":"/etc/uhttpd.key"}]}}}]}`,
		"master key log":   `{"outbounds":[{"tag":"a","protocol":"vless","streamSettings":{"security":"tls","tlsSettings":{"masterKeyLog":"/overlay/keys.log"}}}]}`,
		"ext path escape":  `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"rules":[{"domain":["ext:../../etc/shadow:x"],"outboundTag":"a"}]}}`,
		"ext absolute":     `{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"rules":[{"ip":["EXT:/tmp/x.dat:y"],"outboundTag":"a"}]}}`,
		"ext in dns":       `{"outbounds":[{"tag":"a","protocol":"freedom"}],"dns":{"servers":[{"address":"1.1.1.1","domains":["ext:/root/x.dat:z"]}]}}`,
		"fakedns over lan": `{"outbounds":[{"tag":"a","protocol":"freedom"}],"fakedns":[{"ipPool":"192.168.1.0/24","poolSize":200}]}`,
		"fakedns wider":    `{"outbounds":[{"tag":"a","protocol":"freedom"}],"fakedns":{"ipPool":"198.16.0.0/13"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions()); err == nil {
				t.Fatalf("spliced %s", doc)
			}
		})
	}
	// Anti-vacuity: inline certificates, an empty masterKeyLog, a geo list in
	// the asset directory, xray's own FakeDNS ranges whatever their poolSize,
	// and names merely ending in "file" — a header, a host — all pass.
	for _, doc := range []string{
		`{"outbounds":[{"tag":"a","protocol":"freedom"}],"fakedns":{"ipPool":"198.18.0.0/15","poolSize":10000000}}`,
		`{"outbounds":[{"tag":"a","protocol":"vless","settings":{"vnext":[{"address":"cdn.profile","port":443}]},"streamSettings":{"xhttpSettings":{"headers":{"X-Profile":"1","Profile":"x"},"host":"my.profile"}}}]}`,
		`{"outbounds":[{"tag":"a","protocol":"vless","streamSettings":{"realitySettings":{"keyFile":"not a tls block"}}}]}`,
		`{"outbounds":[{"tag":"a","protocol":"vless","streamSettings":{"tlsSettings":{"masterKeyLog":"","certificates":[{"usage":"verify","certificate":["-----BEGIN CERTIFICATE-----"]}]}}}]}`,
		`{"outbounds":[{"tag":"a","protocol":"freedom"}],"routing":{"rules":[{"domain":["ext:custom.dat:ads","geosite:ru"],"outboundTag":"a"}]}}`,
		`{"outbounds":[{"tag":"a","protocol":"freedom"}],"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535},{"ipPool":"fc00::/18","poolSize":65535}]}`,
	} {
		if _, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions()); err != nil {
			t.Fatalf("a clean document was refused: %v\n%s", err, doc)
		}
	}
}

// The top level is an allowlist. reverse (bridges and portals into the
// router's network) refuses the document, in any spelling; every other key
// the router does not take — env (where xray reads its files from), api,
// metrics, transport, $schema, assets, anything unknown — is dropped and the
// rest applied.
func TestSpliceTakesOnlyTheAllowedTopLevelKeys(t *testing.T) {
	for _, key := range []string{"reverse", "Reverse"} {
		doc := `{"` + key + `":{},"outbounds":[{"tag":"d","protocol":"freedom"}]}`
		_, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions())
		if !errors.Is(err, xray.ErrProviderRefused) {
			t.Fatalf("a document with %q: %v", key, err)
		}
	}
	for _, key := range []string{"env", "ENV", "api", "Api", "metrics", "transport", "$schema", "assets", "zulu"} {
		doc := `{"` + key + `":{"listen":"0.0.0.0:1","XRAY_LOCATION_ASSET":"/"},"outbounds":[{"tag":"d","protocol":"freedom"}]}`
		out, res, err := xray.Splice([]byte(doc), testTproxy(), routerOptions())
		if err != nil {
			t.Fatalf("a document with %q was refused: %v", key, err)
		}
		if strings.Join(res.DroppedKeys, ",") != key || strings.Contains(string(out), "0.0.0.0:1") || strings.Contains(string(out), "XRAY_LOCATION_ASSET") {
			t.Fatalf("%q: dropped %v, out %s", key, res.DroppedKeys, out)
		}
	}
	// Anti-vacuity: every allowed key, in other spellings too, passes and stays.
	doc := `{"remarks":"🇵🇱","Log":{},"DNS":{"servers":["1.1.1.1"]},"stats":{},"Policy":{},"version":{"min":"26.3.27"},` +
		`"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}],"observatory":{"subjectSelector":["d"]},` +
		`"burstObservatory":{"subjectSelector":["d"]},"Routing":{"rules":[]},"Inbounds":[],"Outbounds":[{"tag":"d","protocol":"freedom"}]}`
	out, res, err := xray.Splice([]byte(doc), testTproxy(), routerOptions())
	if err != nil {
		t.Fatalf("a document of allowed keys was refused: %v", err)
	}
	if !res.InboundsReplaced || res.OutboundsMarked != 1 || len(res.DroppedKeys) != 0 {
		t.Fatalf("a key in another spelling was not handled as xray reads it: %+v\n%s", res, out)
	}
}

// A real entry carries a top-level "assets" (1111's #28): dropped, the rest
// of the document as it is (testdata/provider/README.md).
func TestARealEntryWithAssetsIsAppliedWithoutThem(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("testdata/provider/entry-28-assets.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, res, err := xray.Splice(raw, testTproxy(), routerOptions())
	if err != nil {
		t.Fatalf("the entry was refused: %v", err)
	}
	if strings.Join(res.DroppedKeys, ",") != "assets" || strings.Contains(string(out), "assets.provider.invalid") {
		t.Fatalf("dropped %v", res.DroppedKeys)
	}
	plain, _, err := xray.Splice(providerFixture(t), testTproxy(), routerOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("dropping assets changed the rest of the render")
	}
}

// reverse inside an outbound (VLESS's reverse proxy) and a freedom outbound
// that redirects every connection open a path into the LAN.
func TestSpliceRefusesReverseAndRedirectInOutbounds(t *testing.T) {
	for name, doc := range map[string]string{
		"vless reverse":    `{"outbounds":[{"tag":"a","protocol":"vless","settings":{"vnext":[],"reverse":{"tag":"r"}}}]}`,
		"freedom redirect": `{"outbounds":[{"tag":"a","protocol":"freedom","settings":{"redirect":"192.168.1.1:22"}}]}`,
		"Freedom Redirect": `{"outbounds":[{"tag":"a","protocol":"Freedom","Settings":{"Redirect":"192.168.1.1:22"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions()); err == nil {
				t.Fatalf("spliced %s", doc)
			}
		})
	}
	ok := `{"outbounds":[{"tag":"a","protocol":"freedom","settings":{"domainStrategy":"UseIPv4","redirect":""}}]}`
	if _, _, err := xray.Splice([]byte(ok), testTproxy(), routerOptions()); err != nil {
		t.Fatalf("a plain freedom was refused: %v", err)
	}
}

// With DNS through the tunnel the router's own lookups are xray's, and hosts
// answer before any server: an entry over the panel or NTP is dropped, the
// rest of hosts and of the document applied.
func TestSpliceDropsHostsOverTheRoutersDirectNames(t *testing.T) {
	opts := xray.SpliceOptions{DNS: &xray.DNSOptions{Listen: "127.0.0.1:10053", DirectResolvers: []string{"8.8.8.8"},
		DirectDomains: []string{"full:router.vectra-pro.net", "domain:pool.ntp.org"}}}
	base := `{"outbounds":[{"tag":"n","protocol":"vless","settings":{"vnext":[{"address":"n.example","port":443}]}}],"dns":{"servers":["1.1.1.1"],"hosts":{"n.example":"203.0.113.1",%s}}}`
	for _, hosts := range []string{
		`"router.vectra-pro.net":"203.0.113.9"`,
		`"full:ROUTER.vectra-pro.net":"203.0.113.9"`,
		`"domain:vectra-pro.net":"203.0.113.9"`,
		`"pool.ntp.org":"203.0.113.9"`,
		`"0.ru.pool.ntp.org":"203.0.113.9"`,
		`"domain:ntp.org":"203.0.113.9"`,
		`"keyword:ntp":"203.0.113.9"`,
		`"regexp:^router\\.":"203.0.113.9"`,
	} {
		doc := strings.Replace(base, "%s", hosts, 1)
		out, res, err := xray.Splice([]byte(doc), testTproxy(), opts)
		if err != nil {
			t.Fatalf("hosts {%s} refused the document: %v", hosts, err)
		}
		if len(res.DNS.DroppedHosts) != 1 || strings.Contains(string(out), "203.0.113.9") || !strings.Contains(string(out), `"n.example":"203.0.113.1"`) {
			t.Fatalf("hosts {%s}: dropped %v\n%s", hosts, res.DNS.DroppedHosts, out)
		}
	}
	// Anti-vacuity: the provider's own names, a category and other names
	// stay; without DNS through the tunnel hosts are not the router's.
	for _, hosts := range []string{`"geosite:category-ads-all":"127.0.0.1"`, `"domain:example.org":"203.0.113.2"`} {
		doc := strings.Replace(base, "%s", hosts, 1)
		_, res, err := xray.Splice([]byte(doc), testTproxy(), opts)
		if err != nil || len(res.DNS.DroppedHosts) != 0 {
			t.Fatalf("hosts {%s}: dropped %v, %v", hosts, res.DNS.DroppedHosts, err)
		}
	}
	doc := strings.Replace(base, "%s", `"pool.ntp.org":"203.0.113.9"`, 1)
	if out, _, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{}); err != nil || !strings.Contains(string(out), "203.0.113.9") {
		t.Fatalf("hosts touched without DNS through the tunnel: %v", err)
	}
}
