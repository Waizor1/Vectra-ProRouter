package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/uaguard"
)

// FetchOptions configures Fetch. The defaults mirror PassWall2's behavior:
// short connect timeout (5s), modest total timeout (30s), 2 retries.
type FetchOptions struct {
	URL string

	// Device identity. The provider REFUSES a device whose x-hwid changes, so
	// these must be the real facts read off the router (see device.go), not the
	// ubus board model — that is a different string from /tmp/sysinfo/model.
	UserAgent    string // selects the payload variant; see DecodeBody
	HWID         string // pre-computed sha256 hex; if empty + MAC+Model set, we compute.
	MAC          string // eth0 MAC, lowercase colon-separated; only used if HWID is empty
	Model        string // TRIMMED /tmp/sysinfo/model string
	OSRelease    string // /etc/openwrt_release DISTRIB_RELEASE (e.g. "24.10.6")
	DeviceOS     string // default "OpenWrt"
	ExtraHeaders map[string]string

	// SignUserAgent, when set, makes the User-Agent for each attempt: a Vectra
	// router's own, signed for this HWID and URL at this moment
	// (internal/uatoken). It replaces UserAgent, so a retry never re-sends an
	// old token.
	SignUserAgent func(hwid, url string) (string, error)

	// HTTP behavior
	ConnectTimeout time.Duration
	MaxTimeout     time.Duration
	Retries        int
	// MaxBytes caps the response read. 0 = DefaultMaxFetchBytes. The JSON
	// variant is ~485 KB today, but the cap must be tunable rather than a
	// wired-in 4 MiB constant.
	MaxBytes int

	// HTTPClient lets tests inject a stub. If nil, a default client is built.
	HTTPClient *http.Client
}

// DefaultMaxFetchBytes is the default response read cap (4 MiB): generous for
// the ~485 KB JSON variant, small enough that a hostile response cannot
// exhaust a 234 MB router.
const DefaultMaxFetchBytes = 4 << 20

// ComputeHWID returns sha256(mac + "-" + model) lowercase hex.
// Identical to PassWall2's subscribe.lua header construction.
//
// Both inputs are trimmed: /tmp/sysinfo/model has a trailing newline on disk
// (25 bytes / 24 after trim on the AX3000T) and the TRIMMED form is what
// reproduces the production HWID. Hashing the untrimmed bytes yields a
// different device identity and the provider rejects the router.
func ComputeHWID(mac, model string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(mac) + "-" + strings.TrimSpace(model)))
	return hex.EncodeToString(sum[:])
}

// errRefusedUserAgent marks a request the User-Agent guard stopped before it
// was sent. It is returned as-is rather than retried: no attempt would differ.
type errRefusedUserAgent struct{ err error }

func (e errRefusedUserAgent) Error() string { return e.err.Error() }

// Fetch performs the HTTP GET and returns the raw body plus parsed metadata
// from the V2RayN-convention response headers.
func Fetch(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("subscription.Fetch: URL required")
	}
	// Pin HTTPS: a cleartext subscription lets an on-path attacker reshape which
	// traffic is proxied vs. sent direct.
	if u, err := url.Parse(opts.URL); err != nil || !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("subscription.Fetch: refusing non-https url: %s", RedactURL(opts.URL))
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
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxFetchBytes
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.MaxTimeout}
	}
	client = HTTPSOnlyRedirects(client)

	hwid := opts.HWID
	if hwid == "" && opts.MAC != "" && opts.Model != "" {
		hwid = ComputeHWID(opts.MAC, opts.Model)
	}
	// x-device-model must carry the same trimmed string that went into the HWID.
	model := strings.TrimSpace(opts.Model)
	// Build a fresh request per attempt — net/http forbids re-using a *http.Request
	// across Do() calls (per docs); reuse leads to undefined behavior on retry.
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		ua := opts.UserAgent
		signed := ""
		if opts.SignUserAgent != nil {
			s, err := opts.SignUserAgent(hwid, opts.URL)
			if err != nil {
				return nil, errRefusedUserAgent{fmt.Errorf("the router's own User-Agent: %w", err)}
			}
			ua, signed = s, s
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		req.Header.Set("x-device-os", opts.DeviceOS)
		if opts.OSRelease != "" {
			req.Header.Set("x-ver-os", opts.OSRelease)
		}
		if model != "" {
			req.Header.Set("x-device-model", model)
		}
		if hwid != "" {
			req.Header.Set("x-hwid", hwid)
		}
		for k, v := range opts.ExtraHeaders {
			req.Header.Set(k, v)
		}
		// Checked on the FINAL header, after ExtraHeaders had their say: a
		// "user-agent" key there overrides UserAgent, and it is the value on
		// the wire that the provider's sweep judges. Only the agent signed
		// just now may be the router's own; anything else was written down.
		check := uaguard.CheckConfigured
		if final := req.Header.Get("User-Agent"); signed != "" && final == signed {
			check = uaguard.Check
		}
		if err := check(req.Header.Get("User-Agent")); err != nil {
			return nil, errRefusedUserAgent{err}
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
			var refused errRefusedUserAgent
			if errors.As(err, &refused) {
				return nil, fmt.Errorf("subscription.Fetch: %w", refused.err)
			}
			// http.NewRequestWithContext returns *url.Error from url.Parse.
			return nil, fmt.Errorf("new request: %w", Sanitize(err, opts.URL))
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(opts.MaxBytes)))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if len(body) == opts.MaxBytes {
			// Exactly at the cap means we very likely truncated. A truncated
			// provider document must never be adopted.
			lastErr = fmt.Errorf("response hit the %d-byte read cap (likely truncated); raise subscription.maxBytes", opts.MaxBytes)
			continue
		}
		return buildFetchResult(opts.URL, resp, body), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("subscription.Fetch: unknown error")
	}
	// NEVER %w a raw transport error here: net/http wraps everything client.Do
	// touches in *url.Error, whose Error() prints the FULL url — bearer token
	// and all. See Sanitize.
	return nil, fmt.Errorf("subscription.Fetch: after %d attempts: %w", opts.Retries+1, Sanitize(lastErr, opts.URL))
}

// HTTPSOnlyRedirects is c refusing to follow a redirect to anything but
// https: the request carries a secret — a subscription's token in its URL, the
// device's identity in x-hwid — that a redirect to plain http would send in the
// clear. c's own redirect policy, if any, still has its say after.
func HTTPSOnlyRedirects(c *http.Client) *http.Client {
	cp := *c
	prev := c.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !strings.EqualFold(req.URL.Scheme, "https") {
			return fmt.Errorf("refusing a redirect to %s: not https", RedactURL(req.URL.String()))
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cp
}

// RedactURL returns a loggable form of rawURL: scheme + host only.
//
// Everything after the host — path, query, fragment and userinfo — is dropped,
// because that is where every subscription provider puts the bearer token
// (/sub/<uuid>, ?token=..., https://user:pass@host/...). Scheme and host are
// kept because they are what an operator actually needs to diagnose a failed
// fetch, and they are already visible to anyone watching the router's DNS.
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "<redacted-url>"
	}
	out := u.Scheme + "://" + u.Host
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		out += "/<redacted>"
	}
	return out
}

// Sanitize strips the secret-bearing parts of rawURL out of err.
//
// Go's *url.Error stringifies the whole URL, so a DNS blip, a captive portal or
// a TLS failure produces an error whose text contains the subscription bearer
// token. That error travels: job result -> panel database, and job journal ->
// /etc/vectra-controller-pro/state.json, which lib/upgrade/keep.d preserves into
// every sysupgrade backup. The router fetches the provider document itself
// precisely SO the panel never holds provider secrets (see cmd_agent_jobs.go),
// and leaking it through an error message defeats that design.
//
// The *url.Error is unwrapped rather than wrapped: its URL field is replaced
// with the redacted form and only the inner cause is kept. A literal scrub then
// catches anything the cause itself may have quoted.
func Sanitize(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = fmt.Errorf("%s %s: %w", ue.Op, RedactURL(ue.URL), ue.Err)
	}
	msg := err.Error()
	if scrubbed := Scrub(msg, rawURL); scrubbed != msg {
		// Deliberately drops the wrapped chain: an unwrapped error could be
		// re-stringified by a caller and would leak again.
		return errors.New(scrubbed)
	}
	return err
}

// Scrub replaces every occurrence of the secret-bearing parts of rawURL in s
// with a redaction marker. It is the belt-and-braces pass for text the fetcher
// never saw — command output, third-party error strings, job results.
//
// Over-redacting an error message is harmless; under-redacting one puts a
// bearer token in the panel database, so the needle list is deliberately wide
// (full URL, scheme-less form, path+query, bare query, userinfo).
func Scrub(s, rawURL string) string {
	for _, needle := range secretNeedles(rawURL) {
		s = strings.ReplaceAll(s, needle, "<redacted>")
	}
	return s
}

// SecretParts are the substrings of rawURL that carry its secret — the URL
// whole, without its scheme, its path and query, its user info — longest
// first; nothing for a URL that is only a scheme and a host.
func SecretParts(rawURL string) []string { return secretNeedles(rawURL) }

// secretNeedles lists the substrings of rawURL that must never appear in
// operator-visible output, longest first so a replacement can never leave a
// shorter needle's remnant behind.
func secretNeedles(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil
	}
	// Scheme and host are NOT secrets and must never become needles: scrubbing
	// them would eat the very "https://host/<redacted>" form RedactURL builds.
	// A URL with no path, query, fragment or userinfo carries nothing to hide.
	if u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		// Guard against degenerate needles ("/", "") that would shred every
		// message they touched.
		if len(v) < 4 || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(rawURL)
	add(strings.TrimPrefix(rawURL, u.Scheme+"://"))
	if u.User != nil {
		add(u.User.String())
	}
	if ru := u.RequestURI(); ru != "/" {
		add(ru)
		add(u.EscapedPath())
	}
	add(u.RawQuery)
	add(u.Fragment)
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
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

	// Classify the payload here so callers see the JSON entries without a
	// second decode pass. Entries hold provider bytes VERBATIM.
	payload, format := DecodeBody(body, r.ContentType)
	r.BodyFormat = format
	if format == FormatJSON {
		if entries, remarks, err := SplitJSONEntries(payload); err == nil {
			r.Entries = entries
			r.Remarks = remarks
		}
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
