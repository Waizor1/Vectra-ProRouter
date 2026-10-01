package retire

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// testEnv is a router in a temp dir: PassWall2's config there, its package
// and init script not yet.
func testEnv(t *testing.T) Env {
	t.Helper()
	dir := t.TempDir()
	e := Env{
		StatusFile:   filepath.Join(dir, "opkg", "status"),
		InfoDir:      filepath.Join(dir, "opkg", "info"),
		PassWall:     []string{filepath.Join(dir, "init.d", "passwall2"), filepath.Join(dir, "init.d", "passwall")},
		MarkerDir:    filepath.Join(dir, "etc", "vectra-controller-pro"),
		TrialMarkers: filepath.Join(dir, "tmp", "vectra-trial.d"),
		Snippet:      filepath.Join(dir, "etc", "uci-defaults", "99-vectra-trial-passwall-switch"),
		BackupDir:    filepath.Join(dir, "etc", "vectra-controller-pro", "backup"),
		Configs:      []string{filepath.Join(dir, "etc", "config", "passwall2"), filepath.Join(dir, "etc", "config", "passwall2_server")},
	}
	for _, d := range []string{e.InfoDir, filepath.Dir(e.PassWall[0]), e.MarkerDir, e.TrialMarkers, filepath.Dir(e.Snippet), filepath.Dir(e.Configs[0])} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// PassWall2 is on the router while its package is installed or an init
// script of its is there — either spelling.
func TestPresentIsItsPackageOrItsInitScript(t *testing.T) {
	e := testEnv(t)
	if e.Present() {
		t.Fatal("present on a router without it")
	}
	write(t, filepath.Join(e.InfoDir, App+".control"), "Package: "+App+"\n", 0o644)
	if !e.Present() {
		t.Fatal("its package installed, not present")
	}
	os.Remove(filepath.Join(e.InfoDir, App+".control"))
	write(t, e.PassWall[1], "#!/bin/sh\n", 0o755)
	if !e.Present() {
		t.Fatal("its older init script there, not present")
	}
}

// The window's clock: started once, at the first look that counts, and read
// back as it was — a restart, or the daily reboot, does not start it over.
func TestTheClockStartsOnceAndIsReadBack(t *testing.T) {
	e := testEnv(t)
	if _, ok := e.Since(); ok {
		t.Fatal("a clock before any start")
	}
	got, err := e.StartClock(t0)
	if err != nil || !got.Equal(t0) {
		t.Fatalf("start = %v, %v", got, err)
	}
	if got, err := e.StartClock(t0.Add(5 * time.Hour)); err != nil || !got.Equal(t0) {
		t.Fatalf("a second start moved the clock: %v, %v", got, err)
	}
	if since, ok := e.Since(); !ok || !since.Equal(t0) {
		t.Fatalf("since = %v, %v", since, ok)
	}
	// A clock nobody can read is no clock: started again.
	write(t, filepath.Join(e.MarkerDir, ClockName), "yesterday\n", 0o644)
	if _, ok := e.Since(); ok {
		t.Fatal("garbage read as a start")
	}
	if got, _ := e.StartClock(t0.Add(time.Hour)); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("the unreadable clock was not started over: %v", got)
	}
}

// The backup: PassWall2's configuration files as they are, in one archive
// only root can read, in a directory only root can enter — paths relative to
// / so that `tar -xzf <backup> -C /` puts them back.
func TestTheBackupKeepsPassWallsConfiguration(t *testing.T) {
	e := testEnv(t)
	write(t, e.Configs[0], "config global\n\toption enabled '0'\n", 0o600)
	write(t, e.Configs[1], "config global\n\toption enable '0'\n", 0o644)
	path, err := e.Backup(t0)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(e.BackupDir, "passwall2-1790769600.tar.gz"); path != want {
		t.Fatalf("backup at %s, want %s", path, want)
	}
	if st, _ := os.Stat(path); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v, want 0600", st)
	}
	if st, _ := os.Stat(e.BackupDir); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir mode %v, want 0700", st)
	}
	got := untar(t, path)
	for _, c := range e.Configs {
		name := strings.TrimPrefix(c, "/")
		want, _ := os.ReadFile(c)
		if got[name] != string(want) {
			t.Errorf("%s in the backup: %q, want %q (entries %v)", name, got[name], want, mapKeys(got))
		}
	}
}

// Only the newest three are kept.
func TestTheBackupsAreRotated(t *testing.T) {
	e := testEnv(t)
	write(t, e.Configs[0], "config global\n", 0o600)
	for i := 0; i < 5; i++ {
		if _, err := e.Backup(t0.Add(time.Duration(i) * time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Someone else's file there stays.
	write(t, filepath.Join(e.BackupDir, "notes.txt"), "mine\n", 0o600)
	if _, err := e.Backup(t0.Add(10 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(e.BackupDir)
	var names []string
	for _, d := range ents {
		names = append(names, d.Name())
	}
	sort.Strings(names)
	want := []string{"notes.txt", "passwall2-1790780400.tar.gz", "passwall2-1790784000.tar.gz", "passwall2-1790805600.tar.gz"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("backups %v, want %v", names, want)
	}
}

// Nothing to back up is no reason to stop: there is nothing to lose.
func TestNoConfigurationNoBackup(t *testing.T) {
	e := testEnv(t)
	path, err := e.Backup(t0)
	if err != nil || path != "" {
		t.Fatalf("backup of nothing: %q, %v", path, err)
	}
	if exists(e.BackupDir) {
		t.Fatal("an empty backup directory was made")
	}
}

// A configuration that cannot be read is a backup that cannot be made: the
// caller must not go on.
func TestAnUnreadableConfigurationFailsTheBackup(t *testing.T) {
	e := testEnv(t)
	if err := os.MkdirAll(e.Configs[0], 0o755); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	if _, err := e.Backup(t0); err == nil {
		t.Fatal("backed up a configuration it could not read")
	}
	if ents, _ := os.ReadDir(e.BackupDir); len(ents) != 0 {
		t.Fatalf("a failed backup left %v", ents)
	}
}

// The record of the retirement: when, what went, what stayed, where the
// backup is — written before anything goes, read back by the UI's status and
// by the hand-back.
func TestTheRecordIsReadBack(t *testing.T) {
	e := testEnv(t)
	if _, ok := e.ReadRecord(); ok {
		t.Fatal("a record before any retirement")
	}
	r := Record{At: t0, Removed: []string{App, "geoview"}, Kept: []string{"tcping"}, Backup: "/etc/vectra-controller-pro/backup/passwall2-1.tar.gz"}
	if err := e.writeRecord(r); err != nil {
		t.Fatal(err)
	}
	got, ok := e.ReadRecord()
	if !ok || !reflect.DeepEqual(got, r) {
		t.Fatalf("record %+v (%v), want %+v", got, ok, r)
	}
	raw, _ := os.ReadFile(filepath.Join(e.MarkerDir, RecordName))
	if !strings.Contains(string(raw), "removed=luci-app-passwall2 geoview\n") {
		t.Fatalf("the record is not the one line a person reads:\n%s", raw)
	}
}

// Its state for the UI: installed while it is on the router; retired, with
// when, once vctl took it off; absent where it never was.
func TestTheStateSaysInstalledRetiredOrAbsent(t *testing.T) {
	e := testEnv(t)
	if s, at := e.State(); s != "absent" || !at.IsZero() {
		t.Fatalf("a router without it: %s %v", s, at)
	}
	write(t, e.PassWall[0], "#!/bin/sh\n", 0o755)
	if s, _ := e.State(); s != "installed" {
		t.Fatalf("with its init script: %s", s)
	}
	if err := e.writeRecord(Record{At: t0, Removed: []string{App}}); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.State(); s != "installed" {
		t.Fatalf("a record, but PassWall2 is back: %s", s)
	}
	os.Remove(e.PassWall[0])
	if s, at := e.State(); s != "retired" || !at.Equal(t0) {
		t.Fatalf("retired: %s %v", s, at)
	}
	if s, _ := (Env{}).State(); s != "absent" {
		t.Fatalf("an env with no paths: %s", s)
	}
}

// With PassWall2 gone nothing is owed it: the takeover's breadcrumbs — its
// rc.d link and its own switch, on /etc and a trial's on tmpfs — and the
// trial's snippet go. The old agent's stays: it is still owed.
func TestForgetTakesWhatIsOwedPassWallAway(t *testing.T) {
	e := testEnv(t)
	owed := []string{
		filepath.Join(e.MarkerDir, ".passwall-disabled-by-vctl"),
		filepath.Join(e.MarkerDir, ".passwall-switch-off-by-vctl"),
		filepath.Join(e.TrialMarkers, ".passwall-disabled-by-vctl"),
		filepath.Join(e.TrialMarkers, ".passwall-switch-off-by-vctl"),
		e.Snippet,
	}
	for _, p := range owed {
		write(t, p, "", 0o644)
	}
	agent := filepath.Join(e.MarkerDir, ".legacy-agent-disabled-by-vctl")
	write(t, agent, "", 0o644)
	if err := e.forget(); err != nil {
		t.Fatal(err)
	}
	for _, p := range owed {
		if exists(p) {
			t.Errorf("%s left", p)
		}
	}
	if !exists(agent) {
		t.Fatal("the agent's breadcrumb went too: it is still owed")
	}
	if err := e.forget(); err != nil {
		t.Fatalf("forgetting twice: %v", err)
	}
}

func untar(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func mapKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
