package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/geodat"
)

// Encoders for synthetic geo files: a category is its code and n rules.
func pbVarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbBytes(field int, v []byte) []byte {
	b := pbVarint(uint64(field<<3 | 2))
	b = append(b, pbVarint(uint64(len(v)))...)
	return append(b, v...)
}

// cat is one synthetic category: a geosite one holds domains, a geoip one
// /32 addresses.
type cat struct {
	code string
	n    int
}

func siteFileOf(cats ...cat) []byte {
	var b []byte
	for _, c := range cats {
		e := pbBytes(1, []byte(c.code))
		for i := 0; i < c.n; i++ {
			// Domain{type: RootDomain (2), value}
			e = append(e, pbBytes(2, append([]byte{0x08, 0x02}, pbBytes(2, []byte(fmt.Sprintf("d%d.%s.example", i, strings.ToLower(c.code))))...))...)
		}
		b = append(b, pbBytes(1, e)...)
	}
	return b
}

func ipFileOf(cats ...cat) []byte {
	var b []byte
	for k, c := range cats {
		e := pbBytes(1, []byte(c.code))
		for i := 0; i < c.n; i++ {
			// CIDR{ip: 10.k.i.1, prefix: 32}
			e = append(e, pbBytes(2, append(pbBytes(1, []byte{10, byte(k), byte(i), 1}), 0x10, 32))...)
		}
		b = append(b, pbBytes(1, e)...)
	}
	return b
}

// upstream stands in for the four pinned releases.
type upstream struct {
	itdog, loyalSite, roscom, loyalIP []cat
	sums                              map[string]string // a checksum file's body, overriding the right one
}

func itdogCodes() []string {
	var out []string
	for _, c := range outputs[0].Required {
		switch c {
		case "CATEGORY-GOV-RU", "CATEGORY-RU", "PRIVATE":
		default:
			out = append(out, c)
		}
	}
	return out
}

func newUpstream() *upstream {
	u := &upstream{sums: map[string]string{}}
	for _, c := range itdogCodes() {
		u.itdog = append(u.itdog, cat{c, 10})
	}
	u.loyalSite = []cat{{"CN", 50}, {"category-gov-ru", 10}, {"CATEGORY-RU", 10}, {"TELEGRAM", 3}, {"PRIVATE", 10}}
	u.roscom = []cat{{"DIRECT", 10}, {"PRIVATE", 3}, {"WHITELIST", 10}}
	u.loyalIP = []cat{{"CN", 40}, {"PRIVATE", 5}, {"RU", 10}, {"TELEGRAM", 10}}
	return u
}

func (u *upstream) files() map[string][]byte {
	return map[string][]byte{
		"/itdog/releases/download/v1/geosite.dat": siteFileOf(u.itdog...),
		"/loyal/geosite.dat":                      siteFileOf(u.loyalSite...),
		"/roscom/geoip.dat":                       ipFileOf(u.roscom...),
		"/loyal/geoip.dat":                        ipFileOf(u.loyalIP...),
	}
}

func (u *upstream) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/itdog/releases/latest/download/geosite.dat" {
			http.Redirect(w, r, "/itdog/releases/download/v1/geosite.dat", http.StatusFound)
			return
		}
		files := u.files()
		if body, ok := u.sums[p]; ok {
			fmt.Fprint(w, body)
			return
		}
		for _, suffix := range []string{".sha256sum", ".sha256"} {
			if f, ok := files[strings.TrimSuffix(p, suffix)]; ok && strings.HasSuffix(p, suffix) {
				fmt.Fprintf(w, "%s  %s\n", sha256hex(f), filepath.Base(strings.TrimSuffix(p, suffix)))
				return
			}
		}
		f, ok := files[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(f)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var t0 = time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)

// testBuilder builds from the pinned sources' own settings, served by srv.
func testBuilder(t *testing.T, srv *httptest.Server, now time.Time) (*builder, *bytes.Buffer) {
	t.Helper()
	paths := []string{"/itdog/releases/latest/download/geosite.dat", "/loyal/geosite.dat", "/roscom/geoip.dat", "/loyal/geoip.dat"}
	if len(pinned) != len(paths) {
		t.Fatalf("%d pinned sources, the test serves %d", len(pinned), len(paths))
	}
	sources := slices.Clone(pinned)
	for i := range sources {
		sources[i].URL = srv.URL + paths[i]
	}
	var log bytes.Buffer
	return &builder{client: srv.Client(), sources: sources, outputs: outputs, now: func() time.Time { return now }, log: &log}, &log
}

func readManifest(t *testing.T, path string) (manifest, []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m, b
}

func sortedCopy(s []string) []string {
	s = slices.Clone(s)
	sort.Strings(s)
	return s
}

func TestBuildMergesSourcesIntoManifest(t *testing.T) {
	u := newUpstream()
	srv := u.serve(t)
	b, log := testBuilder(t, srv, t0)
	out := t.TempDir()
	if err := b.build(context.Background(), out, "", nil); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	m, raw := readManifest(t, filepath.Join(out, "geo.json"))

	// Exactly the agreed shape, nothing more.
	var top map[string]json.RawMessage
	json.Unmarshal(raw, &top)
	if keys := sortedKeys(top); !slices.Equal(keys, []string{"builtAt", "files", "schema", "serial", "sources"}) {
		t.Fatalf("manifest keys %v", keys)
	}
	var files map[string]map[string]json.RawMessage
	json.Unmarshal(top["files"], &files)
	if keys := sortedKeys(files); !slices.Equal(keys, []string{"geoip.dat", "geosite.dat"}) {
		t.Fatalf("files %v", keys)
	}
	for name, f := range files {
		if keys := sortedKeys(f); !slices.Equal(keys, []string{"codes", "path", "sha256", "size"}) {
			t.Fatalf("%s keys %v", name, keys)
		}
	}
	if m.Schema != 1 || m.Serial != t0.Unix() || m.BuiltAt != "2026-10-17T03:00:00Z" {
		t.Fatalf("schema %d serial %d builtAt %q", m.Schema, m.Serial, m.BuiltAt)
	}

	wantSite := sortedCopy(outputs[0].Required)
	wantIP := []string{"DIRECT", "PRIVATE", "RU", "TELEGRAM", "WHITELIST"}
	for name, want := range map[string][]string{"geosite.dat": wantSite, "geoip.dat": wantIP} {
		f := m.Files[name]
		prefix := strings.TrimSuffix(name, ".dat")
		if !regexp.MustCompile(`^geo/`+prefix+`-[0-9a-f]{12}\.dat$`).MatchString(f.Path) || !strings.Contains(f.Path, f.SHA256[:12]) {
			t.Fatalf("%s path %q, sha256 %s", name, f.Path, f.SHA256)
		}
		data, err := os.ReadFile(filepath.Join(out, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if sha256hex(data) != f.SHA256 || int64(len(data)) != f.Size {
			t.Fatalf("%s: the file is not what the manifest says", name)
		}
		if !slices.Equal(f.Codes, want) {
			t.Fatalf("%s codes %v, want %v", name, f.Codes, want)
		}
		codes, err := geodat.Codes(data)
		if err != nil || !slices.Equal(sortedCopy(codes), want) {
			t.Fatalf("%s holds %v (%v)", name, codes, err)
		}
	}

	// The file keeps the sources' order; PRIVATE is roscomvpn's (the first
	// geoip source), not Loyalsoldier's.
	ip, _ := os.ReadFile(filepath.Join(out, m.Files["geoip.dat"].Path))
	if codes, _ := geodat.Codes(ip); !slices.Equal(codes, []string{"DIRECT", "PRIVATE", "WHITELIST", "RU", "TELEGRAM"}) {
		t.Fatalf("geoip order %v", codes)
	}
	cats, err := geodat.Parse(ip, "PRIVATE", "RU")
	if err != nil || len(cats["PRIVATE"].V4) != 3 || len(cats["RU"].V4) != 10 {
		t.Fatalf("PRIVATE %d, RU %d addresses (%v); want roscomvpn's 3 and 10", len(cats["PRIVATE"].V4), len(cats["RU"].V4), err)
	}

	// Sources: the tagged URL the latest link resolved to, and what was read.
	if len(m.Sources) != 4 {
		t.Fatalf("sources %+v", m.Sources)
	}
	served := u.files()
	if s := m.Sources[0]; s.URL != srv.URL+"/itdog/releases/download/v1/geosite.dat" || s.SHA256 != sha256hex(served["/itdog/releases/download/v1/geosite.dat"]) {
		t.Fatalf("itdog source %+v", s)
	}
	if s := m.Sources[3]; s.Name != "Loyalsoldier/v2ray-rules-dat geoip.dat" || s.SHA256 != sha256hex(served["/loyal/geoip.dat"]) {
		t.Fatalf("Loyalsoldier geoip source %+v", s)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func entriesIn(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if p != dir {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestBuildRefusesMissingCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(u *upstream)
		want   string
	}{
		{"itdog lost a list", func(u *upstream) { u.itdog = u.itdog[:len(u.itdog)-1] }, "YOUTUBE is missing"},
		{"a required list is empty", func(u *upstream) { u.roscom[0].n = 0 }, "DIRECT is empty"},
		{"a source lacks what is taken from it", func(u *upstream) { u.loyalIP = u.loyalIP[:3] }, "has no TELEGRAM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream()
			tc.change(u)
			b, _ := testBuilder(t, u.serve(t), t0)
			out := t.TempDir()
			err := b.build(context.Background(), out, "", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if got := entriesIn(t, out); len(got) != 0 {
				t.Fatalf("a refused build wrote %v", got)
			}
		})
	}
}

func TestBuildAgainstThePreviousBuild(t *testing.T) {
	u := newUpstream()
	u.itdog = append(u.itdog, cat{"NEWCAT", 10})
	srv := u.serve(t)
	channel := t.TempDir()
	b, log := testBuilder(t, srv, t0)
	if err := b.build(context.Background(), channel, "", nil); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	prev := filepath.Join(channel, "geo.json")
	t1 := t0.Add(24 * time.Hour)
	build := func(now time.Time, accept ...string) (string, error) {
		b, log := testBuilder(t, srv, now)
		out := t.TempDir()
		err := b.build(context.Background(), out, prev, splitCodes(strings.Join(accept, ",")))
		return out, func() error {
			if err != nil {
				return fmt.Errorf("%w\n%s", err, log)
			}
			return nil
		}()
	}

	// The same upstream: nothing to publish.
	out, err := build(t1)
	if err != nil {
		t.Fatal(err)
	}
	if got := entriesIn(t, out); len(got) != 0 {
		t.Fatalf("an unchanged build wrote %v", got)
	}

	// Down to half its rules is still a list; below half is a broken upstream.
	inside := slices.IndexFunc(u.itdog, func(c cat) bool { return c.code == "RUSSIA-INSIDE" })
	u.itdog[inside].n = 5
	if _, err := build(t1); err != nil {
		t.Fatalf("half the rules: %v", err)
	}
	u.itdog[inside].n = 4
	if _, err := build(t1); err == nil || !strings.Contains(err.Error(), "RUSSIA-INSIDE shrank from 10 to 4 rules") {
		t.Fatalf("err %v", err)
	}
	out, err = build(t1, "russia-inside")
	if err != nil {
		t.Fatalf("an accepted shrink: %v", err)
	}
	if m, _ := readManifest(t, filepath.Join(out, "geo.json")); m.Serial != t1.Unix() {
		t.Fatalf("serial %d", m.Serial)
	}
	u.itdog[inside].n = 10

	// A category of the previous build must stay, unless accepted; a
	// required one stays whatever is accepted.
	u.itdog = u.itdog[:len(u.itdog)-1] // NEWCAT
	if _, err := build(t1); err == nil || !strings.Contains(err.Error(), "NEWCAT (in the previous build) is missing") {
		t.Fatalf("err %v", err)
	}
	if _, err := build(t1, "NEWCAT"); err != nil {
		t.Fatalf("an accepted removal: %v", err)
	}
	u.itdog = u.itdog[:len(u.itdog)-1] // YOUTUBE
	if _, err := build(t1, "NEWCAT", "YOUTUBE"); err == nil || !strings.Contains(err.Error(), "YOUTUBE is missing") {
		t.Fatalf("err %v", err)
	}
	u.itdog = append(u.itdog, cat{"YOUTUBE", 10}, cat{"NEWCAT", 10})

	// A new build must come after the published one.
	u.loyalIP[2].n = 11
	if _, err := build(t0); err == nil || !strings.Contains(err.Error(), "is not above") {
		t.Fatalf("err %v", err)
	}
}

func TestThePreviousBuildMustBeWhole(t *testing.T) {
	u := newUpstream()
	srv := u.serve(t)
	channel := t.TempDir()
	b, _ := testBuilder(t, srv, t0)
	if err := b.build(context.Background(), channel, "", nil); err != nil {
		t.Fatal(err)
	}
	prev := filepath.Join(channel, "geo.json")
	m, _ := readManifest(t, prev)
	rebuild := func() error {
		b, _ := testBuilder(t, srv, t0.Add(time.Hour))
		return b.build(context.Background(), t.TempDir(), prev, nil)
	}

	site := filepath.Join(channel, m.Files["geosite.dat"].Path)
	good, _ := os.ReadFile(site)
	os.WriteFile(site, append(slices.Clone(good), 0x0a, 0x00), 0o644)
	if err := rebuild(); err == nil || !strings.Contains(err.Error(), "is not the file") {
		t.Fatalf("a changed file: %v", err)
	}
	os.Remove(site)
	if err := rebuild(); err == nil {
		t.Fatal("a missing file must stop the build")
	}
	os.WriteFile(site, good, 0o644)

	f := m.Files["geoip.dat"]
	f.Path = "../geoip.dat"
	m.Files["geoip.dat"] = f
	js, _ := json.Marshal(m)
	os.WriteFile(prev, js, 0o644)
	if err := rebuild(); err == nil || !strings.Contains(err.Error(), `"../geoip.dat"`) {
		t.Fatalf("a path out of geo/: %v", err)
	}
}

func TestDownloadsAreChecked(t *testing.T) {
	ctx := context.Background()
	u := newUpstream()
	srv := u.serve(t)

	b, _ := testBuilder(t, srv, t0)
	b.sources[2].Max = 100
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "more than 100") {
		t.Fatalf("an oversized download: %v", err)
	}

	u.sums["/loyal/geoip.dat.sha256sum"] = strings.Repeat("ab", 32) + "  geoip.dat\n"
	b, _ = testBuilder(t, srv, t0)
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "the release says "+strings.Repeat("ab", 32)) {
		t.Fatalf("a checksum mismatch: %v", err)
	}
	u.sums["/loyal/geoip.dat.sha256sum"] = "Not Found"
	b, _ = testBuilder(t, srv, t0)
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "names no sha256") {
		t.Fatalf("a checksum file without a sum: %v", err)
	}
	delete(u.sums, "/loyal/geoip.dat.sha256sum")

	b, _ = testBuilder(t, srv, t0)
	b.sources[1].URL = srv.URL + "/nosuch/geosite.dat"
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing release: %v", err)
	}

	b, _ = testBuilder(t, srv, t0)
	b.sources[0].URL = strings.Replace(srv.URL, "https://", "http://", 1) + "/itdog/releases/latest/download/geosite.dat"
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "https only") {
		t.Fatalf("an http source: %v", err)
	}

	b, _ = testBuilder(t, srv, t0)
	b.outputs = slices.Clone(outputs)
	b.outputs[1].Max = 200
	if err := b.build(ctx, t.TempDir(), "", nil); err == nil || !strings.Contains(err.Error(), "more than the 200 a router takes") {
		t.Fatalf("an oversized output: %v", err)
	}
}

func TestDownloadLimits(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 4096)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, "http://example.invalid/geoip.dat", http.StatusFound)
		case "/streamed":
			// No Content-Length: the cap must hold while reading.
			for i := 0; i < 4; i++ {
				w.Write(big)
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer srv.Close()
	b := &builder{client: srv.Client()}
	if _, _, err := b.get(context.Background(), srv.URL+"/to-http", 1<<20); err == nil || !strings.Contains(err.Error(), "https only") {
		t.Fatalf("a redirect to http: %v", err)
	}
	if _, _, err := b.get(context.Background(), srv.URL+"/streamed", 10000); err == nil || !strings.Contains(err.Error(), "more than 10000 bytes") {
		t.Fatalf("a streamed oversized body: %v", err)
	}
	if data, _, err := b.get(context.Background(), srv.URL+"/streamed", 4*4096); err != nil || len(data) != 4*4096 {
		t.Fatalf("a body at the cap: %d %v", len(data), err)
	}
}

func TestRunNeedsOut(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-prev", "x"}, &out); err == nil {
		t.Fatal("geobuild without -out must refuse")
	}
	if got := splitCodes(" russia-inside, ,NewCat"); !got["RUSSIA-INSIDE"] || !got["NEWCAT"] || len(got) != 2 {
		t.Fatalf("splitCodes %v", got)
	}
}
