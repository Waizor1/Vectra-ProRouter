package localctl

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"vectra-controller-pro/internal/vault"
)

// EntriesCache is the provider's whole array from the last fetch. The entries
// carry node credentials, exactly like the provider document the daemon
// already persists, so the file is written 0600 and never leaves the router.
type EntriesCache struct {
	SubscriptionID string            `json:"subscriptionId"`
	FetchedAt      time.Time         `json:"fetchedAt"`
	Remarks        []string          `json:"remarks"`
	Entries        []json.RawMessage `json:"entries"`
}

// EntrySummary is what the UI may see about one location: no credentials, no
// addresses — a name and two counts.
type EntrySummary struct {
	Index         int    `json:"index"`
	Remark        string `json:"remark"`
	NodeCount     int    `json:"nodeCount"`
	BalancerCount int    `json:"balancerCount"`
	// Digest is sha256 of the entry's exact bytes — the same digest the
	// applier records for the document it installs, which is how the daemon
	// tells which location is running.
	Digest string `json:"digest"`
}

// EntriesIndex is the small, credential-free sidecar the UI reads, so a UI
// poll never has to decompress and parse half a megabyte.
type EntriesIndex struct {
	SubscriptionID string         `json:"subscriptionId"`
	FetchedAt      time.Time      `json:"fetchedAt"`
	Digest         string         `json:"digest"`
	Entries        []EntrySummary `json:"entries"`
}

// maxEntriesBytes bounds what a decompressed cache may expand to, so a
// corrupted or hostile file cannot balloon in a 234 MB router's RAM.
const maxEntriesBytes = 16 << 20

func (c *EntriesCache) digest() string {
	h := sha256.New()
	for i, e := range c.Entries {
		fmt.Fprintf(h, "%d\x00%s\x00", i, c.Remarks[i])
		h.Write(e)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Summarize counts dialling outbounds and balancers per entry.
func Summarize(c *EntriesCache) []EntrySummary {
	out := make([]EntrySummary, 0, len(c.Entries))
	for i, e := range c.Entries {
		var doc struct {
			Outbounds []struct {
				Protocol string `json:"protocol"`
			} `json:"outbounds"`
			Routing struct {
				Balancers []json.RawMessage `json:"balancers"`
			} `json:"routing"`
		}
		_ = json.Unmarshal(e, &doc)
		nodes := 0
		for _, ob := range doc.Outbounds {
			switch ob.Protocol {
			case "freedom", "blackhole", "loopback", "dns", "":
			default:
				nodes++
			}
		}
		remark := ""
		if i < len(c.Remarks) {
			remark = c.Remarks[i]
		}
		sum := sha256.Sum256(e)
		out = append(out, EntrySummary{Index: i, Remark: remark, NodeCount: nodes, BalancerCount: len(doc.Routing.Balancers), Digest: hex.EncodeToString(sum[:])})
	}
	return out
}

// SaveEntries persists the cache and its index. An unchanged array (same
// digest as the index on disk) is not rewritten: the flash is not worn by a
// nightly refresh that brought nothing new. Returns whether it wrote.
func SaveEntries(cachePath, indexPath string, c *EntriesCache) (bool, error) {
	if len(c.Entries) == 0 {
		return false, fmt.Errorf("localctl: refusing to cache an empty provider array")
	}
	if len(c.Remarks) != len(c.Entries) {
		return false, fmt.Errorf("localctl: %d remarks for %d entries", len(c.Remarks), len(c.Entries))
	}
	d := c.digest()
	if idx, err := LoadEntriesIndex(indexPath); err == nil && idx.Digest == d && idx.SubscriptionID == c.SubscriptionID {
		if raw, err := vault.ReadFile(cachePath); err == nil {
			clear(raw)
			// Same array: the half-megabyte cache is not rewritten, but the
			// small index learns when it was last confirmed, which is what the
			// UI shows as "updated".
			idx.FetchedAt = c.FetchedAt
			if ir, err := json.MarshalIndent(idx, "", "  "); err == nil {
				_ = WriteFileAtomic(indexPath, append(ir, '\n'), 0o644)
			}
			return false, nil
		}
	}
	raw, err := encodeEntries(c)
	if err != nil {
		return false, err
	}
	defer clear(raw)
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return false, err
	}
	if err := zw.Close(); err != nil {
		return false, err
	}
	defer func() { clear(gz.Bytes()) }()
	// The cache first, the index second: an index never describes a cache
	// that is not on disk yet.
	if err := vault.WriteFile(cachePath, gz.Bytes()); err != nil {
		return false, err
	}
	idx := EntriesIndex{SubscriptionID: c.SubscriptionID, FetchedAt: c.FetchedAt, Digest: d, Entries: Summarize(c)}
	ir, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return false, err
	}
	if err := WriteFileAtomic(indexPath, append(ir, '\n'), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// LoadEntries reads the full cache.
func LoadEntries(cachePath string) (*EntriesCache, error) {
	data, err := vault.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("localctl: %s: %w", cachePath, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxEntriesBytes+1))
	if err != nil {
		return nil, fmt.Errorf("localctl: %s: %w", cachePath, err)
	}
	if len(raw) > maxEntriesBytes {
		return nil, fmt.Errorf("localctl: %s expands beyond %d bytes", cachePath, maxEntriesBytes)
	}
	c, err := decodeEntries(raw)
	if err != nil {
		return nil, fmt.Errorf("localctl: %s: %w", cachePath, err)
	}
	if len(c.Entries) == 0 || len(c.Remarks) != len(c.Entries) {
		return nil, fmt.Errorf("localctl: %s holds %d entries and %d remarks", cachePath, len(c.Entries), len(c.Remarks))
	}
	return c, nil
}

// MigrateEntries validates the legacy gzip container before sealing it.
func MigrateEntries(path string) error {
	return vault.MigrateFile(path, func(data []byte) error {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer zr.Close()
		raw, err := io.ReadAll(io.LimitReader(zr, maxEntriesBytes+1))
		if err != nil {
			return err
		}
		defer clear(raw)
		if len(raw) > maxEntriesBytes {
			return errors.New("entries cache too large")
		}
		c, err := decodeEntries(raw)
		if err != nil {
			return err
		}
		if len(c.Entries) == 0 || len(c.Entries) != len(c.Remarks) {
			return errors.New("invalid entries cache")
		}
		return nil
	})
}

// The cache container keeps every entry's bytes EXACTLY as the provider sent
// them. encoding/json cannot: marshalling a json.RawMessage compacts it and
// escapes <, > and &, so a re-read entry would differ from the one the daemon
// installed — a different digest, and a document that is no longer the
// provider's own bytes, which is the one property this codebase protects
// (see internal/coreengine/xray/doc.go).
//
//	VCTLENTRIES1\n
//	<header JSON: subscriptionId, fetchedAt, remarks>\n
//	<decimal length>\n<entry bytes>   ... once per entry
const entriesMagic = "VCTLENTRIES1\n"

type entriesHeader struct {
	SubscriptionID string    `json:"subscriptionId"`
	FetchedAt      time.Time `json:"fetchedAt"`
	Remarks        []string  `json:"remarks"`
}

func encodeEntries(c *EntriesCache) ([]byte, error) {
	h, err := json.Marshal(entriesHeader{c.SubscriptionID, c.FetchedAt, c.Remarks})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(entriesMagic)
	b.Write(h)
	b.WriteByte('\n')
	for _, e := range c.Entries {
		fmt.Fprintf(&b, "%d\n", len(e))
		b.Write(e)
	}
	return b.Bytes(), nil
}

func decodeEntries(raw []byte) (*EntriesCache, error) {
	if !bytes.HasPrefix(raw, []byte(entriesMagic)) {
		return nil, errors.New("not an entries cache")
	}
	raw = raw[len(entriesMagic):]
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, errors.New("truncated header")
	}
	var h entriesHeader
	if err := json.Unmarshal(raw[:nl], &h); err != nil {
		return nil, err
	}
	raw = raw[nl+1:]
	c := &EntriesCache{SubscriptionID: h.SubscriptionID, FetchedAt: h.FetchedAt, Remarks: h.Remarks}
	for len(raw) > 0 {
		if len(c.Entries) >= len(c.Remarks) {
			return nil, errors.New("more entries than the header names")
		}
		nl := bytes.IndexByte(raw, '\n')
		if nl < 0 || nl > 10 {
			return nil, errors.New("malformed entry length")
		}
		n, err := strconv.Atoi(string(raw[:nl]))
		if err != nil || n <= 0 || n > len(raw)-nl-1 {
			return nil, errors.New("malformed, empty or truncated entry")
		}
		c.Entries = append(c.Entries, json.RawMessage(raw[nl+1:nl+1+n]))
		raw = raw[nl+1+n:]
	}
	return c, nil
}

// HasRemark reports whether the array offers a location with this remark.
func (idx *EntriesIndex) HasRemark(remark string) bool {
	for _, e := range idx.Entries {
		if e.Remark == remark {
			return true
		}
	}
	return false
}

// LoadEntriesIndex reads the credential-free index.
func LoadEntriesIndex(indexPath string) (*EntriesIndex, error) {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var idx EntriesIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("localctl: %s: %w", indexPath, err)
	}
	return &idx, nil
}

// ErrNoEntry is returned by Resolve when a remark chosen on the router is no
// longer in the array.
var ErrNoEntry = errors.New("localctl: the location chosen on the router is not in the subscription any more")

// Resolve picks the entry to run: the router's choice when there is one and it
// still exists, otherwise the panel's (remark, then index). stale reports a
// router choice that could not be honoured.
func Resolve(remarks []string, ov Overrides, panelRemark string, panelIndex int) (idx int, local, stale bool, err error) {
	if ov.HasEntry() {
		// Two locations with one remark: the index the choice was made at
		// tells them apart, while it still points at that remark.
		if i := ov.EntryIndex; i != nil && *i >= 0 && *i < len(remarks) && remarks[*i] == ov.EntryRemark {
			return *i, true, false, nil
		}
		for i, r := range remarks {
			if r == ov.EntryRemark {
				return i, true, false, nil
			}
		}
		stale = true
	}
	if panelRemark != "" {
		for i, r := range remarks {
			if r == panelRemark {
				return i, false, stale, nil
			}
		}
		return 0, false, stale, fmt.Errorf("no entry with remarks=%q (have %d entries)", panelRemark, len(remarks))
	}
	if panelIndex < 0 || panelIndex >= len(remarks) {
		return 0, false, stale, fmt.Errorf("entry index %d out of range (have %d entries)", panelIndex, len(remarks))
	}
	return panelIndex, false, stale, nil
}
