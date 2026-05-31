// Package geo handles Xray geoip.dat / geosite.dat updates: HTTP download,
// SHA256 verification (when known), atomic swap into the asset directory.
package geo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Asset describes one file to update.
type Asset struct {
	Filename       string // e.g. "geoip.dat"
	URL            string
	ExpectedSHA256 string // optional; empty = no verification
}

// Result captures what UpdateOne did.
type Result struct {
	Asset     Asset
	OldExists bool
	Updated   bool   // true if the file changed (or was created)
	SHA256    string // sha256 of the file we ended up with
	Bytes     int64
	Took      time.Duration
	Error     error
}

// UpdateOne downloads one asset into dir. On success, the file at
// dir/asset.Filename is the new bytes (or unchanged if hash matched what's
// already there).
func UpdateOne(ctx context.Context, dir string, a Asset, hc *http.Client) Result {
	r := Result{Asset: a}
	start := time.Now()
	defer func() { r.Took = time.Since(start) }()
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.Error = fmt.Errorf("mkdir: %w", err)
		return r
	}
	target := filepath.Join(dir, a.Filename)
	if existing, err := os.Stat(target); err == nil {
		r.OldExists = true
		if existing.Size() > 0 && a.ExpectedSHA256 != "" {
			if cur, err := sha256File(target); err == nil && cur == a.ExpectedSHA256 {
				r.SHA256 = cur
				r.Bytes = existing.Size()
				return r
			}
		}
	}
	tmp := target + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		r.Error = fmt.Errorf("create tmp: %w", err)
		return r
	}
	// Single cleanup point: unless we successfully renamed, tmp must go.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		_ = f.Close()
		r.Error = fmt.Errorf("new request: %w", err)
		return r
	}
	resp, err := hc.Do(req)
	if err != nil {
		_ = f.Close()
		r.Error = fmt.Errorf("get: %w", err)
		return r
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		_ = f.Close()
		r.Error = fmt.Errorf("http %d", resp.StatusCode)
		return r
	}
	h := sha256.New()
	mw := io.MultiWriter(f, h)
	n, err := io.Copy(mw, resp.Body)
	if err != nil {
		_ = f.Close()
		r.Error = fmt.Errorf("copy: %w", err)
		return r
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		r.Error = fmt.Errorf("fsync: %w", err)
		return r
	}
	if err := f.Close(); err != nil {
		r.Error = fmt.Errorf("close: %w", err)
		return r
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if a.ExpectedSHA256 != "" && sum != a.ExpectedSHA256 {
		r.Error = fmt.Errorf("sha256 mismatch: got %s want %s", sum, a.ExpectedSHA256)
		return r
	}
	if err := os.Rename(tmp, target); err != nil {
		r.Error = fmt.Errorf("rename: %w", err)
		return r
	}
	renamed = true
	// Best-effort dir fsync.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	r.Updated = true
	r.SHA256 = sum
	r.Bytes = n
	return r
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
