// Package feedverify checks a signed opkg feed the way opkg checks one before
// it trusts it: the feed's index is signed with usign, and the key that signed
// it must be one the router holds — the file in /etc/opkg/keys named after
// the key's number (`usign -V -P /etc/opkg/keys`).
//
// usign's files are its structs, base64-encoded under an "untrusted comment:"
// line; tools/feedtool/usign.go writes them, byte for byte as usign does:
//
//	pubkey  "Ed" | key number[8] | ed25519 public key[32]
//	sig     "Ed" | key number[8] | ed25519 signature[64]
//
// A key's number names its file ("%016x"). The signature is pure Ed25519 over
// the uncompressed index, Packages, exactly as it is: for a src/gz feed opkg
// downloads Packages.gz and checks it unpacked (opkg-key verify: zcat |
// usign -V -m -). Packages.sig never covers the .gz.
package feedverify

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrUnknownKey: the signature names a key the router does not hold.
	ErrUnknownKey = errors.New("unknown key")
	// ErrKeyMismatch: the key filed under the signature's number is another key.
	ErrKeyMismatch = errors.New("key number mismatch")
	// ErrBadSignature: the signature does not verify over the file.
	ErrBadSignature = errors.New("bad signature")
)

const commentPrefix = "untrusted comment: "

var algEd = []byte("Ed")

// KeyNumber is a usign key's number: every signature the key makes carries
// it, and it names the key's file in a keys directory.
type KeyNumber [8]byte

// String is the key's file name in /etc/opkg/keys, as usign -F prints it.
func (n KeyNumber) String() string { return fmt.Sprintf("%016x", binary.BigEndian.Uint64(n[:])) }

// PublicKey is a usign public key.
type PublicKey struct {
	Number KeyNumber
	Key    ed25519.PublicKey
}

// Signature is a usign signature.
type Signature struct {
	Number KeyNumber
	Sig    []byte
}

// unarmor reads a usign file: the comment line, then the base64 of the struct.
func unarmor(data []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], commentPrefix) {
		return nil, errors.New("not a usign file: the first line must start with \"untrusted comment: \"")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
}

// ParsePublicKey reads a usign public key file.
func ParsePublicKey(data []byte) (*PublicKey, error) {
	blob, err := unarmor(data)
	if err != nil {
		return nil, err
	}
	if len(blob) != 2+8+ed25519.PublicKeySize || !bytes.Equal(blob[:2], algEd) {
		return nil, fmt.Errorf("not an Ed25519 usign public key (%d bytes)", len(blob))
	}
	k := &PublicKey{Key: ed25519.PublicKey(append([]byte(nil), blob[10:]...))}
	copy(k.Number[:], blob[2:10])
	return k, nil
}

// ParseSignature reads a usign signature file.
func ParseSignature(data []byte) (*Signature, error) {
	blob, err := unarmor(data)
	if err != nil {
		return nil, err
	}
	if len(blob) != 2+8+ed25519.SignatureSize || !bytes.Equal(blob[:2], algEd) {
		return nil, fmt.Errorf("not an Ed25519 usign signature (%d bytes)", len(blob))
	}
	s := &Signature{Sig: append([]byte(nil), blob[10:]...)}
	copy(s.Number[:], blob[2:10])
	return s, nil
}

// VerifyWithKeys checks a usign signature over message as opkg does (usign -V
// -P keysDir): the key is the file in keysDir named after the number the
// signature carries, it must carry that number itself, and the signature must
// verify under it. It returns the number of the key that vouched.
func VerifyWithKeys(keysDir string, message, sigFile []byte) (KeyNumber, error) {
	sig, err := ParseSignature(sigFile)
	if err != nil {
		return KeyNumber{}, err
	}
	p := filepath.Join(keysDir, sig.Number.String())
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return KeyNumber{}, fmt.Errorf("%w: signed by key %s, which is not in %s", ErrUnknownKey, sig.Number, keysDir)
	}
	if err != nil {
		return KeyNumber{}, err
	}
	key, err := ParsePublicKey(raw)
	if err != nil {
		return KeyNumber{}, fmt.Errorf("%s: %w", p, err)
	}
	if key.Number != sig.Number {
		return KeyNumber{}, fmt.Errorf("%w: %s holds key %s, not key %s", ErrKeyMismatch, p, key.Number, sig.Number)
	}
	if !ed25519.Verify(key.Key, message, sig.Sig) {
		return KeyNumber{}, fmt.Errorf("%w: it does not match the file (key %s)", ErrBadSignature, sig.Number)
	}
	return sig.Number, nil
}

// Package is a package of a feed's index: the fields a check needs.
type Package struct {
	Name, Version, Architecture, Filename, SHA256 string
}

// ParseIndex reads a Packages index: each package a run of "Field: value"
// lines (a value runs on over indented lines), one blank line after it. What
// it cannot read for sure — a line that is no field, a field twice in one
// package, a package with no name — is an error, not a guess.
func ParseIndex(index []byte) ([]Package, error) {
	var out []Package
	fields := map[string]string{}
	last := ""
	end := func() error {
		if len(fields) == 0 {
			return nil
		}
		if fields["Package"] == "" {
			return errors.New("a package without a Package field")
		}
		out = append(out, Package{
			Name: fields["Package"], Version: fields["Version"], Architecture: fields["Architecture"],
			Filename: fields["Filename"], SHA256: fields["SHA256sum"],
		})
		fields, last = map[string]string{}, ""
		return nil
	}
	for i, line := range strings.Split(string(index), "\n") {
		if strings.TrimSpace(line) == "" {
			if err := end(); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if last == "" {
				return nil, fmt.Errorf("line %d: a continuation line before any field", i+1)
			}
			continue // the rest of a Description: nothing a check reads
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("line %d: not a field: %q", i+1, line)
		}
		if _, dup := fields[name]; dup {
			return nil, fmt.Errorf("line %d: %s twice in one package", i+1, name)
		}
		fields[name] = strings.TrimSpace(value)
		last = name
	}
	if err := end(); err != nil {
		return nil, fmt.Errorf("at the end: %w", err)
	}
	return out, nil
}

// Feed is a feed of opkg's feed list: `src/gz <name> <url>` (or `src`).
type Feed struct {
	Name string
	URL  string
	// Gzip: a src/gz feed, whose index opkg downloads as Packages.gz.
	Gzip bool
}

// FindFeed is the feed called name in an opkg feed list (customfeeds.conf).
func FindFeed(conf []byte, name string) (Feed, bool) {
	for _, line := range strings.Split(string(conf), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || (f[0] != "src" && f[0] != "src/gz") || f[1] != name {
			continue
		}
		return Feed{Name: name, URL: strings.TrimRight(f[2], "/"), Gzip: f[0] == "src/gz"}, true
	}
	return Feed{}, false
}

// IndexURL is the index opkg downloads for the feed.
func (f Feed) IndexURL() string {
	if f.Gzip {
		return f.URL + "/Packages.gz"
	}
	return f.URL + "/Packages"
}

// SignatureURL is the index's signature, Packages.sig.
func (f Feed) SignatureURL() string { return f.URL + "/Packages.sig" }

// Unpack is the index as it was signed: Packages.gz unpacked for a src/gz
// feed. Like opkg-key (zcat || cat), an index a server already unpacked is
// taken as it is — the signature is what decides, not the wrapping. An index
// longer than max is refused.
func (f Feed) Unpack(raw []byte, max int64) ([]byte, error) {
	out := raw
	if f.Gzip && bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("Packages.gz: %w", err)
		}
		if out, err = io.ReadAll(io.LimitReader(zr, max+1)); err != nil {
			return nil, fmt.Errorf("Packages.gz: %w", err)
		}
	}
	if int64(len(out)) > max {
		return nil, fmt.Errorf("the index is longer than %d bytes", max)
	}
	return out, nil
}
