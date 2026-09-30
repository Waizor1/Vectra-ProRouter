// Package uatoken makes the User-Agent a router fetches its subscription with:
//
//	VectraRouter/<version> vr1.<token>
//
// Only our software can make one: the token is signed with the router's own
// device key (ed25519), bound to this request — the time, the router's HWID
// and the subscription it asks for — and sealed to Vectra's key, so to anyone
// else it is noise that cannot be replayed on another subscription or device.
// The subscription backend opens it (Open is the reference; the contract is
// ui/contract/ua-vector.json) with the device key it learned when the router
// was linked to the account: the key never travels in the token.
//
// Short on purpose: the whole agent stays under 255 bytes (a column that size
// keeps it at the backend). The model is not in it — x-device-model says it.
//
//	token   = base64url-nopad(kid(1) || ephPub(32) || nonce(12) || AES-256-GCM(key, nonce, plain, AAD))
//	key     = HKDF-SHA256(ikm = X25519(ephPriv, serverPub), salt = ephPub || serverPub,
//	          info = "vectra-router-ua/v1", 32 bytes)
//	AAD     = "VECTRA:UA1" || kid
//	plain   = ver(1)=1 | ts(4, unix s, big-endian) | didLen(1) | did | sig(64)
//	sig     = ed25519(device key) over "vectra-router-ua/v1" || 0x00 || ts(4) || pk(32) ||
//	          didLen(1) || did || hwid(32) || reqHash(32)
//	pk      = the device's public key (bound by the signature, not sent)
//	hwid    = the x-hwid header, hex-decoded (32 bytes)
//	reqHash = SHA-256 of the request target: the URL's escaped path, plus "?" and
//	          the raw query when there is one (the host is left out: a backend
//	          behind a proxy may not see the one the router dialled)
//
// The key is the one the router seals its claim QR to (check-in's claimKey),
// with its own HKDF info and AAD, so a token can never pass for a claim or a
// claim for a token.
package uatoken

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	// Product is the User-Agent's product token.
	Product = "VectraRouter"
	// Scheme prefixes the token inside the User-Agent.
	Scheme = "vr1."

	version  = 1
	hkdfInfo = "vectra-router-ua/v1"
	aadLabel = "VECTRA:UA1"
	sigLabel = "vectra-router-ua/v1"

	// MaxDeviceID and MaxVersion keep the whole agent within MaxLen: with both
	// at their limit it is 252 bytes (vctl's ids are 19: "vectra-" + 12 hex).
	MaxDeviceID = 32
	MaxVersion  = 16
	// MaxLen is the longest agent: a column that size keeps it at the backend.
	MaxLen = 255

	// BuiltinKid and BuiltinKey are Vectra's key a router seals to when the
	// panel has named none in check-in: key 1, raw X25519, standard base64.
	// Its private half is held by Vectra Connect's backend, never by a router
	// or this repository. A key the panel names (claimKey) takes precedence,
	// which is how a new one is rolled out.
	BuiltinKid = 1
	BuiltinKey = "77QrxchvI9IzwhHFMVBLuGSk76IhwTovmWI5HKiBgXM="
)

// Request is everything a token binds.
type Request struct {
	Version   string             // vctl's version, e.g. 0.5.0-r1
	DeviceID  string             // the router's device identifier (the panel knows it)
	DeviceKey ed25519.PrivateKey // the router's own key (state.json)
	HWID      string             // the x-hwid header: hex SHA-256
	URL       string             // the subscription URL
	Kid       byte               // Vectra's key id
	ServerPub []byte             // Vectra's X25519 public key, 32 bytes
	Now       time.Time
}

// UserAgent seals a fresh token for r. rand supplies the ephemeral key and the
// nonce (crypto/rand.Reader outside tests).
func UserAgent(r Request, rand io.Reader) (string, error) {
	if len(r.DeviceKey) != ed25519.PrivateKeySize {
		return "", errors.New("uatoken: no device key")
	}
	if r.DeviceID == "" || len(r.DeviceID) > MaxDeviceID {
		return "", fmt.Errorf("uatoken: device id of %d bytes", len(r.DeviceID))
	}
	hwid, err := hwidBytes(r.HWID)
	if err != nil {
		return "", err
	}
	target, err := RequestTarget(r.URL)
	if err != nil {
		return "", err
	}
	server, err := ecdh.X25519().NewPublicKey(r.ServerPub)
	if err != nil {
		return "", fmt.Errorf("uatoken: Vectra's key: %w", err)
	}
	ts := r.Now.Unix()
	if ts <= 0 || ts > 0xFFFFFFFF {
		return "", errors.New("uatoken: the clock is not set")
	}
	pk := r.DeviceKey.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(r.DeviceKey, signed(uint32(ts), pk, r.DeviceID, hwid, target))

	plain := make([]byte, 0, 1+4+1+len(r.DeviceID)+64)
	plain = append(plain, version)
	plain = binary.BigEndian.AppendUint32(plain, uint32(ts))
	plain = append(plain, byte(len(r.DeviceID)))
	plain = append(plain, r.DeviceID...)
	plain = append(plain, sig...)

	// 32 bytes read, not ecdh's GenerateKey (which reads a random extra byte
	// on purpose): the same reader gives the same token, which the contract's
	// test vector needs.
	seed := make([]byte, 32)
	if _, err := io.ReadFull(rand, seed); err != nil {
		return "", err
	}
	eph, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand, nonce); err != nil {
		return "", err
	}
	sealed, err := seal(plain, r.Kid, eph, server, nonce)
	if err != nil {
		return "", err
	}
	ua := Product + "/" + clean(r.Version, MaxVersion) + " " + Scheme + sealed
	if len(ua) > MaxLen {
		return "", fmt.Errorf("uatoken: the agent would be %d bytes, over %d", len(ua), MaxLen)
	}
	return ua, nil
}

func seal(plain []byte, kid byte, eph *ecdh.PrivateKey, server *ecdh.PublicKey, nonce []byte) (string, error) {
	shared, err := eph.ECDH(server)
	if err != nil {
		return "", err
	}
	aead, err := newAEAD(deriveKey(shared, eph.PublicKey().Bytes(), server.Bytes()))
	if err != nil {
		return "", err
	}
	out := []byte{kid}
	out = append(out, eph.PublicKey().Bytes()...)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plain, aad(kid))
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// Opened is what a valid token says.
type Opened struct {
	Kid       byte
	Time      time.Time
	DeviceID  string
	DeviceKey ed25519.PublicKey
}

// Keys finds Vectra's private key by its number.
type Keys func(kid byte) (*ecdh.PrivateKey, bool)

// DeviceKeys finds the public key of a router linked to the subscription's
// account, by its device id; a router that is not linked there has none.
type DeviceKeys func(deviceID string) (ed25519.PublicKey, bool)

// Open checks a User-Agent the way the subscription backend must: the token
// opens under Vectra's key, names a router linked to this account, its
// signature holds under that router's key for this request (the x-hwid header
// and the request target the backend received), and its time is within
// maxSkew of now.
func Open(userAgent string, keys Keys, devices DeviceKeys, hwidHeader, requestTarget string, now time.Time, maxSkew time.Duration) (*Opened, error) {
	i := strings.LastIndex(userAgent, " "+Scheme)
	if !strings.HasPrefix(userAgent, Product+"/") || i < 0 {
		return nil, errors.New("uatoken: not a Vectra router User-Agent")
	}
	raw, err := base64.RawURLEncoding.DecodeString(userAgent[i+1+len(Scheme):])
	if err != nil {
		return nil, fmt.Errorf("uatoken: token: %w", err)
	}
	if len(raw) < 1+32+12+16 {
		return nil, errors.New("uatoken: token too short")
	}
	kid := raw[0]
	priv, ok := keys(kid)
	if !ok {
		return nil, fmt.Errorf("uatoken: no key %d", kid)
	}
	eph, err := ecdh.X25519().NewPublicKey(raw[1:33])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(eph)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(deriveKey(shared, raw[1:33], priv.PublicKey().Bytes()))
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, raw[33:45], raw[45:], aad(kid))
	if err != nil {
		return nil, errors.New("uatoken: the token does not open under Vectra's key")
	}
	if len(plain) < 1+4+1 || plain[0] != version {
		return nil, errors.New("uatoken: unknown token version")
	}
	ts := binary.BigEndian.Uint32(plain[1:5])
	n := int(plain[5])
	if len(plain) != 6+n+ed25519.SignatureSize || n == 0 {
		return nil, errors.New("uatoken: damaged token")
	}
	did := string(plain[6 : 6+n])
	pk, ok := devices(did)
	if !ok || len(pk) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("uatoken: %s is not a router linked to this account", did)
	}
	hwid, err := hwidBytes(hwidHeader)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pk, signed(ts, pk, did, hwid, requestTarget), plain[6+n:]) {
		return nil, errors.New("uatoken: the signature does not hold for this device and subscription")
	}
	at := time.Unix(int64(ts), 0).UTC()
	if d := now.Sub(at); d > maxSkew || d < -maxSkew {
		return nil, fmt.Errorf("uatoken: made at %s, %s away from now", at.Format(time.RFC3339), d.Round(time.Second))
	}
	return &Opened{Kid: kid, Time: at, DeviceID: did, DeviceKey: pk}, nil
}

// RequestTarget is what reqHash covers: the escaped path and the raw query.
func RequestTarget(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("uatoken: the subscription URL must be https://host/…")
	}
	t := u.EscapedPath()
	if t == "" {
		t = "/"
	}
	if u.RawQuery != "" {
		t += "?" + u.RawQuery
	}
	return t, nil
}

func signed(ts uint32, pk ed25519.PublicKey, did string, hwid []byte, target string) []byte {
	sum := sha256.Sum256([]byte(target))
	m := append([]byte(sigLabel), 0)
	m = binary.BigEndian.AppendUint32(m, ts)
	m = append(m, pk...)
	m = append(m, byte(len(did)))
	m = append(m, did...)
	m = append(m, hwid...)
	return append(m, sum[:]...)
}

func hwidBytes(h string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil || len(b) != sha256.Size {
		return nil, errors.New("uatoken: x-hwid must be a hex SHA-256")
	}
	return b, nil
}

// deriveKey is HKDF-SHA256 (RFC 5869), one block: 32 bytes.
func deriveKey(shared, ephPub, serverPub []byte) []byte {
	salt := append(append([]byte{}, ephPub...), serverPub...)
	extract := hmac.New(sha256.New, salt)
	extract.Write(shared)
	expand := hmac.New(sha256.New, extract.Sum(nil))
	expand.Write([]byte(hkdfInfo))
	expand.Write([]byte{1})
	return expand.Sum(nil)
}

func aad(kid byte) []byte { return append([]byte(aadLabel), kid) }

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// clean keeps a User-Agent comment printable ASCII without parentheses.
func clean(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '(' || r == ')' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) > max {
		out = strings.TrimSpace(out[:max])
	}
	if out == "" {
		out = "unknown"
	}
	return out
}
