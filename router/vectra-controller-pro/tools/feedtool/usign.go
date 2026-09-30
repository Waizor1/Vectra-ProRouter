package main

// usign-compatible keys and signatures, the format opkg checks a feed with
// (`usign -V -P /etc/opkg/keys -m Packages -x Packages.sig`). The layouts are
// usign's own structs, base64-encoded under an "untrusted comment:" line:
//
//	pubkey  "Ed" | fingerprint[8] | ed25519 public key[32]
//	seckey  "Ed" | "BK" | kdfrounds(u32, 0 = no passphrase) | salt[16] |
//	        checksum[8] = sha512(seckey)[:8] | fingerprint[8] | seed[32] || public[32]
//	sig     "Ed" | fingerprint[8] | ed25519 signature[64]
//
// usign signs the file's bytes as they are (pure Ed25519, RFC 8032), so Go's
// crypto/ed25519 produces the very signature usign would.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const commentPrefix = "untrusted comment: "

var (
	algEd     = [2]byte{'E', 'd'}
	kdfBcrypt = [2]byte{'B', 'K'}
)

type pubKey struct {
	Fingerprint [8]byte
	Key         ed25519.PublicKey
}

type secKey struct {
	Fingerprint [8]byte
	Key         ed25519.PrivateKey // seed || public, as usign keeps it
}

// fingerprintHex is how usign names a key in /etc/opkg/keys ("%016"PRIx64).
func fingerprintHex(fp [8]byte) string { return fmt.Sprintf("%016x", binary.BigEndian.Uint64(fp[:])) }

func generateKey(random io.Reader) (*pubKey, *secKey, error) {
	var fp [8]byte
	if _, err := io.ReadFull(random, fp[:]); err != nil {
		return nil, nil, err
	}
	pub, priv, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, nil, err
	}
	return &pubKey{Fingerprint: fp, Key: pub}, &secKey{Fingerprint: fp, Key: priv}, nil
}

func (k *pubKey) marshal() []byte {
	var b bytes.Buffer
	b.Write(algEd[:])
	b.Write(k.Fingerprint[:])
	b.Write(k.Key)
	return b.Bytes()
}

func (k *secKey) marshal(salt [16]byte) []byte {
	sum := sha512.Sum512(k.Key)
	var b bytes.Buffer
	b.Write(algEd[:])
	b.Write(kdfBcrypt[:])
	_ = binary.Write(&b, binary.BigEndian, uint32(0)) // no passphrase
	b.Write(salt[:])
	b.Write(sum[:8])
	b.Write(k.Fingerprint[:])
	b.Write(k.Key)
	return b.Bytes()
}

// armor writes a usign file: the comment line, then the base64 of the struct.
func armor(comment string, blob []byte) []byte {
	return []byte(commentPrefix + comment + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

// unarmor reads what armor wrote: the comment line must be usign's, the second
// line is the payload.
func unarmor(data []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], commentPrefix) {
		return nil, errors.New("not a usign file: the first line must start with \"untrusted comment: \"")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
}

func parsePubKey(data []byte) (*pubKey, error) {
	blob, err := unarmor(data)
	if err != nil {
		return nil, err
	}
	if len(blob) != 2+8+ed25519.PublicKeySize || !bytes.Equal(blob[:2], algEd[:]) {
		return nil, fmt.Errorf("not an Ed25519 usign public key (%d bytes)", len(blob))
	}
	k := &pubKey{Key: ed25519.PublicKey(append([]byte(nil), blob[10:]...))}
	copy(k.Fingerprint[:], blob[2:10])
	return k, nil
}

func parseSecKey(data []byte) (*secKey, error) {
	blob, err := unarmor(data)
	if err != nil {
		return nil, err
	}
	const size = 2 + 2 + 4 + 16 + 8 + 8 + ed25519.PrivateKeySize
	if len(blob) != size || !bytes.Equal(blob[:2], algEd[:]) || !bytes.Equal(blob[2:4], kdfBcrypt[:]) {
		return nil, fmt.Errorf("not an Ed25519 usign secret key (%d bytes)", len(blob))
	}
	if rounds := binary.BigEndian.Uint32(blob[4:8]); rounds != 0 {
		return nil, errors.New("the secret key is protected by a passphrase; sign with usign itself")
	}
	checksum := blob[24:32]
	k := &secKey{Key: ed25519.PrivateKey(append([]byte(nil), blob[40:]...))}
	copy(k.Fingerprint[:], blob[32:40])
	if sum := sha512.Sum512(k.Key); !bytes.Equal(sum[:8], checksum) {
		return nil, errors.New("the secret key's checksum does not match (a damaged key file)")
	}
	// The public half is stored, not derived: check it matches the seed.
	if !bytes.Equal(ed25519.NewKeyFromSeed(k.Key.Seed()), k.Key) {
		return nil, errors.New("the secret key's public half does not belong to its seed")
	}
	return k, nil
}

func sign(k *secKey, message []byte) []byte {
	var b bytes.Buffer
	b.Write(algEd[:])
	b.Write(k.Fingerprint[:])
	b.Write(ed25519.Sign(k.Key, message))
	return armor("signed by key "+fmt.Sprintf("%x", binary.BigEndian.Uint64(k.Fingerprint[:])), b.Bytes())
}

// verify checks sig over message the way `usign -V` does: the signature must
// name the key's fingerprint and verify under it.
func verify(k *pubKey, message, sig []byte) error {
	blob, err := unarmor(sig)
	if err != nil {
		return err
	}
	if len(blob) != 2+8+ed25519.SignatureSize || !bytes.Equal(blob[:2], algEd[:]) {
		return fmt.Errorf("not an Ed25519 usign signature (%d bytes)", len(blob))
	}
	var fp [8]byte
	copy(fp[:], blob[2:10])
	if fp != k.Fingerprint {
		return fmt.Errorf("signed by key %s, not by %s", fingerprintHex(fp), fingerprintHex(k.Fingerprint))
	}
	if !ed25519.Verify(k.Key, message, blob[10:]) {
		return errors.New("the signature does not match the file")
	}
	return nil
}

func cmdKeygen(args []string) error {
	fs := newFlags("keygen", "-pub FILE -sec FILE [-comment TEXT]")
	pubPath := fs.String("pub", "", "public key file to write")
	secPath := fs.String("sec", "", "secret key file to write (0600)")
	comment := fs.String("comment", "Vectra Pro feed", "comment line of both files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubPath == "" || *secPath == "" {
		return usageError(fs)
	}
	for _, p := range []string{*pubPath, *secPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s exists; a key is never overwritten", p)
		}
	}
	pub, sec, err := generateKey(rand.Reader)
	if err != nil {
		return err
	}
	var salt [16]byte
	if _, err := io.ReadFull(rand.Reader, salt[:]); err != nil {
		return err
	}
	if err := os.WriteFile(*secPath, armor(*comment, sec.marshal(salt)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*pubPath, armor(*comment, pub.marshal()), 0o644); err != nil {
		return err
	}
	fmt.Println(fingerprintHex(pub.Fingerprint))
	return nil
}

func cmdSign(args []string) error {
	fs := newFlags("sign", "-sec FILE -in FILE [-out FILE]")
	secPath := fs.String("sec", "", "secret key")
	in := fs.String("in", "", "file to sign")
	out := fs.String("out", "", "signature file (default: IN.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *secPath == "" || *in == "" {
		return usageError(fs)
	}
	if *out == "" {
		*out = *in + ".sig"
	}
	raw, err := os.ReadFile(*secPath)
	if err != nil {
		return err
	}
	k, err := parseSecKey(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *secPath, err)
	}
	msg, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, sign(k, msg), 0o644)
}

func cmdVerify(args []string) error {
	fs := newFlags("verify", "-pub FILE -in FILE [-sig FILE]")
	pubPath := fs.String("pub", "", "public key")
	in := fs.String("in", "", "signed file")
	sigPath := fs.String("sig", "", "signature (default: IN.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubPath == "" || *in == "" {
		return usageError(fs)
	}
	if *sigPath == "" {
		*sigPath = *in + ".sig"
	}
	raw, err := os.ReadFile(*pubPath)
	if err != nil {
		return err
	}
	k, err := parsePubKey(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *pubPath, err)
	}
	msg, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return err
	}
	if err := verify(k, msg, sig); err != nil {
		return fmt.Errorf("%s: %w", *in, err)
	}
	fmt.Println("OK")
	return nil
}

func cmdFingerprint(args []string) error {
	fs := newFlags("fingerprint", "-pub FILE")
	pubPath := fs.String("pub", "", "public key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubPath == "" {
		return usageError(fs)
	}
	raw, err := os.ReadFile(*pubPath)
	if err != nil {
		return err
	}
	k, err := parsePubKey(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *pubPath, err)
	}
	fmt.Println(fingerprintHex(k.Fingerprint))
	return nil
}
