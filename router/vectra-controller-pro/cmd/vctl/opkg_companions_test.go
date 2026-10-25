package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// theDefaultControllerInstall is runControllerInstall as vctl has it, before
// any test stands it in.
var theDefaultControllerInstall = runControllerInstall

// A router whose vctl came from the pro feed: vectra-geodata and
// vectra-reporter came in as its dependencies, as did xray-core and a kmod.
const companionStatus = `Package: vectra-controller-pro
Version: 0.7.0-r28
Depends: ca-bundle, xray-core (>= 26.3.27), vectra-geodata, vectra-reporter (>= 1.0.0-r4)
Status: install user installed
Architecture: aarch64_cortex-a53
Conffiles:
 /etc/config/vectra-controller-pro 1f2e
Installed-Time: 1792900000

Package: vectra-geodata
Version: 2026.9.28-r2
Status: install ok installed
Architecture: all
Installed-Time: 1792900000
Auto-Installed: yes

Package: xray-core
Version: 26.7.28-r1
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1792900000
Auto-Installed: yes

Package: vectra-reporter
Version: 1.0.0-r4
Status: install ok installed
Architecture: aarch64_cortex-a53
Conffiles:
 /etc/config/vectra-reporter 9a8b
Installed-Time: 1792900000
Auto-Installed: yes

Package: vectra-geodata-extra
Version: 1
Status: install ok installed
Architecture: all
Installed-Time: 1792900000
Auto-Installed: yes

Package: kmod-nft-tproxy
Version: 6.6.119-r1
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1792900000
Auto-Installed: yes

`

func companionStand(t *testing.T, status string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "status")
	if err := os.WriteFile(p, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	prevStatus, prevLock, prevProc, prevWait := opkgStatusFile, opkgLockFile, opkgProcDir, companionsWait
	opkgStatusFile, opkgLockFile, opkgProcDir, companionsWait = p, filepath.Join(dir, "opkg.lock"), filepath.Join(dir, "proc"), 0
	prevAfter, prevBefore := afterTheLock, beforeTheRename
	t.Cleanup(func() {
		opkgStatusFile, opkgLockFile, opkgProcDir, companionsWait = prevStatus, prevLock, prevProc, prevWait
		afterTheLock, beforeTheRename = prevAfter, prevBefore
	})
	// The router's processes: init, and no opkg.
	fakeProcess(t, "1", "init")
	return p
}

// fakeProcess puts a process named comm in the stand's process list; its
// removal ends it.
func fakeProcess(t *testing.T, pid, comm string) func() {
	t.Helper()
	dir := filepath.Join(opkgProcDir, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() { os.RemoveAll(dir) }
}

// holdOpkgLock locks path from another process, as an opkg does — an fcntl
// lock is the process's, so this one's own would be granted again — until
// the returned func is called.
func holdOpkgLock(t *testing.T, path string) func() {
	t.Helper()
	holder := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsTheOpkgLock$")
	holder.Env = append(os.Environ(), "VCTL_TEST_HOLD_OPKG_LOCK="+path)
	stdin, _ := holder.StdinPipe()
	stdout, _ := holder.StdoutPipe()
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	release := func() { stdin.Close(); holder.Wait() }
	t.Cleanup(release)
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		t.Fatalf("the holder said %q", line)
	}
	return release
}

// Only the companions lose the mark: xray-core, the kmod and a package whose
// name merely starts like one keep theirs, and every other byte stays.
func TestKeepCompanionsDropsOnlyTheirMarks(t *testing.T) {
	p := companionStand(t, companionStatus)
	kept, err := keepCompanions()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kept, []string{"vectra-geodata", "vectra-reporter"}) {
		t.Fatalf("kept %v", kept)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(companionStatus, "Installed-Time: 1792900000\nAuto-Installed: yes\n\nPackage: xray-core",
		"Installed-Time: 1792900000\n\nPackage: xray-core", 1)
	want = strings.Replace(want, " /etc/config/vectra-reporter 9a8b\nInstalled-Time: 1792900000\nAuto-Installed: yes\n",
		" /etc/config/vectra-reporter 9a8b\nInstalled-Time: 1792900000\n", 1)
	if string(got) != want {
		t.Fatalf("status now:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(string(got), autoInstalledMark) != 3 {
		t.Fatalf("the other packages' marks are gone:\n%s", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if _, err := os.Stat(p + ".vctl"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file stayed: %v", err)
	}
	// Once more: nothing to drop, nothing written.
	before, _ := os.Stat(p)
	if kept, err := keepCompanions(); err != nil || kept != nil {
		t.Fatalf("again: kept %v, err %v", kept, err)
	}
	if after, _ := os.Stat(p); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the file was written again")
	}
}

// An opkg that is alive though its lock is free — one that has let go of the
// lock and has its unlink still ahead, or one that will fail to lock: the
// marks stay, and the file is not touched.
func TestKeepCompanionsLeavesTheFileToAnotherOpkg(t *testing.T) {
	p := companionStand(t, companionStatus)
	fakeProcess(t, "4242", "opkg")
	if _, err := keepCompanions(); !errors.Is(err, errOpkgRunning) {
		t.Fatalf("err %v, want errOpkgRunning", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed:\n%s", got)
	}
}

// An opkg holds its lock (lockf) from start to end: then too the file is not
// touched — and while keepCompanions holds it, no opkg starts. fcntl locks
// belong to a process, so another process holds this one: the test binary,
// run again as TestHelperHoldsTheOpkgLock.
func TestKeepCompanionsWaitsForOpkgsLock(t *testing.T) {
	p := companionStand(t, companionStatus)
	release := holdOpkgLock(t, opkgLockFile)
	if _, err := keepCompanions(); !errors.Is(err, errOpkgRunning) {
		t.Fatalf("err %v, want errOpkgRunning", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed:\n%s", got)
	}
	release()
	if kept, err := keepCompanions(); err != nil || len(kept) != 2 {
		t.Fatalf("once the lock is free: kept %v, err %v", kept, err)
	}
}

// An opkg ends as libopkg/opkg_conf.c has it: it lets go of its lock, closes
// the file and then unlinks it — and the next opkg creat()s a new file and
// locks that. A lock granted in between is on the file that goes: it holds
// nothing, and the status is not touched, until the lock is the path's.
func TestALockOnAnUnlinkedLockFileHoldsNothing(t *testing.T) {
	p := companionStand(t, companionStatus)
	var release func()
	afterTheLock = func() {
		afterTheLock = func() {}
		// The finishing opkg's unlink, and the next opkg's creat() and lock.
		if err := os.Remove(opkgLockFile); err != nil {
			t.Fatal(err)
		}
		release = holdOpkgLock(t, opkgLockFile)
	}
	if _, err := keepCompanions(); !errors.Is(err, errOpkgRunning) {
		t.Fatalf("err %v, want errOpkgRunning", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed under the next opkg's lock:\n%s", got)
	}
	// The next opkg holds the new file: still nothing.
	if _, err := keepCompanions(); !errors.Is(err, errOpkgRunning) {
		t.Fatalf("while the next opkg runs: err %v, want errOpkgRunning", err)
	}
	release()
	if kept, err := keepCompanions(); err != nil || len(kept) != 2 {
		t.Fatalf("once it is done: kept %v, err %v", kept, err)
	}
}

// A writer that takes no lock (an installer before r31) writes the status
// after the last look and before the rename: the rename does not put the
// copy made before it back; the status stays as that writer made it.
func TestAWriteBeforeTheRenameIsNotUndone(t *testing.T) {
	p := companionStand(t, companionStatus)
	theirs := companionStatus + "Package: luci-app-new\nVersion: 1\nStatus: install user installed\nArchitecture: all\n\n"
	beforeTheRename = func() {
		if err := os.WriteFile(p, []byte(theirs), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := keepCompanions(); err == nil {
		t.Fatal("no error: the marks were dropped over another write")
	}
	if got, _ := os.ReadFile(p); string(got) != theirs {
		t.Fatalf("status now:\n%s\nwant the other writer's:\n%s", got, theirs)
	}
	if _, err := os.Stat(p + ".vctl"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file stayed: %v", err)
	}
}

// Fail closed: without the lock's directory, or without a look at the
// processes, nothing is held, and nothing is written.
func TestNoLockAndNoProcessListWriteNothing(t *testing.T) {
	p := companionStand(t, companionStatus)
	opkgLockFile = filepath.Join(filepath.Dir(p), "no-such-dir", "opkg.lock")
	if _, err := keepCompanions(); err == nil || errors.Is(err, errOpkgRunning) {
		t.Fatalf("without /var/lock: err %v", err)
	}
	opkgLockFile = filepath.Join(filepath.Dir(p), "opkg.lock")
	opkgProcDir = filepath.Join(filepath.Dir(p), "empty-proc")
	if err := os.MkdirAll(opkgProcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := keepCompanions(); err == nil || errors.Is(err, errOpkgRunning) {
		t.Fatalf("without processes: err %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed:\n%s", got)
	}
}

// Where nothing is marked — every start of a vctl after the first — opkg's
// lock is not even taken: an opkg started then is not refused.
func TestNothingMarkedTakesNoLock(t *testing.T) {
	_, status := dropCompanionMarks([]byte(companionStatus))
	companionStand(t, string(status))
	holdOpkgLock(t, opkgLockFile)
	if kept, err := keepCompanions(); err != nil || kept != nil {
		t.Fatalf("kept %v, err %v", kept, err)
	}
}

// An older vctl's self-update — r29's installs the one package and keeps
// nothing — brought this vctl in, and the companions the opkg installed as
// its dependencies are marked: the new vctl drops the marks once it starts,
// that opkg waited out.
func TestTheStartDropsTheMarksAnOldUpdaterLeft(t *testing.T) {
	p := companionStand(t, companionStatus)
	prevWait, prevRetry := companionsStartWait, companionsStartRetry
	companionsStartWait, companionsStartRetry = 5*time.Second, 10*time.Millisecond
	t.Cleanup(func() { companionsStartWait, companionsStartRetry = prevWait, prevRetry })
	// The old updater's opkg is still at the postinst that started this vctl.
	end := fakeProcess(t, "4242", "opkg")
	done := make(chan struct{})
	go func() { (&daemon{}).keepCompanionsAtStart(context.Background()); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed under the running opkg:\n%s", got)
	}
	end()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the start's pass did not end")
	}
	got, _ := os.ReadFile(p)
	if kept, _ := dropCompanionMarks(got); kept != nil {
		t.Fatalf("the marks of %v stay after the start", kept)
	}
	if strings.Count(string(got), autoInstalledMark) != 3 {
		t.Fatalf("the other packages' marks are gone:\n%s", got)
	}
}

// What the installer and Rollout-VctlFleet.py run after their opkg.
func TestTheKeepCompanionsCommand(t *testing.T) {
	p := companionStand(t, companionStatus)
	var out strings.Builder
	prev := keepCompanionsOut
	keepCompanionsOut = &out
	t.Cleanup(func() { keepCompanionsOut = prev })
	end := fakeProcess(t, "4242", "opkg")
	if err := cmdKeepCompanions([]string{"--wait", "0"}); err == nil || !strings.Contains(err.Error(), "another opkg is running") {
		t.Fatalf("while an opkg runs: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed:\n%s", got)
	}
	end()
	if err := cmdKeepCompanions(nil); err != nil {
		t.Fatal(err)
	}
	if err := cmdKeepCompanions(nil); err != nil {
		t.Fatal(err)
	}
	if want := "companions: kept vectra-geodata vectra-reporter (Auto-Installed mark dropped)\ncompanions: none marked\n"; out.String() != want {
		t.Fatalf("said %q, want %q", out.String(), want)
	}
	if err := cmdKeepCompanions([]string{"now"}); err == nil {
		t.Fatal("an argument went through")
	}
}

// TestHelperHoldsTheOpkgLock is holdOpkgLock's other process: it takes the
// lock as opkg does and keeps it until stdin closes.
func TestHelperHoldsTheOpkgLock(t *testing.T) {
	path := os.Getenv("VCTL_TEST_HOLD_OPKG_LOCK")
	if path == "" {
		t.Skip("a helper process")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("locked\n")
	bufio.NewReader(os.Stdin).ReadString('\n')
	f.Close()
}

func TestKeepCompanionsWithoutAStatusFile(t *testing.T) {
	p := companionStand(t, "")
	os.Remove(p)
	if kept, err := keepCompanions(); err != nil || kept != nil {
		t.Fatalf("kept %v, err %v", kept, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("a status file was made")
	}
}

// The self-update's opkg — update_controller's and Connect's automatic update
// alike — finds the marks gone already: opkg would remove a companion before
// anything after it runs. And what that opkg marks again — a companion it
// installed as the new vctl's dependency — loses the mark once it is done.
func TestTheSelfUpdateDropsTheMarksBeforeAndAfterOpkg(t *testing.T) {
	p := companionStand(t, companionStatus)
	dir := t.TempDir()
	seen := filepath.Join(dir, "status-at-opkg")
	if err := os.WriteFile(filepath.Join(dir, "marked"), []byte(companionStatus), 0o644); err != nil {
		t.Fatal(err)
	}
	// It notes the status it found, then leaves the companions marked, as an
	// opkg that installed them as dependencies does.
	opkg := "#!/bin/sh\ncp \"$VCTL_TEST_STATUS\" \"$VCTL_TEST_SEEN\"\necho \"$@\" > \"$VCTL_TEST_SEEN.args\"\ncp \"$VCTL_TEST_MARKED\" \"$VCTL_TEST_STATUS\"\n"
	if err := os.WriteFile(filepath.Join(dir, "opkg"), []byte(opkg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VCTL_TEST_STATUS", p)
	t.Setenv("VCTL_TEST_SEEN", seen)
	t.Setenv("VCTL_TEST_MARKED", filepath.Join(dir, "marked"))
	if out, err := theDefaultControllerInstall(context.Background(), "/tmp/vectra-controller-pro-update.ipk"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	args, _ := os.ReadFile(seen + ".args")
	if strings.TrimSpace(string(args)) != "install /tmp/vectra-controller-pro-update.ipk" {
		t.Fatalf("opkg %q", args)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if kept, _ := dropCompanionMarks(got); kept != nil {
		t.Fatalf("opkg ran with the marks of %v still there", kept)
	}
	after, _ := os.ReadFile(p)
	if kept, _ := dropCompanionMarks(after); kept != nil {
		t.Fatalf("after the install the marks of %v are back", kept)
	}
	if strings.Count(string(after), autoInstalledMark) != 3 {
		t.Fatalf("the other packages' marks are gone:\n%s", after)
	}
}
