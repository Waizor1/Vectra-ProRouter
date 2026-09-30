package subscription

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-pro/internal/uatoken"
)

// A router's own User-Agent is made per attempt: a retry carries a new token
// (a token is bound to its moment), and each one opens for exactly this HWID
// and subscription path at the backend.
func TestFetchSignsEveryAttempt(t *testing.T) {
	server, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, dev, _ := ed25519.GenerateKey(rand.Reader)
	var mu sync.Mutex
	var seen []string
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("User-Agent")+"\n"+r.Header.Get("x-hwid")+"\n"+r.URL.RequestURI())
		mu.Unlock()
		if hits.Add(1) == 1 {
			// A dropped connection is what Fetch retries.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"outbounds":[],"remarks":"a"}]`))
	}))
	defer srv.Close()

	sign := func(hwid, url string) (string, error) {
		return uatoken.UserAgent(uatoken.Request{
			Version: "0.5.0-r1", DeviceID: "vectra-0123456789ab", DeviceKey: dev,
			HWID: hwid, URL: url, Kid: 1, ServerPub: server.PublicKey().Bytes(), Now: time.Now(),
		}, rand.Reader)
	}
	if _, err := Fetch(context.Background(), FetchOptions{
		URL: srv.URL + "/api/sub/AbC", UserAgent: "v2rayNG/1.9.5", SignUserAgent: sign,
		MAC: "aa:bb:cc:dd:ee:ff", Model: "Xiaomi Mi Router AX3000T",
		HTTPClient: srv.Client(), Retries: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("%d requests, want a failed one and a retry", len(seen))
	}
	keys := func(byte) (*ecdh.PrivateKey, bool) { return server, true }
	linked := func(string) (ed25519.PublicKey, bool) { return dev.Public().(ed25519.PublicKey), true }
	var uas []string
	for _, s := range seen {
		p := strings.SplitN(s, "\n", 3)
		if !strings.HasPrefix(p[0], "VectraRouter/0.5.0-r1 vr1.") {
			t.Fatalf("sent %q, not the router's own agent", p[0])
		}
		if _, err := uatoken.Open(p[0], keys, linked, p[1], p[2], time.Now(), time.Minute); err != nil {
			t.Fatalf("the backend could not open what was sent: %v", err)
		}
		uas = append(uas, p[0])
	}
	if uas[0] == uas[1] {
		t.Fatal("the retry re-sent the first attempt's token")
	}
}

func TestFetchRefusesWhenTheRouterCannotSign(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, err := Fetch(context.Background(), FetchOptions{
		URL: srv.URL + "/api/sub/AbC", HTTPClient: srv.Client(), Retries: 2,
		SignUserAgent: func(string, string) (string, error) { return "", errors.New("no device key") },
	})
	if err == nil || !strings.Contains(err.Error(), "no device key") {
		t.Fatalf("Fetch = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("a request went out without the router's own agent")
	}
}

// Only the agent signed just now may be the router's own: a copy written into
// the headers, or into UserAgent, never leaves the router.
func TestFetchRefusesACopyOfTheRoutersOwnAgent(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, opts := range map[string]FetchOptions{
		"userAgent": {UserAgent: "VectraRouter/0.5.0 (AX3000T)"},
		"headers over a signed one": {
			SignUserAgent: func(string, string) (string, error) { return "VectraRouter/0.5.0 vr1.AQID", nil },
			ExtraHeaders:  map[string]string{"User-Agent": "VectraRouter/0.4.0 vr1.old"},
		},
	} {
		opts.URL, opts.HTTPClient, opts.Retries = srv.URL+"/api/sub/AbC", srv.Client(), 1
		if _, err := Fetch(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "leave userAgent empty") {
			t.Errorf("%s: Fetch = %v, want the copy refused", name, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests went out with a copied agent", hits.Load())
	}
}
