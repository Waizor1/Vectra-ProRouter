package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// jsonUA reproduces the measured provider behaviour: the SAME url and the SAME
// device headers return different payloads depending only on User-Agent.
func jsonUA(ua string) bool {
	return strings.HasPrefix(ua, "Happ/") || strings.HasPrefix(ua, "v2rayNG/")
}

const linkList = "vless://uuid@pl1.example:443?type=tcp&security=reality&sni=s&pbk=K&sid=I#PL-1\n" +
	"vless://uuid@pl2.example:443?type=tcp&security=reality&sni=s&pbk=K&sid=I#PL-2"

// providerArray builds a 3-entry JSON array shaped like the real payload.
func providerArray() []byte {
	entry := func(remark string) string {
		return `{"burstObservatory":{"subjectSelector":["bridge-"]},` +
			`"dns":{"queryStrategy":"UseIPv4"},` +
			`"inbounds":[{"tag":"socks","port":10808,"protocol":"socks","settings":{"udp":true}},` +
			`{"tag":"http","port":10809,"protocol":"http","settings":{}}],` +
			`"log":{"loglevel":"warning"},` +
			`"outbounds":[{"tag":"DIRECT","protocol":"freedom","streamSettings":{"tcpSettings":{}}},` +
			`{"tag":"stage-wl","protocol":"loopback","settings":{"inboundTag":"STAGE_WL"}}],` +
			`"policy":{"levels":{"8":{"connIdle":300}}},` +
			`"remarks":"` + remark + `",` +
			`"routing":{"domainStrategy":"IPIfNonMatch","rules":[{"type":"field","inboundTag":["STAGE_WL"],"outboundTag":"DIRECT"}]},` +
			`"stats":{}}`
	}
	return []byte("[" + entry("PL-1") + "," + entry("PL-2") + "," + entry("PL-3") + "]")
}

type capturedRequest struct {
	UA      string
	Headers http.Header
}

// newProviderStub serves the JSON variant to JSON user agents and the degraded
// base64 link list to everything else, recording every request.
func newProviderStub(t *testing.T) (*httptest.Server, *[]capturedRequest, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var seen []capturedRequest
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		mu.Lock()
		seen = append(seen, capturedRequest{UA: ua, Headers: r.Header.Clone()})
		mu.Unlock()
		if jsonUA(ua) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(providerArray())
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(linkList))))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &mu
}

func TestFetchJSONVariant(t *testing.T) {
	srv, _, _ := newProviderStub(t)
	res, err := Fetch(context.Background(), FetchOptions{
		URL:        srv.URL,
		UserAgent:  "v2rayNG/1.9.5",
		MAC:        prodMAC,
		Model:      prodModel,
		OSRelease:  "24.10.6",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.BodyFormat != FormatJSON {
		t.Fatalf("BodyFormat = %q, want %q", res.BodyFormat, FormatJSON)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("Entries = %d, want 3", len(res.Entries))
	}
	if strings.Join(res.Remarks, ",") != "PL-1,PL-2,PL-3" {
		t.Fatalf("Remarks = %v", res.Remarks)
	}
	// Each entry must still be a complete, verbatim Xray document.
	for i, e := range res.Entries {
		if !json.Valid(e) {
			t.Fatalf("entry %d is not valid JSON", i)
		}
		if !strings.Contains(string(e), `"tcpSettings":{}`) {
			t.Errorf("entry %d lost its empty tcpSettings object", i)
		}
		if !strings.Contains(string(e), `"stats":{}`) {
			t.Errorf("entry %d lost its empty stats object", i)
		}
	}
	// ParseBody must agree with the fetcher's classification.
	pr := ParseBody(res.Body, res.ContentType)
	if pr.BodyFormat != FormatJSON || len(pr.Entries) != 3 {
		t.Fatalf("ParseBody: format=%q entries=%d", pr.BodyFormat, len(pr.Entries))
	}
}

func TestFetchDegradedLinkList(t *testing.T) {
	srv, _, _ := newProviderStub(t)
	for _, ua := range []string{"passwall2/26.7.16", "Xray/26.7.28", "sing-box/1.9.0"} {
		t.Run(ua, func(t *testing.T) {
			res, err := Fetch(context.Background(), FetchOptions{
				URL:        srv.URL,
				UserAgent:  ua,
				MAC:        prodMAC,
				Model:      prodModel,
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if res.BodyFormat != FormatBase64Links {
				t.Fatalf("BodyFormat = %q, want %q", res.BodyFormat, FormatBase64Links)
			}
			if len(res.Entries) != 0 {
				t.Fatalf("degraded payload must yield no JSON entries, got %d", len(res.Entries))
			}
			pr := ParseBody(res.Body, res.ContentType)
			if len(pr.Nodes) != 2 {
				t.Fatalf("link list should parse 2 nodes, got %d", len(pr.Nodes))
			}
		})
	}
}

// TestHWIDStableAcrossUserAgents: the device identity must not depend on which
// payload variant we ask for.
func TestHWIDStableAcrossUserAgents(t *testing.T) {
	srv, seen, mu := newProviderStub(t)
	agents := []string{"Happ/4.2.1/Windows/2609041405606", "v2rayNG/1.9.5", "passwall2/26.7.16"}
	for _, ua := range agents {
		if _, err := Fetch(context.Background(), FetchOptions{
			URL:        srv.URL,
			UserAgent:  ua,
			MAC:        prodMAC,
			Model:      prodModel,
			OSRelease:  "24.10.6",
			HTTPClient: srv.Client(),
		}); err != nil {
			t.Fatalf("Fetch(%s): %v", ua, err)
		}
	}
	mu.Lock()
	got := append([]capturedRequest(nil), *seen...)
	mu.Unlock()

	if len(got) != len(agents) {
		t.Fatalf("expected %d requests, got %d", len(agents), len(got))
	}
	for i, req := range got {
		if req.UA != agents[i] {
			t.Fatalf("request %d UA = %q, want %q", i, req.UA, agents[i])
		}
		if h := req.Headers.Get("x-hwid"); h != prodHWID {
			t.Errorf("UA %s: x-hwid = %q, want %q", req.UA, h, prodHWID)
		}
		if m := req.Headers.Get("x-device-model"); m != prodModel {
			t.Errorf("UA %s: x-device-model = %q, want %q", req.UA, m, prodModel)
		}
	}
}

func TestFetchSendsAllDeviceHeaders(t *testing.T) {
	srv, seen, mu := newProviderStub(t)
	if _, err := Fetch(context.Background(), FetchOptions{
		URL:        srv.URL,
		UserAgent:  "v2rayNG/1.9.5",
		MAC:        prodMAC,
		Model:      prodModel + "\n", // untrimmed on purpose
		OSRelease:  "24.10.6",
		HTTPClient: srv.Client(),
	}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	mu.Lock()
	h := (*seen)[0].Headers
	mu.Unlock()

	want := map[string]string{
		"x-device-os":    "OpenWrt",
		"x-ver-os":       "24.10.6",
		"x-device-model": prodModel, // TRIMMED, matching what went into the HWID
		"x-hwid":         prodHWID,
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

func TestFetchRefusesTruncatedResponseAtCap(t *testing.T) {
	body := strings.Repeat("A", 4096)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()
	_, err := Fetch(context.Background(), FetchOptions{
		URL:        srv.URL,
		UserAgent:  "v2rayNG/1.9.5",
		MaxBytes:   1024,
		HTTPClient: srv.Client(),
	})
	if err == nil {
		t.Fatal("expected a refusal when the response hits the read cap")
	}
	if !strings.Contains(err.Error(), "read cap") {
		t.Errorf("error should explain the cap: %v", err)
	}
}

func TestFetchRefusesNonHTTPS(t *testing.T) {
	if _, err := Fetch(context.Background(), FetchOptions{URL: "http://sub.example/x"}); err == nil {
		t.Fatal("expected a refusal for a cleartext subscription URL")
	}
}
