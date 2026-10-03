package retire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opkg runs with exactly the arguments given — no flag added — its output
// captured, and a failure is an error.
func TestRunOpkgRunsOpkgAsGiven(t *testing.T) {
	bin := t.TempDir()
	write(t, filepath.Join(bin, "opkg"), "#!/bin/sh\necho \"opkg $*\"\necho \"to stderr\" >&2\n[ \"$1\" = remove ] || exit 3\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := RunOpkg(context.Background(), "remove", "luci-app-passwall2", "geoview")
	if err != nil {
		t.Fatalf("err %v, out %s", err, out)
	}
	if !strings.Contains(string(out), "opkg remove luci-app-passwall2 geoview\n") || !strings.Contains(string(out), "to stderr") {
		t.Fatalf("output %q", out)
	}
	if _, err := RunOpkg(context.Background(), "install", "x"); err == nil {
		t.Fatal("a failing opkg read as a success")
	}
}

// vctl's own end (its context cancelled, procd stopping it) does not end an
// opkg half way through the package database; only its time does.
func TestRunOpkgOutlivesItsCaller(t *testing.T) {
	bin := t.TempDir()
	done := filepath.Join(bin, "done")
	write(t, filepath.Join(bin, "opkg"), "#!/bin/sh\nsleep 1\ntrue > '"+done+"'\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunOpkg(ctx, "remove", "x"); err != nil {
		t.Fatalf("a cancelled caller ended opkg: %v", err)
	}
	if !exists(done) {
		t.Fatal("opkg did not finish")
	}
	prev := OpkgTime
	OpkgTime = 200 * time.Millisecond
	defer func() { OpkgTime = prev }()
	write(t, filepath.Join(bin, "opkg"), "#!/bin/sh\nsleep 5\n", 0o755)
	start := time.Now()
	if _, err := RunOpkg(context.Background(), "remove", "x"); err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("an opkg past its time: err %v after %v", err, time.Since(start))
	}
}

// A record PassWall2 is back from says nothing true any more: it goes.
func TestAStaleRecordGoes(t *testing.T) {
	e := testEnv(t)
	if err := e.writeRecord(Record{At: t0, Removed: []string{App}}); err != nil {
		t.Fatal(err)
	}
	if dropped, err := e.DropStaleRecord(); dropped || err != nil {
		t.Fatalf("PassWall2 gone, yet the record was dropped: %v %v", dropped, err)
	}
	write(t, filepath.Join(e.InfoDir, App+".control"), "Package: "+App+"\n", 0o644)
	if dropped, err := e.DropStaleRecord(); !dropped || err != nil {
		t.Fatalf("PassWall2 back, record kept: %v %v", dropped, err)
	}
	if _, ok := e.ReadRecord(); ok {
		t.Fatal("still there")
	}
}
