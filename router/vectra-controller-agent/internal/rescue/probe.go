package rescue

import (
	"context"
	"fmt"
	"net/http"
	"time"
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

func NewHTTPProber(timeout time.Duration) HTTPProber {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return HTTPProber{
		Client: &http.Client{Timeout: timeout},
	}
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
