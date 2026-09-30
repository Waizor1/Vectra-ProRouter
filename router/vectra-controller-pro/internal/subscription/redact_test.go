package subscription

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// token is the bearer material a provider puts in the subscription URL. Every
// assertion below is "this string must not appear", because an error containing
// it ends up in the panel database (job result) and in
// /etc/vectra-controller-pro/state.json, which keep.d preserves into every
// sysupgrade backup.
const token = "BEARER-e1f4c0de-DO-NOT-LEAK"

// secretURL builds a subscription URL that carries the token in BOTH places
// providers use: the path and the query.
func secretURL(base string) string {
	return base + "/sub/" + token + "?token=" + token
}

func mustNotLeak(t *testing.T, err error, u string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("bearer token leaked into the error:\n  %v", err)
	}
	if strings.Contains(err.Error(), u) {
		t.Fatalf("the full subscription URL leaked into the error:\n  %v", err)
	}
}

// hostOf returns the host:port of a URL, which SHOULD survive redaction — an
// operator has to be able to tell a DNS failure from a TLS one.
func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Host
}

// failingTransport dials with a fixed error, so a DNS-class failure can be
// reproduced without touching a resolver.
type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// A resolver failure is the most common way this leaks in the field: a router
// on a flapping WAN or behind a captive portal produces *url.Error, whose
// Error() prints the whole URL.
func TestFetchDNSFailureDoesNotLeakTheURL(t *testing.T) {
	u := secretURL("https://provider.invalid")
	_, err := Fetch(context.Background(), FetchOptions{
		URL: u,
		HTTPClient: &http.Client{Transport: failingTransport{
			err: &net.DNSError{Err: "no such host", Name: "provider.invalid", IsNotFound: true},
		}},
	})
	mustNotLeak(t, err, u)
	if !strings.Contains(err.Error(), "provider.invalid") {
		t.Errorf("redaction must keep the host so the failure is diagnosable: %v", err)
	}
}

func TestFetchConnectionRefusedDoesNotLeakTheURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	base := srv.URL
	srv.Close() // nothing is listening on that port any more

	u := secretURL(base)
	_, err := Fetch(context.Background(), FetchOptions{URL: u, HTTPClient: client})
	mustNotLeak(t, err, u)
	if !strings.Contains(err.Error(), hostOf(t, base)) {
		t.Errorf("redaction must keep the host: %v", err)
	}
}

func TestFetchTLSFailureDoesNotLeakTheURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	u := secretURL(srv.URL)
	// Default client: does NOT trust httptest's self-signed CA.
	_, err := Fetch(context.Background(), FetchOptions{URL: u, HTTPClient: &http.Client{}})
	mustNotLeak(t, err, u)
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("expected a TLS verification failure, got: %v", err)
	}
}

// The non-https refusal used to interpolate opts.URL verbatim.
func TestFetchNonHTTPSRefusalDoesNotLeakTheURL(t *testing.T) {
	u := secretURL("http://provider.example")
	_, err := Fetch(context.Background(), FetchOptions{URL: u})
	mustNotLeak(t, err, u)
	if !strings.Contains(err.Error(), "non-https") {
		t.Errorf("the refusal reason must survive redaction: %v", err)
	}
}

// A URL that url.Parse itself rejects reaches http.NewRequestWithContext, which
// returns *url.Error too.
func TestFetchUnparseableURLDoesNotLeakTheURL(t *testing.T) {
	// Valid enough for the https gate, rejected when the request is built.
	u := "https://provider.example/sub/" + token + "?token=" + token + "\x7f"
	_, err := Fetch(context.Background(), FetchOptions{URL: u})
	if err != nil && strings.Contains(err.Error(), token) {
		t.Fatalf("bearer token leaked into the error:\n  %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://p.example/sub/" + token:       "https://p.example/<redacted>",
		"https://p.example/api?token=" + token: "https://p.example/<redacted>",
		"https://user:" + token + "@p.example": "https://p.example/<redacted>",
		"https://p.example:8443/x#" + token:    "https://p.example:8443/<redacted>",
		"https://p.example":                    "https://p.example",
		"https://p.example/":                   "https://p.example",
		"://not a url":                         "<redacted-url>",
		"":                                     "<redacted-url>",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Scrub is what the daemon runs over a job result. It must remove the token in
// every form it can appear in, and must NOT shred a message that merely
// mentions the provider host.
func TestScrub(t *testing.T) {
	u := "https://p.example/sub/" + token + "?token=" + token
	for _, in := range []string{
		"curl: (6) could not resolve " + u,
		"Get \"" + u + "\": dial tcp: i/o timeout",
		"path was /sub/" + token,
		"query was token=" + token,
	} {
		if got := Scrub(in, u); strings.Contains(got, token) {
			t.Errorf("Scrub(%q) left the token: %q", in, got)
		}
	}
	// A secret-free URL contributes no needles, so unrelated text is untouched.
	plain := "https://p.example"
	if got := Scrub("could not reach https://p.example", plain); got != "could not reach https://p.example" {
		t.Errorf("a URL with no path/query must not scrub the host: %q", got)
	}
	// And the redacted form itself must survive a scrub pass unharmed.
	if got := Scrub(RedactURL(u), u); got != RedactURL(u) {
		t.Errorf("Scrub ate the redacted form: %q", got)
	}
}

// Sanitize must never hand back an error that can be unwrapped into the raw
// *url.Error and re-stringified.
func TestSanitizeDropsTheUnwrappableURLError(t *testing.T) {
	u := secretURL("https://p.example")
	orig := &url.Error{Op: "Get", URL: u, Err: errors.New("connection reset")}

	got := Sanitize(orig, u)
	if strings.Contains(got.Error(), token) {
		t.Fatalf("Sanitize leaked the token: %v", got)
	}
	var ue *url.Error
	if errors.As(got, &ue) && strings.Contains(ue.URL, token) {
		t.Fatalf("the raw *url.Error is still reachable via errors.As: %v", ue)
	}
	if Sanitize(nil, u) != nil {
		t.Error("Sanitize(nil) must stay nil")
	}
}
