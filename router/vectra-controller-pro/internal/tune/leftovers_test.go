package tune

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only vctl's own leftovers, by their exact names, older than ten minutes
// and open in no process, are removed — never anything else in /tmp.
func TestRemoveLeftoversTakesOnlyVctlsOwnStaleFiles(t *testing.T) {
	dir := t.TempDir()
	env := Env{ProcDir: filepath.Join(dir, "proc"), TmpDir: filepath.Join(dir, "tmp"), RunDir: filepath.Join(dir, "run")}
	put := func(path string, age time.Duration) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("12345678"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		return path
	}
	gone := []string{
		put(filepath.Join(env.TmpDir, "vectra-controller-pro-update.ipk"), time.Hour),
		put(filepath.Join(env.TmpDir, "vectra-controller-pro-auto-81234.ipk"), time.Hour),
		put(filepath.Join(env.RunDir, ".vault-tmp-5"), time.Hour),
	}
	kept := []string{
		put(filepath.Join(env.TmpDir, "vectra-controller-pro-auto-1.ipk"), time.Minute),
		put(filepath.Join(env.TmpDir, "luci-app-passwall2.ipk"), time.Hour),
		put(filepath.Join(env.RunDir, "xray.json"), time.Hour),
		put(filepath.Join(env.RunDir, ".vault-tmp-open"), time.Hour),
	}
	if err := os.MkdirAll(filepath.Join(env.ProcDir, "77", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kept[3], filepath.Join(env.ProcDir, "77", "fd", "3")); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveLeftovers(env, time.Now())
	if err != nil || len(removed) != 3 || leftoverBytes(removed) != 24 {
		t.Fatalf("removed %v, %v", removed, err)
	}
	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s stayed", p)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed", p)
		}
	}
	if ls := Leftovers(Env{}, time.Now()); ls != nil {
		t.Fatalf("an Env naming nowhere found %v", ls)
	}
}
