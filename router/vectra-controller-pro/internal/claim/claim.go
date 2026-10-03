// Package claim is the router's side of linking it to a Vectra account
// (ADR-0006): a one-time nonce that rotates, the short code derived from it,
// and the QR that carries it to Vectra's backend, sealed to Vectra's key and
// signed with the router's own.
//
// Formats (the backend implements the opener against ui/contract/
// claim-vector.json):
//
//	code      = Crockford base32 of SHA-256("vectra-claim-code/v1:" || n)[:5] (8 chars)
//	codeHash  = hex SHA-256("vectra-claim-codehash/v1:" || code)
//	QR        = "VECTRA:R1:" + base64url-nopad(kid(1) || ephPub(32) || gcmNonce(12) || AES-256-GCM(plain))
//	key       = HKDF-SHA256(ikm = X25519(ephPriv, serverPub), salt = ephPub || serverPub,
//	            info = "vectra-router-claim/v1", 32 bytes)
//	AAD       = "VECTRA:R1" || kid
//	plain     = ver(1)=1 | exp(4, unix s, big-endian) | n(16) | pk(32) |
//	            didLen(1) | did | modelLen(1) | model (<= 40 bytes) |
//	            sig(64) = ed25519(device key) over every byte before it
package claim

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Rotation of the nonce: a new one every Period; each stays valid for Grace
// after the next replaced it, so a scan of the code just replaced still works.
// A code is shown for Period and taken for Period+Grace: 30 minutes, the
// expiry the router reports (Nonces.Current) and the panel keeps as it is.
//
// Ten minutes was too short for a code passed on by people: on a router
// migrated from PassWall (2026-10-03) the code went from the router to the
// operator, to the owner, to the person with the app, and expired 50 s
// before it was typed. Half an hour is still safe: a code is 8 characters
// of Crockford base32, 40 bits, and the backend lets about 5 guesses a
// minute through — 150 over a code's life against 2^40 codes, about one
// chance in 7 billion, twice that with the code just replaced still valid.
// A router shows a code only while it is unclaimed, and a claimed one has
// none at all.
const (
	Period = 20 * time.Minute
	Grace  = 10 * time.Minute
)

// NonceSize is n's length in bytes.
const NonceSize = 16

// QRPrefix starts every claim QR; the AAD is it without the trailing colon,
// followed by the key id.
const (
	QRPrefix = "VECTRA:R1:"
	aadLabel = "VECTRA:R1"
	hkdfInfo = "vectra-router-claim/v1"
)

// MaxModel bounds the model string in the QR, in bytes.
const MaxModel = 40

// crockford is Crockford's base32 alphabet: no I, L, O or U.
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// Code is the short code a person types, 8 characters.
func Code(n []byte) string {
	sum := sha256.Sum256(append([]byte("vectra-claim-code/v1:"), n...))
	return crockford.EncodeToString(sum[:5])
}

// CodeHash is what the router tells the panel about the code: never the code.
func CodeHash(code string) string {
	sum := sha256.Sum256([]byte("vectra-claim-codehash/v1:" + code))
	return hex.EncodeToString(sum[:])
}

// Nonces holds the claim nonce and rotates it every period. Safe for use by
// several goroutines.
type Nonces struct {
	period time.Duration
	rand   io.Reader

	mu    sync.Mutex
	cur   []byte
	since time.Time
}

// NewNonces rotates every period (<= 0: Period), drawing from rand.
func NewNonces(period time.Duration, rand io.Reader) *Nonces {
	if period <= 0 {
		period = Period
	}
	return &Nonces{period: period, rand: rand}
}

// Current is the nonce in force at now, minting a new one when there is none
// or it is due, and when it stops being valid.
func (n *Nonces) Current(now time.Time) (nonce []byte, expiresAt time.Time, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cur == nil || !now.Before(n.since.Add(n.period)) {
		fresh := make([]byte, NonceSize)
		if _, err := io.ReadFull(n.rand, fresh); err != nil {
			return nil, time.Time{}, fmt.Errorf("claim: mint a nonce: %w", err)
		}
		n.cur, n.since = fresh, now
	}
	return append([]byte(nil), n.cur...), n.since.Add(n.period + Grace), nil
}

// RegisterMessage is what a register request's proof signs: exactly the
// UTF-8 bytes "vectra-register/v1\n<deviceID>\n<timestamp>", the timestamp in
// unix seconds, base 10.
func RegisterMessage(deviceID string, timestamp int64) []byte {
	return []byte("vectra-register/v1\n" + deviceID + "\n" + strconv.FormatInt(timestamp, 10))
}

// RegisterProof proves the router holds its device key: the ed25519
// signature of RegisterMessage, standard base64.
func RegisterProof(device ed25519.PrivateKey, deviceID string, timestamp int64) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(device, RegisterMessage(deviceID, timestamp)))
}

// Reset drops the nonce in force: the next Current mints a fresh one, so a
// code seen before (by whoever claimed the router last) is no longer the
// router's current code.
func (n *Nonces) Reset() {
	n.mu.Lock()
	n.cur = nil
	n.mu.Unlock()
}

// Key is Vectra's claim key: a raw X25519 public key and its number.
type Key struct {
	Kid       byte
	PublicKey []byte
}

// ParseKey reads a key as the panel sends it (kid, base64 raw X25519).
func ParseKey(kid int, publicKey string) (Key, error) {
	if kid < 0 || kid > 255 {
		return Key{}, fmt.Errorf("claim: key id %d out of range", kid)
	}
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return Key{}, fmt.Errorf("claim: key: %w", err)
	}
	if _, err := ecdh.X25519().NewPublicKey(pub); err != nil {
		return Key{}, fmt.Errorf("claim: key: %w", err)
	}
	return Key{Kid: byte(kid), PublicKey: pub}, nil
}

// Plain is what a QR carries, before the signature.
type Plain struct {
	Exp      time.Time
	Nonce    []byte // NonceSize bytes
	DeviceID string
	Model    string // cut to MaxModel bytes, on a rune boundary
}

// Marshal lays out the plaintext and signs it with the device key.
func (p Plain) Marshal(device ed25519.PrivateKey) ([]byte, error) {
	if len(p.Nonce) != NonceSize {
		return nil, fmt.Errorf("claim: nonce is %d bytes, want %d", len(p.Nonce), NonceSize)
	}
	if len(p.DeviceID) == 0 || len(p.DeviceID) > 255 {
		return nil, fmt.Errorf("claim: device id of %d bytes", len(p.DeviceID))
	}
	if len(device) != ed25519.PrivateKeySize {
		return nil, errors.New("claim: no device key")
	}
	model := cutModel(p.Model)
	out := []byte{1}
	out = binary.BigEndian.AppendUint32(out, uint32(p.Exp.Unix()))
	out = append(out, p.Nonce...)
	out = append(out, device.Public().(ed25519.PublicKey)...)
	out = append(out, byte(len(p.DeviceID)))
	out = append(out, p.DeviceID...)
	out = append(out, byte(len(model)))
	out = append(out, model...)
	return append(out, ed25519.Sign(device, out)...), nil
}

func cutModel(m string) string {
	if len(m) <= MaxModel {
		return m
	}
	m = m[:MaxModel]
	for len(m) > 0 && !utf8.ValidString(m) {
		m = m[:len(m)-1]
	}
	return m
}

// Seal builds the QR string for p with the given ephemeral key and GCM nonce.
func Seal(p Plain, device ed25519.PrivateKey, key Key, eph *ecdh.PrivateKey, gcmNonce []byte) (string, error) {
	plain, err := p.Marshal(device)
	if err != nil {
		return "", err
	}
	server, err := ecdh.X25519().NewPublicKey(key.PublicKey)
	if err != nil {
		return "", fmt.Errorf("claim: key: %w", err)
	}
	shared, err := eph.ECDH(server)
	if err != nil {
		return "", fmt.Errorf("claim: x25519: %w", err)
	}
	ephPub := eph.PublicKey().Bytes()
	aead, err := newAEAD(DeriveKey(shared, ephPub, key.PublicKey))
	if err != nil {
		return "", err
	}
	if len(gcmNonce) != aead.NonceSize() {
		return "", fmt.Errorf("claim: GCM nonce of %d bytes", len(gcmNonce))
	}
	env := []byte{key.Kid}
	env = append(env, ephPub...)
	env = append(env, gcmNonce...)
	env = aead.Seal(env, gcmNonce, plain, AAD(key.Kid))
	return QRPrefix + base64.RawURLEncoding.EncodeToString(env), nil
}

// SealStable builds the QR for p with an ephemeral key and GCM nonce derived
// from the device key, the Vectra key and the nonce and expiry: the same code
// gives the same QR on every poll, a new code (or a new Vectra key) a new one,
// and nobody without the device key can predict either.
func SealStable(p Plain, device ed25519.PrivateKey, key Key) (string, error) {
	if len(device) != ed25519.PrivateKeySize {
		return "", errors.New("claim: no device key")
	}
	msg := []byte{key.Kid}
	msg = append(msg, key.PublicKey...)
	msg = append(msg, p.Nonce...)
	msg = binary.BigEndian.AppendUint32(msg, uint32(p.Exp.Unix()))
	derive := func(label string) []byte {
		m := hmac.New(sha256.New, device.Seed())
		m.Write([]byte(label))
		m.Write(msg)
		return m.Sum(nil)
	}
	eph, err := ecdh.X25519().NewPrivateKey(derive("vectra-claim-eph/v1:"))
	if err != nil {
		return "", err
	}
	return Seal(p, device, key, eph, derive("vectra-claim-gcm/v1:")[:12])
}

// DeriveKey is the envelope key: HKDF-SHA256 (RFC 5869) with salt ephPub ||
// serverPub and info "vectra-router-claim/v1", 32 bytes.
func DeriveKey(shared, ephPub, serverPub []byte) []byte {
	salt := append(append([]byte{}, ephPub...), serverPub...)
	extract := hmac.New(sha256.New, salt)
	extract.Write(shared)
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	expand.Write([]byte(hkdfInfo))
	expand.Write([]byte{1})
	return expand.Sum(nil) // one block: exactly 32 bytes
}

// AAD authenticates the format and the key id.
func AAD(kid byte) []byte { return append([]byte(aadLabel), kid) }

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// DeviceKey reads the router's ed25519 key as state.json keeps it (base64 of
// the 64-byte private key, or of a 32-byte seed).
func DeviceKey(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("claim: device key: %w", err)
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	}
	return nil, fmt.Errorf("claim: device key of %d bytes", len(raw))
}
