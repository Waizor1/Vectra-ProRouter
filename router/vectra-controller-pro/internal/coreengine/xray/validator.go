package xray

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/memguard"
)

// Validator runs `xray run -test -c stdin:` against a candidate config.
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

// Test sends candidate through a private stdin pipe and asks Xray to parse it.
// Child output is discarded because parse errors may contain credentials.
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

	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, v.Binary, "run", "-test", "-c", "stdin:")
	cmd.Env = config.XrayAssetEnv(os.Environ(), v.AssetDir)
	cmd.Stdin = bytes.NewReader(candidate)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("xray validate: start %s: %w", v.Binary, err)
	}
	if v.OOMScoreAdj != 0 {
		_ = memguard.SetOOMScoreAdj(cmd.Process.Pid, v.OOMScoreAdj)
	}
	if err := cmd.Wait(); err != nil {
		if runCtx.Err() != nil {
			return fmt.Errorf("xray validate: check canceled or timed out: %w", runCtx.Err())
		}
		return fmt.Errorf("xray validate: configuration rejected: %w", err)
	}
	return nil
}
