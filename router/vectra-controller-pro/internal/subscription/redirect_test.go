package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A redirect off https is refused: it would send the subscription's URL — its
// token — and the device's identity (x-hwid) in the clear. An https one is
// followed, as providers move their endpoints.
func TestFetchRefusesARedirectToPlainHTTP(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		_, _ = w.Write([]byte("[]"))
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/moved") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"remarks":"x","outbounds":[]}]`))
			return
		}
		to := plain.URL
		if r.URL.Query().Get("to") == "https" {
			to = "https://" + r.Host + "/moved"
		}
		http.Redirect(w, r, to+"/sub/"+token, http.StatusFound)
	}))
	defer srv.Close()

	u := secretURL(srv.URL)
	_, err := Fetch(context.Background(), FetchOptions{URL: u, UserAgent: "v2rayNG/1.9.5", HWID: "h", HTTPClient: srv.Client()})
	mustNotLeak(t, err, u)
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if n := plainHits.Load(); n > 0 {
		t.Fatalf("the redirect to plain http was followed %d time(s)", n)
	}

	res, err := Fetch(context.Background(), FetchOptions{URL: srv.URL + "/sub/x?to=https", UserAgent: "v2rayNG/1.9.5", HWID: "h", HTTPClient: srv.Client()})
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("an https redirect was not followed: %v", err)
	}
}
