package bugreport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sender posts reports to Vectra Connect (docs/CONNECT-BUG-REPORTS.md).
type Sender struct {
	URL       string
	Client    *http.Client
	Sign      func(hwid, rawURL string) (string, error)
	HWID      string
	Model     string
	OSRelease string
	Now       func() time.Time
}

// Outcome is what became of one send: Done takes the report out of the
// spool (delivered, or refused for good); otherwise it goes again after
// Retry.
type Outcome struct {
	Status int
	Done   bool
	Retry  time.Duration
	Err    error
}

var backoff = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// clockSet: before this the router's clock is not set (no NTP yet), and a
// token made now would be refused.
var clockSet = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Send posts e once.
func (s Sender) Send(ctx context.Context, e Entry) Outcome {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if now().Before(clockSet) {
		return Outcome{Retry: 10 * time.Minute, Err: errors.New("the clock is not set")}
	}
	body, err := e.Report.Encode()
	if err != nil {
		return Outcome{Done: true, Err: fmt.Errorf("encode: %w", err)}
	}
	sum := sha256.Sum256(body)
	sep := "?"
	if strings.Contains(s.URL, "?") {
		sep = "&"
	}
	u := s.URL + sep + "b=" + hex.EncodeToString(sum[:])
	ua, err := s.Sign(s.HWID, u)
	if err != nil {
		return Outcome{Retry: 10 * time.Minute, Err: fmt.Errorf("sign: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return Outcome{Done: true, Err: err}
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-hwid", s.HWID)
	req.Header.Set("x-device-os", "OpenWrt")
	if s.Model != "" {
		req.Header.Set("x-device-model", s.Model)
	}
	if s.OSRelease != "" {
		req.Header.Set("x-ver-os", s.OSRelease)
	}
	req.Header.Set("x-vectra-report-id", e.Report.ReportID)
	resp, err := s.Client.Do(req)
	if err != nil {
		return Outcome{Retry: backoff[min(e.Meta.Attempts, len(backoff)-1)], Err: err}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	o := Outcome{Status: resp.StatusCode}
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		o.Done = true
	case c == 400, c == 413, c == 422:
		o.Done, o.Err = true, fmt.Errorf("refused for good: %s", resp.Status)
	case c == 401, c == 403:
		o.Retry, o.Err = 6*time.Hour, fmt.Errorf("not recognised: %s", resp.Status)
	case c == 404:
		o.Retry, o.Err = time.Hour, errors.New("no endpoint yet")
	case c == 429:
		o.Retry, o.Err = retryAfter(resp.Header.Get("Retry-After"), now()), errors.New("too many")
	default:
		o.Retry, o.Err = backoff[min(e.Meta.Attempts, len(backoff)-1)], fmt.Errorf("server: %s", resp.Status)
	}
	return o
}

func retryAfter(v string, now time.Time) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return time.Hour
}
