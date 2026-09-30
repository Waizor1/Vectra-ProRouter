package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func minimalConfig() string {
	return `{
	  "schema": 1,
	  "process": {"xrayBinary": "/usr/sbin/vctl-xray-wrapper", "workDir": "/var/run/vctl"},
	  "inbounds": {"tproxy": {"listenIP": "0.0.0.0", "port": 12345, "fwmark": 1, "udpEnabled": true,
	    "sniffing": {"enabled": true, "destOverride": ["http","tls","quic"]}}},
	  "geo": {"assetDir": "/usr/share/v2ray"},
	  "subscriptions": [{"id": "provider", "url": "https://sub.example.invalid/t", "enabled": true, "userAgent": "v2rayNG/1.9.5"}]
	}`
}

func TestReadMinimalOperatorConfig(t *testing.T) {
	c, err := Read(strings.NewReader(minimalConfig()), "test")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Inbounds.Tproxy == nil || c.Inbounds.Tproxy.Port != 12345 {
		t.Fatalf("tproxy inbound: %+v", c.Inbounds.Tproxy)
	}
	if c.Subscriptions[0].Mode != SubscriptionModeJSON {
		t.Errorf("subscription mode should default to %q, got %q", SubscriptionModeJSON, c.Subscriptions[0].Mode)
	}
	// The panel's configs name the fleet's old directory: kept as given,
	// read as vctl's own.
	if c.Geo.AssetDir != LegacyGeoAssetDir || GeoAssetDir(c.Geo.AssetDir) != DefaultGeoAssetDir {
		t.Errorf("assetDir = %q (read as %q), want %q read as %q", c.Geo.AssetDir, GeoAssetDir(c.Geo.AssetDir), LegacyGeoAssetDir, DefaultGeoAssetDir)
	}
}

// xray reads vctl's own geo data (vectra-geodata's) unless a config names
// another directory; the fleet's old /usr/share/v2ray — PassWall's packages',
// gone with them — reads as vctl's own.
func TestGeoAssetDirIsVctlsOwn(t *testing.T) {
	if DefaultGeoAssetDir != "/usr/share/vectra-controller-pro/geo" {
		t.Fatalf("DefaultGeoAssetDir = %q", DefaultGeoAssetDir)
	}
	c := &Config{}
	ApplyDefaults(c)
	if c.Geo.AssetDir != DefaultGeoAssetDir {
		t.Fatalf("ApplyDefaults set assetDir to %q", c.Geo.AssetDir)
	}
	for in, want := range map[string]string{
		"":                  DefaultGeoAssetDir,
		"  ":                DefaultGeoAssetDir,
		"/usr/share/v2ray":  DefaultGeoAssetDir,
		"/usr/share/v2ray/": DefaultGeoAssetDir,
		"/usr/share/vectra-controller-pro/route-geo": "/usr/share/vectra-controller-pro/route-geo",
		"/mnt/usb/geo/": "/mnt/usb/geo",
	} {
		if got := GeoAssetDir(in); got != want {
			t.Errorf("GeoAssetDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// vctl's own directory unless its files are missing where the old one has
// them (a vctl built without the vectra-geodata dependency, on a PassWall
// router); a directory named otherwise is read as named.
func TestResolveGeoAssetDirFallsBackOnlyForVctlsOwn(t *testing.T) {
	old := GeoFilesAt
	t.Cleanup(func() { GeoFilesAt = old })
	for _, tc := range []struct {
		have       []string
		configured string
		want       string
	}{
		{[]string{DefaultGeoAssetDir, LegacyGeoAssetDir}, "/usr/share/v2ray", DefaultGeoAssetDir},
		{[]string{LegacyGeoAssetDir}, "/usr/share/v2ray", LegacyGeoAssetDir},
		{[]string{LegacyGeoAssetDir}, "", LegacyGeoAssetDir},
		{[]string{LegacyGeoAssetDir}, DefaultGeoAssetDir, LegacyGeoAssetDir},
		{nil, "/usr/share/v2ray", DefaultGeoAssetDir},
		{[]string{LegacyGeoAssetDir}, "/mnt/usb/geo", "/mnt/usb/geo"},
	} {
		have := map[string]bool{}
		for _, d := range tc.have {
			have[d] = true
		}
		GeoFilesAt = func(dir string) bool { return have[dir] }
		if got := ResolveGeoAssetDir(tc.configured); got != tc.want {
			t.Errorf("files in %v, configured %q: %q, want %q", tc.have, tc.configured, got, tc.want)
		}
	}
	GeoFilesAt = old
	dir := t.TempDir()
	if GeoFilesAt(dir) {
		t.Fatal("an empty directory has geo files")
	}
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte{0x0a}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !GeoFilesAt(dir) {
		t.Fatal("both files there, not seen")
	}
}

// A PROVIDER document must never be mistakable for an operator config.
func TestUnmarshalRejectsProviderDocument(t *testing.T) {
	provider, err := os.ReadFile(filepath.Join("..", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if err != nil {
		t.Fatalf("read provider fixture: %v", err)
	}
	if _, err := Unmarshal(provider); err == nil {
		t.Fatal("Unmarshal must reject a provider document (DisallowUnknownFields)")
	}
}

func TestValidateRequiresTproxyAndHTTPS(t *testing.T) {
	cases := map[string]string{
		"no tproxy": `{"schema":1,"process":{"xrayBinary":"/x","workDir":"/w","restartBackoff":{"initialMs":1,"factor":1,"maxMs":1}},"inbounds":{},"geo":{}}`,
		"http url":  `{"schema":1,"process":{"xrayBinary":"/x","workDir":"/w","restartBackoff":{"initialMs":1,"factor":1,"maxMs":1}},"inbounds":{"tproxy":{"port":1}},"geo":{},"subscriptions":[{"id":"a","url":"http://x/y"}]}`,
		"bad mode":  `{"schema":1,"process":{"xrayBinary":"/x","workDir":"/w","restartBackoff":{"initialMs":1,"factor":1,"maxMs":1}},"inbounds":{"tproxy":{"port":1}},"geo":{},"subscriptions":[{"id":"a","url":"https://x/y","mode":"yaml"}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := Unmarshal([]byte(doc))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := Validate(c); err == nil {
				t.Fatal("expected Validate to fail")
			}
		})
	}
}

// destOverride:"fakedns" would ask Xray to sniff into a pool the provider
// document does not declare — a hard start failure.
func TestValidateRejectsFakednsSniffing(t *testing.T) {
	c, err := Read(strings.NewReader(minimalConfig()), "test")
	if err != nil {
		t.Fatal(err)
	}
	c.Inbounds.Tproxy.Sniffing.DestOverride = []string{"http", "tls", "fakedns"}
	err = Validate(c)
	if err == nil || !strings.Contains(err.Error(), "fakedns") {
		t.Fatalf("expected a fakedns refusal, got %v", err)
	}
}

// SaveRaw must write EXACTLY the bytes given: the provider document's sha256 is
// the config digest, so an appended newline would change it on the next boot.
func TestSaveRawIsByteExact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	data := []byte(`{"log":{"loglevel":"warning"},"stats":{}}`)
	if err := SaveRaw(path, data); err != nil {
		t.Fatalf("SaveRaw: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("SaveRaw wrote %q, want %q", got, data)
	}
}

func TestXrayAssetEnvReplacesInheritedValue(t *testing.T) {
	env := []string{"PATH=/usr/bin", XrayAssetEnvKey + "=/usr/share/xray", "HOME=/root"}
	out := XrayAssetEnv(env, "/usr/share/v2ray")

	var seen int
	for _, kv := range out {
		if strings.HasPrefix(kv, XrayAssetEnvKey+"=") {
			seen++
			if kv != XrayAssetEnvKey+"=/usr/share/v2ray" {
				t.Errorf("asset env = %q", kv)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("expected exactly one %s entry, got %d: %v", XrayAssetEnvKey, seen, out)
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/root"} {
		if !containsString(out, want) {
			t.Errorf("inherited env entry %q was dropped", want)
		}
	}
	// Empty assetDir falls back to the fleet default.
	if !containsString(XrayAssetEnv(nil, ""), XrayAssetEnvKey+"="+DefaultGeoAssetDir) {
		t.Error("empty assetDir must fall back to DefaultGeoAssetDir")
	}
}

func containsString(hay []string, needle string) bool {
	for _, v := range hay {
		if v == needle {
			return true
		}
	}
	return false
}

// A NEW config carrying a malformed Happ agent is refused at adoption
// (ValidateSubscriptionAgents); the first fetch would get the customer's device
// deleted (see internal/uaguard).
func TestValidateSubscriptionAgentsRefusesAMalformedHappAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ua      string
		headers map[string]string
	}{
		{"userAgent", "Happ/1.0", nil},
		{"headers override", "v2rayNG/1.9.5", map[string]string{"User-Agent": "Happ/1.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Read(strings.NewReader(minimalConfig()), "test")
			if err != nil {
				t.Fatal(err)
			}
			c.Subscriptions = []Subscription{{
				ID: "primary", URL: "https://example.test/sub", Enabled: true,
				UserAgent: tc.ua, Headers: tc.headers,
			}}
			err = ValidateSubscriptionAgents(c)
			if err == nil || !strings.Contains(err.Error(), "anti-fraud") {
				t.Fatalf("ValidateSubscriptionAgents = %v, want the malformed Happ agent refused", err)
			}
		})
	}
	// Anti-vacuity: the same config with a harmless agent passes.
	c, err := Read(strings.NewReader(minimalConfig()), "test")
	if err != nil {
		t.Fatal(err)
	}
	c.Subscriptions = []Subscription{{ID: "primary", URL: "https://example.test/sub", Enabled: true, UserAgent: "v2rayNG/1.9.5"}}
	if err := ValidateSubscriptionAgents(c); err != nil {
		t.Fatalf("v2rayNG/1.9.5: %v", err)
	}
}

// A copy of the router's own agent is refused at adoption: the router makes
// its own when userAgent is empty, and a written-down one is a forgery.
func TestValidateSubscriptionAgentsRefusesACopyOfTheRoutersOwnAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ua      string
		headers map[string]string
	}{
		{"userAgent", "VectraRouter/0.5.0 (AX3000T)", nil},
		{"headers override", "", map[string]string{"user-agent": "VectraRouter/0.5.0 vr1.AQID"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Read(strings.NewReader(minimalConfig()), "test")
			if err != nil {
				t.Fatal(err)
			}
			c.Subscriptions = []Subscription{{
				ID: "primary", URL: "https://example.test/sub", Enabled: true,
				UserAgent: tc.ua, Headers: tc.headers,
			}}
			if err := ValidateSubscriptionAgents(c); err == nil || !strings.Contains(err.Error(), "leave userAgent empty") {
				t.Fatalf("ValidateSubscriptionAgents = %v, want the copy refused", err)
			}
		})
	}
	// Anti-vacuity: empty — the router's own, made by the router — passes.
	c, err := Read(strings.NewReader(minimalConfig()), "test")
	if err != nil {
		t.Fatal(err)
	}
	c.Subscriptions = []Subscription{{ID: "primary", URL: "https://example.test/sub", Enabled: true}}
	if err := ValidateSubscriptionAgents(c); err != nil {
		t.Fatalf("empty userAgent: %v", err)
	}
}

// A router configured before the guard existed has "Happ/1.0" in its STORED
// config. Loading it must still work — otherwise the daemon comes up with no
// desired config and never programs the firewall after a restart.
func TestAStoredConfigWithTheOldAgentStillLoads(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"userAgent": "v2rayNG/1.9.5"`, `"userAgent": "Happ/1.0"`, 1)
	if raw == minimalConfig() {
		t.Fatal("precondition: the minimal config has no userAgent to swap")
	}
	if _, err := Read(strings.NewReader(raw), "stored"); err != nil {
		t.Fatalf("a stored config with the old agent no longer loads: %v", err)
	}
}
