package localctl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Unix socket paths are limited to ~104 bytes on macOS; t.TempDir() under
// /var/folders can exceed that, so use a short private directory.
func shortSocketPath(t *testing.T) string {
	dir, err := os.MkdirTemp("/tmp", "lc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "ui.sock")
}

func TestSocketRoundTripIsPrivate(t *testing.T) {
	path := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, path, func(_ context.Context, req SocketRequest) SocketResponse {
			if req.Op != OpRuntime {
				return SocketResponse{Code: "invalid_params"}
			}
			return SocketResponse{OK: true, Runtime: &Runtime{ControllerPID: 42, Engine: Engine{State: "running"}}}
		})
	}()
	var resp SocketResponse
	var err error
	for i := 0; i < 100; i++ {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Second)
		resp, err = Call(cctx, path, SocketRequest{Op: OpRuntime})
		ccancel()
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Runtime == nil || resp.Runtime.ControllerPID != 42 {
		t.Fatalf("resp = %+v", resp)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", fi.Mode().Perm())
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve returned %v on shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop with its context")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("socket file left behind after shutdown")
	}
}

func TestCallReportsADaemonThatIsNotThere(t *testing.T) {
	_, err := Call(context.Background(), shortSocketPath(t), SocketRequest{Op: OpRuntime})
	if !errors.Is(err, ErrDaemonDown) {
		t.Fatalf("err = %v, want ErrDaemonDown", err)
	}
}

func TestServeRefusesToReplaceSomethingThatIsNotASocket(t *testing.T) {
	path := shortSocketPath(t)
	if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Serve(ctx, path, nil); err == nil {
		t.Fatal("Serve replaced a regular file")
	}
	if b, _ := os.ReadFile(path); string(b) != "state" {
		t.Fatal("the file was modified")
	}
}

func TestCallHonoursTheDeadlineOfASlowHandler(t *testing.T) {
	path := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, path, func(ctx context.Context, _ SocketRequest) SocketResponse {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		return SocketResponse{OK: true}
	})
	time.Sleep(50 * time.Millisecond)
	cctx, ccancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer ccancel()
	start := time.Now()
	if _, err := Call(cctx, path, SocketRequest{Op: OpReapply}); err == nil {
		t.Fatal("a slow handler did not time the call out")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline not applied")
	}
}
