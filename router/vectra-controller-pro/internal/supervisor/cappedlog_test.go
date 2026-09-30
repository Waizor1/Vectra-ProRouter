package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedLogNeverHoldsMoreThanTwoGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.log")
	c := newCappedLog(path, 1000)
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < 500; i++ { // 50 KB through a 1 KB cap
		if n, err := c.Write([]byte(line)); n != len(line) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	cur, _ := os.Stat(path)
	old, _ := os.Stat(path + ".1")
	if cur == nil || old == nil {
		t.Fatal("expected xray.log and xray.log.1")
	}
	if cur.Size() > 1000 || old.Size() > 1000 {
		t.Fatalf("sizes %d/%d exceed the cap", cur.Size(), old.Size())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("%d files in the log dir; rotation must keep exactly one old generation", len(entries))
	}
	// The newest line is in the current file.
	b, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(b), line) {
		t.Fatal("the newest write is not at the end of xray.log")
	}
}

// A restart reopens the same log: the size it resumes from must be the file's,
// or the cap is exceeded by one generation's worth after every restart.
func TestCappedLogResumesFromTheExistingSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 900)), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newCappedLog(path, 1000)
	_, _ = c.Write([]byte(strings.Repeat("z", 200)))
	if st, _ := os.Stat(path); st.Size() != 200 {
		t.Fatalf("size = %d; the pre-existing 900 bytes were not counted", st.Size())
	}
}

func TestCappedLogSwallowsAnUnwritableDir(t *testing.T) {
	c := newCappedLog("/nonexistent-dir-for-test/xray.log", 1000)
	if n, err := c.Write([]byte("hello\n")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; a log failure must never reach xray", n, err)
	}
}

// The controller runs with its own GOGC/GOMEMLIMIT (init script); xray must not
// inherit them, or the wrapper's 80 MiB default never applies.
func TestChildEnvDropsTheControllersGoTuning(t *testing.T) {
	got := childEnv([]string{"PATH=/usr/bin", "GOMEMLIMIT=32MiB", "GOGC=50", "GOGCX=1", "HOME=/root"})
	want := "PATH=/usr/bin,GOGCX=1,HOME=/root"
	if strings.Join(got, ",") != want {
		t.Fatalf("childEnv = %v, want %s", got, want)
	}
}

// xray's log is root's alone: its errors quote the config they choke on — a
// user id, a password — and a log a previous version left readable to all
// (0644) is made root's too.
func TestCappedLogIsReadableOnlyByRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.log")
	for _, p := range []string{path, path + ".1"} {
		if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := newCappedLog(path, 1000)
	_, _ = c.Write([]byte("new\n"))
	for _, p := range []string{path, path + ".1"} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v; want 0600", filepath.Base(p), st.Mode().Perm(), err)
		}
	}
	// A fresh log, and each generation rotation makes, is root's alone too.
	fresh := filepath.Join(t.TempDir(), "xray.log")
	c = newCappedLog(fresh, 100)
	for i := 0; i < 30; i++ {
		_, _ = c.Write([]byte(strings.Repeat("x", 9) + "\n"))
	}
	for _, p := range []string{fresh, fresh + ".1"} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v; want 0600", filepath.Base(p), st.Mode().Perm(), err)
		}
	}
}
