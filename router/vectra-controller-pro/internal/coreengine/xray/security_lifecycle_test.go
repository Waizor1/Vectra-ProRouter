package xray

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidatorDescendantHelper(t *testing.T) {
	if os.Getenv("VECTRA_SYNTHETIC_PIPE_CHILD") != "1" {
		return
	}
	cmd := exec.Command("/bin/sleep", "2")
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		os.Exit(5)
	}
	if err := os.WriteFile(os.Getenv("VECTRA_SYNTHETIC_PIPE_READY"), []byte("synthetic descendant started"), 0600); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}
func TestValidatorRejectsDescendantHeldStdin(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	t.Setenv("VECTRA_SYNTHETIC_PIPE_CHILD", "1")
	t.Setenv("VECTRA_SYNTHETIC_PIPE_READY", ready)
	binary := filepath.Join(dir, "fake-validator")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestValidatorDescendantHelper$\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := (Validator{Binary: binary, Timeout: time.Second}).Test(context.Background(), []byte(strings.Repeat("SYNTHETIC-ONLY", 100000)))
	elapsed := time.Since(start)
	if _, e := os.Stat(ready); e != nil {
		t.Fatal("fixture descendant did not start")
	}
	t.Log("synthetic descendant retained stdin; validator duration", elapsed, "result", err)
	if err == nil {
		t.Fatal("incomplete stdin accepted as valid")
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatal("validator deadline not bounding descendant-held stdin copy")
	}
}
