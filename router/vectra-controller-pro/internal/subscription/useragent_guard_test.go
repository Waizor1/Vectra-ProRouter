package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The guard is only worth something if the refused request never reaches the
// provider: the sweep acts on the FIRST request. So every refusal here is
// checked against a server that counts what actually arrived.
func TestFetchRefusesAMalformedHappAgentBeforeAnyByteLeaves(t *testing.T) {
	var hits atomic.Int32
	var gotUA atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotUA.Store(r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"outbounds":[],"remarks":"a"}]`))
	}))
	defer srv.Close()

	fetch := func(ua string, extra map[string]string) error {
		_, err := Fetch(context.Background(), FetchOptions{
			URL: srv.URL + "/api/sub/x", UserAgent: ua, ExtraHeaders: extra,
			HTTPClient: srv.Client(), Retries: 2,
		})
		return err
	}

	for _, tc := range []struct {
		name  string
		ua    string
		extra map[string]string
	}{
		{"the agent this codebase used to document", "Happ/1.0", nil},
		{"a lower-case variant", "happ/2", nil},
		{"a malformed agent smuggled in through the extra headers", "v2rayNG/1.9.5",
			map[string]string{"user-agent": "Happ/1.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			err := fetch(tc.ua, tc.extra)
			if err == nil || !strings.Contains(err.Error(), "anti-fraud") {
				t.Fatalf("Fetch = %v, want an anti-fraud refusal", err)
			}
			if n := hits.Load() - before; n != 0 {
				t.Fatalf("the provider received %d request(s); a refused agent must never be sent", n)
			}
		})
	}

	// Anti-vacuity: the same server does receive a request with an agent the
	// guard lets through, so the zero above is the guard and not a dead server.
	const real = "Happ/4.2.1/Windows/2609041405606"
	if err := fetch(real, nil); err != nil {
		t.Fatalf("Fetch with the real client's shape: %v", err)
	}
	if hits.Load() != 1 || gotUA.Load() != real {
		t.Fatalf("hits=%d ua=%v; want exactly one request carrying %q", hits.Load(), gotUA.Load(), real)
	}
}
