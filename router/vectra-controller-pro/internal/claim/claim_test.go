package claim

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opened is what Vectra's backend reads out of a QR.
type opened struct {
	Kid      byte
	Exp      time.Time
	Nonce    []byte
	PubKey   ed25519.PublicKey
	DeviceID string
	Model    string
	Plain    []byte
}

// open is the backend's side, written from the format alone: it is what the
// vector below lets a second implementation check itself against.
func open(qr string, serverPriv *ecdh.PrivateKey) (*opened, error) {
	rest, ok := strings.CutPrefix(qr, QRPrefix)
	if !ok {
		return nil, errors.New("not a VECTRA:R1 code")
	}
	env, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, err
	}
	if len(env) < 1+32+12+16 {
		return nil, errors.New("short")
	}
	kid, ephPub, nonce, sealed := env[0], env[1:33], env[33:45], env[45:]
	eph, err := ecdh.X25519().NewPublicKey(ephPub)
	if err != nil {
		return nil, err
	}
	shared, err := serverPriv.ECDH(eph)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(DeriveKey(shared, ephPub, serverPriv.PublicKey().Bytes()))
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, sealed, AAD(kid))
	if err != nil {
		return nil, err
	}
	// ver | exp | n | pk | didLen | did | modelLen | model | sig
	if len(plain) < 1+4+16+32+1+1+64 || plain[0] != 1 {
		return nil, errors.New("bad plaintext")
	}
	o := &opened{Kid: kid, Plain: plain}
	o.Exp = time.Unix(int64(binary.BigEndian.Uint32(plain[1:5])), 0).UTC()
	o.Nonce, o.PubKey = plain[5:21], ed25519.PublicKey(plain[21:53])
	i := 53
	take := func() (string, error) {
		if i >= len(plain) || i+1+int(plain[i]) > len(plain)-64 {
			return "", errors.New("bad length")
		}
		s := string(plain[i+1 : i+1+int(plain[i])])
		i += 1 + int(plain[i])
		return s, nil
	}
	if o.DeviceID, err = take(); err != nil {
		return nil, err
	}
	if o.Model, err = take(); err != nil {
		return nil, err
	}
	if i != len(plain)-64 {
		return nil, errors.New("trailing bytes")
	}
	if !ed25519.Verify(o.PubKey, plain[:i], plain[i:]) {
		return nil, errors.New("bad signature")
	}
	return o, nil
}

func fixed(label string) []byte {
	s := sha256.Sum256([]byte("vectra-claim-vector/" + label))
	return s[:]
}

// vector is ui/contract/claim-vector.json. Every binary value is standard
// base64.
type vector struct {
	Format string `json:"format"`
	Server struct {
		Kid        int    `json:"kid"`
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	} `json:"server"`
	Ephemeral struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	} `json:"ephemeral"`
	GCMNonce string `json:"gcmNonce"`
	Device   struct {
		Seed             string `json:"seed"`
		PublicKey        string `json:"publicKey"`
		DeviceIdentifier string `json:"deviceIdentifier"`
		Model            string `json:"model"`
	} `json:"device"`
	Nonce        string `json:"nonce"`
	Exp          int64  `json:"exp"`
	Code         string `json:"code"`
	CodeHash     string `json:"codeHash"`
	SharedSecret string `json:"sharedSecret"`
	Key          string `json:"key"`
	AAD          string `json:"aad"`
	Plaintext    string `json:"plaintext"`
	Signature    string `json:"signature"`
	QR           string `json:"qr"`
}

const vectorPath = "../../ui/contract/claim-vector.json"

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// buildVector computes every value of the vector from its fixed inputs.
func buildVector(t *testing.T) vector {
	t.Helper()
	server, _ := ecdh.X25519().NewPrivateKey(fixed("server"))
	eph, _ := ecdh.X25519().NewPrivateKey(fixed("ephemeral"))
	device := ed25519.NewKeyFromSeed(fixed("device"))
	var v vector
	v.Format = "VECTRA:R1 — see internal/claim/claim.go and the Setup wizard section of ui/contract/README.md. Every binary value here is standard base64; exp is unix seconds."
	v.Server.Kid, v.Server.PrivateKey, v.Server.PublicKey = 1, b64(server.Bytes()), b64(server.PublicKey().Bytes())
	v.Ephemeral.PrivateKey, v.Ephemeral.PublicKey = b64(eph.Bytes()), b64(eph.PublicKey().Bytes())
	v.GCMNonce = b64(fixed("gcm")[:12])
	v.Device.Seed, v.Device.PublicKey = b64(device.Seed()), b64(device.Public().(ed25519.PublicKey))
	v.Device.DeviceIdentifier, v.Device.Model = "vectra-07bf0887f662", "Xiaomi Mi Router AX3000T"
	n := fixed("nonce")[:NonceSize]
	v.Nonce, v.Exp = b64(n), 1790579520
	v.Code = Code(n)
	v.CodeHash = CodeHash(v.Code)
	shared, err := eph.ECDH(server.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	v.SharedSecret = b64(shared)
	v.Key = b64(DeriveKey(shared, eph.PublicKey().Bytes(), server.PublicKey().Bytes()))
	v.AAD = b64(AAD(1))
	p := Plain{Exp: time.Unix(v.Exp, 0), Nonce: n, DeviceID: v.Device.DeviceIdentifier, Model: v.Device.Model}
	plain, err := p.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	v.Plaintext, v.Signature = b64(plain), b64(plain[len(plain)-64:])
	key := Key{Kid: 1, PublicKey: server.PublicKey().Bytes()}
	if v.QR, err = Seal(p, device, key, eph, fixed("gcm")[:12]); err != nil {
		t.Fatal(err)
	}
	return v
}

// The vector is the contract with Vectra's backend: the router's sealer must
// produce exactly this QR from these inputs, and an opener written from the
// format alone must read these fields back out of it. Regenerate (only after a
// deliberate format change, with a new format version) with
//
//	CLAIM_VECTOR_UPDATE=1 go test ./internal/claim -run Vector
func TestTheClaimVectorSealsAndOpens(t *testing.T) {
	want := buildVector(t)
	if os.Getenv("CLAIM_VECTOR_UPDATE") == "1" {
		out, _ := json.MarshalIndent(want, "", "  ")
		if err := os.WriteFile(filepath.Clean(vectorPath), append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", vectorPath)
		return
	}
	raw, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatalf("%v (regenerate with CLAIM_VECTOR_UPDATE=1)", err)
	}
	var got vector
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("the committed vector is not what the router computes:\n got  %+v\n want %+v", got, want)
	}

	// Open it as the backend would, from the file's values alone.
	priv, _ := base64.StdEncoding.DecodeString(got.Server.PrivateKey)
	server, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	o, err := open(got.QR, server)
	if err != nil {
		t.Fatalf("the vector's QR does not open: %v", err)
	}
	n, _ := base64.StdEncoding.DecodeString(got.Nonce)
	pk, _ := base64.StdEncoding.DecodeString(got.Device.PublicKey)
	if o.Kid != 1 || o.Exp.Unix() != got.Exp || !bytes.Equal(o.Nonce, n) || !bytes.Equal(o.PubKey, pk) ||
		o.DeviceID != got.Device.DeviceIdentifier || o.Model != got.Device.Model || b64(o.Plain) != got.Plaintext {
		t.Fatalf("opened %+v", o)
	}
	if Code(o.Nonce) != got.Code || CodeHash(got.Code) != got.CodeHash {
		t.Fatal("the code does not follow from the opened nonce")
	}
}

func TestCodeIsEightCrockfordCharacters(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := Code(fixed(string(rune('a'+i%26)) + string(rune('0'+i/26)))[:NonceSize])
		if len(c) != 8 || strings.Trim(c, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 199 {
		t.Fatalf("%d distinct codes out of 200", len(seen))
	}
	if CodeHash("7KQ4M9XD") == CodeHash("7KQ4M9XE") || len(CodeHash("7KQ4M9XD")) != 64 {
		t.Fatal("code hash")
	}
}

func TestNoncesRotateAndStayValidForTheGrace(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat(fixed("rand"), 8))
	n := NewNonces(10*time.Minute, src)
	t0 := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	a, expA, err := n.Current(t0)
	if err != nil {
		t.Fatal(err)
	}
	if !expA.Equal(t0.Add(10*time.Minute + Grace)) {
		t.Errorf("expiresAt = %s, want the 10 minutes plus the grace", expA)
	}
	b, expB, _ := n.Current(t0.Add(10*time.Minute - time.Second))
	if !bytes.Equal(a, b) || !expB.Equal(expA) {
		t.Fatal("the nonce changed before its period")
	}
	c, expC, _ := n.Current(t0.Add(10 * time.Minute))
	if bytes.Equal(a, c) || !expC.Equal(t0.Add(20*time.Minute+Grace)) {
		t.Fatalf("no rotation at the period: expiresAt %s", expC)
	}
	c[0] ^= 0xff // the caller's copy is its own
	if d, _, _ := n.Current(t0.Add(10 * time.Minute)); d[0] == c[0] {
		t.Fatal("Current handed out its own slice")
	}
	if _, _, err := NewNonces(0, bytes.NewReader(nil)).Current(t0); err == nil {
		t.Fatal("minted a nonce from an empty source")
	}
}

// A code is shown for 20 minutes and taken for 30: long enough to be passed
// from the router through two people to the app (claim.go, Period).
func TestACodeIsTakenForHalfAnHourByDefault(t *testing.T) {
	n := NewNonces(0, bytes.NewReader(bytes.Repeat(fixed("rand"), 4)))
	t0 := time.Date(2026, 10, 3, 13, 47, 0, 0, time.UTC)
	a, exp, err := n.Current(t0)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("expiresAt %s, want half an hour after the code was minted", exp)
	}
	if b, _, _ := n.Current(t0.Add(20*time.Minute - time.Second)); !bytes.Equal(a, b) {
		t.Fatal("the code changed before 20 minutes")
	}
	if b, _, _ := n.Current(t0.Add(20 * time.Minute)); bytes.Equal(a, b) {
		t.Fatal("the code was not replaced after 20 minutes")
	}
}

// The register proof signs exactly these bytes, and a second implementation
// (the panel) must land on this very signature: ed25519 is deterministic.
// Cross-checked with Python's cryptography.
func TestTheRegisterProofSignsExactlyTheseBytes(t *testing.T) {
	const (
		deviceID = "vectra-07bf0887f662"
		ts       = int64(1790579520)
		pub      = "0vE9iJroHQmUD9l8bN8ru0UmOPuOOf3nfCfhWdi3sJY="
		sig      = "MZ3D9cCsAGO9sQef6Y/4rd5GDj31RWS+pHX2J+2BJE5FpaVuFZ1MNpYCoiHrP61R5Vsls9XWR38FeyxipBfYBw=="
	)
	if got := string(RegisterMessage(deviceID, ts)); got != "vectra-register/v1\nvectra-07bf0887f662\n1790579520" {
		t.Fatalf("message = %q", got)
	}
	key := ed25519.NewKeyFromSeed(fixed("register"))
	if got := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)); got != pub {
		t.Fatalf("public key = %s", got)
	}
	got := RegisterProof(key, deviceID, ts)
	if got != sig {
		t.Fatalf("signature = %s, want %s", got, sig)
	}
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil || !ed25519.Verify(key.Public().(ed25519.PublicKey), RegisterMessage(deviceID, ts), raw) {
		t.Fatal("the signature does not verify with the public key")
	}
	for _, other := range [][]byte{RegisterMessage(deviceID, ts+1), RegisterMessage("vectra-000000000000", ts),
		[]byte("vectra-register/v1\nvectra-07bf0887f662\n1790579520\n")} {
		if ed25519.Verify(key.Public().(ed25519.PublicKey), other, raw) {
			t.Fatalf("the signature also verifies over %q", other)
		}
	}
}

// A released router starts over: the code its last owner saw is not its
// code any more, well before that code's period would have ended.
func TestResetMintsAFreshNonceAtOnce(t *testing.T) {
	n := NewNonces(10*time.Minute, bytes.NewReader(append(fixed("first"), fixed("second")...)))
	t0 := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	a, _, err := n.Current(t0)
	if err != nil {
		t.Fatal(err)
	}
	n.Reset()
	b, exp, err := n.Current(t0.Add(time.Minute))
	if err != nil || bytes.Equal(a, b) || !exp.Equal(t0.Add(11*time.Minute+Grace)) {
		t.Fatalf("after Reset: the same nonce=%v, expiresAt %s, err %v", bytes.Equal(a, b), exp, err)
	}
}

func TestSealStableIsOneQRPerCode(t *testing.T) {
	server, _ := ecdh.X25519().NewPrivateKey(fixed("server"))
	key := Key{Kid: 3, PublicKey: server.PublicKey().Bytes()}
	device := ed25519.NewKeyFromSeed(fixed("device"))
	p := Plain{Exp: time.Unix(1790000000, 0), Nonce: fixed("n1")[:16], DeviceID: "vectra-000000000001", Model: "Cudy WR3000E"}
	q1, err := SealStable(p, device, key)
	if err != nil {
		t.Fatal(err)
	}
	if q2, _ := SealStable(p, device, key); q2 != q1 {
		t.Fatal("the same code sealed twice gave two QRs")
	}
	p2 := p
	p2.Nonce = fixed("n2")[:16]
	if q3, _ := SealStable(p2, device, key); q3 == q1 {
		t.Fatal("a new code kept the old QR")
	}
	o, err := open(q1, server)
	if err != nil || o.Kid != 3 || o.Model != "Cudy WR3000E" {
		t.Fatalf("opened %+v, %v", o, err)
	}
}

func TestATamperedQRDoesNotOpen(t *testing.T) {
	v := buildVector(t)
	server, _ := ecdh.X25519().NewPrivateKey(fixed("server"))
	env, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v.QR, QRPrefix))
	for name, mutate := range map[string]func([]byte){
		"the key id":     func(e []byte) { e[0] = 2 },
		"the ciphertext": func(e []byte) { e[len(e)-20] ^= 1 },
		"the nonce":      func(e []byte) { e[40] ^= 1 },
	} {
		e := append([]byte(nil), env...)
		mutate(e)
		if _, err := open(QRPrefix+base64.RawURLEncoding.EncodeToString(e), server); err == nil {
			t.Errorf("%s changed and the QR still opened", name)
		}
	}
	other, _ := ecdh.X25519().NewPrivateKey(fixed("not-vectra"))
	if _, err := open(v.QR, other); err == nil {
		t.Error("opened with another key")
	}
}

func TestPlainIsCheckedAndTheModelCutOnARune(t *testing.T) {
	device := ed25519.NewKeyFromSeed(fixed("device"))
	p := Plain{Exp: time.Unix(1, 0), Nonce: fixed("n")[:16], DeviceID: "vectra-1",
		Model: strings.Repeat("я", 30)} // 60 bytes
	plain, err := p.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	// ver 1 | exp 4 | n 16 | pk 32 | 1 | did 8 | 1 | model | sig 64
	if model := plain[1+4+16+32+1+8+1 : len(plain)-64]; len(model) != 40 || string(model) != strings.Repeat("я", 20) {
		t.Fatalf("model = %q (%d bytes)", model, len(model))
	}
	for name, bad := range map[string]Plain{
		"a short nonce":    {Nonce: []byte{1}, DeviceID: "d"},
		"no device id":     {Nonce: fixed("n")[:16]},
		"a long device id": {Nonce: fixed("n")[:16], DeviceID: strings.Repeat("d", 256)},
	} {
		if _, err := bad.Marshal(device); err == nil {
			t.Errorf("%s: marshalled", name)
		}
	}
	if _, err := p.Marshal(nil); err == nil {
		t.Error("marshalled without a device key")
	}
}

func TestKeysAreReadAsTheyAreSent(t *testing.T) {
	server, _ := ecdh.X25519().NewPrivateKey(fixed("server"))
	k, err := ParseKey(1, b64(server.PublicKey().Bytes()))
	if err != nil || k.Kid != 1 || !bytes.Equal(k.PublicKey, server.PublicKey().Bytes()) {
		t.Fatalf("%+v %v", k, err)
	}
	for name, tc := range map[string]struct {
		kid int
		pub string
	}{
		"a kid past a byte": {256, b64(server.PublicKey().Bytes())},
		"not base64":        {1, "!!"},
		"a short key":       {1, b64([]byte{1, 2, 3})},
	} {
		if _, err := ParseKey(tc.kid, tc.pub); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	device := ed25519.NewKeyFromSeed(fixed("device"))
	for _, form := range []string{b64(device), b64(device.Seed())} {
		if k, err := DeviceKey(form); err != nil || !bytes.Equal(k, device) {
			t.Errorf("device key %d bytes: %v", len(form), err)
		}
	}
	if _, err := DeviceKey(b64([]byte{1})); err == nil {
		t.Error("a 1-byte device key read")
	}
}
