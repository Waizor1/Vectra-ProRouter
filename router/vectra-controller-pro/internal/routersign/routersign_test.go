package routersign

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

// A token made for a URL with a query opens with the key the panel named, for
// that path and that query only: a report's body hash rides in the query.
func TestSignerCoversThePathAndQuery(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	vectra, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	st := state.PersistedState{
		DeviceIdentifier: "vectra-0123456789ab",
		DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
		ClaimKey:         &controlplane.ClaimKey{Kid: 7, PublicKey: base64.StdEncoding.EncodeToString(vectra.PublicKey().Bytes())},
	}
	now := time.Unix(1790000000, 0)
	hwid := "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	ua, err := Signer(st, "0.6.0-r19", func() time.Time { return now })(hwid, "https://api-app.vectra-pro.net/errors/router?b=abc123")
	if err != nil {
		t.Fatal(err)
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return vectra, kid == 7 }
	devices := func(id string) (ed25519.PublicKey, bool) { return pub, id == st.DeviceIdentifier }
	if _, err := uatoken.Open(ua, keys, devices, hwid, "/errors/router?b=abc123", now, time.Minute); err != nil {
		t.Fatalf("does not open for its own path and query: %v", err)
	}
	if _, err := uatoken.Open(ua, keys, devices, hwid, "/errors/router?b=abc124", now, time.Minute); err == nil {
		t.Fatal("opened for another body hash")
	}
}

// Without a key from check-in a token is sealed to the built-in one.
func TestKeyIsTheBuiltinOneWithoutCheckIn(t *testing.T) {
	k, err := Key(state.PersistedState{})
	if err != nil || k.Kid != uatoken.BuiltinKid {
		t.Fatalf("key %+v, err %v", k, err)
	}
}
