package feedverify

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vector is a file of the test vector OpenWrt's own usign made
// (tools/feedtool/testdata: usign -G, usign -S over Packages, usign -F) — a
// throwaway key that signs nothing anywhere. What usign wrote is what opkg
// checks a feed with.
func vector(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tools", "feedtool", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// testKey is a usign key made the way feedtool keygen makes one
// (tools/feedtool/usign.go): "Ed" | key number | ed25519 key, base64 under
// usign's comment line. Obviously fake: its seed is one byte repeated.
type testKey struct {
	number [8]byte
	priv   ed25519.PrivateKey
}

func newTestKey(seed byte) testKey {
	k := testKey{priv: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))}
	for i := range k.number {
		k.number[i] = seed*16 + byte(i)
	}
	return k
}

func (k testKey) id() string { return fmt.Sprintf("%016x", binary.BigEndian.Uint64(k.number[:])) }

func (k testKey) pub() []byte {
	blob := append(append([]byte("Ed"), k.number[:]...), k.priv.Public().(ed25519.PublicKey)...)
	return []byte("untrusted comment: test key\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

// sign is usign -S: the signature names the key's number.
func (k testKey) sign(msg []byte) []byte { return k.signAs(k.number, msg) }

// signAs is a signature by k that names another key's number.
func (k testKey) signAs(number [8]byte, msg []byte) []byte {
	blob := append(append([]byte("Ed"), number[:]...), ed25519.Sign(k.priv, msg)...)
	return []byte("untrusted comment: signed by key " + k.id() + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

// keysWith is /etc/opkg/keys holding these keys, each filed under its number
// as the installer files the Vectra key.
func keysWith(t *testing.T, keys ...testKey) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range keys {
		writeFile(t, filepath.Join(dir, k.id()), k.pub())
	}
	return dir
}

// What OpenWrt's usign signed verifies under the key /etc/opkg/keys files
// under its number — opkg's own check (usign -V -P /etc/opkg/keys) — and a
// changed index does not.
func TestVerifyWithKeysChecksWhatUsignSigned(t *testing.T) {
	id := strings.TrimSpace(string(vector(t, "usign-test.fingerprint")))
	keys := t.TempDir()
	writeFile(t, filepath.Join(keys, id), vector(t, "usign-test.pub"))
	msg, sig := vector(t, "Packages"), vector(t, "Packages.sig")

	n, err := VerifyWithKeys(keys, msg, sig)
	if err != nil {
		t.Fatalf("usign's own signature: %v", err)
	}
	if n.String() != id {
		t.Fatalf("verified by key %s, usign -F says %s", n, id)
	}
	if _, err := VerifyWithKeys(keys, append(append([]byte(nil), msg...), 'x'), sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a changed index: %v, want ErrBadSignature", err)
	}
}

// Only a key the router holds can vouch for a feed: a signature by any other
// key is refused, whatever it signed, and the refusal names the key.
func TestVerifyWithKeysRefusesAKeyTheRouterDoesNotHold(t *testing.T) {
	ours, theirs := newTestKey(1), newTestKey(2)
	keys := keysWith(t, ours)
	msg := []byte("Package: vectra-controller-pro\n\n")

	_, err := VerifyWithKeys(keys, msg, theirs.sign(msg))
	if !errors.Is(err, ErrUnknownKey) || !strings.Contains(err.Error(), theirs.id()) {
		t.Fatalf("a signature by another key: %v", err)
	}
	// Anti-vacuity: the router's own key verifies the same index.
	if n, err := VerifyWithKeys(keys, msg, ours.sign(msg)); err != nil || n.String() != ours.id() {
		t.Fatalf("our key: %v %v", n, err)
	}
}

// A signature that names our key's number but was made by another key does
// not verify under ours.
func TestVerifyWithKeysRefusesAnotherKeysSignatureUnderOurNumber(t *testing.T) {
	ours, theirs := newTestKey(1), newTestKey(2)
	msg := []byte("Package: vectra-controller-pro\n\n")
	if _, err := VerifyWithKeys(keysWith(t, ours), msg, theirs.signAs(ours.number, msg)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("another key's signature under our number: %v, want ErrBadSignature", err)
	}
}

// The key filed under a number must carry that number, as feedtool verify
// checks: another key filed under our number is not our key, even over a
// signature it made itself.
func TestVerifyWithKeysRefusesAKeyFiledUnderAnotherNumber(t *testing.T) {
	ours, theirs := newTestKey(1), newTestKey(2)
	keys := t.TempDir()
	writeFile(t, filepath.Join(keys, ours.id()), theirs.pub())
	msg := []byte("Package: vectra-controller-pro\n\n")

	_, err := VerifyWithKeys(keys, msg, theirs.signAs(ours.number, msg))
	if !errors.Is(err, ErrKeyMismatch) || !strings.Contains(err.Error(), theirs.id()) {
		t.Fatalf("a key filed under another number: %v, want ErrKeyMismatch", err)
	}
}

// What is not a usign file is no key and no signature: the comment line, the
// algorithm and the length are all checked.
func TestParseRefusesWhatIsNotUsign(t *testing.T) {
	k := newTestKey(3)
	pubBlob, _ := base64.StdEncoding.DecodeString(strings.Split(string(k.pub()), "\n")[1])
	sigBlob, _ := base64.StdEncoding.DecodeString(strings.Split(string(k.sign([]byte("x"))), "\n")[1])
	armor := func(b []byte) []byte {
		return []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString(b) + "\n")
	}
	// A secret key as feedtool keygen writes one: "Ed" | "BK" | kdfrounds |
	// salt | checksum | key number | seed || public.
	secBlob := append([]byte("EdBK"), make([]byte, 4+16+8)...)
	secBlob = append(append(secBlob, k.number[:]...), k.priv...)
	if _, err := ParsePublicKey(k.pub()); err != nil {
		t.Fatalf("a good key: %v", err)
	}
	if _, err := ParseSignature(k.sign([]byte("x"))); err != nil {
		t.Fatalf("a good signature: %v", err)
	}
	for name, bad := range map[string][]byte{
		"no comment line":   []byte(base64.StdEncoding.EncodeToString(pubBlob) + "\n"),
		"not base64":        []byte("untrusted comment: x\n!!!\n"),
		"a signature":       armor(sigBlob),
		"another algorithm": armor(append([]byte("Xx"), pubBlob[2:]...)),
		"a short key":       armor(pubBlob[:20]),
		"nothing after it":  []byte("untrusted comment: x\n"),
		"an empty file":     nil,
		"a secret key":      armor(secBlob),
	} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey accepted %s", name)
		}
	}
	for name, bad := range map[string][]byte{
		"a public key":      armor(pubBlob),
		"another algorithm": armor(append([]byte("Xx"), sigBlob[2:]...)),
		"a short signature": armor(sigBlob[:40]),
	} {
		if _, err := ParseSignature(bad); err == nil {
			t.Errorf("ParseSignature accepted %s", name)
		}
	}
}

// The index as feedtool index writes it (tools/feedtool/index.go) and opkg
// reads it: each package's control fields plus Filename, Size and SHA256sum,
// a Description that runs over lines, one blank line after each package.
func TestParseIndex(t *testing.T) {
	idx := "Package: vectra-controller-pro\n" +
		"Version: 0.6.0-r37\n" +
		"Depends: ca-bundle, xray-core (>= 26.3.27), vectra-geodata\n" +
		"Architecture: aarch64_cortex-a53\n" +
		"Installed-Size: 31457280\n" +
		"Filename: vectra-controller-pro_0.6.0-r37_aarch64_cortex-a53.ipk\n" +
		"Size: 10485760\n" +
		"SHA256sum: 1111111111111111111111111111111111111111111111111111111111111111\n" +
		"Description: Vectra Controller Pro: owns xray end to end (config, process,\n" +
		" firewall, DNS, subscription) with the router UI in LuCI.\n" +
		"\n" +
		"Package: vectra-geodata\n" +
		"Version: 2026.9.28-r2\n" +
		"Architecture: all\n" +
		"Filename: vectra-geodata_2026.9.28-r2_all.ipk\n" +
		"SHA256sum: 2222222222222222222222222222222222222222222222222222222222222222\n" +
		"Description: geoip.dat and geosite.dat\n" +
		"\n"
	pkgs, err := ParseIndex([]byte(idx))
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{
		{Name: "vectra-controller-pro", Version: "0.6.0-r37", Architecture: "aarch64_cortex-a53",
			Filename: "vectra-controller-pro_0.6.0-r37_aarch64_cortex-a53.ipk", SHA256: strings.Repeat("1", 64)},
		{Name: "vectra-geodata", Version: "2026.9.28-r2", Architecture: "all",
			Filename: "vectra-geodata_2026.9.28-r2_all.ipk", SHA256: strings.Repeat("2", 64)},
	}
	if fmt.Sprint(pkgs) != fmt.Sprint(want) {
		t.Fatalf("parsed\n%+v\nwant\n%+v", pkgs, want)
	}
	// No blank line after the last package: the same.
	if again, err := ParseIndex([]byte(strings.TrimRight(idx, "\n"))); err != nil || fmt.Sprint(again) != fmt.Sprint(want) {
		t.Fatalf("without the last blank line: %+v %v", again, err)
	}
}

// An index the check cannot read for sure is refused, not read loosely: a
// field twice in one package (which SHA256sum would count?), a line that is
// no field, a package with no name.
func TestParseIndexRefusesWhatItCannotReadForSure(t *testing.T) {
	for name, bad := range map[string]string{
		"a field twice":             "Package: a\nSHA256sum: 11\nSHA256sum: 22\n\n",
		"a line that is no field":   "Package: a\nthis is not a field\n\n",
		"a continuation first":      " continued\nPackage: a\n\n",
		"a package with no name":    "Version: 1\nArchitecture: all\n\n",
		"a field name with a space": "Package: a\nSHA256 sum: 11\n\n",
	} {
		if _, err := ParseIndex([]byte(bad)); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if pkgs, err := ParseIndex(nil); err != nil || len(pkgs) != 0 {
		t.Errorf("an empty index: %v %v", pkgs, err)
	}
}

// The feed is the line the installer writes to customfeeds.conf
// (install/install.sh: "src/gz vectra_pro <url>/<arch>"); a commented-out
// line or another feed's is not it.
func TestFindFeed(t *testing.T) {
	conf := "# add your custom package feeds here\n" +
		"#\n" +
		"# src/gz example_feed_name http://www.example.com/path/to/files\n" +
		"#src/gz vectra_pro https://elsewhere.invalid/pro\n" +
		"src/gz openwrt_core https://downloads.openwrt.org/releases/24.10.6/targets/mediatek/filogic/packages\n" +
		"src/gz vectra_pro https://api.vectra-pro.net/artifacts/openwrt/pro-canary/aarch64_cortex-a53\n"
	f, ok := FindFeed([]byte(conf), "vectra_pro")
	if !ok {
		t.Fatal("the feed was not found")
	}
	base := "https://api.vectra-pro.net/artifacts/openwrt/pro-canary/aarch64_cortex-a53"
	if f.URL != base || !f.Gzip {
		t.Fatalf("feed %+v", f)
	}
	if f.IndexURL() != base+"/Packages.gz" || f.SignatureURL() != base+"/Packages.sig" {
		t.Fatalf("index %s, signature %s", f.IndexURL(), f.SignatureURL())
	}
	// A plain src feed: opkg downloads Packages; a trailing slash adds none.
	f, ok = FindFeed([]byte("src vectra_pro "+base+"/\n"), "vectra_pro")
	if !ok || f.Gzip || f.IndexURL() != base+"/Packages" || f.SignatureURL() != base+"/Packages.sig" {
		t.Fatalf("a src feed: %+v %v", f, ok)
	}
	for _, none := range []string{"", conf[:strings.LastIndex(conf, "src/gz vectra_pro")], "src/gz vectra_pro\n", "src/gz vectra_pro_old " + base + "\n"} {
		if f, ok := FindFeed([]byte(none), "vectra_pro"); ok {
			t.Errorf("found %+v in %q", f, none)
		}
	}
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The signature covers Packages, not Packages.gz: a src/gz feed's index is
// checked unpacked, as opkg-key checks it (zcat | usign -V -m -) — and, as
// opkg-key does, one a server already unpacked is taken as it is. Bigger than
// the limit is refused, packed or not.
func TestUnpack(t *testing.T) {
	idx := []byte("Package: vectra-controller-pro\nVersion: 0.6.0-r37\n\n")
	src, srcgz := Feed{}, Feed{Gzip: true}
	for name, c := range map[string]struct {
		f   Feed
		raw []byte
	}{
		"Packages.gz":            {srcgz, gz(t, idx)},
		"an unpacked index":      {srcgz, idx},
		"Packages of a src feed": {src, idx},
	} {
		got, err := c.f.Unpack(c.raw, 1024)
		if err != nil || !bytes.Equal(got, idx) {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	big := bytes.Repeat([]byte("Description: x\n"), 100)
	for name, c := range map[string]struct {
		f   Feed
		raw []byte
	}{
		"a big Packages.gz":     {srcgz, gz(t, big)},
		"a big Packages":        {src, big},
		"a damaged Packages.gz": {srcgz, gz(t, idx)[:20]},
	} {
		if _, err := c.f.Unpack(c.raw, 1024); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
