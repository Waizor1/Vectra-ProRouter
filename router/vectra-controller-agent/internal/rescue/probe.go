package rescue

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"vectra-controller-agent/internal/netmark"
)

type HTTPProbeResult struct {
	URL        string    `json:"url"`
	Reachable  bool      `json:"reachable"`
	StatusCode int       `json:"statusCode,omitempty"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
}

type HTTPProber struct {
	Client *http.Client
}

// NewHTTPProber builds an UNMARKED prober. It dials over whatever path the
// router's routing currently provides — i.e. through the PassWall2 tproxy when
// proxy mode is active. This is the correct behaviour for the RU/foreign/
// Telegram/YouTube/Instagram reachability probes, which must measure the real
// proxied client path so the rescue trigger reflects what users actually see.
func NewHTTPProber(timeout time.Duration) HTTPProber {
	return HTTPProber{Client: buildHTTPClient(timeout, 0)}
}

// NewHTTPProberWithFwmark builds a prober whose sockets carry SO_MARK == fwmark
// (Linux only), so its traffic is matched by the nftables carve-out and egresses
// DIRECTLY, bypassing the PassWall2 tproxy — the SAME path the control-plane
// check-in uses. Only the panel-reachability probe may use this: it must measure
// the direct path the check-in actually takes, otherwise a dead-proxy router
// reports the panel as blocked (because the probe went through the dead proxy)
// even though the marked check-in would have succeeded. A zero fwmark is
// identical to NewHTTPProber (no marking).
func NewHTTPProberWithFwmark(timeout time.Duration, fwmark uint) HTTPProber {
	return HTTPProber{Client: buildHTTPClient(timeout, fwmark)}
}

// buildHTTPClient constructs an *http.Client with the given timeout. When fwmark
// is non-zero (and the platform supports SO_MARK), it installs a custom
// transport whose dialer stamps the mark; otherwise it leaves Transport nil so
// the standard-library default is used and dialing is unchanged.
func buildHTTPClient(timeout time.Duration, fwmark uint) *http.Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	if control := netmark.Control(fwmark); control != nil {
		dialer := &net.Dialer{Timeout: timeout, Control: control}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = dialer.DialContext
		client.Transport = transport
	}
	return client
}

func (p HTTPProber) Probe(ctx context.Context, url string) HTTPProbeResult {
	result := HTTPProbeResult{
		URL:       url,
		CheckedAt: time.Now().UTC(),
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	response, err := client.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()

	result.StatusCode = response.StatusCode
	result.Reachable = response.StatusCode >= 200 && response.StatusCode < 400
	if !result.Reachable {
		result.Error = fmt.Sprintf("unexpected status %d", response.StatusCode)
	}

	return result
}

func ProbeAny(ctx context.Context, prober HTTPProber, urls []string) HTTPProbeResult {
	last := HTTPProbeResult{}
	for _, url := range urls {
		if url == "" {
			continue
		}
		result := prober.Probe(ctx, url)
		if result.Reachable {
			return result
		}
		last = result
	}

	return last
}
