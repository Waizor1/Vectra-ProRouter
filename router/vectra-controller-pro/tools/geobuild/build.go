package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/geodat"
)

const (
	siteFile     = "geosite.dat"
	ipFile       = "geoip.dat"
	manifestName = "geo.json"
	// A release's checksum file is one line.
	maxSumBytes = 4 << 10
)

// source is one upstream geo file and the categories taken from it.
type source struct {
	Name  string   // as the manifest names it
	File  string   // the output it goes into: siteFile or ipFile
	URL   string   // https only, redirects too
	Sum   string   // suffix of the release's own sha256 file; "" when it publishes none
	Max   int64    // bytes read at most
	Codes []string // the categories taken; none takes every one
}

// output is one file a build writes.
type output struct {
	Name     string // the manifest's key: the name xray reads the file by
	Prefix   string // the file is geo/<Prefix>-<sha256[:12]>.dat
	Max      int64
	Required []string
}

// manifest is geo.json: what a router fetches, checks and swaps in. Paths
// are relative to the channel's root, where geo.json itself is served.
type manifest struct {
	Schema  int                     `json:"schema"`
	Serial  int64                   `json:"serial"`
	BuiltAt string                  `json:"builtAt"`
	Files   map[string]manifestFile `json:"files"`
	Sources []sourceRef             `json:"sources"`
}

type manifestFile struct {
	Path   string   `json:"path"`
	SHA256 string   `json:"sha256"`
	Size   int64    `json:"size"`
	Codes  []string `json:"codes"`
}

type sourceRef struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type builder struct {
	client  *http.Client
	sources []source
	outputs []output
	now     func() time.Time
	log     io.Writer
}

// previous is the published build: its serial, its files and how many rules
// each of their categories had.
type previous struct {
	serial int64
	files  map[string]manifestFile
	items  map[string]map[string]int
}

// build downloads every source, merges them into the outputs (an earlier
// source's category wins), checks them against the required categories and
// the previous build, then writes the files and, last, the manifest. Nothing
// is written unless every check passes.
func (b *builder) build(ctx context.Context, out, prevPath string, accept map[string]bool) error {
	var prev *previous
	if prevPath != "" {
		var err error
		if prev, err = loadPrevious(prevPath, b.outputs); err != nil {
			return fmt.Errorf("the previous build: %w", err)
		}
	}

	lists := map[string][][]geodat.Entry{}
	var refs []sourceRef
	for _, s := range b.sources {
		entries, ref, err := b.source(ctx, s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		lists[s.File] = append(lists[s.File], entries)
		refs = append(refs, ref)
	}

	files := map[string]manifestFile{}
	data := map[string][]byte{}
	for _, o := range b.outputs {
		merged := geodat.Merge(lists[o.Name]...)
		var was map[string]int
		if prev != nil {
			was = prev.items[o.Name]
		}
		if err := check(o, merged, was, accept); err != nil {
			return err
		}
		d := geodat.Encode(merged)
		if int64(len(d)) > o.Max {
			return fmt.Errorf("%s: %d bytes, more than the %d a router takes", o.Name, len(d), o.Max)
		}
		codes := make([]string, 0, len(merged))
		for _, e := range merged {
			codes = append(codes, e.Code)
		}
		if o.Name == ipFile {
			// vctl reads geoip.dat's addresses itself (DIRECT goes into
			// the kernel): the same reader must take every category.
			if _, err := geodat.Parse(d, codes...); err != nil {
				return fmt.Errorf("%s: %w", o.Name, err)
			}
		}
		sort.Strings(codes)
		sum := sha256hex(d)
		files[o.Name] = manifestFile{
			Path:   "geo/" + o.Prefix + "-" + sum[:12] + ".dat",
			SHA256: sum,
			Size:   int64(len(d)),
			Codes:  codes,
		}
		data[o.Name] = d
		fmt.Fprintf(b.log, "%s: %d categories, %d bytes, sha256 %s\n", o.Name, len(codes), len(d), sum)
	}

	if prev != nil && sameFiles(prev.files, files) {
		fmt.Fprintf(b.log, "unchanged since serial %d: nothing written\n", prev.serial)
		return nil
	}
	now := b.now().UTC()
	m := manifest{Schema: 1, Serial: now.Unix(), BuiltAt: now.Format(time.RFC3339), Files: files, Sources: refs}
	if prev != nil && m.Serial <= prev.serial {
		return fmt.Errorf("serial %d is not above the previous build's %d (is the clock right?)", m.Serial, prev.serial)
	}
	for _, o := range b.outputs {
		if err := writeFile(filepath.Join(out, filepath.FromSlash(files[o.Name].Path)), data[o.Name]); err != nil {
			return err
		}
	}
	js, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, manifestName), append(js, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(b.log, "serial %d: %s\n", m.Serial, filepath.Join(out, manifestName))
	return nil
}

// source downloads one upstream file, checks it against the release's own
// checksum when there is one, and selects its categories: every one it is
// asked for must be there.
func (b *builder) source(ctx context.Context, s source) ([]geodat.Entry, sourceRef, error) {
	data, resolved, err := b.get(ctx, s.URL, s.Max)
	if err != nil {
		return nil, sourceRef{}, err
	}
	sum := sha256hex(data)
	if s.Sum != "" {
		// From the tagged release the download resolved to: a release
		// published in between cannot be checked against the wrong sum.
		txt, _, err := b.get(ctx, resolved+s.Sum, maxSumBytes)
		if err != nil {
			return nil, sourceRef{}, fmt.Errorf("its checksum: %w", err)
		}
		want, ok := firstSHA256(txt)
		if !ok {
			return nil, sourceRef{}, fmt.Errorf("%s%s names no sha256", resolved, s.Sum)
		}
		if want != sum {
			return nil, sourceRef{}, fmt.Errorf("sha256 %s, the release says %s", sum, want)
		}
	}
	entries, err := geodat.Select(data, s.Codes...)
	if err != nil {
		return nil, sourceRef{}, err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Code] = true
	}
	var missing []string
	for _, c := range s.Codes {
		if !have[strings.ToUpper(c)] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, sourceRef{}, fmt.Errorf("has no %s", strings.Join(missing, ", "))
	}
	if len(entries) == 0 {
		return nil, sourceRef{}, errors.New("has no categories")
	}
	fmt.Fprintf(b.log, "%s: %s, %d bytes, sha256 %s, %d categories taken\n", s.Name, resolved, len(data), sum, len(entries))
	return entries, sourceRef{Name: s.Name, URL: resolved, SHA256: sum}, nil
}

// get reads an https URL, at most max bytes. The URL it returns is the last
// one on the original host the redirects went through: for a GitHub
// latest/download link, the tagged release (not the CDN's signed URL).
func (b *builder) get(ctx context.Context, rawURL string, max int64) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "https" {
		return nil, "", fmt.Errorf("%s: https only", rawURL)
	}
	resolved := rawURL
	c := *b.client
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" {
			return fmt.Errorf("redirected to %s: https only", r.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if r.URL.Host == u.Host {
			resolved = r.URL.String()
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	if resp.ContentLength > max {
		return nil, "", fmt.Errorf("%s: %d bytes, more than %d", rawURL, resp.ContentLength, max)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", rawURL, err)
	}
	if int64(len(data)) > max {
		return nil, "", fmt.Errorf("%s: more than %d bytes", rawURL, max)
	}
	return data, resolved, nil
}

// check refuses an output that lacks a required category, has one empty,
// lost a category the previous build had or shrank one below half its rules
// — unless accept names it; the required categories stay required.
func check(o output, entries []geodat.Entry, prev map[string]int, accept map[string]bool) error {
	have := make(map[string]int, len(entries))
	for _, e := range entries {
		n, err := e.Items()
		if err != nil {
			return fmt.Errorf("%s: %s: %w", o.Name, e.Code, err)
		}
		have[e.Code] = n
	}
	var problems []string
	required := map[string]bool{}
	for _, c := range o.Required {
		required[c] = true
		if n, ok := have[c]; !ok {
			problems = append(problems, c+" is missing")
		} else if n == 0 {
			problems = append(problems, c+" is empty")
		}
	}
	codes := make([]string, 0, len(prev))
	for c := range prev {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		n, ok := have[c]
		switch {
		case accept[c]:
		case !ok && !required[c]:
			problems = append(problems, c+" (in the previous build) is missing")
		case ok && n*2 < prev[c]:
			problems = append(problems, fmt.Sprintf("%s shrank from %d to %d rules", c, prev[c], n))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: refusing to publish: %s", o.Name, strings.Join(problems, "; "))
	}
	return nil
}

// The paths a manifest may name: under geo/, content-addressed.
var filePath = regexp.MustCompile(`^geo/[a-z]+-[0-9a-f]{12}\.dat$`)

// loadPrevious reads the published manifest and the files it names, beside
// it as the channel serves them, each checked against its sha256.
func loadPrevious(path string, outs []output) (*previous, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("%s: schema %d", path, m.Schema)
	}
	p := &previous{serial: m.Serial, files: m.Files, items: map[string]map[string]int{}}
	root := filepath.Dir(path)
	for _, o := range outs {
		f, ok := m.Files[o.Name]
		if !ok {
			return nil, fmt.Errorf("%s names no %s", path, o.Name)
		}
		if !filePath.MatchString(f.Path) {
			return nil, fmt.Errorf("%s: %s at %q", path, o.Name, f.Path)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			return nil, err
		}
		if got := sha256hex(data); got != f.SHA256 {
			return nil, fmt.Errorf("%s is not the file %s names (sha256 %s, not %s)", f.Path, path, got, f.SHA256)
		}
		entries, err := geodat.Select(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		items := make(map[string]int, len(entries))
		for _, e := range entries {
			if items[e.Code], err = e.Items(); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", f.Path, e.Code, err)
			}
		}
		p.items[o.Name] = items
	}
	return p, nil
}

func sameFiles(a, b map[string]manifestFile) bool {
	if len(a) != len(b) {
		return false
	}
	for name, f := range b {
		if a[name].SHA256 != f.SHA256 {
			return false
		}
	}
	return true
}

// firstSHA256 is the first 64-hex-digit word of a checksum file ("<sum>
// <name>" in sha256sum's format).
func firstSHA256(b []byte) (string, bool) {
	for _, w := range strings.Fields(string(b)) {
		if len(w) != 64 {
			continue
		}
		if _, err := hex.DecodeString(w); err == nil {
			return strings.ToLower(w), true
		}
	}
	return "", false
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writeFile replaces path atomically, readable by the web server.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
