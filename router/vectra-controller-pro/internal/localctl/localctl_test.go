package localctl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOverridesMissingFileIsZero(t *testing.T) {
	o, err := LoadOverrides(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || o.HasEntry() || len(o.Pins) != 0 || o.ProbeIntervalSec != 0 {
		t.Fatalf("LoadOverrides(missing) = %+v, %v", o, err)
	}
}

func TestUpdateOverridesRoundTripsAndIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "etc", "local-overrides.json")
	idx := 3
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		o.EntryRemark, o.EntryIndex = "🇩🇪 Германия", &idx
		o.Pins = map[string]string{"BL-MAIN": "bridge-de5"}
		o.ProbeIntervalSec = 600
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.EntryRemark != "🇩🇪 Германия" || *got.EntryIndex != 3 || got.Pins["BL-MAIN"] != "bridge-de5" || got.ProbeIntervalSec != 600 || got.UpdatedAt.IsZero() {
		t.Fatalf("round trip = %+v", got)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

// Two processes (daemon, rpcd) read-modify-write the same file. Without the
// lock, concurrent updates lose each other's writes; with it none is lost.
func TestUpdateOverridesSerializesConcurrentWriters(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.json")
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := UpdateOverrides(p, func(o *Overrides) error {
				if o.Pins == nil {
					o.Pins = map[string]string{}
				}
				o.Pins["b"+strconv.Itoa(i)] = "n"
				return nil
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := LoadOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pins) != n {
		t.Fatalf("%d pins survived %d concurrent writers", len(got.Pins), n)
	}
}

func TestUpdateOverridesAbortsWithoutWriting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.json")
	if _, err := UpdateOverrides(p, func(o *Overrides) error { o.ProbeIntervalSec = 60; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		o.ProbeIntervalSec = 999
		return os.ErrInvalid
	}); err == nil {
		t.Fatal("the fn error was swallowed")
	}
	if got, _ := LoadOverrides(p); got.ProbeIntervalSec != 60 {
		t.Fatalf("an aborted update was written: %+v", got)
	}
}

// A released router forgets every choice made on it; the lock file stays, so
// an update waiting on it still serializes with whatever comes next.
func TestClearOverridesRemovesTheFileUnderTheLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.json")
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		o.EntryRemark, o.Pins, o.Direct = "B", map[string]string{"BL": "n"}, []string{"bank.ru"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if removed, err := ClearOverrides(p); !removed || err != nil {
		t.Fatalf("ClearOverrides = %v, %v", removed, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("the overrides are still there: %v", err)
	}
	if _, err := os.Stat(p + ".lock"); err != nil {
		t.Fatalf("the lock file went too: %v", err)
	}
	if got, err := LoadOverrides(p); err != nil || got.HasEntry() || got.Pins != nil || got.Direct != nil {
		t.Fatalf("after clearing = %+v, %v", got, err)
	}
	if removed, err := ClearOverrides(p); removed || err != nil {
		t.Fatalf("clearing nothing = %v, %v", removed, err)
	}
}

func fixtureEntries(t *testing.T) *EntriesCache {
	t.Helper()
	raw, err := os.ReadFile("../coreengine/xray/testdata/provider/entry-00.json")
	if err != nil {
		t.Fatal(err)
	}
	small := json.RawMessage(`{"outbounds":[{"tag":"a","protocol":"vless"},{"tag":"DIRECT","protocol":"freedom"}],"routing":{"balancers":[]}}`)
	return &EntriesCache{
		SubscriptionID: "primary",
		FetchedAt:      time.Date(2026, 9, 27, 0, 4, 11, 0, time.UTC),
		Remarks:        []string{"🇷🇺🇪🇺 Авто Самый стабильный", "🇩🇪 Германия"},
		Entries:        []json.RawMessage{raw, small},
	}
}

func TestSaveEntriesWritesOnceAndSummarizesWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	cp, ip := filepath.Join(dir, "e.json.gz"), filepath.Join(dir, "e.index.json")
	c := fixtureEntries(t)

	wrote, err := SaveEntries(cp, ip, c)
	if err != nil || !wrote {
		t.Fatalf("first save: wrote=%v err=%v", wrote, err)
	}
	st1, _ := os.Stat(cp)
	wrote, err = SaveEntries(cp, ip, c)
	if err != nil || wrote {
		t.Fatalf("an unchanged array was rewritten (wrote=%v err=%v): flash wear for nothing", wrote, err)
	}
	if st2, _ := os.Stat(cp); !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("cache file touched on an unchanged save")
	}
	if st1.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %v, want 0600 (it holds node credentials)", st1.Mode().Perm())
	}

	back, err := LoadEntries(cp)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 2 || string(back.Entries[0]) != string(c.Entries[0]) {
		t.Fatal("entries did not round-trip byte for byte")
	}

	idx, err := LoadEntriesIndex(ip)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 2 {
		t.Fatalf("index = %+v", idx)
	}
	// The real fixture: 22 dialling outbounds (29 minus DIRECT, BLOCK and five
	// loopbacks) and 7 balancers.
	if e := idx.Entries[0]; e.NodeCount != 22 || e.BalancerCount != 7 || e.Remark != c.Remarks[0] {
		t.Errorf("summary[0] = %+v, want 22 nodes / 7 balancers", e)
	}
	if e := idx.Entries[1]; e.NodeCount != 1 || e.BalancerCount != 0 {
		t.Errorf("summary[1] = %+v", e)
	}
	ir, _ := os.ReadFile(ip)
	for _, secret := range []string{`"id"`, "publicKey", "shortId", "password"} {
		if strings.Contains(string(ir), secret) {
			t.Errorf("the UI-readable index contains %q", secret)
		}
	}

	// A changed array IS written.
	c.Remarks[1] = "🇵🇱 Польша"
	if wrote, err := SaveEntries(cp, ip, c); err != nil || !wrote {
		t.Fatalf("changed array: wrote=%v err=%v", wrote, err)
	}
}

func TestSaveEntriesRefusesInconsistentInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveEntries(filepath.Join(dir, "a"), filepath.Join(dir, "b"), &EntriesCache{}); err == nil {
		t.Error("cached an empty array")
	}
	c := fixtureEntries(t)
	c.Remarks = c.Remarks[:1]
	if _, err := SaveEntries(filepath.Join(dir, "a"), filepath.Join(dir, "b"), c); err == nil {
		t.Error("cached entries without their remarks")
	}
}

func TestResolveHonoursTheRouterChoiceByRemark(t *testing.T) {
	remarks := []string{"auto", "de", "pl"}
	i2 := 2
	cases := []struct {
		name        string
		ov          Overrides
		panelRemark string
		panelIndex  int
		wantIdx     int
		wantLocal   bool
		wantStale   bool
		wantErr     bool
	}{
		{"panel index", Overrides{}, "", 1, 1, false, false, false},
		{"panel remark", Overrides{}, "pl", 0, 2, false, false, false},
		{"router choice wins", Overrides{EntryRemark: "de"}, "pl", 0, 1, true, false, false},
		// The provider reordered: the remark still finds Germany, the stale
		// index (2 = Poland now) is ignored.
		{"remark, not index", Overrides{EntryRemark: "de", EntryIndex: &i2}, "", 0, 1, true, false, false},
		{"router choice gone", Overrides{EntryRemark: "fr"}, "pl", 0, 2, false, true, false},
		{"panel remark gone", Overrides{}, "fr", 0, 0, false, false, true},
		{"panel index out of range", Overrides{}, "", 7, 0, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, local, stale, err := Resolve(remarks, tc.ov, tc.panelRemark, tc.panelIndex)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err == nil && (idx != tc.wantIdx || local != tc.wantLocal || stale != tc.wantStale) {
				t.Fatalf("Resolve = %d local=%v stale=%v; want %d %v %v", idx, local, stale, tc.wantIdx, tc.wantLocal, tc.wantStale)
			}
		})
	}
}

// encoding/json would compact a RawMessage and escape <, > and &; the cache
// must hand back the provider's bytes exactly, or the running entry's digest
// no longer matches and the document is no longer the provider's own.
func TestEntriesCacheIsByteExact(t *testing.T) {
	dir := t.TempDir()
	cp, ip := filepath.Join(dir, "e.gz"), filepath.Join(dir, "e.idx")
	odd := json.RawMessage("{\n  \"remarks\": \"a <b> & c\",\n  \"outbounds\": [ ]\n}")
	c := &EntriesCache{SubscriptionID: "s", Remarks: []string{"a <b> & c", "x"}, Entries: []json.RawMessage{odd, json.RawMessage(`{}`)}}
	if _, err := SaveEntries(cp, ip, c); err != nil {
		t.Fatal(err)
	}
	back, err := LoadEntries(cp)
	if err != nil {
		t.Fatal(err)
	}
	if string(back.Entries[0]) != string(odd) || string(back.Entries[1]) != "{}" {
		t.Fatalf("entries changed on the way through the cache:\n%q", back.Entries[0])
	}
	if back.Remarks[0] != "a <b> & c" {
		t.Fatalf("remark = %q", back.Remarks[0])
	}
	idx, _ := LoadEntriesIndex(ip)
	sum := sha256.Sum256(odd)
	if idx.Entries[0].Digest != hex.EncodeToString(sum[:]) {
		t.Fatal("the index digest is not sha256 of the exact bytes")
	}
	if !idx.HasRemark("x") || idx.HasRemark("y") {
		t.Fatal("HasRemark")
	}
}

func TestLoadEntriesRejectsACorruptContainer(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("not gzip"),
	} {
		p := filepath.Join(t.TempDir(), "e.gz")
		_ = os.WriteFile(p, raw, 0o600)
		if _, err := LoadEntries(p); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, body := range []string{
		"VCTLENTRIES1\n{\"remarks\":[\"a\"]}\n99\n{}",      // length beyond the data
		"VCTLENTRIES1\n{\"remarks\":[\"a\",\"b\"]}\n2\n{}", // remark without entry
		"something else",
	} {
		if _, err := decodeEntriesChecked(body); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func decodeEntriesChecked(body string) (*EntriesCache, error) {
	c, err := decodeEntries([]byte(body))
	if err != nil {
		return nil, err
	}
	if len(c.Entries) == 0 || len(c.Remarks) != len(c.Entries) {
		return nil, os.ErrInvalid
	}
	return c, nil
}

// A rules change replaces both lists — with copies, not the request's own
// slices — leaves every other choice alone, and an emptied list leaves the
// file rather than lingering as [].
func TestSetRulesReplacesBothListsAndEmptiesCleanly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.json")
	req := &Rules{Direct: []string{"sberbank.ru", "госуслуги.рф"}, Proxy: []string{"example.org"}}
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		o.ProbeIntervalSec = 600
		Change{SetRules: req}.ApplyTo(o)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req.Direct[0] = "changed.after.the.fact"
	got, err := LoadOverrides(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Direct, ",") != "sberbank.ru,госуслуги.рф" || strings.Join(got.Proxy, ",") != "example.org" || got.ProbeIntervalSec != 600 {
		t.Fatalf("round trip = %+v", got)
	}
	// A change about something else keeps them.
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		Change{ResetEntry: true}.ApplyTo(o)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadOverrides(p); len(got.Direct) != 2 || len(got.Proxy) != 1 {
		t.Fatalf("another change dropped the sites: %+v", got)
	}
	if _, err := UpdateOverrides(p, func(o *Overrides) error {
		Change{SetRules: &Rules{Direct: []string{}, Proxy: nil}}.ApplyTo(o)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), `"direct"`) || strings.Contains(string(raw), `"proxy"`) {
		t.Fatalf("emptied lists linger: %s", raw)
	}
}

// A service's country is set by its own change and cleared by an empty one;
// the other services keep theirs.
func TestAServiceChoiceIsSetAndCleared(t *testing.T) {
	o := Overrides{Services: map[string]string{"youtube": "BY"}}
	Change{SetService: &ServiceChoice{ID: "tiktok", Country: "DE"}}.ApplyTo(&o)
	if o.Services["tiktok"] != "DE" || o.Services["youtube"] != "BY" {
		t.Fatalf("%v", o.Services)
	}
	Change{SetService: &ServiceChoice{ID: "tiktok"}}.ApplyTo(&o)
	if _, ok := o.Services["tiktok"]; ok || o.Services["youtube"] != "BY" {
		t.Fatalf("%v", o.Services)
	}
	var fresh Overrides
	Change{SetService: &ServiceChoice{ID: "telegram", Country: "NL"}}.ApplyTo(&fresh)
	if fresh.Services["telegram"] != "NL" {
		t.Fatalf("%v", fresh.Services)
	}
}
