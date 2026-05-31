package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FetchOptions configures Fetch. The defaults mirror PassWall2's behavior:
// short connect timeout (5s), modest total timeout (30s), 2 retries.
type FetchOptions struct {
	URL string

	// Impersonation
	UserAgent       string // e.g. "passwall2/26.5.1"
	HWID            string // pre-computed sha256 hex; if empty + MAC+Model set, we compute.
	MAC             string // eth0 MAC, lowercase colon-separated; only used if HWID is empty
	Model           string // /tmp/sysinfo/model exact string; only used if HWID is empty
	OSRelease       string // /etc/openwrt_release DISTRIB_RELEASE (e.g. "24.10.6")
	DeviceOS        string // default "OpenWrt"
	ExtraHeaders    map[string]string

	// HTTP behavior
	ConnectTimeout time.Duration
	MaxTimeout     time.Duration
	Retries        int

	// HTTPClient lets tests inject a stub. If nil, a default client is built.
	HTTPClient *http.Client
}

// ComputeHWID returns sha256(mac + "-" + model) lowercase hex.
// Identical to PassWall2's subscribe.lua header construction.
func ComputeHWID(mac, model string) string {
	sum := sha256.Sum256([]byte(mac + "-" + model))
	return hex.EncodeToString(sum[:])
}

// Fetch performs the HTTP GET and returns the raw body plus parsed metadata
// from the V2RayN-convention response headers.
func Fetch(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("subscription.Fetch: URL required")
	}
	// Pin HTTPS: a cleartext subscription lets an on-path attacker reshape which
	// traffic is proxied vs. sent direct.
	if u, err := url.Parse(opts.URL); err != nil || !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("subscription.Fetch: refusing non-https url: %s", opts.URL)
	}
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 5 * time.Second
	}
	if opts.MaxTimeout == 0 {
		opts.MaxTimeout = 30 * time.Second
	}
	if opts.DeviceOS == "" {
		opts.DeviceOS = "OpenWrt"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.MaxTimeout}
	}

	hwid := opts.HWID
	if hwid == "" && opts.MAC != "" && opts.Model != "" {
		hwid = ComputeHWID(opts.MAC, opts.Model)
	}
	// Build a fresh request per attempt — net/http forbids re-using a *http.Request
	// across Do() calls (per docs); reuse leads to undefined behavior on retry.
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		if opts.UserAgent != "" {
			req.Header.Set("User-Agent", opts.UserAgent)
		}
		req.Header.Set("x-device-os", opts.DeviceOS)
		if opts.OSRelease != "" {
			req.Header.Set("x-ver-os", opts.OSRelease)
		}
		if opts.Model != "" {
			req.Header.Set("x-device-model", opts.Model)
		}
		if hwid != "" {
			req.Header.Set("x-hwid", hwid)
		}
		for k, v := range opts.ExtraHeaders {
			req.Header.Set(k, v)
		}
		return req, nil
	}

	var lastErr error
	for attempt := 0; attempt <= opts.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := newReq()
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap is generous for any subscription
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return buildFetchResult(opts.URL, resp, body), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("subscription.Fetch: unknown error")
	}
	return nil, fmt.Errorf("subscription.Fetch: after %d attempts: %w", opts.Retries+1, lastErr)
}

func buildFetchResult(url string, resp *http.Response, body []byte) *FetchResult {
	r := &FetchResult{
		URL:             url,
		StatusCode:      resp.StatusCode,
		ContentType:     resp.Header.Get("Content-Type"),
		Body:            body,
		BodyBytes:       len(body),
		FetchedAt:       time.Now().UTC(),
		UpstreamHeaders: map[string]string{},
	}
	// Selected diagnostic headers (preserve as-is).
	for _, h := range []string{
		"subscription-userinfo",
		"profile-title",
		"profile-update-interval",
		"profile-web-page-url",
		"support-url",
		"announce",
		"content-disposition",
	} {
		if v := resp.Header.Get(h); v != "" {
			r.UpstreamHeaders[h] = v
		}
	}
	if v := resp.Header.Get("subscription-userinfo"); v != "" {
		if u := parseUserInfo(v); u != nil {
			r.UserInfo = u
		}
	}
	if v := resp.Header.Get("profile-title"); v != "" {
		r.ProfileTitle = decodeBase64Header(v)
	}
	if v := resp.Header.Get("profile-update-interval"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			r.ProfileUpdateIntervalDays = n
		}
	}
	r.ProfileWebPageURL = resp.Header.Get("profile-web-page-url")
	r.SupportURL = resp.Header.Get("support-url")
	if v := resp.Header.Get("announce"); v != "" {
		r.Announcement = decodeBase64Header(v)
	}
	return r
}

func parseUserInfo(v string) *UserInfo {
	u := &UserInfo{}
	any := false
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		val = strings.TrimSpace(val)
		switch k {
		case "upload":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				u.UploadBytes = n
				any = true
			}
		case "download":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				u.DownloadBytes = n
				any = true
			}
		case "total":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				u.TotalBytes = n
				any = true
			}
		case "expire":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil && n > 0 {
				u.ExpireAt = time.Unix(n, 0).UTC()
				any = true
			}
		}
	}
	if !any {
		return nil
	}
	return u
}

// decodeBase64Header strips an optional "base64:" prefix and decodes the rest.
func decodeBase64Header(v string) string {
	v = strings.TrimSpace(v)
	const prefix = "base64:"
	if strings.HasPrefix(strings.ToLower(v), prefix) {
		v = v[len(prefix):]
	}
	out, err := decodeBase64Tolerant([]byte(v))
	if err != nil {
		return v
	}
	return string(out)
}
