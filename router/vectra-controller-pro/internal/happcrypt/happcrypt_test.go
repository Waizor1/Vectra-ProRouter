package happcrypt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedKeysAreRSA4096(t *testing.T) {
	for _, v := range []int{2, 3, 4} {
		pub, err := PublicKey(v)
		if err != nil {
			t.Fatalf("PublicKey(%d): %v", v, err)
		}
		if pub == nil || pub.Size() != 512 {
			t.Errorf("crypt%d key is not RSA-4096 (size=%d bytes)", v, pub.Size())
		}
	}
}

// TestEncryptRSARoundTrip proves the encryption is correct PKCS#1 v1.5 by
// round-tripping through a freshly generated keypair (independent of the
// official keys, whose private halves we deliberately don't hold).
func TestEncryptRSARoundTrip(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatal(err)
	}
	const msg = "https://sub.example.net/api/sub/SECRET-TOKEN?x=1"
	b64, err := EncryptRSA(&priv.PublicKey, msg)
	if err != nil {
		t.Fatalf("EncryptRSA: %v", err)
	}
	ct, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("output is not standard base64: %v", err)
	}
	if len(ct) != 512 {
		t.Errorf("ciphertext = %d bytes, want 512 (one RSA-4096 block)", len(ct))
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, priv, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plain) != msg {
		t.Errorf("round-trip mismatch: got %q", string(plain))
	}
}

func TestLinkOfflineFormat(t *testing.T) {
	link, err := LinkOffline(4, "https://sub.example.net/s/tok")
	if err != nil {
		t.Fatalf("LinkOffline: %v", err)
	}
	if !strings.HasPrefix(link, PrefixV4) {
		t.Fatalf("link missing crypt4 prefix: %q", link)
	}
	ct, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, PrefixV4))
	if err != nil || len(ct) != 512 {
		t.Errorf("payload not a 512-byte base64 RSA-4096 block (err=%v len=%d)", err, len(ct))
	}
	// Non-deterministic (PKCS#1 v1.5 is randomized): two calls differ.
	link2, _ := LinkOffline(4, "https://sub.example.net/s/tok")
	if link == link2 {
		t.Error("expected randomized PKCS#1 v1.5 output to differ between calls")
	}
}

func TestLinkOfflineSizeCap(t *testing.T) {
	_, err := LinkOffline(4, strings.Repeat("a", 600))
	if err == nil {
		t.Fatal("expected size-cap error for >501-byte content")
	}
	if !strings.Contains(err.Error(), "crypt5") {
		t.Errorf("error should point to crypt5 for long URLs: %v", err)
	}
}

func TestLinkOfflineRejectsV5(t *testing.T) {
	if _, err := LinkOffline(5, "https://x"); err == nil {
		t.Fatal("crypt5 must not be offline-mintable here (API only)")
	}
}

func TestAPIClientEncryptV5(t *testing.T) {
	const want = "happ://crypt5/neir1mEONt81wafHgn64EtHdu=jVEvft"
	var gotBody map[string]string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(apiResponse{EncryptedLink: want})
	}))
	defer srv.Close()

	c := &APIClient{Endpoint: srv.URL, HTTP: srv.Client(), Retries: 1}
	got, err := c.EncryptV5(context.Background(), "https://sub.example.net/s/tok")
	if err != nil {
		t.Fatalf("EncryptV5: %v", err)
	}
	if got != want {
		t.Errorf("link = %q, want %q", got, want)
	}
	if gotBody["url"] != "https://sub.example.net/s/tok" {
		t.Errorf("request body url = %q", gotBody["url"])
	}
}

func TestAPIClientRejectsNonCrypt5(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(apiResponse{EncryptedLink: "happ://crypt4/oops"})
	}))
	defer srv.Close()
	c := &APIClient{Endpoint: srv.URL, HTTP: srv.Client()}
	if _, err := c.EncryptV5(context.Background(), "https://x"); err == nil {
		t.Fatal("expected rejection of non-crypt5 link from API")
	}
}

func TestAPIClientSurfacesNon2xx(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()
	c := &APIClient{Endpoint: srv.URL, HTTP: srv.Client(), Retries: 0}
	_, err := c.EncryptV5(context.Background(), "https://x")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 surfaced, got %v", err)
	}
}

func TestEncryptV5RequiresHTTPS(t *testing.T) {
	c := &APIClient{Endpoint: "http://crypto.happ.su/api-v2.php", HTTP: &http.Client{Timeout: time.Second}}
	if _, err := c.EncryptV5(context.Background(), "https://x"); err == nil {
		t.Fatal("expected non-https endpoint to be rejected")
	}
}
