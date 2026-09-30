package bugreport

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/routersign"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

const testHWID = "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"

type signed struct {
	st     state.PersistedState
	devPub ed25519.PublicKey
	vectra *ecdh.PrivateKey
	now    time.Time
}

func newSigned(t *testing.T) signed {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	vectra, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signed{
		st: state.PersistedState{DeviceIdentifier: "vectra-0123456789ab", DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
			ClaimKey: &controlplane.ClaimKey{Kid: 9, PublicKey: base64.StdEncoding.EncodeToString(vectra.PublicKey().Bytes())}},
		devPub: pub, vectra: vectra, now: time.Unix(1790000000, 0),
	}
}

// The server's side of the contract: the body's hash is the query's b, and
// the token opens for this path and query, this HWID, this device.
func (s signed) verify(t *testing.T, r *http.Request, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	if r.URL.Query().Get("b") != hex.EncodeToString(sum[:]) {
		t.Errorf("b = %q", r.URL.Query().Get("b"))
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return s.vectra, kid == 9 }
	devices := func(id string) (ed25519.PublicKey, bool) { return s.devPub, id == s.st.DeviceIdentifier }
	if _, err := uatoken.Open(r.UserAgent(), keys, devices, r.Header.Get("x-hwid"), r.URL.RequestURI(), s.now, time.Minute); err != nil {
		t.Errorf("token: %v", err)
	}
	for h, want := range map[string]string{"x-device-os": "OpenWrt", "x-device-model": "Xiaomi Mi Router AX3000T", "x-ver-os": "24.10.6", "Content-Type": "application/json"} {
		if r.Header.Get(h) != want {
			t.Errorf("%s = %q", h, r.Header.Get(h))
		}
	}
}

func (s signed) sender(url string, client *http.Client) Sender {
	return Sender{URL: url, Client: client, Sign: routersign.Signer(s.st, "0.6.0-r19", func() time.Time { return s.now }),
		HWID: testHWID, Model: "Xiaomi Mi Router AX3000T", OSRelease: "24.10.6", Now: func() time.Time { return s.now }}
}

func entry() Entry {
	at := time.Unix(1790000000, 0).UTC()
	return Entry{Report: rep("TEST", "t", "low", at), Meta: Meta{QueuedAt: at}}
}

func TestSendSignsAndCountsWhatTheServerSays(t *testing.T) {
	s := newSigned(t)
	status := http.StatusAccepted
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.verify(t, r, body)
		if r.Header.Get("x-vectra-report-id") == "" {
			t.Error("no report id")
		}
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	snd := s.sender(srv.URL+"/errors/router", srv.Client())
	for _, c := range []struct {
		status int
		done   bool
		retry  time.Duration
	}{
		{202, true, 0}, {400, true, 0}, {413, true, 0}, {422, true, 0},
		{401, false, 6 * time.Hour}, {403, false, 6 * time.Hour}, {404, false, time.Hour},
		{429, false, 2 * time.Minute}, {503, false, time.Minute},
	} {
		status = c.status
		o := snd.Send(context.Background(), entry())
		if o.Status != c.status || o.Done != c.done || o.Retry != c.retry {
			t.Errorf("%d: %+v", c.status, o)
		}
	}
}

func TestSendBacksOffByAttempt(t *testing.T) {
	s := newSigned(t)
	snd := s.sender("https://127.0.0.1:1/errors/router", &http.Client{Timeout: time.Second})
	e := entry()
	for attempts, want := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, time.Hour} {
		e.Meta.Attempts = attempts
		if o := snd.Send(context.Background(), e); o.Done || o.Retry != want || o.Err == nil {
			t.Errorf("attempt %d: %+v", attempts, o)
		}
	}
}

func TestSendWaitsForTheClock(t *testing.T) {
	s := newSigned(t)
	hit := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	snd := s.sender(srv.URL+"/errors/router", srv.Client())
	snd.Now = func() time.Time { return time.Unix(100, 0) }
	if o := snd.Send(context.Background(), entry()); o.Done || o.Retry != 10*time.Minute || hit {
		t.Fatalf("%+v, hit %v", o, hit)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	s := newSigned(t)
	portal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer portal.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, portal.URL+"/login", http.StatusFound)
	}))
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = controlplane.MarkedHTTPClient(0, time.Second).CheckRedirect
	if o := s.sender(srv.URL+"/errors/router", client).Send(context.Background(), entry()); o.Done || o.Status != http.StatusFound {
		t.Fatalf("%+v", o)
	}
}
