package xray_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
)

func routerOptions() xray.SpliceOptions {
	return xray.SpliceOptions{
		APIListen:     "127.0.0.1:10085",
		MetricsListen: "127.0.0.1:10086",
		ProbeInterval: 10 * time.Minute,
	}
}

// The options may touch exactly three things: add "api", add "metrics", and
// change ONE field inside burstObservatory. Every other top-level value must be
// byte-identical to plain SpliceInbounds — which is itself proven verbatim
// against the provider by TestSplicePreservesEveryByteExceptInboundsAndOutbounds.
func TestSpliceOptionsTouchOnlyTheirOwnKeys(t *testing.T) {
	raw := providerFixture(t)
	plain, _, err := xray.SpliceInbounds(raw, testTproxy())
	if err != nil {
		t.Fatal(err)
	}
	withOpts, res, err := xray.Splice(raw, testTproxy(), routerOptions())
	if err != nil {
		t.Fatal(err)
	}

	a, b := topLevel(t, plain), topLevel(t, withOpts)
	for k, v := range a {
		if k == xray.BurstObservatoryKey {
			continue
		}
		if !bytes.Equal(v, b[k]) {
			t.Errorf("top-level %q changed under the options", k)
		}
	}
	if _, had := a["api"]; had {
		t.Fatal("fixture unexpectedly carries an api block; the test assumes the provider ships none")
	}

	// Positions: the provider's keys keep their order, ours are appended.
	gotKeys := orderedKeys(t, withOpts)
	wantKeys := append(orderedKeys(t, plain), "api", "metrics")
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("key order = %v, want %v", gotKeys, wantKeys)
	}

	var api struct {
		Tag      string   `json:"tag"`
		Listen   string   `json:"listen"`
		Services []string `json:"services"`
	}
	if err := json.Unmarshal(b["api"], &api); err != nil {
		t.Fatal(err)
	}
	if api.Listen != "127.0.0.1:10085" || api.Tag != xray.APITag || !contains(api.Services, "RoutingService") {
		t.Errorf("api = %+v; want loopback listen, our tag and RoutingService", api)
	}
	var metrics struct{ Tag, Listen string }
	if err := json.Unmarshal(b["metrics"], &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.Listen != "127.0.0.1:10086" || metrics.Tag != xray.MetricsTag {
		t.Errorf("metrics = %+v", metrics)
	}

	// Inside burstObservatory only pingConfig.interval moved.
	var before, after struct {
		SubjectSelector json.RawMessage            `json:"subjectSelector"`
		PingConfig      map[string]json.RawMessage `json:"pingConfig"`
	}
	if err := json.Unmarshal(a[xray.BurstObservatoryKey], &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b[xray.BurstObservatoryKey], &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.SubjectSelector, after.SubjectSelector) {
		t.Error("subjectSelector changed")
	}
	for k, v := range before.PingConfig {
		if k == "interval" {
			continue
		}
		if !bytes.Equal(v, after.PingConfig[k]) {
			t.Errorf("pingConfig.%s changed", k)
		}
	}
	if got := string(after.PingConfig["interval"]); got != `"600s"` {
		t.Errorf("interval = %s, want \"600s\"", got)
	}
	if res.ProviderProbeInterval != 12*time.Hour || res.ProbeInterval != 10*time.Minute {
		t.Errorf("result intervals = %s -> %s, want 12h0m0s -> 10m0s", res.ProviderProbeInterval, res.ProbeInterval)
	}
	if res.APIListen != "127.0.0.1:10085" || res.MetricsListen != "127.0.0.1:10086" {
		t.Errorf("result = %+v", res)
	}
}

func TestZeroSpliceOptionsAreExactlySpliceInbounds(t *testing.T) {
	raw := providerFixture(t)
	plain, _, err := xray.SpliceInbounds(raw, testTproxy())
	if err != nil {
		t.Fatal(err)
	}
	zero, _, err := xray.Splice(raw, testTproxy(), xray.SpliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, zero) {
		t.Fatal("zero options changed the output")
	}
}

// The API can re-point every balancer and add outbounds, and neither endpoint
// authenticates. Anything but loopback is refused outright.
func TestSpliceRefusesToExposeTheAPIBeyondLoopback(t *testing.T) {
	raw := providerFixture(t)
	for _, addr := range []string{"0.0.0.0:10085", "192.168.1.1:10085", "[::]:10085", "10085", "localhost:10085", "127.0.0.1:0", "127.0.0.1:x"} {
		for _, opts := range []xray.SpliceOptions{{APIListen: addr}, {MetricsListen: addr}} {
			if _, _, err := xray.Splice(raw, testTproxy(), opts); err == nil {
				t.Errorf("Splice accepted listen %q (%+v)", addr, opts)
			}
		}
	}
	if _, _, err := xray.Splice(raw, testTproxy(), xray.SpliceOptions{APIListen: "[::1]:10085"}); err != nil {
		t.Errorf("IPv6 loopback refused: %v", err)
	}
}

func TestSpliceBoundsTheProbeInterval(t *testing.T) {
	raw := providerFixture(t)
	for _, d := range []time.Duration{time.Second, 59 * time.Second, 25 * time.Hour} {
		if _, _, err := xray.Splice(raw, testTproxy(), xray.SpliceOptions{ProbeInterval: d}); err == nil {
			t.Errorf("probe interval %s accepted", d)
		}
	}
}

// A provider that ships its own api or metrics is refused: its listen
// (possibly 0.0.0.0, possibly a service set we do not want) is not the
// provider's to decide, in any spelling (provider_guard.go).
func TestSpliceRefusesAProviderAPIBlock(t *testing.T) {
	for _, doc := range []string{
		`{"api":{"tag":"x","listen":"0.0.0.0:8080","services":["HandlerService"]},"outbounds":[{"tag":"d","protocol":"freedom"}]}`,
		`{"Api":{"listen":"0.0.0.0:8080"},"outbounds":[{"tag":"d","protocol":"freedom"}]}`,
		`{"metrics":{"tag":"m","listen":"0.0.0.0:9090"},"outbounds":[{"tag":"d","protocol":"freedom"}]}`,
	} {
		for _, opts := range []xray.SpliceOptions{routerOptions(), {}} {
			if out, _, err := xray.Splice([]byte(doc), testTproxy(), opts); err == nil {
				t.Fatalf("spliced %s into %s", doc, out)
			}
		}
	}
}

func TestSpliceRewritesTheClassicObservatoryToo(t *testing.T) {
	doc := []byte(`{"observatory":{"subjectSelector":["n"],"probeUrl":"https://x.test/204","probeInterval":"1h"},"outbounds":[]}`)
	out, res, err := xray.Splice(doc, testTproxy(), xray.SpliceOptions{ProbeInterval: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	obs := topLevel(t, out)["observatory"]
	if !strings.Contains(string(obs), `"probeInterval":"300s"`) || !strings.Contains(string(obs), `"probeUrl":"https://x.test/204"`) {
		t.Errorf("observatory = %s", obs)
	}
	if res.ProviderProbeInterval != time.Hour {
		t.Errorf("ProviderProbeInterval = %s", res.ProviderProbeInterval)
	}
}

func TestProviderProbeIntervalReadsTheFixture(t *testing.T) {
	d, sampling, ok := xray.ProviderProbeInterval(providerFixture(t))
	if !ok || d != 12*time.Hour || sampling != 2 {
		t.Fatalf("ProviderProbeInterval = %s, %d, %v; want 12h, 2, true", d, sampling, ok)
	}
	if _, _, ok := xray.ProviderProbeInterval([]byte(`{"outbounds":[]}`)); ok {
		t.Fatal("reported an interval for a document without an observatory")
	}
}

func TestParseXrayDurationAcceptsWhatXrayAccepts(t *testing.T) {
	for in, want := range map[string]time.Duration{`"43200s"`: 12 * time.Hour, `"10m"`: 10 * time.Minute, `1000000000`: time.Second} {
		got, err := xray.ParseXrayDuration(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("ParseXrayDuration(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := xray.ParseXrayDuration(json.RawMessage(`true`)); err == nil {
		t.Error("accepted a boolean")
	}
}

func TestSpliceOptionsKeyChangesWithEveryOption(t *testing.T) {
	base := routerOptions()
	keys := map[string]bool{base.Key(): true}
	for _, o := range []xray.SpliceOptions{
		{MetricsListen: base.MetricsListen, ProbeInterval: base.ProbeInterval},
		{APIListen: base.APIListen, ProbeInterval: base.ProbeInterval},
		{APIListen: base.APIListen, MetricsListen: base.MetricsListen, ProbeInterval: time.Hour},
	} {
		if keys[o.Key()] {
			t.Errorf("Key collision for %+v", o)
		}
		keys[o.Key()] = true
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// The provider leaves log.access unset, which makes xray print a line per
// connection to stdout. The router option turns it off; the rest of the log
// block is the router's own (TestSpliceReplacesTheProvidersWholeLog).
func TestNoAccessLogSetsAccessNone(t *testing.T) {
	raw := providerFixture(t)
	out, _, err := xray.Splice(raw, testTproxy(), xray.SpliceOptions{NoAccessLog: true})
	if err != nil {
		t.Fatal(err)
	}
	var logBlock map[string]json.RawMessage
	if err := json.Unmarshal(topLevel(t, out)["log"], &logBlock); err != nil {
		t.Fatal(err)
	}
	if string(logBlock["access"]) != `"none"` || string(logBlock["loglevel"]) != `"warning"` {
		t.Fatalf("log = %s", topLevel(t, out)["log"])
	}
	// A document without a log block gets one.
	out, _, err = xray.Splice([]byte(`{"outbounds":[]}`), testTproxy(), xray.SpliceOptions{NoAccessLog: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(topLevel(t, out)["log"]); got != `{"access":"none","loglevel":"warning"}` {
		t.Fatalf("appended log = %s", got)
	}
	if (xray.SpliceOptions{NoAccessLog: true}).Key() == (xray.SpliceOptions{}).Key() {
		t.Fatal("Key() ignores NoAccessLog")
	}
}

// xray decodes with encoding/json: keys match case-insensitively and the last
// one wins. A document that repeats a key in another spelling is refused — the
// splice would rewrite one spelling and xray would run the other.
func TestSpliceRefusesKeysRepeatedInAnotherSpelling(t *testing.T) {
	for name, doc := range map[string]string{
		"api moved off loopback": `{"api":{"tag":"a"},"outbounds":[],"Api":{"listen":"0.0.0.0:10085","services":["HandlerService"]}}`,
		"metrics moved":          `{"outbounds":[],"METRICS":{"listen":"0.0.0.0:10086"},"metrics":{}}`,
		"open inbounds restored": `{"inbounds":[],"outbounds":[],"Inbounds":[{"protocol":"socks","port":1080}]}`,
		"egress un-marked":       `{"outbounds":[{"tag":"n","protocol":"vless","streamSettings":{},"StreamSettings":{"sockopt":{"mark":0}}}]}`,
		"nested access log":      `{"outbounds":[],"log":{"access":"none","Access":"/tmp/a.log"}}`,
		"exact duplicate":        `{"outbounds":[],"outbounds":[{"tag":"x","protocol":"freedom"}]}`,
		"kelvin sign folds to k": "{\"outbounds\":[],\"routing\":{\"rules\":[],\"Key\":1,\"key\":2}}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(doc), testTproxy(), routerOptions()); err == nil {
				t.Fatalf("spliced a document that repeats a key: %s", doc)
			}
		})
	}
	// Anti-vacuity: distinct keys in distinct objects are fine, and the real
	// fixture (which repeats "tag" in every outbound — different objects) passes.
	if _, _, err := xray.Splice([]byte(`{"outbounds":[{"tag":"a","protocol":"freedom"},{"tag":"b","protocol":"freedom"}],"log":{"loglevel":"warning"}}`), testTproxy(), routerOptions()); err != nil {
		t.Fatalf("a clean document was refused: %v", err)
	}
	if _, _, err := xray.Splice(providerFixture(t), testTproxy(), routerOptions()); err != nil {
		t.Fatalf("the real fixture was refused: %v", err)
	}
}

// xray accepts "log": null; the router's rewrite must too.
func TestSpliceTreatsANullLogBlockAsEmpty(t *testing.T) {
	opts := routerOptions()
	opts.NoAccessLog = true
	out, _, err := xray.Splice([]byte(`{"log":null,"burstObservatory":null,"outbounds":[]}`), testTproxy(), opts)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if got := string(topLevel(t, out)["log"]); got != `{"access":"none","loglevel":"warning"}` {
		t.Fatalf("log = %s", got)
	}
}
