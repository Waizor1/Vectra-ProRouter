package xray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestValidatorPrivateStdinAndRejectScrubbing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	p := filepath.Join(dir, "xray")
	script := "#!/bin/sh\n[ \"$1 $2 $3 $4\" = 'run -test -c stdin:' ] || exit 41\nIFS= read -r value\n[ \"$value\" = 'synthetic-private-secret' ] || exit 42\nprintf '%s' \"$value\" >&2\nexit 1\n"
	if err := os.WriteFile(p, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	err := (Validator{Binary: p}).Test(context.Background(), []byte("synthetic-private-secret\n"))
	if err == nil || strings.Contains(err.Error(), "synthetic-private-secret") {
		t.Fatalf("unsafe rejection: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "xray" {
		t.Fatalf("validator created artifact: %v", entries)
	}
}

// Opt in with a verified native official Xray binary. No network or listening
// sockets are needed to check the stdin protocol and JSON format selection.
func TestValidatorActualXrayPrivateStdin(t *testing.T) {
	binary := os.Getenv("VECTRA_TEST_XRAY")
	if binary == "" {
		t.Skip("set VECTRA_TEST_XRAY to a verified native Xray binary")
	}
	v := Validator{Binary: binary}
	if err := v.Test(context.Background(), []byte(`{"log":{"loglevel":"none"},"outbounds":[{"protocol":"freedom","tag":"direct"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := v.Test(context.Background(), []byte(`{"synthetic-private-secret"`)); err == nil || strings.Contains(err.Error(), "synthetic-private-secret") {
		t.Fatalf("unsafe rejection: %v", err)
	}
}
