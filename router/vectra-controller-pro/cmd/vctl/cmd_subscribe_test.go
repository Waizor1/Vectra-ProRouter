package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// What `vctl subscribe parse -out FILE` writes — the nodes, user ids and
// passwords among them, or one provider entry verbatim — is root's alone,
// whatever FILE was before: readable to all, or a link someone left in /tmp
// to a file of theirs.
func TestSubscribeParseWritesForRootAlone(t *testing.T) {
	dir := t.TempDir()
	links := "vless://0badc0de-feed-4000-8000-000000000001@ru1.provider.invalid:443?security=reality#DE\n" +
		"trojan://not-a-real-trojan-pass@tr.provider.invalid:443#TR\n"
	in := filepath.Join(dir, "sub.txt")
	if err := os.WriteFile(in, []byte(base64.StdEncoding.EncodeToString([]byte(links))), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := filepath.Join(dir, "sub.json")
	if err := os.WriteFile(entries, []byte(`[{"remarks":"DE","outbounds":[{"tag":"x","protocol":"vless"}]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"nodes", []string{"-in", in}},
		{"entry", []string{"-in", entries, "-content-type", "application/json", "-entry", "0"}},
	} {
		// A file readable to all, and a link to another's file.
		out := filepath.Join(dir, c.name+".json")
		if err := os.WriteFile(out, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(out, 0o644); err != nil {
			t.Fatal(err)
		}
		theirs := filepath.Join(dir, c.name+"-theirs")
		if err := os.WriteFile(theirs, nil, 0o666); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, c.name+"-link.json")
		if err := os.Symlink(theirs, link); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{out, link} {
			if err := subscribeParse(append(append([]string(nil), c.args...), "-out", target)); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			st, err := os.Lstat(target)
			if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 || st.Size() == 0 {
				t.Errorf("%s -out %s: %v, %v; want a file of root's alone with what was parsed", c.name, filepath.Base(target), st.Mode(), err)
			}
		}
		if st, _ := os.Stat(theirs); st.Size() != 0 {
			t.Errorf("%s: written through the link into another's file", c.name)
		}
	}
}
