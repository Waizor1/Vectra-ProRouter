package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/feedverify"
)

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testdata/ holds a key and a signature made by OpenWrt's own usign
// (`usign -G`, `usign -S`, 24.10 userland): what opkg checks must be exactly
// what feedtool writes.
func TestMatchesRealUsign(t *testing.T) {
	pub, err := parsePubKey(read(t, "testdata/usign-test.pub"))
	if err != nil {
		t.Fatal(err)
	}
	sec, err := parseSecKey(read(t, "testdata/usign-test.sec"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fingerprintHex(pub.Fingerprint), strings.TrimSpace(string(read(t, "testdata/usign-test.fingerprint"))); got != want {
		t.Fatalf("fingerprint %s, usign -F says %s", got, want)
	}
	if sec.Fingerprint != pub.Fingerprint {
		t.Fatal("the secret and public keys name different fingerprints")
	}
	msg := read(t, "testdata/Packages")
	usignSig := read(t, "testdata/Packages.sig")
	if err := verify(pub, msg, usignSig); err != nil {
		t.Fatalf("usign's own signature does not verify: %v", err)
	}
	if got := sign(sec, msg); !bytes.Equal(got, usignSig) {
		t.Fatalf("feedtool's signature differs from usign's:\n%s\nvs\n%s", got, usignSig)
	}
	// A changed index is refused, as opkg refuses it.
	if err := verify(pub, append(append([]byte(nil), msg...), 'x'), usignSig); err == nil {
		t.Fatal("a signature verified over a changed file")
	}
}

func TestKeygenSignVerify(t *testing.T) {
	pub, sec, err := generateKey(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := parsePubKey(armor("x", pub.marshal()))
	if err != nil {
		t.Fatal(err)
	}
	sec2, err := parseSecKey(armor("x", sec.marshal([16]byte{1})))
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("Package: a\n\n")
	sig := sign(sec2, msg)
	if err := verify(pub2, msg, sig); err != nil {
		t.Fatal(err)
	}
	other, _, err := generateKey(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(other, msg, sig); err == nil || !strings.Contains(err.Error(), "signed by key") {
		t.Fatalf("a signature by another key: %v", err)
	}
}

func TestSecretKeyGuards(t *testing.T) {
	_, sec, err := generateKey(bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	blob := sec.marshal([16]byte{})
	damaged := append([]byte(nil), blob...)
	damaged[len(damaged)-40] ^= 1 // a byte of the seed
	if _, err := parseSecKey(armor("x", damaged)); err == nil {
		t.Fatal("a damaged secret key was accepted")
	}
	locked := append([]byte(nil), blob...)
	locked[7] = 1 // kdfrounds
	if _, err := parseSecKey(armor("x", locked)); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("a passphrase-protected key: %v", err)
	}
	if _, err := parseSecKey([]byte("hello\nworld\n")); err == nil {
		t.Fatal("a file without usign's comment line was accepted")
	}
}

func write(t *testing.T, p string, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func payload(t *testing.T, arch string) (data, ctrl string) {
	t.Helper()
	root := t.TempDir()
	data, ctrl = filepath.Join(root, "data"), filepath.Join(root, "control")
	write(t, filepath.Join(data, "usr/sbin/vctl"), "binary", 0o755)
	write(t, filepath.Join(data, "etc/config/vectra-controller-pro"), "config main\n", 0o600)
	write(t, filepath.Join(ctrl, "control"), "Package: vectra-controller-pro\nVersion: 0.5.0-r1\nDepends: xray-core, vectra-geodata\nArchitecture: "+arch+"\nInstalled-Size: 1\nDescription: Vectra\n more text\n", 0o644)
	write(t, filepath.Join(ctrl, "postinst"), "#!/bin/sh\nexit 0\n", 0o755)
	write(t, filepath.Join(ctrl, "conffiles"), "/etc/config/vectra-controller-pro\n", 0o644)
	return data, ctrl
}

type entry struct {
	name string
	mode int64
	body string
}

func entries(t *testing.T, tgz []byte) []entry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Fatalf("%s owned by %d:%d", h.Name, h.Uid, h.Gid)
		}
		b, _ := io.ReadAll(tr)
		out = append(out, entry{h.Name, h.Mode, string(b)})
	}
}

func TestIpkIsWhatOpkgReads(t *testing.T) {
	data, ctrl := payload(t, "aarch64_cortex-a53")
	out := filepath.Join(t.TempDir(), "a.ipk")
	when := time.Unix(1700000000, 0)
	if err := buildIpk(data, ctrl, out, when); err != nil {
		t.Fatal(err)
	}
	ipk := read(t, out)

	outer := entries(t, ipk)
	var names []string
	for _, e := range outer {
		names = append(names, e.name)
	}
	if strings.Join(names, " ") != "./debian-binary ./data.tar.gz ./control.tar.gz" {
		t.Fatalf("outer members %v", names)
	}
	if outer[0].body != "2.0\n" {
		t.Fatalf("debian-binary %q", outer[0].body)
	}
	got := map[string]entry{}
	for _, e := range entries(t, []byte(outer[1].body)) {
		got[e.name] = e
	}
	if e := got["./usr/sbin/vctl"]; e.mode != 0o755 || e.body != "binary" {
		t.Fatalf("vctl entry %+v", e)
	}
	if e := got["./etc/config/vectra-controller-pro"]; e.mode != 0o600 {
		t.Fatalf("the config keeps its 0600, got %o", e.mode)
	}
	if _, ok := got["./usr/"]; !ok {
		t.Fatal("directories are archived too")
	}

	c, err := readIpkControl(ipk)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := parseControl(c)
	if err != nil {
		t.Fatal(err)
	}
	if fields.get("Installed-Size") != "18" { // "binary" + "config main\n"
		t.Fatalf("Installed-Size %q", fields.get("Installed-Size"))
	}
	if !strings.HasSuffix(string(c), "Description: Vectra\n more text\n") {
		t.Fatalf("the description lost its continuation line:\n%s", c)
	}
	var postinst entry
	for _, e := range entries(t, []byte(outer[2].body)) {
		if e.name == "./postinst" {
			postinst = e
		}
	}
	if postinst.mode != 0o755 {
		t.Fatalf("postinst must stay executable, got %o", postinst.mode)
	}

	// The same payload gives the same bytes: the feed's SHA256sum is stable.
	again := filepath.Join(t.TempDir(), "b.ipk")
	if err := buildIpk(data, ctrl, again, when); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ipk, read(t, again)) {
		t.Fatal("two builds of the same payload differ")
	}
}

func TestAppleDoubleRefused(t *testing.T) {
	data, ctrl := payload(t, "all")
	write(t, filepath.Join(data, "usr/sbin/._vctl"), "x", 0o644)
	if err := buildIpk(data, ctrl, filepath.Join(t.TempDir(), "a.ipk"), time.Unix(0, 0)); err == nil {
		t.Fatal("an AppleDouble file went into the package")
	}
}

func TestIndex(t *testing.T) {
	feed := t.TempDir()
	data, ctrl := payload(t, "aarch64_generic")
	pkg := filepath.Join(feed, "vectra-controller-pro_0.5.0-r1_aarch64_generic.ipk")
	if err := buildIpk(data, ctrl, pkg, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	gdata, gctrl := payload(t, "all")
	write(t, filepath.Join(gctrl, "control"), "Package: vectra-geodata\nVersion: 1-r1\nArchitecture: all\nDescription: geo\n", 0o644)
	geo := filepath.Join(feed, "vectra-geodata_1-r1_all.ipk")
	if err := buildIpk(gdata, gctrl, geo, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	n, err := buildIndex(feed, "aarch64_generic")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d packages", n)
	}
	idx := string(read(t, filepath.Join(feed, "Packages")))
	sum := sha256.Sum256(read(t, pkg))
	want := "Filename: vectra-controller-pro_0.5.0-r1_aarch64_generic.ipk\nSize: "
	if !strings.Contains(idx, want) || !strings.Contains(idx, "SHA256sum: "+hex.EncodeToString(sum[:])+"\nDescription: Vectra\n more text\n") {
		t.Fatalf("index:\n%s", idx)
	}
	if strings.Count(idx, "\n\n") != 2 {
		t.Fatalf("one blank line after each package:\n%q", idx)
	}
	gz, err := gzip.NewReader(bytes.NewReader(read(t, filepath.Join(feed, "Packages.gz"))))
	if err != nil {
		t.Fatal(err)
	}
	unz, _ := io.ReadAll(gz)
	if string(unz) != idx {
		t.Fatal("Packages.gz is not Packages")
	}

	// A package built for another architecture does not belong here.
	if _, err := buildIndex(feed, "mipsel_24kc"); err == nil || !strings.Contains(err.Error(), "built for aarch64_generic") {
		t.Fatalf("a foreign architecture: %v", err)
	}
	// Nor does a second version of the same package.
	if err := buildIpk(data, ctrl, filepath.Join(feed, "vectra-controller-pro_0.4.0-r1_aarch64_generic.ipk"), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := buildIndex(feed, "aarch64_generic"); err == nil || !strings.Contains(err.Error(), "one version per package") {
		t.Fatalf("two versions of one package: %v", err)
	}
}

// What a router checks before vctl updates itself is what feedtool publishes:
// a key feedtool makes, filed as the installer files it, verifies feedtool's
// signature over the index feedtool builds (internal/feedverify, the router's
// side), Packages.gz unpacks to what was signed, and the index reads there as
// feedtool wrote it.
func TestTheRouterVerifiesWhatFeedtoolSigns(t *testing.T) {
	pub, sec, err := generateKey(bytes.NewReader(bytes.Repeat([]byte{5}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	feed := t.TempDir()
	data, ctrl := payload(t, "aarch64_cortex-a53")
	pkg := filepath.Join(feed, "vectra-controller-pro_0.5.0-r1_aarch64_cortex-a53.ipk")
	if err := buildIpk(data, ctrl, pkg, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := buildIndex(feed, "aarch64_cortex-a53"); err != nil {
		t.Fatal(err)
	}
	index := read(t, filepath.Join(feed, "Packages"))
	sig := sign(sec, index)

	// install/install.sh: printf 'untrusted comment: Vectra Pro feed\n%s\n' "$FEED_KEY" > "$KEYS/$FEED_KEY_ID"
	keys := t.TempDir()
	write(t, filepath.Join(keys, fingerprintHex(pub.Fingerprint)), string(armor("Vectra Pro feed", pub.marshal())), 0o644)
	n, err := feedverify.VerifyWithKeys(keys, index, sig)
	if err != nil || n.String() != fingerprintHex(pub.Fingerprint) {
		t.Fatalf("the router does not verify feedtool's signature: %v %v", n, err)
	}
	unpacked, err := feedverify.Feed{Gzip: true}.Unpack(read(t, filepath.Join(feed, "Packages.gz")), 1<<20)
	if err != nil || !bytes.Equal(unpacked, index) {
		t.Fatalf("Packages.gz does not unpack to what was signed: %v", err)
	}
	pkgs, err := feedverify.ParseIndex(index)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(read(t, pkg))
	want := feedverify.Package{Name: "vectra-controller-pro", Version: "0.5.0-r1", Architecture: "aarch64_cortex-a53",
		Filename: filepath.Base(pkg), SHA256: hex.EncodeToString(sum[:])}
	if len(pkgs) != 1 || pkgs[0] != want {
		t.Fatalf("the router reads the index as\n%+v\nwant\n%+v", pkgs, want)
	}
}

func TestControlSetKeepsOrder(t *testing.T) {
	c, err := parseControl([]byte("Package: a\nVersion: 1\nDescription: x\n y\n"))
	if err != nil {
		t.Fatal(err)
	}
	c.set("Installed-Size", "5")
	c.set("Version", "2")
	if got := string(c.bytes()); got != "Package: a\nVersion: 2\nInstalled-Size: 5\nDescription: x\n y\n" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"Package: a\n\nVersion: 1\n", " lead\n", "Package: a\nPackage: b\n", "no colon\n"} {
		if _, err := parseControl([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
