package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"vectra-controller-pro/internal/config"
)

func TestPrivateConfigFreshEachStart(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "xray")
	// The child asserts the public argv then checks synthetic data from stdin.
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1 $2 $3\" = 'run -c stdin:' ] || exit 41\nIFS= read -r value\n[ \"$value\" = \"$EXPECT_SYNTHETIC\" ] || exit 42\nprintf '%s' \"$value\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := NewProcess(config.Process{XrayBinary: script, ConfigFile: filepath.Join(dir, "runtime.json"), LogDir: filepath.Join(dir, "logs")})
	n := 0
	p.SetConfigSource(func() ([]byte, error) {
		n++
		if n == 1 {
			return []byte("first\n"), nil
		}
		return []byte("refreshed\n"), nil
	})
	for _, value := range []string{"first", "refreshed"} {
		p.SetChildEnv([]string{"EXPECT_SYNTHETIC=" + value})
		if err := p.startOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.cmd.Args[0] != "vctl-xray-private" {
			t.Fatalf("missing scoped ownership argv: %v", p.cmd.Args)
		}
		if err, _ := p.waitOnce(); err != nil {
			t.Fatal(err)
		}
	}
	if n != 2 {
		t.Fatalf("source calls %d", n)
	}
	if err := p.WriteXrayConfig([]byte("secret")); err == nil {
		t.Fatal("file write allowed")
	}
	if _, err := os.Stat(p.configFile); !os.IsNotExist(err) {
		t.Fatalf("runtime artifact: %v", err)
	}
	if _, err := os.Stat(p.logDir); !os.IsNotExist(err) {
		t.Fatalf("child output artifact: %v", err)
	}
}

func TestPrivateConfigUnavailableFailsClosed(t *testing.T) {
	p := NewProcess(config.Process{XrayBinary: "/must-not-run"})
	p.SetConfigSource(func() ([]byte, error) { return nil, errors.New("synthetic-private-secret") })
	if err := p.startOnce(context.Background()); err == nil || err.Error() != "supervisor: private configuration unavailable" {
		t.Fatalf("unsafe source error: %v", err)
	}
	p.SetConfigSource(func() ([]byte, error) { return nil, nil })
	if err := p.startOnce(context.Background()); err == nil {
		t.Fatal("empty config started")
	}
}

func TestPrivateConfigActualXrayRestart(t *testing.T) {
	binary := os.Getenv("VECTRA_TEST_XRAY")
	if binary == "" {
		t.Skip("set VECTRA_TEST_XRAY to a verified native Xray binary")
	}
	p := NewProcess(config.Process{XrayBinary: binary, ConfigFile: filepath.Join(t.TempDir(), "must-not-exist.json")})
	n := 0
	p.SetConfigSource(func() ([]byte, error) {
		n++
		tag := "first"
		if n > 1 {
			tag = "refreshed"
		}
		return []byte(`{"log":{"loglevel":"none"},"outbounds":[{"protocol":"freedom","tag":"` + tag + `"}]}`), nil
	})
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := p.startOnce(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		// Keep the official child alive long enough to parse stdin. No listener or
		// outbound connection is configured; cancellation bounds the local check.
		time.Sleep(100 * time.Millisecond)
		if err := p.Reload(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		err, _ := p.waitOnce()
		cancel()
		if err != nil {
			t.Fatalf("actual Xray start/restart: %v", err)
		}
	}
	if n != 2 {
		t.Fatalf("source calls %d", n)
	}
	if _, err := os.Stat(p.configFile); !os.IsNotExist(err) {
		t.Fatalf("runtime artifact: %v", err)
	}
}
