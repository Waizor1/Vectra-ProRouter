package happcrypt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIEndpoint is Happ's official crypto-link minting service. crypt5's
// algorithm + keys are closed, so this API is the license-clean way to mint a
// real happ://crypt5/ link (POST {"url":...} -> {"encrypted_link":...}).
const DefaultAPIEndpoint = "https://crypto.happ.su/api-v2.php"

// APIClient mints crypt5 links via Happ's official service. Treat it as a
// third-party dependency: callers SHOULD cache results keyed by the source
// subscription URL (the underlying URL is stable) and only re-mint on rotation,
// to minimize how often a secret-bearing URL is sent off-box.
type APIClient struct {
	Endpoint string
	HTTP     *http.Client
	Retries  int
}

// NewAPIClient returns a client with sane production defaults (15s timeout, 2
// retries with backoff).
func NewAPIClient() *APIClient {
	return &APIClient{
		Endpoint: DefaultAPIEndpoint,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		Retries:  2,
	}
}

type apiResponse struct {
	EncryptedLink string `json:"encrypted_link"`
}

// EncryptV5 mints a happ://crypt5/ link for subURL via the Happ API. It pins
// HTTPS (a downgraded endpoint must never carry a secret-bearing URL) and
// retries transient failures with linear backoff.
func (c *APIClient) EncryptV5(ctx context.Context, subURL string) (string, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultAPIEndpoint
	}
	if err := requireHTTPSURL(endpoint); err != nil {
		return "", err
	}
	if strings.TrimSpace(subURL) == "" {
		return "", fmt.Errorf("happcrypt: empty subscription URL")
	}

	body, err := json.Marshal(map[string]string{"url": subURL})
	if err != nil {
		return "", fmt.Errorf("happcrypt: marshal request: %w", err)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}

	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("happcrypt: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			lastErr = fmt.Errorf("happcrypt: API status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			continue
		}
		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			lastErr = fmt.Errorf("happcrypt: decode API response: %w", err)
			continue
		}
		if !strings.HasPrefix(out.EncryptedLink, PrefixV5) {
			return "", fmt.Errorf("happcrypt: API returned a non-crypt5 link %q", out.EncryptedLink)
		}
		return out.EncryptedLink, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("happcrypt: unknown API error")
	}
	return "", fmt.Errorf("happcrypt: crypt5 API failed after %d attempt(s): %w", c.Retries+1, lastErr)
}

func requireHTTPSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("happcrypt: parse endpoint: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("happcrypt: endpoint must be https (got %q)", u.Scheme)
	}
	return nil
}
