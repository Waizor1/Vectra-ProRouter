package subscription

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type originRedirectTransport struct {
	location string
	requests []*http.Request
}

func (tr *originRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.requests = append(tr.requests, r.Clone(context.Background()))
	if len(tr.requests) == 1 {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{tr.location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
}
func TestSubscriptionRedirectOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, location string
		allowed        bool
	}{
		{"same origin", "https://provider.invalid/redirected", true},
		{"cross host", "https://collector.invalid/redirected", false},
		{"cross port", "https://provider.invalid:8443/redirected", false},
		{"cross scheme", "http://provider.invalid/redirected", false},
		{"userinfo", "https://synthetic:fixture@provider.invalid/redirected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &originRedirectTransport{location: tc.location}
			_, err := Fetch(context.Background(), FetchOptions{URL: "https://provider.invalid/sub/synthetic", HWID: strings.Repeat("a", 64), SignUserAgent: func(string, string) (string, error) { return "VectraRouter/test synthetic", nil }, ExtraHeaders: map[string]string{"X-Synthetic-Secret": "fixture-only", "Authorization": "Bearer synthetic-only"}, HTTPClient: &http.Client{Transport: tr}})
			if tc.allowed {
				if err != nil || len(tr.requests) != 2 {
					t.Fatal("same-origin redirect rejected")
				}
			} else {
				if err == nil || len(tr.requests) != 1 {
					t.Fatalf("unsafe redirect reached transport: requests=%d", len(tr.requests))
				}
			}
		})
	}
}
