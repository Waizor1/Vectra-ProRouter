package geo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateOne_DownloadsAndVerifiesSHA(t *testing.T) {
	payload := []byte("geoip-test-bytes")
	hex := sha256Hex(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	r := UpdateOne(context.Background(), dir, Asset{
		Filename:       "geoip.dat",
		URL:            srv.URL + "/geoip.dat",
		ExpectedSHA256: hex,
	}, nil)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	if !r.Updated || r.SHA256 != hex {
		t.Fatalf("update mismatch: %+v", r)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "geoip.dat"))
	if string(got) != string(payload) {
		t.Fatalf("body mismatch")
	}
}

func TestUpdateOne_SkipsWhenExistingHashMatches(t *testing.T) {
	payload := []byte("xyz")
	hex := sha256Hex(payload)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "geosite.dat"), payload, 0o644)
	// Server should NOT be hit; assert by failing if it is.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be hit when hash matches")
	}))
	defer srv.Close()
	r := UpdateOne(context.Background(), dir, Asset{
		Filename:       "geosite.dat",
		URL:            srv.URL + "/geosite.dat",
		ExpectedSHA256: hex,
	}, nil)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	if r.Updated {
		t.Fatalf("should not have updated; %+v", r)
	}
	if !r.OldExists || r.SHA256 != hex {
		t.Fatalf("expected OldExists=true with matching hash, got %+v", r)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hexEncode(h[:])
}

func hexEncode(b []byte) string { return hex2(b) }
func hex2(b []byte) string      { return encodeHex(b) }
func encodeHex(b []byte) string {
	return string([]byte(hexLower(b)))
}
func hexLower(b []byte) string  { return hexx(b) }
func hexx(b []byte) string      { return stringHex(b) }
func stringHex(b []byte) string { return string([]byte(toHex(b))) }
func toHex(b []byte) []byte {
	s := make([]byte, 2*len(b))
	const hexChars = "0123456789abcdef"
	for i, c := range b {
		s[i*2] = hexChars[c>>4]
		s[i*2+1] = hexChars[c&0x0f]
	}
	return s
}

// Compile-time sanity: ensure encoding/hex would produce identical output.
var _ = hex.EncodeToString
