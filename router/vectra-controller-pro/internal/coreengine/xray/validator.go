package xray

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/memguard"
)

// Validator runs `xray run -test -c <file>` against a candidate config.
// It is the write gate: a document that Xray refuses is never installed, so
// the previously-good config stays live.
type Validator struct {
	// Binary is the xray executable (or the vctl-xray-wrapper that execs it).
	Binary string
	// AssetDir is exported to the child as XRAY_LOCATION_ASSET.
	AssetDir string
	// Timeout bounds the check. 0 = 20s.
	Timeout time.Duration
	// OOMScoreAdj, when not 0, is written for the check's xray. It runs next
	// to the xray carrying the router and holds as much again of its own
	// (~15 MB for the fleet's configuration); if that tips the router over,
	// the check is the process to lose (memguard.TransientAdj).
	OOMScoreAdj int
	// MemFloorKB, when not 0, is the MemAvailable below which the check is
	// not started: the configuration is refused as "not now", nothing is
	// installed, and the caller tries again later (memguard.HeavyFloorKB).
	MemFloorKB uint64
	// ReadMem reads the router's memory; nil = memguard.Read.
	ReadMem func() (memguard.Info, error)
}

// ErrLowMemory is wrapped by Test when there was too little memory to check.
var ErrLowMemory = fmt.Errorf("too little free memory to check a configuration now")

// Test writes candidate to a temp file and asks Xray to parse it.
// Returns nil only when Xray reports the config is usable.
func (v Validator) Test(ctx context.Context, candidate []byte) error {
	if v.Binary == "" {
		return fmt.Errorf("xray validate: no xray binary configured (refusing to install an unchecked config)")
	}
	if v.MemFloorKB > 0 {
		read := v.ReadMem
		if read == nil {
			read = memguard.Read
		}
		if in, err := read(); err == nil && in.AvailableKB < v.MemFloorKB {
			return fmt.Errorf("xray validate: %w (%d MiB free, %d MiB wanted); nothing was changed, it is tried again later",
				ErrLowMemory, memguard.MiB(in.AvailableKB), memguard.MiB(v.MemFloorKB))
		}
	}
	dir, err := os.MkdirTemp("", "vctl-xray-test-")
	if err != nil {
		return fmt.Errorf("xray validate: tempdir: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "candidate.json")
	if err := os.WriteFile(path, candidate, 0o600); err != nil {
		return fmt.Errorf("xray validate: write candidate: %w", err)
	}

	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, v.Binary, "run", "-test", "-c", path)
	cmd.Env = config.XrayAssetEnv(os.Environ(), v.AssetDir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("xray validate: start %s: %w", v.Binary, err)
	}
	if v.OOMScoreAdj != 0 {
		_ = memguard.SetOOMScoreAdj(cmd.Process.Pid, v.OOMScoreAdj)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("xray validate: %s run -test rejected the config: %w: %s",
			v.Binary, err, lastLines(out.String(), 800))
	}
	return nil
}

func lastLines(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return "..." + s[len(s)-max:]
}
