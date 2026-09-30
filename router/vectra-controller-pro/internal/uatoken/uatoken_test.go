package uatoken

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// vector is ui/contract/ua-vector.json: fixed keys, ephemeral seed and nonce,
// so the subscription backend can check its opener against exactly this UA.
type vector struct {
	Note             string `json:"note"`
	ServerPrivateKey string `json:"serverPrivateKey"`
	ServerPublicKey  string `json:"serverPublicKey"`
	Kid              int    `json:"kid"`
	DeviceSeed       string `json:"deviceSeed"`
	DevicePublicKey  string `json:"devicePublicKey"`
	DeviceID         string `json:"deviceId"`
	Version          string `json:"version"`
	HWID             string `json:"hwid"`
	URL              string `json:"url"`
	RequestTarget    string `json:"requestTarget"`
	Now              int64  `json:"now"`
	EphemeralSeed    string `json:"ephemeralSeed"`
	Nonce            string `json:"nonce"`
	UserAgent        string `json:"userAgent"`
}

const vectorPath = "../../ui/contract/ua-vector.json"

func b64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func fixed() (vector, Request, *ecdh.PrivateKey) {
	serverSeed := sha256.Sum256([]byte("ua-vector server"))
	server, _ := ecdh.X25519().NewPrivateKey(serverSeed[:])
	devSeed := sha256.Sum256([]byte("ua-vector device"))
	dev := ed25519.NewKeyFromSeed(devSeed[:])
	hw := sha256.Sum256([]byte("aa:bb:cc:dd:ee:ff-Xiaomi Mi Router AX3000T"))
	eph := sha256.Sum256([]byte("ua-vector ephemeral"))
	nonce := sha256.Sum256([]byte("ua-vector nonce"))
	v := vector{
		Note:             "Test vector for the router User-Agent token (internal/uatoken). Throwaway keys: nothing trusts them.",
		ServerPrivateKey: base64.StdEncoding.EncodeToString(server.Bytes()),
		ServerPublicKey:  base64.StdEncoding.EncodeToString(server.PublicKey().Bytes()),
		Kid:              1,
		DeviceSeed:       base64.StdEncoding.EncodeToString(devSeed[:]),
		DevicePublicKey:  base64.StdEncoding.EncodeToString(dev.Public().(ed25519.PublicKey)),
		DeviceID:         "vectra-3f9a2c7e11d4",
		Version:          "0.5.0-r1",
		HWID:             hex.EncodeToString(hw[:]),
		URL:              "https://sub.example.invalid/api/sub/AbCdEf123",
		RequestTarget:    "/api/sub/AbCdEf123",
		Now:              1790000000,
		EphemeralSeed:    base64.StdEncoding.EncodeToString(eph[:]),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:12]),
	}
	r := Request{
		Version: v.Version, DeviceID: v.DeviceID, DeviceKey: dev,
		HWID: v.HWID, URL: v.URL, Kid: 1, ServerPub: server.PublicKey().Bytes(), Now: time.Unix(v.Now, 0),
	}
	return v, r, server
}

func rng(v vector) *bytes.Reader {
	return bytes.NewReader(append(b64(v.EphemeralSeed), b64(v.Nonce)...))
}

func keysOf(server *ecdh.PrivateKey) Keys {
	return func(kid byte) (*ecdh.PrivateKey, bool) { return server, kid == 1 }
}

// linked is the backend's view: the routers linked to the subscription's account.
func linked(v vector) DeviceKeys {
	return func(did string) (ed25519.PublicKey, bool) {
		if did != v.DeviceID {
			return nil, false
		}
		return ed25519.PublicKey(b64(v.DevicePublicKey)), true
	}
}

// The committed vector is exactly what the code makes; UPDATE_VECTOR=1 rewrites it.
func TestContractVector(t *testing.T) {
	v, r, server := fixed()
	ua, err := UserAgent(r, rng(v))
	if err != nil {
		t.Fatal(err)
	}
	v.UserAgent = ua
	want, _ := json.MarshalIndent(v, "", "  ")
	want = append(want, '\n')
	if os.Getenv("UPDATE_VECTOR") == "1" {
		if err := os.WriteFile(vectorPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatalf("%v (UPDATE_VECTOR=1 writes it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ua-vector.json is not what the code makes; UPDATE_VECTOR=1 rewrites it deliberately\ngot:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasPrefix(ua, "VectraRouter/0.5.0-r1 vr1.") || len(ua) > 255 {
		t.Fatalf("UA %q (%d bytes)", ua, len(ua))
	}
	o, err := Open(ua, keysOf(server), linked(v), v.HWID, v.RequestTarget, time.Unix(v.Now+30, 0), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if o.DeviceID != v.DeviceID || o.Time.Unix() != v.Now || o.Kid != 1 {
		t.Fatalf("opened %+v", o)
	}
}

func TestOpenRefuses(t *testing.T) {
	v, r, server := fixed()
	ua, err := UserAgent(r, rng(v))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(v.Now, 0)
	other := sha256.Sum256([]byte("another router"))
	otherServer, _ := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{9}, 32))
	tok := ua[strings.LastIndex(ua, Scheme)+len(Scheme):]
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	raw[len(raw)-1] ^= 1
	tampered := ua[:strings.LastIndex(ua, Scheme)] + Scheme + base64.RawURLEncoding.EncodeToString(raw)
	// A router whose key is not the one the account linked: a copied device id
	// signed with any other key.
	strangerKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
	stranger := func(string) (ed25519.PublicKey, bool) { return strangerKey.Public().(ed25519.PublicKey), true }
	nobody := func(string) (ed25519.PublicKey, bool) { return nil, false }
	for name, c := range map[string]struct {
		ua, hwid, target string
		keys             Keys
		devices          DeviceKeys
		now              time.Time
		want             string
	}{
		"another device's HWID":    {ua, hex.EncodeToString(other[:]), v.RequestTarget, keysOf(server), linked(v), now, "signature"},
		"another subscription":     {ua, v.HWID, "/api/sub/SomeoneElse", keysOf(server), linked(v), now, "signature"},
		"too old":                  {ua, v.HWID, v.RequestTarget, keysOf(server), linked(v), now.Add(11 * time.Minute), "away from now"},
		"from the future":          {ua, v.HWID, v.RequestTarget, keysOf(server), linked(v), now.Add(-11 * time.Minute), "away from now"},
		"not Vectra's key":         {ua, v.HWID, v.RequestTarget, func(byte) (*ecdh.PrivateKey, bool) { return otherServer, true }, linked(v), now, "does not open"},
		"an unknown key id":        {ua, v.HWID, v.RequestTarget, func(byte) (*ecdh.PrivateKey, bool) { return nil, false }, linked(v), now, "no key"},
		"a changed byte":           {tampered, v.HWID, v.RequestTarget, keysOf(server), linked(v), now, "does not open"},
		"a router not linked here": {ua, v.HWID, v.RequestTarget, keysOf(server), nobody, now, "not a router linked"},
		"another router's key":     {ua, v.HWID, v.RequestTarget, keysOf(server), stranger, now, "signature"},
		"a plain VectraRouter UA":  {"VectraRouter/0.5.0 (AX3000T)", v.HWID, v.RequestTarget, keysOf(server), linked(v), now, "not a Vectra router"},
		"a Happ UA":                {"Happ/4.2.1/Android/2609041405606", v.HWID, v.RequestTarget, keysOf(server), linked(v), now, "not a Vectra router"},
	} {
		if _, err := Open(c.ua, c.keys, c.devices, c.hwid, c.target, c.now, 10*time.Minute); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestEveryTokenIsNew(t *testing.T) {
	v, r, server := fixed()
	a, err := UserAgent(r, randReader{1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := UserAgent(r, randReader{2})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two tokens for the same request are the same bytes")
	}
	for _, ua := range []string{a, b} {
		if _, err := Open(ua, keysOf(server), linked(v), r.HWID, "/api/sub/AbCdEf123", r.Now, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
}

type randReader struct{ b byte }

func (r randReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b + byte(i)
	}
	return len(p), nil
}

func TestRequestAndInputs(t *testing.T) {
	for in, want := range map[string]string{
		"https://sub.example.invalid/api/sub/AbC":          "/api/sub/AbC",
		"https://sub.example.invalid/api/sub/AbC?format=x": "/api/sub/AbC?format=x",
		"https://sub.example.invalid":                      "/",
	} {
		if got, err := RequestTarget(in); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	if _, err := RequestTarget("http://sub.example.invalid/x"); err == nil {
		t.Error("a plain-http subscription was accepted")
	}
	v, r, _ := fixed()
	_ = v
	bad := r
	bad.HWID = "not-hex"
	if _, err := UserAgent(bad, randReader{1}); err == nil {
		t.Error("a bad HWID was accepted")
	}
	bad = r
	bad.Now = time.Unix(0, 0)
	if _, err := UserAgent(bad, randReader{1}); err == nil {
		t.Error("an unset clock was accepted")
	}
	bad = r
	bad.Version = "0.5.0 (evil) \x01" + strings.Repeat("9", 80)
	bad.DeviceID = strings.Repeat("d", MaxDeviceID)
	ua, err := UserAgent(bad, randReader{1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(ua, "()\x01") || len(ua) > 255 {
		t.Fatalf("a hostile version or the longest device id broke the agent (%d bytes): %q", len(ua), ua)
	}
	bad.DeviceID = strings.Repeat("d", MaxDeviceID+1)
	if _, err := UserAgent(bad, randReader{1}); err == nil {
		t.Error("a device id that would push the agent past 255 bytes was accepted")
	}
}

// The built-in key must be a usable X25519 key, and never the contract
// vector's throwaway one: anyone can open what is sealed to that.
func TestBuiltinKey(t *testing.T) {
	pub, err := base64.StdEncoding.DecodeString(BuiltinKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ecdh.X25519().NewPublicKey(pub); err != nil {
		t.Fatalf("BuiltinKey is not an X25519 key: %v", err)
	}
	if BuiltinKid < 0 || BuiltinKid > 255 {
		t.Fatalf("BuiltinKid %d is not a byte", BuiltinKid)
	}
	v, _, _ := fixed()
	if BuiltinKey == v.ServerPublicKey {
		t.Fatal("BuiltinKey is the contract vector's throwaway key")
	}
}
