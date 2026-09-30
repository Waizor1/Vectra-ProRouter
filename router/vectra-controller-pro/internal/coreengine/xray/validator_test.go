package xray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/memguard"
)

func fakeXrayTest(t *testing.T, exit string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'checked'\nexit "+exit+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A check that would take the router below its heavy floor is not started:
// the configuration is refused as "not now", and says so.
func TestValidatorRefusesToCheckOnLowMemory(t *testing.T) {
	v := Validator{Binary: fakeXrayTest(t, "0"), MemFloorKB: 24 * 1024, OOMScoreAdj: memguard.TransientAdj,
		ReadMem: func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 20 * 1024}, nil }}
	err := v.Test(context.Background(), []byte(`{}`))
	if !errors.Is(err, ErrLowMemory) {
		t.Fatalf("20 MiB free, floor 24: err %v, want ErrLowMemory", err)
	}
	v.ReadMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 60 * 1024}, nil }
	if err := v.Test(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("60 MiB free: %v", err)
	}
	// No meminfo (not Linux): the check runs.
	v.ReadMem = func() (memguard.Info, error) { return memguard.Info{}, errors.New("no /proc") }
	if err := v.Test(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("no meminfo: %v", err)
	}
	v.Binary = fakeXrayTest(t, "1")
	if err := v.Test(context.Background(), []byte(`{}`)); err == nil || errors.Is(err, ErrLowMemory) {
		t.Fatalf("a rejected config: err %v", err)
	}
}
