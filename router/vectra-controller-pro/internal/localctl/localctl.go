// Package localctl holds what the router's own UI may change, and the cache
// that lets it change the location without asking the provider again.
//
// The panel stays the source of the operator config. What lives here are
// LOCAL choices made on the router — a location other than the panel's, a
// balancer pinned to one node, a probe interval, the owner's own sites —
// plus the full provider array from the last fetch, so switching among its
// locations is a local re-render rather than another subscription request
// (each of which the provider logs against the customer's device).
//
// Two processes touch these files: the daemon and `vctl rpcd`, one short-lived
// process per UI call. Every read-modify-write therefore runs under an
// exclusive flock on a sidecar lock file, and every write is atomic
// (tmp + fsync + rename), so neither can observe or produce half a file.
package localctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// Default locations. /etc survives reboots and sysupgrade (the package's
// keep.d lists the directory); /var/run is tmpfs and holds only live state.
const (
	DefaultOverridesPath    = "/etc/vectra-controller-pro/local-overrides.json"
	DefaultEntriesPath      = "/etc/vectra-controller-pro/provider-entries.json.gz"
	DefaultEntriesIndexPath = "/etc/vectra-controller-pro/provider-entries.index.json"
	DefaultSocketPath       = "/var/run/vectra-controller-pro/ui.sock"
)

// Overrides are the choices made on the router itself.
type Overrides struct {
	// EntryRemark is the location chosen on the router, matched exactly
	// against the provider's "remarks". The remark, not the index, is what is
	// honoured: the provider reorders its array, and an index would silently
	// land on a different country. EntryIndex is kept for display only.
	EntryDigest string `json:"entryDigest,omitempty"`
	EntryRemark string `json:"entryRemark,omitempty"`
	EntryIndex  *int   `json:"entryIndex,omitempty"`
	// Pins maps a balancer tag to the outbound tag it is pinned to.
	Pins map[string]string `json:"pins,omitempty"`
	// ProbeIntervalSec overrides the observatory probe interval; 0 = the
	// router default.
	ProbeIntervalSec int `json:"probeIntervalSec,omitempty"`
	// Direct and Proxy are the owner's own sites ("My sites"), in the
	// canonical form of internal/sites: always without the VPN, always
	// through it. Part of every render (xray.SpliceOptions.Rules).
	ConnectRules bool     `json:"connectRules,omitempty"`
	Direct       []string `json:"direct,omitempty"`
	Proxy        []string `json:"proxy,omitempty"`
	// Services are the owner's country per service ("tiktok": "DE"); a
	// service not named runs on the entry's own path.
	ServiceEntries map[string]string `json:"serviceEntries,omitempty"`
	Services       map[string]string `json:"services,omitempty"`
	UpdatedAt      time.Time         `json:"updatedAt,omitempty"`
}

// HasEntry reports whether a location was chosen on the router.
func (o Overrides) HasEntry() bool { return o.EntryRemark != "" }

// PinnedBalancers returns the pinned balancer tags in a stable order.
func (o Overrides) PinnedBalancers() []string {
	out := make([]string, 0, len(o.Pins))
	for b := range o.Pins {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

// LoadOverrides reads the overrides; a missing file is the zero value.
func LoadOverrides(path string) (Overrides, error) {
	var o Overrides
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return Overrides{}, fmt.Errorf("localctl: %s is not valid: %w", path, err)
	}
	return o, nil
}

// UpdateOverrides applies fn to the current overrides under the lock and
// writes the result. fn returning an error aborts without writing.
func UpdateOverrides(path string, fn func(*Overrides) error) (Overrides, error) {
	var out Overrides
	err := withLock(path, func() error {
		o, err := LoadOverrides(path)
		if err != nil {
			return err
		}
		if err := fn(&o); err != nil {
			return err
		}
		o.UpdatedAt = time.Now().UTC()
		if len(o.Pins) == 0 {
			o.Pins = nil
		}
		if len(o.Direct) == 0 {
			o.Direct = nil
		}
		if len(o.Proxy) == 0 {
			o.Proxy = nil
		}
		if len(o.Services) == 0 {
			o.Services = nil
		}
		raw, err := json.MarshalIndent(o, "", "  ")
		if err != nil {
			return err
		}
		if err := WriteFileAtomic(path, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

// ClearOverrides removes the overrides file under the lock, so no update in
// flight lands in the middle of it. It reports whether there was one.
func ClearOverrides(path string) (bool, error) {
	removed := false
	err := withLock(path, func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		removed = err == nil
		return err
	})
	return removed, err
}

// withLock runs fn holding an exclusive flock on path+".lock".
func withLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("localctl: lock %s: %w", path, err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// WriteFileAtomic writes data via tmp + fsync + rename, then fsyncs the
// directory, so a power cut leaves either the old file or the new one.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
